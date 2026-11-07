package fleetagent

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/mywio/git-ops/pkg/fleetapi"
)

var safeIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

type runnerManager struct {
	cfg    Config
	logger *slog.Logger
	http   *http.Client
}

type runnerTaskPayload struct {
	Spec       fleetapi.RunnerSpec `json:"spec"`
	PackageURL string              `json:"package_url,omitempty"`
	SHA256     string              `json:"sha256,omitempty"`
	Version    string              `json:"version,omitempty"`
	Force      bool                `json:"force,omitempty"`
	Busy       bool                `json:"busy,omitempty"`
}

func newRunnerManager(cfg Config, logger *slog.Logger) (*runnerManager, error) {
	for _, path := range []string{cfg.RunnerRoot, cfg.WorkRoot, filepath.Join(cfg.StateDir, "runners")} {
		if err := os.MkdirAll(path, 0o750); err != nil {
			return nil, err
		}
	}
	return &runnerManager{cfg: cfg, logger: logger, http: &http.Client{Timeout: 10 * time.Minute}}, nil
}

func (m *runnerManager) execute(ctx context.Context, task fleetapi.Task, credential func() (fleetapi.RegistrationCredential, error)) error {
	var payload runnerTaskPayload
	data, err := json.Marshal(task.Payload)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	if err := validateRunnerSpec(payload.Spec); err != nil {
		return err
	}
	switch task.Type {
	case "runner.install":
		return m.install(ctx, payload, credential)
	case "runner.update":
		return m.update(ctx, payload)
	case "runner.start", "runner.stop", "runner.restart":
		return m.service(ctx, payload.Spec, strings.TrimPrefix(task.Type, "runner."))
	case "runner.drain":
		if payload.Busy && !payload.Force {
			return fmt.Errorf("runner is busy; drain refused without force")
		}
		return m.service(ctx, payload.Spec, "stop")
	case "runner.remove":
		return m.remove(ctx, payload, credential)
	default:
		return fmt.Errorf("unsupported runner task %q", task.Type)
	}
}

func validateRunnerSpec(spec fleetapi.RunnerSpec) error {
	if spec.ID == "" || spec.AgentID == "" || spec.Name == "" || spec.Owner == "" ||
		!safeIDPattern.MatchString(spec.ID) || !safeIDPattern.MatchString(spec.Name) {
		return fmt.Errorf("runner id, agent id, name, and owner are required and ids must be safe")
	}
	if spec.Scope != "organization" && spec.Scope != "repository" {
		return fmt.Errorf("runner scope must be organization or repository")
	}
	if spec.Scope == "repository" && spec.Repository == "" {
		return fmt.Errorf("repository runner requires repository")
	}
	return nil
}

func (m *runnerManager) install(ctx context.Context, payload runnerTaskPayload, credential func() (fleetapi.RegistrationCredential, error)) error {
	if payload.PackageURL == "" || len(payload.SHA256) != sha256.Size*2 {
		return fmt.Errorf("runner package URL and SHA-256 are required")
	}
	if len(m.inventory()) >= m.cfg.MaxRunners {
		if _, err := os.Stat(m.specFile(payload.Spec.ID)); os.IsNotExist(err) {
			return fmt.Errorf("agent runner limit reached")
		}
	}
	for _, group := range payload.Spec.SupplementaryGroups {
		if !contains(m.cfg.AllowedSupplementaryGroups, group) {
			return fmt.Errorf("supplementary group %q is not allowed", group)
		}
	}
	root := m.runnerDir(payload.Spec.ID)
	if err := ensureWithin(m.cfg.RunnerRoot, root); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(m.cfg.StateDir, "runner-*.tar.gz")
	if err != nil {
		return err
	}
	tempName := temporary.Name()
	_ = temporary.Close()
	defer func() { _ = os.Remove(tempName) }()
	if err := m.download(ctx, payload.PackageURL, tempName, payload.SHA256); err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return err
	}
	if err := os.MkdirAll(m.workDir(payload.Spec.ID), 0o750); err != nil {
		return err
	}
	if err := extractRunnerArchive(tempName, root); err != nil {
		return err
	}
	user := runnerUser(payload.Spec.ID)
	if err := ensureSystemUser(ctx, user, payload.Spec.SupplementaryGroups); err != nil {
		return err
	}
	if err := runBounded(ctx, "chown", "-R", user+":"+user, root, m.workDir(payload.Spec.ID)); err != nil {
		return err
	}
	credentialValue, err := credential()
	if err != nil {
		return fmt.Errorf("obtain short-lived registration credential: %w", err)
	}
	args := []string{"-u", user, "--", filepath.Join(root, "config.sh"), "--unattended", "--replace",
		"--url", credentialValue.URL, "--token", credentialValue.Token, "--name", payload.Spec.Name,
		"--work", m.workDir(payload.Spec.ID), "--labels", strings.Join(payload.Spec.Labels, ",")}
	if payload.Spec.Group != "" {
		args = append(args, "--runnergroup", payload.Spec.Group)
	}
	if !payload.Spec.AutoUpdate {
		args = append(args, "--disableupdate")
	}
	if err := runBoundedSecret(ctx, "runuser", args...); err != nil {
		return fmt.Errorf("configure runner: %w", err)
	}
	if err := runBounded(ctx, filepath.Join(root, "svc.sh"), "install", user); err != nil {
		return err
	}
	if err := runBounded(ctx, filepath.Join(root, "svc.sh"), "start"); err != nil {
		return err
	}
	if err := m.persistSpec(payload.Spec, payload.Version); err != nil {
		return err
	}
	return nil
}

