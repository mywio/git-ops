package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mywio/git-ops/pkg/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type composeRunnerPluginRegistry struct {
	plugins []core.Plugin
}

func (r *composeRunnerPluginRegistry) GetPlugin(name string) (core.Plugin, error) { return nil, assert.AnError }
func (r *composeRunnerPluginRegistry) GetPluginsWithCapability(cap core.Capability) []core.Plugin {
	var out []core.Plugin
	for _, plugin := range r.plugins {
		for _, candidate := range plugin.Capabilities() {
			if candidate == cap {
				out = append(out, plugin)
				break
			}
		}
	}
	return out
}
func (r *composeRunnerPluginRegistry) ListPlugins() []core.Plugin { return r.plugins }
func (r *composeRunnerPluginRegistry) RegisterEventType(desc core.EventTypeDesc) error { return nil }
func (r *composeRunnerPluginRegistry) GetMuxServer() *http.ServeMux { return nil }
func (r *composeRunnerPluginRegistry) Subscribe(pattern string, handler core.Listener) {}
func (r *composeRunnerPluginRegistry) GetHTTPClient() *http.Client { return nil }
func (r *composeRunnerPluginRegistry) GetConfig() map[string]map[string]any { return nil }

type composeRunnerSecretPlugin struct {
	secrets map[string]string
}

func (p *composeRunnerSecretPlugin) Name() string { return "secret_test" }
func (p *composeRunnerSecretPlugin) Description() string { return "test secret plugin" }
func (p *composeRunnerSecretPlugin) Capabilities() []core.Capability { return []core.Capability{core.CapabilitySecrets} }
func (p *composeRunnerSecretPlugin) Status() core.ServiceStatus { return core.StatusHealthy }
func (p *composeRunnerSecretPlugin) Init(ctx context.Context, logger *slog.Logger, registry core.PluginRegistry) error {
	return nil
}
func (p *composeRunnerSecretPlugin) Start(ctx context.Context) error { return nil }
func (p *composeRunnerSecretPlugin) Stop(ctx context.Context) error { return nil }
func (p *composeRunnerSecretPlugin) Execute(ctx context.Context, action string, params map[string]interface{}) (interface{}, error) {
	if action != "get_secrets" {
		return nil, fmt.Errorf("unknown action: %s", action)
	}
	return p.secrets, nil
}

type composeRunnerRuntimePlugin struct {
	files []core.RuntimeFile
}

func (p *composeRunnerRuntimePlugin) Name() string { return "runtime_test" }
func (p *composeRunnerRuntimePlugin) Description() string { return "test runtime plugin" }
func (p *composeRunnerRuntimePlugin) Capabilities() []core.Capability { return []core.Capability{core.CapabilityRuntimeFiles} }
func (p *composeRunnerRuntimePlugin) Status() core.ServiceStatus { return core.StatusHealthy }
func (p *composeRunnerRuntimePlugin) Init(ctx context.Context, logger *slog.Logger, registry core.PluginRegistry) error {
	return nil
}
func (p *composeRunnerRuntimePlugin) Start(ctx context.Context) error { return nil }
func (p *composeRunnerRuntimePlugin) Stop(ctx context.Context) error { return nil }
func (p *composeRunnerRuntimePlugin) Execute(ctx context.Context, action string, params map[string]interface{}) (interface{}, error) {
	if action != "get_runtime_files" {
		return nil, fmt.Errorf("unknown action: %s", action)
	}
	return p.files, nil
}

