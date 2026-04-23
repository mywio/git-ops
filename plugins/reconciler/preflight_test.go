package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v57/github"
	"github.com/mywio/git-ops/pkg/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunComposePreflightFailsWhenComposeFileMissing(t *testing.T) {
	repoPath := t.TempDir()

	err := runComposePreflight(repoPath, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, errComposeFileMissing)
	assert.Equal(t, core.FailureClassValidation, classifyFailure(err, core.ExecutionStageComposeUp))
}

func TestRunComposePreflightSupportsComposeYAML(t *testing.T) {
	repoPath := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "compose.yaml"), []byte("services: {}"), 0o644))

	err := runComposePreflight(repoPath, nil)

	require.NoError(t, err)
}

func TestEnsureDirectoryWritableFailsWhenPathIsNotDirectory(t *testing.T) {
	baseDir := t.TempDir()
	notDir := filepath.Join(baseDir, "not-a-directory")
	require.NoError(t, os.WriteFile(notDir, []byte("content"), 0644))

	err := ensureDirectoryWritable(notDir)

	require.Error(t, err)
	assert.ErrorIs(t, err, errTargetDirNotWritable)
	assert.Equal(t, core.FailureClassValidation, classifyFailure(err, core.ExecutionStageComposeUp))
}

func TestValidateRuntimeFileEnvFailsWhenMaterializedFileMissing(t *testing.T) {
	err := validateRuntimeFileEnv([]string{"TLS_CERT=/tmp/does-not-exist.pem"})

	require.Error(t, err)
	assert.ErrorIs(t, err, errRuntimeFilesNotMaterialized)
	assert.Equal(t, core.FailureClassValidation, classifyFailure(err, core.ExecutionStageComposeUp))
}

func TestMaterializeRuntimeFilesCleanupKeepsFilesAvailableForRunningStacks(t *testing.T) {
	env, cleanup, err := materializeRuntimeFiles([]core.RuntimeFile{{
		EnvKey:   "TLS_CERT_FILE",
		Filename: "tls.crt",
		Content:  []byte("cert-data"),
		Mode:     0o600,
	}})

	require.NoError(t, err)
	require.Len(t, env, 1)

	materializedPath := strings.TrimPrefix(env[0], "TLS_CERT_FILE=")
	_, statErr := os.Stat(materializedPath)
	require.NoError(t, statErr)

	cleanup()

	_, statErr = os.Stat(materializedPath)
	assert.NoError(t, statErr, "runtime files must remain available after compose-up returns")
}

func TestHandleRestartOnlyKeepsRuntimeFilesAvailableAfterComposeReturns(t *testing.T) {
	repoPath := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "docker-compose.yml"), []byte("services: {}"), 0o644))

	runtimePlugin := &testRuntimeFilePlugin{files: []core.RuntimeFile{{
		EnvKey:   "TLS_CERT_FILE",
		Filename: "tls.crt",
		Content:  []byte("cert-data"),
		Mode:     0o600,
	}}}

	originalComposeCommand := executeComposeCommand
	var runtimePath string
	executeComposeCommand = func(repoLocalPath string, cmdEnv, runtimeFileEnv []string, args ...string) error {
		assert.Equal(t, repoPath, repoLocalPath)
		assert.Equal(t, []string{"restart"}, args)
		require.Len(t, runtimeFileEnv, 1)
		runtimePath = strings.TrimPrefix(runtimeFileEnv[0], "TLS_CERT_FILE=")
		_, err := os.Stat(runtimePath)
		require.NoError(t, err)
		return nil
	}
	defer func() {
		executeComposeCommand = originalComposeCommand
	}()

	state := newExecutionStateManager(fixedTimes(
		time.Date(2026, 3, 21, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 21, 10, 0, 1, 0, time.UTC),
	))
	reconciler := &Reconciler{
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		registry:       &testPluginRegistry{plugins: []core.Plugin{runtimePlugin}},
		executionState: state,
	}
	_, acquired := state.acquire("acme/api", "acme", "api", "node-a", "manual")
	require.True(t, acquired)

	repo := &github.Repository{Name: github.String("api"), Owner: &github.User{Login: github.String("acme")}}
	spec := composeSpec{repoLocalPath: repoPath}

	handled := reconciler.handleRestartOnly(context.Background(), "acme/api", repo, spec, "restart_only", reconciler.logger)

	assert.True(t, handled)
	require.NotEmpty(t, runtimePath)
	_, err := os.Stat(runtimePath)
	assert.NoError(t, err, "runtime file should still exist after restart flow returns")
}