// update replaces the runner binaries while preserving the existing GitHub
// registration. Registration tokens are single-use credentials and are not
// required for an in-place package update.
func (m *runnerManager) update(ctx context.Context, payload runnerTaskPayload) error {
	if payload.PackageURL == "" || len(payload.SHA256) != sha256.Size*2 {
		return fmt.Errorf("runner package URL and SHA-256 are required")
	}
	root := m.runnerDir(payload.Spec.ID)
	if err := ensureWithin(m.cfg.RunnerRoot, root); err != nil {
		return err
	}
	if _, err := os.Stat(m.specFile(payload.Spec.ID)); err != nil {
		return fmt.Errorf("runner is not installed")
	}
	temporary, err := os.CreateTemp(m.cfg.StateDir, "runner-update-*.tar.gz")
	if err != nil {
		return err
	}
	tempName := temporary.Name()
	_ = temporary.Close()
	defer func() { _ = os.Remove(tempName) }()
	if err := m.download(ctx, payload.PackageURL, tempName, payload.SHA256); err != nil {
		return err
	}
	if err := m.service(ctx, payload.Spec, "stop"); err != nil {
		return err
	}
	if err := extractRunnerArchive(tempName, root); err != nil {
		return err
	}
	user := runnerUser(payload.Spec.ID)
	if err := runBounded(ctx, "chown", "-R", user+":"+user, root); err != nil {
		return err
	}
	if err := m.service(ctx, payload.Spec, "start"); err != nil {
		return err
	}
	return m.persistSpec(payload.Spec, payload.Version)
}

func (m *runnerManager) remove(ctx context.Context, payload runnerTaskPayload, credential func() (fleetapi.RegistrationCredential, error)) error {
	if payload.Busy && !payload.Force {
		return fmt.Errorf("runner is busy; removal refused without force")
	}
	root := m.runnerDir(payload.Spec.ID)
	if err := ensureWithin(m.cfg.RunnerRoot, root); err != nil {
		return err
	}
	_ = runBounded(ctx, filepath.Join(root, "svc.sh"), "stop")
	_ = runBounded(ctx, filepath.Join(root, "svc.sh"), "uninstall")
	credentialValue, err := credential()
	if err == nil {
		_ = runBoundedSecret(ctx, "runuser", "-u", runnerUser(payload.Spec.ID), "--", filepath.Join(root, "config.sh"),
			"remove", "--token", credentialValue.Token)
	}
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	if err := os.RemoveAll(m.workDir(payload.Spec.ID)); err != nil {
		return err
	}
	return os.Remove(m.specFile(payload.Spec.ID))
}

func (m *runnerManager) service(ctx context.Context, spec fleetapi.RunnerSpec, action string) error {
	script := filepath.Join(m.runnerDir(spec.ID), "svc.sh")
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("runner is not installed")
	}
	return runBounded(ctx, script, action)
}