func TestComposeRunnerReturnsNoUpdateWhenImageIdentitiesUnchanged(t *testing.T) {
	stackPath := writeComposeFile(t)
	registry := testComposeRunnerRegistry()
	commands, restore := stubComposeRunnerCommands(t, func(state *composeRunnerStubState, stackPath string, composeEnv, runtimeEnv []string, args ...string) ([]byte, error) {
		state.calls = append(state.calls, append([]string(nil), args...))
		if len(args) == 2 && args[0] == "config" && args[1] == "--images" {
			state.composeEnv = append([]string(nil), composeEnv...)
			state.runtimeEnv = append([]string(nil), runtimeEnv...)
			return []byte("ghcr.io/acme/api:latest\n"), nil
		}
		return nil, nil
	}, func(imageRef string) (string, error) {
		return "sha256:same", nil
	})
	defer restore()

	result := runImageRefreshAttempt(context.Background(), registry, slog.New(slog.NewTextHandler(io.Discard, nil)), testRefreshJobRequestWithPath(stackPath, "commit-1", 0))

	assert.Equal(t, composeRunStatusNoUpdate, result.Status)
	assert.NoError(t, result.Err)
	assert.Equal(t, [][]string{{"config", "--images"}, {"pull"}, {"config", "--images"}}, commands.calls)
	assert.Contains(t, commands.composeEnv, "API_TOKEN=secret-value")
	require.Len(t, commands.runtimeEnv, 1)
	assert.True(t, strings.HasPrefix(commands.runtimeEnv[0], "TLS_CERT="))
	_, err := os.Stat(strings.TrimPrefix(commands.runtimeEnv[0], "TLS_CERT="))
	assert.Error(t, err)
}

func TestComposeRunnerRunsUpWhenImageIdentitiesChange(t *testing.T) {
	stackPath := writeComposeFile(t)
	registry := testComposeRunnerRegistry()
	pulled := false
	commands, restore := stubComposeRunnerCommands(t, func(state *composeRunnerStubState, stackPath string, composeEnv, runtimeEnv []string, args ...string) ([]byte, error) {
		state.calls = append(state.calls, append([]string(nil), args...))
		switch {
		case len(args) == 2 && args[0] == "config" && args[1] == "--images":
			return []byte("ghcr.io/acme/api:latest\n"), nil
		case len(args) == 1 && args[0] == "pull":
			pulled = true
			return nil, nil
		case len(args) == 3 && args[0] == "up":
			state.composeEnv = append([]string(nil), composeEnv...)
			state.runtimeEnv = append([]string(nil), runtimeEnv...)
			return nil, nil
		default:
			return nil, nil
		}
	}, func(imageRef string) (string, error) {
		if pulled {
			return "sha256:new", nil
		}
		return "sha256:old", nil
	})
	defer restore()

	result := runImageRefreshAttempt(context.Background(), registry, slog.New(slog.NewTextHandler(io.Discard, nil)), testRefreshJobRequestWithPath(stackPath, "commit-2", 0))

	assert.Equal(t, composeRunStatusUpdated, result.Status)
	assert.NoError(t, result.Err)
	assert.Equal(t, [][]string{{"config", "--images"}, {"pull"}, {"config", "--images"}, {"up", "-d", "--remove-orphans"}}, commands.calls)
	assert.Contains(t, commands.composeEnv, "API_TOKEN=secret-value")
}

func TestComposeRunnerRetriesAfterPullFailure(t *testing.T) {
	stackPath := writeComposeFile(t)
	registry := testComposeRunnerRegistry()
	commands, restore := stubComposeRunnerCommands(t, func(state *composeRunnerStubState, stackPath string, composeEnv, runtimeEnv []string, args ...string) ([]byte, error) {
		state.calls = append(state.calls, append([]string(nil), args...))
		if len(args) == 2 && args[0] == "config" && args[1] == "--images" {
			return []byte("ghcr.io/acme/api:latest\n"), nil
		}
		if len(args) == 1 && args[0] == "pull" {
			return nil, fmt.Errorf("registry unavailable")
		}
		return nil, nil
	}, func(imageRef string) (string, error) {
		return "sha256:old", nil
	})
	defer restore()

	result := runImageRefreshAttempt(context.Background(), registry, slog.New(slog.NewTextHandler(io.Discard, nil)), testRefreshJobRequestWithPath(stackPath, "commit-3", 0))

	assert.Equal(t, composeRunStatusRetryableFailure, result.Status)
	assert.Error(t, result.Err)
	assert.Equal(t, [][]string{{"config", "--images"}, {"pull"}}, commands.calls)
}