type testPluginRegistry struct {
	plugins []core.Plugin
}

func (r *testPluginRegistry) GetPlugin(name string) (core.Plugin, error) { return nil, assert.AnError }
func (r *testPluginRegistry) GetPluginsWithCapability(cap core.Capability) []core.Plugin {
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
func (r *testPluginRegistry) ListPlugins() []core.Plugin { return r.plugins }
func (r *testPluginRegistry) RegisterEventType(desc core.EventTypeDesc) error { return nil }
func (r *testPluginRegistry) Publish(ctx context.Context, event core.InternalEvent) {}
func (r *testPluginRegistry) GetMuxServer() *http.ServeMux { return nil }
func (r *testPluginRegistry) Subscribe(pattern string, handler core.Listener) {}
func (r *testPluginRegistry) GetHTTPClient() *http.Client { return nil }
func (r *testPluginRegistry) GetConfig() map[string]map[string]any { return nil }

type testRuntimeFilePlugin struct {
	files []core.RuntimeFile
}

func (p *testRuntimeFilePlugin) Name() string { return "runtime_test" }
func (p *testRuntimeFilePlugin) Description() string { return "test runtime plugin" }
func (p *testRuntimeFilePlugin) Capabilities() []core.Capability {
	return []core.Capability{core.CapabilityRuntimeFiles}
}
func (p *testRuntimeFilePlugin) Status() core.ServiceStatus { return core.StatusHealthy }
func (p *testRuntimeFilePlugin) Init(ctx context.Context, logger *slog.Logger, registry core.PluginRegistry) error {
	return nil
}
func (p *testRuntimeFilePlugin) Start(ctx context.Context) error { return nil }
func (p *testRuntimeFilePlugin) Stop(ctx context.Context) error { return nil }
func (p *testRuntimeFilePlugin) Execute(ctx context.Context, action string, params map[string]interface{}) (interface{}, error) {
	if action != "get_runtime_files" {
		return nil, fmt.Errorf("unknown action: %s", action)
	}
	return p.files, nil
}

func TestFailExecutionPublishesFailureClassificationForPreflightError(t *testing.T) {
	executionID := fmt.Sprintf("exec-%d", time.Now().UnixNano())
	events := make(chan core.InternalEvent, 1)

	state := newExecutionStateManager(fixedTimes(time.Date(2026, 3, 21, 10, 0, 0, 0, time.UTC)))
	reconciler := &Reconciler{
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		executionState: state,
		publishEvent: func(_ context.Context, event core.InternalEvent) {
			if event.Details["execution_id"] == executionID {
				events <- event
			}
		},
	}
	_, acquired := state.acquire("acme/api", "acme", "api", "node-a", "manual")
	require.True(t, acquired)

	snapshot, ok := state.snapshot("acme/api")
	require.True(t, ok)
	snapshot.ExecutionID = executionID
	state.snapshots["acme/api"] = snapshot

	reconciler.failExecution(context.Background(), "acme/api", core.ExecutionStageComposeUp, fmt.Errorf("preflight failed: %w", errComposeFileMissing))

	select {
	case event := <-events:
		assert.Equal(t, string(core.ExecutionStatusFailed), event.Details["status"])
		assert.Equal(t, string(core.FailureClassValidation), event.Details["failure_class"])
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for execution event")
	}
}

func TestClassifyFailureReturnsDependencyForMissingCommand(t *testing.T) {
	err := errors.New("docker: executable file not found in $PATH")

	assert.Equal(t, core.FailureClassDependency, classifyFailure(fmt.Errorf("compose command unavailable: %w: %w", exec.ErrNotFound, err), core.ExecutionStageComposeUp))
}

func TestClassifyFailureReturnsDependencyForUnsupportedComposeCommand(t *testing.T) {
	err := &composeCommandError{
		args:   []string{"up", "-d"},
		output: "docker: 'compose' is not a docker command.\nSee 'docker --help'\n",
		err:    &exec.ExitError{},
	}

	assert.Equal(t, core.FailureClassDependency, classifyFailure(err, core.ExecutionStageComposeUp))
}

func TestPruneServiceDeletesFolderWhenComposeDownFails(t *testing.T) {
	repoPath := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "docker-compose.yml"), []byte("services: {}"), 0644))

	events := make(chan core.InternalEvent, 1)

	originalComposeCommand := executeComposeCommand
	executeComposeCommand = func(repoLocalPath string, cmdEnv, runtimeFileEnv []string, args ...string) error {
		assert.Equal(t, repoPath, repoLocalPath)
		assert.Equal(t, []string{"down", "--remove-orphans"}, args)
		assert.Empty(t, cmdEnv)
		assert.Empty(t, runtimeFileEnv)
		return &composeCommandError{
			args:   args,
			output: "docker: 'compose' is not a docker command.\n",
			err:    &exec.ExitError{},
		}
	}
	defer func() {
		executeComposeCommand = originalComposeCommand
	}()

	state := newExecutionStateManager(fixedTimes(
		time.Date(2026, 3, 21, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 21, 10, 0, 1, 0, time.UTC),
	))
	reconciler := &Reconciler{
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		executionState: state,
		publishEvent: func(_ context.Context, event core.InternalEvent) {
			if event.Details["full_name"] == "acme/api" && event.Details["status"] == string(core.ExecutionStatusFailed) {
				events <- event
			}
		},
	}

	pruned := reconciler.pruneService(context.Background(), "acme/api", "acme", "api", repoPath)

	assert.True(t, pruned)
	_, err := os.Stat(repoPath)
	assert.ErrorIs(t, err, os.ErrNotExist)

	snapshot, ok := state.snapshot("acme/api")
	require.True(t, ok)
	assert.Equal(t, core.ExecutionStatusFailed, snapshot.Status)
	assert.Equal(t, core.ExecutionStageComposeDown, snapshot.Stage)

	select {
	case event := <-events:
		assert.Equal(t, string(core.ExecutionStatusFailed), event.Details["status"])
		assert.Equal(t, string(core.FailureClassDependency), event.Details["failure_class"])
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for prune failure event")
	}
}

