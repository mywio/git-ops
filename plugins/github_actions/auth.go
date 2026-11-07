package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type githubConnection struct {
	cfg     connectionConfig
	client  *http.Client
	baseURL string
	mu      sync.Mutex
	token   string
	expires time.Time
	appKey  *rsa.PrivateKey
}

func newGitHubConnection(cfg connectionConfig, client *http.Client) (*githubConnection, error) {
	connection := &githubConnection{cfg: cfg, client: client, baseURL: "https://api.github.com"}
	if cfg.Auth.Type == "github_app" {
		data, err := os.ReadFile(cfg.Auth.PrivateKeyFile)
		if err != nil {
			return nil, err
		}
		block, _ := pem.Decode(data)
		if block == nil {
			return nil, fmt.Errorf("GitHub App key is not PEM")
		}
		if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
			connection.appKey = key
		} else if value, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
			var ok bool
			connection.appKey, ok = value.(*rsa.PrivateKey)
			if !ok {
				return nil, fmt.Errorf("GitHub App key must be RSA")
			}
		} else {
			return nil, fmt.Errorf("parse GitHub App private key")
		}
	} else {
		token, err := loadPAT(cfg.Auth)
		if err != nil {
			return nil, err
		}
		connection.token = token
		connection.expires = time.Now().Add(100 * 365 * 24 * time.Hour)
	}
	return connection, nil
}

func (c *githubConnection) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Until(c.expires) > 2*time.Minute {
		return c.token, nil
	}
	jwt, err := c.appJWT()
	if err != nil {
		return "", err
	}
	endpoint := fmt.Sprintf("%s/app/installations/%d/access_tokens", c.baseURL, c.cfg.Auth.InstallationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader([]byte("{}")))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return "", fmt.Errorf("GitHub installation token returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var result struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	c.token, c.expires = result.Token, result.ExpiresAt
	return c.token, nil
}

func (c *githubConnection) appJWT() (string, error) {
	now := time.Now().UTC()
	header, _ := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT"})
	payload, _ := json.Marshal(map[string]any{"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(9 * time.Minute).Unix(), "iss": strconv.FormatInt(c.cfg.Auth.AppID, 10)})
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	sum := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, c.appKey, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (c *githubConnection) do(ctx context.Context, method, path string, input, output any) (int, error) {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.baseURL, "/")+path, body)
	if err != nil {
		return 0, err
	}
	token, err := c.accessToken(ctx)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return resp.StatusCode, fmt.Errorf("GitHub returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	if output != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(output); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

func githubPath(parts ...string) string {
	out := ""
	for _, part := range parts {
		out += "/" + url.PathEscape(part)
	}
	return out
}
