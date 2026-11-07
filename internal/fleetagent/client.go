package fleetagent

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mywio/git-ops/pkg/core"
	"github.com/mywio/git-ops/pkg/fleetapi"
)

type controllerClient struct {
	cfg      Config
	http     *http.Client
	agentID  string
	keyFile  string
	certFile string
	caFile   string
}

func newControllerClient(ctx context.Context, cfg Config) (*controllerClient, error) {
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return nil, err
	}
	client := &controllerClient{cfg: cfg, keyFile: filepath.Join(cfg.StateDir, "agent-key.pem"),
		certFile: filepath.Join(cfg.StateDir, "agent-cert.pem"), caFile: filepath.Join(cfg.StateDir, "controller-ca.pem")}
	if data, err := os.ReadFile(filepath.Join(cfg.StateDir, "agent-id")); err == nil {
		client.agentID = strings.TrimSpace(string(data))
	}
	if _, err := os.Stat(client.certFile); err == nil {
		httpClient, err := client.mTLSClient()
		if err != nil {
			return nil, err
		}
		client.http = httpClient
		return client, nil
	}
	if cfg.EnrollmentToken == "" {
		return nil, fmt.Errorf("agent is not enrolled and no enrollment token was supplied")
	}
	if err := client.enroll(ctx); err != nil {
		return nil, err
	}
	return client, nil
}

func (c *controllerClient) enroll(ctx context.Context) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(c.keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return err
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: c.cfg.NodeName}}, key)
	if err != nil {
		return err
	}
	facts := c.facts()
	request := fleetapi.EnrollRequest{Token: c.cfg.EnrollmentToken,
		CSR: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})), Facts: facts}
	client, err := c.serverAuthClient()
	if err != nil {
		return err
	}
	var response fleetapi.EnrollResponse
	if err := c.do(ctx, client, http.MethodPost, "/v1/enroll", request, &response); err != nil {
		return err
	}
	if response.AgentID == "" || response.CertificatePEM == "" {
		return fmt.Errorf("controller returned incomplete enrollment")
	}
	if err := os.WriteFile(c.certFile, []byte(response.CertificatePEM), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(c.caFile, []byte(response.CACertificatePEM), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(c.cfg.StateDir, "agent-id"), []byte(response.AgentID+"\n"), 0o600); err != nil {
		return err
	}
	c.agentID = response.AgentID
	c.http, err = c.mTLSClient()
	return err
}

func (c *controllerClient) facts() fleetapi.AgentFacts {
	return fleetapi.AgentFacts{NodeName: c.cfg.NodeName, OS: runtime.GOOS, Architecture: runtime.GOARCH,
		AgentVersion: core.Version, Pool: c.cfg.Pool}
}

func (c *controllerClient) serverAuthClient() (*http.Client, error) {
	roots, err := loadRoots(c.cfg.ServerCAFile)
	if err != nil {
		return nil, err
	}
	return &http.Client{Timeout: 35 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: roots}}}, nil
}

func (c *controllerClient) mTLSClient() (*http.Client, error) {
	cert, err := tls.LoadX509KeyPair(c.certFile, c.keyFile)
	if err != nil {
		return nil, err
	}
	// The enrollment CA signs the client identity and may be distinct from the
	// CA that signs the controller's HTTPS certificate. Always authenticate the
	// server with the explicitly configured server trust anchor.
	roots, err := loadRoots(c.cfg.ServerCAFile)
	if err != nil {
		return nil, err
	}
	return &http.Client{Timeout: 35 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{cert}}}}, nil
}

func loadRoots(path string) (*x509.CertPool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("CA file contains no certificates")
	}
	return roots, nil
}

func (c *controllerClient) heartbeat(ctx context.Context, runners []fleetapi.RunnerStatus) error {
	return c.do(ctx, c.http, http.MethodPost, "/v1/heartbeat", fleetapi.HeartbeatRequest{Facts: c.facts(), Runners: runners}, &struct{}{})
}

func (c *controllerClient) claim(ctx context.Context) (*fleetapi.Task, error) {
	var task fleetapi.Task
	status, err := c.doStatus(ctx, c.http, http.MethodPost, "/v1/tasks/claim", nil, &task)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNoContent {
		return nil, nil
	}
	return &task, nil
}

func (c *controllerClient) result(ctx context.Context, taskID string, result fleetapi.TaskResult) error {
	return c.do(ctx, c.http, http.MethodPost, "/v1/tasks/"+url.PathEscape(taskID)+"/result", result, &struct{}{})
}

func (c *controllerClient) credential(ctx context.Context, taskID string) (fleetapi.RegistrationCredential, error) {
	var credential fleetapi.RegistrationCredential
	err := c.do(ctx, c.http, http.MethodPost, "/v1/tasks/"+url.PathEscape(taskID)+"/credential", nil, &credential)
	return credential, err
}

func (c *controllerClient) do(ctx context.Context, client *http.Client, method, path string, request, response any) error {
	_, err := c.doStatus(ctx, client, method, path, request, response)
	return err
}

func (c *controllerClient) doStatus(ctx context.Context, client *http.Client, method, path string, request, response any) (int, error) {
	var body io.Reader
	if request != nil {
		data, err := json.Marshal(request)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.cfg.ControllerURL, "/")+path, body)
	if err != nil {
		return 0, err
	}
	if request != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNoContent {
		return resp.StatusCode, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return resp.StatusCode, fmt.Errorf("controller returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	if response != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(response); err != nil && err != io.EOF {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}