func TestPruneServiceFailsWhenFolderDeletionFailsAfterComposeDown(t *testing.T) {
	repoPath := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "docker-compose.yml"), []byte("services: {}"), 0644))

	events := make(chan core.InternalEvent, 1)

	originalComposeCommand := executeComposeCommand
	executeComposeCommand = func(repoLocalPath string, cmdEnv, runtimeFileEnv []string, args ...string) error {
		assert.Equal(t, repoPath, repoLocalPath)
		assert.Equal(t, []string{"down", "--remove-orphans"}, args)
		assert.Empty(t, cmdEnv)
		assert.Empty(t, runtimeFileEnv)
		return nil
	}
	defer func() {
		executeComposeCommand = originalComposeCommand
	}()

	originalRemoveAll := removeAll
	removeAll = func(string) error {
		return errors.New("remove failed")
	}
	defer func() {
		removeAll = originalRemoveAll
	}()

	state := newExecutionStateManager(fixedTimes(
		time.Date(2026, 3, 21, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 21, 10, 0, 1, 0, time.UTC),
		time.Date(2026, 3, 21, 10, 0, 2, 0, time.UTC),
	))
	reconciler := &Reconciler{
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		executionState: state,
		publishEvent: func(_ context.Context, event core.InternalEvent) {
			if event.Details["full_name"] == "acme/api" && event.Details["status"] == string(core.ExecutionStatusFailed) {
				events <- event
			}
		},
	}

	pruned := reconciler.pruneService(context.Background(), "acme/api", "acme", "api", repoPath)

	assert.True(t, pruned)
	_, err := os.Stat(repoPath)
	assert.NoError(t, err)

	snapshot, ok := state.snapshot("acme/api")
	require.True(t, ok)
	assert.Equal(t, core.ExecutionStatusFailed, snapshot.Status)
	assert.Equal(t, core.ExecutionStageComposeDown, snapshot.Stage)

	select {
	case event := <-events:
		assert.Equal(t, string(core.ExecutionStatusFailed), event.Details["status"])
		assert.Equal(t, string(core.ExecutionStageComposeDown), event.Details["stage"])
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for prune delete failure event")
	}
}