func TestComposeRunnerDoesNotRunUpWhenPullFails(t *testing.T) {
	stackPath := writeComposeFile(t)
	registry := testComposeRunnerRegistry()
	commands, restore := stubComposeRunnerCommands(t, func(state *composeRunnerStubState, stackPath string, composeEnv, runtimeEnv []string, args ...string) ([]byte, error) {
		state.calls = append(state.calls, append([]string(nil), args...))
		if len(args) == 2 && args[0] == "config" && args[1] == "--images" {
			return []byte("ghcr.io/acme/api:latest\n"), nil
		}
		if len(args) == 1 && args[0] == "pull" {
			return nil, fmt.Errorf("registry unavailable")
		}
		return nil, nil
	}, func(imageRef string) (string, error) {
		return "sha256:old", nil
	})
	defer restore()

	_ = runImageRefreshAttempt(context.Background(), registry, slog.New(slog.NewTextHandler(io.Discard, nil)), testRefreshJobRequestWithPath(stackPath, "commit-4", 0))

	assert.NotContains(t, commands.calls, []string{"up", "-d", "--remove-orphans"})
}

func TestComposeRunnerTreatsUpFailureAsTerminal(t *testing.T) {
	stackPath := writeComposeFile(t)
	registry := testComposeRunnerRegistry()
	pulled := false
	commands, restore := stubComposeRunnerCommands(t, func(state *composeRunnerStubState, stackPath string, composeEnv, runtimeEnv []string, args ...string) ([]byte, error) {
		state.calls = append(state.calls, append([]string(nil), args...))
		switch {
		case len(args) == 2 && args[0] == "config" && args[1] == "--images":
			return []byte("ghcr.io/acme/api:latest\n"), nil
		case len(args) == 1 && args[0] == "pull":
			pulled = true
			return nil, nil
		case len(args) == 3 && args[0] == "up":
			return nil, fmt.Errorf("container failed healthcheck")
		default:
			return nil, nil
		}
	}, func(imageRef string) (string, error) {
		if pulled {
			return "sha256:new", nil
		}
		return "sha256:old", nil
	})
	defer restore()

	result := runImageRefreshAttempt(context.Background(), registry, slog.New(slog.NewTextHandler(io.Discard, nil)), testRefreshJobRequestWithPath(stackPath, "commit-5", 0))

	assert.Equal(t, composeRunStatusTerminalFailure, result.Status)
	assert.Error(t, result.Err)
	assert.Equal(t, [][]string{{"config", "--images"}, {"pull"}, {"config", "--images"}, {"up", "-d", "--remove-orphans"}}, commands.calls)
}

type composeRunnerStubState struct {
	calls      [][]string
	composeEnv []string
	runtimeEnv []string
}

func stubComposeRunnerCommands(t *testing.T, composeStub func(*composeRunnerStubState, string, []string, []string, ...string) ([]byte, error), inspectStub func(string) (string, error)) (*composeRunnerStubState, func()) {
	t.Helper()
	state := &composeRunnerStubState{}
	originalCompose := runImageRefreshComposeCommand
	originalInspect := inspectDockerImageIdentity
	runImageRefreshComposeCommand = func(stackPath string, composeEnv, runtimeEnv []string, args ...string) ([]byte, error) {
		return composeStub(state, stackPath, composeEnv, runtimeEnv, args...)
	}
	inspectDockerImageIdentity = inspectStub
	return state, func() {
		runImageRefreshComposeCommand = originalCompose
		inspectDockerImageIdentity = originalInspect
	}
}

func writeComposeFile(t *testing.T) string {
	t.Helper()
	stackPath := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(stackPath, "docker-compose.yml"), []byte("services:\n  api:\n    image: ghcr.io/acme/api:latest\n"), 0o644))
	return stackPath
}

func testComposeRunnerRegistry() core.PluginRegistry {
	return &composeRunnerPluginRegistry{plugins: []core.Plugin{
		&composeRunnerSecretPlugin{secrets: map[string]string{"API_TOKEN": "secret-value"}},
		&composeRunnerRuntimePlugin{files: []core.RuntimeFile{{EnvKey: "TLS_CERT", Filename: "tls.pem", Content: []byte("cert-data"), Mode: 0o600}}},
	}}
}

func testRefreshJobRequestWithPath(stackPath, newCommit string, delays ...time.Duration) refreshJobRequest {
	return refreshJobRequest{
		Key: refreshJobKey{FullName: "acme/api", StackPath: stackPath},
		Owner:       "acme",
		Repo:        "api",
		OldCommit:   "old",
		NewCommit:   newCommit,
		RetryDelays: delays,
	}
}