func (m *runnerManager) download(ctx context.Context, source, target, expected string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return err
	}
	resp, err := m.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("runner download returned %s", resp.Status)
	}
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(resp.Body, 500<<20))
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), expected) {
		return fmt.Errorf("runner package checksum mismatch")
	}
	return nil
}

func extractRunnerArchive(archive, destination string) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target := filepath.Join(destination, filepath.Clean(header.Name))
		if err := ensureWithin(destination, target); err != nil {
			return fmt.Errorf("unsafe runner archive path: %w", err)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)&0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if header.Size > 100<<20 {
				return fmt.Errorf("runner archive member is too large")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode)&0o755)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, io.LimitReader(reader, header.Size))
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		case tar.TypeSymlink:
			linkTarget := filepath.Join(filepath.Dir(target), header.Linkname)
			if err := ensureWithin(destination, linkTarget); err != nil {
				return fmt.Errorf("unsafe runner archive link")
			}
			if err := os.Symlink(header.Linkname, target); err != nil && !os.IsExist(err) {
				return err
			}
		default:
			return fmt.Errorf("unsupported runner archive entry %q", header.Name)
		}
	}
	return nil
}

func ensureWithin(root, target string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path escapes managed root")
	}
	return nil
}

func ensureSystemUser(ctx context.Context, user string, groups []string) error {
	if err := exec.CommandContext(ctx, "id", "-u", user).Run(); err == nil {
		return nil
	}
	args := []string{"--system", "--create-home", "--shell", "/usr/sbin/nologin"}
	if len(groups) > 0 {
		args = append(args, "--groups", strings.Join(groups, ","))
	}
	args = append(args, user)
	return runBounded(ctx, "useradd", args...)
}

func runBounded(ctx context.Context, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)
	output, err := command.CombinedOutput()
	if len(output) > 64<<10 {
		output = output[len(output)-(64<<10):]
	}
	if err != nil {
		return fmt.Errorf("%s failed: %w: %s", filepath.Base(name), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func runBoundedSecret(ctx context.Context, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s failed: %w (output redacted)", filepath.Base(name), err)
	}
	return nil
}

func runnerUser(id string) string {
	clean := strings.ToLower(regexp.MustCompile(`[^a-z0-9]`).ReplaceAllString(id, ""))
	if len(clean) > 12 {
		clean = clean[:12]
	}
	return "ghops-" + clean
}

func (m *runnerManager) runnerDir(id string) string { return filepath.Join(m.cfg.RunnerRoot, id) }
func (m *runnerManager) workDir(id string) string   { return filepath.Join(m.cfg.WorkRoot, id) }
func (m *runnerManager) specFile(id string) string {
	return filepath.Join(m.cfg.StateDir, "runners", id+".json")
}

func (m *runnerManager) persistSpec(spec fleetapi.RunnerSpec, version string) error {
	data, err := json.Marshal(struct {
		Spec    fleetapi.RunnerSpec `json:"spec"`
		Version string              `json:"version"`
	}{spec, version})
	if err != nil {
		return err
	}
	path := m.specFile(spec.ID)
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func (m *runnerManager) inventory() []fleetapi.RunnerStatus {
	matches, _ := filepath.Glob(filepath.Join(m.cfg.StateDir, "runners", "*.json"))
	out := make([]fleetapi.RunnerStatus, 0, len(matches))
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var stored struct {
			Spec    fleetapi.RunnerSpec `json:"spec"`
			Version string              `json:"version"`
		}
		if json.Unmarshal(data, &stored) != nil {
			continue
		}
		status := fleetapi.RunnerStatus{ID: stored.Spec.ID, Name: stored.Spec.Name, Scope: stored.Spec.Scope,
			Owner: stored.Spec.Owner, Repository: stored.Spec.Repository, Group: stored.Spec.Group,
			Labels: stored.Spec.Labels, Version: stored.Version, Service: "unknown"}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if exec.CommandContext(ctx, filepath.Join(m.runnerDir(stored.Spec.ID), "svc.sh"), "status").Run() == nil {
			status.Service, status.Online = "running", true
		} else {
			status.Service = "stopped"
		}
		cancel()
		out = append(out, status)
	}
	return out
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

var _ = runtime.GOARCH