func TestRunRemoveImagesIfPresentSkipsWhenLocalComposeStateMissing(t *testing.T) {
	repoPath := t.TempDir()
	composeCalled := false

	originalComposeCommand := executeComposeCommand
	executeComposeCommand = func(repoLocalPath string, cmdEnv, runtimeFileEnv []string, args ...string) error {
		composeCalled = true
		return nil
	}
	defer func() {
		executeComposeCommand = originalComposeCommand
	}()

	reconciler := &Reconciler{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	err := reconciler.runRemoveImagesIfPresent(context.Background(), "acme/api", repoPath, reconciler.logger)

	require.NoError(t, err)
	assert.False(t, composeCalled)
}

func TestRunRestartOnlyFailsPreflightBeforeComposeCommand(t *testing.T) {
	repoPath := t.TempDir()
	composeCalled := false

	originalComposeCommand := executeComposeCommand
	executeComposeCommand = func(repoLocalPath string, cmdEnv, runtimeFileEnv []string, args ...string) error {
		composeCalled = true
		return nil
	}
	defer func() {
		executeComposeCommand = originalComposeCommand
	}()

	state := newExecutionStateManager(fixedTimes(
		time.Date(2026, 3, 21, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 21, 10, 0, 1, 0, time.UTC),
	))
	reconciler := &Reconciler{
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		executionState: state,
	}
	_, acquired := state.acquire("acme/api", "acme", "api", "node-a", "manual")
	require.True(t, acquired)

	err := reconciler.runRestartOnly(context.Background(), "acme/api", repoPath, nil, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, errComposeFileMissing)
	assert.False(t, composeCalled)

	snapshot, ok := state.snapshot("acme/api")
	require.True(t, ok)
	assert.Equal(t, core.ExecutionStatusFailed, snapshot.Status)
	assert.Equal(t, core.ExecutionStageComposeUp, snapshot.Stage)
}

func TestRunComposeDownWithRemoveImagesFailsExecution(t *testing.T) {
	repoPath := t.TempDir()

	originalComposeCommand := executeComposeCommand
	executeComposeCommand = func(repoLocalPath string, cmdEnv, runtimeFileEnv []string, args ...string) error {
		assert.Equal(t, repoPath, repoLocalPath)
		assert.Equal(t, []string{"down", "--rmi", "all", "--remove-orphans"}, args)
		return &composeCommandError{
			args:   args,
			output: "docker: 'compose' is not a docker command.\n",
			err:    &exec.ExitError{},
		}
	}
	defer func() {
		executeComposeCommand = originalComposeCommand
	}()

	state := newExecutionStateManager(fixedTimes(
		time.Date(2026, 3, 21, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 21, 10, 0, 1, 0, time.UTC),
	))
	reconciler := &Reconciler{
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		executionState: state,
	}
	_, acquired := state.acquire("acme/api", "acme", "api", "node-a", "manual")
	require.True(t, acquired)

	err := reconciler.runComposeDown(context.Background(), "acme/api", repoPath, true)

	require.Error(t, err)
	snapshot, ok := state.snapshot("acme/api")
	require.True(t, ok)
	assert.Equal(t, core.ExecutionStatusFailed, snapshot.Status)
	assert.Equal(t, core.ExecutionStageComposeDown, snapshot.Stage)
}

func TestPruneServiceSkipsLockedStack(t *testing.T) {
	repoPath := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, ".git-ops-lock"), []byte("locked"), 0o644))

	events := make(chan core.InternalEvent, 3)
	state := newExecutionStateManager(fixedTimes(time.Date(2026, 3, 21, 10, 0, 0, 0, time.UTC), time.Date(2026, 3, 21, 10, 0, 1, 0, time.UTC)))
	reconciler := &Reconciler{
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		executionState: state,
		publishEvent: func(_ context.Context, event core.InternalEvent) {
			events <- event
		},
	}

	pruned := reconciler.pruneService(context.Background(), "acme/api", "acme", "api", repoPath)

	assert.True(t, pruned)
	_, err := os.Stat(repoPath)
	assert.NoError(t, err)

	deadline := time.After(2 * time.Second)
	foundLocked := false
	for !foundLocked {
		select {
		case event := <-events:
			if event.Type != core.EventTypeName("stack_locked") {
				continue
			}
			assert.Equal(t, "acme/api", event.Details["full_name"])
			foundLocked = true
		case <-deadline:
			t.Fatal("timed out waiting for stack_locked event")
		}
	}

	snapshot, ok := state.snapshot("acme/api")
	require.True(t, ok)
	assert.Equal(t, core.ExecutionStatusSucceeded, snapshot.Status)
}
