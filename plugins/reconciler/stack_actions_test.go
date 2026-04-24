package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mywio/git-ops/pkg/config"
	"github.com/mywio/git-ops/pkg/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildManagedDeploymentIncludesDisabledState(t *testing.T) {
	repoPath := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repoPath, ".git-ops"), 0o755))
	require.NoError(t, saveStackState(repoPath, stackState{Disabled: true}))

	reconciler := &Reconciler{}
	deployment := reconciler.buildManagedDeployment("acme", "api", repoPath)

	assert.Equal(t, true, deployment["disabled"])
}

func TestRunStackControlActionDisablePersistsDisabledState(t *testing.T) {
	repoRoot := t.TempDir()
	repoPath := filepath.Join(repoRoot, "acme", "api")
	require.NoError(t, os.MkdirAll(repoPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "docker-compose.yml"), []byte("services: {}"), 0o644))

	originalComposeCommand := executeComposeCommand
	t.Cleanup(func() { executeComposeCommand = originalComposeCommand })
	executeComposeCommand = func(repoLocalPath string, cmdEnv, runtimeFileEnv []string, args ...string) error {
		assert.Equal(t, repoPath, repoLocalPath)
		assert.Equal(t, []string{"down", "--remove-orphans"}, args)
		return nil
	}

	state := newExecutionStateManager(fixedTimes(
		time.Date(2026, 4, 24, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 24, 10, 0, 1, 0, time.UTC),
		time.Date(2026, 4, 24, 10, 0, 2, 0, time.UTC),
	))
	reconciler := &Reconciler{
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		executionState: state,
		cfg:            config.Config{TargetDir: repoRoot},
	}

	reconciler.runStackControlAction(context.Background(), "acme", "api", "disable_stack")

	assert.True(t, isStackDisabled(repoPath))
	snapshot, ok := state.snapshot("acme/api")
	require.True(t, ok)
	assert.Equal(t, core.ExecutionStatusSucceeded, snapshot.Status)
}

func TestRunStackControlActionStartFailsForDisabledStack(t *testing.T) {
	repoRoot := t.TempDir()
	repoPath := filepath.Join(repoRoot, "acme", "api")
	require.NoError(t, os.MkdirAll(repoPath, 0o755))
	require.NoError(t, saveStackState(repoPath, stackState{Disabled: true}))

	state := newExecutionStateManager(fixedTimes(
		time.Date(2026, 4, 24, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 24, 10, 0, 1, 0, time.UTC),
	))
	reconciler := &Reconciler{
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		executionState: state,
		cfg:            config.Config{TargetDir: repoRoot},
	}

	reconciler.runStackControlAction(context.Background(), "acme", "api", "start_stack")

	snapshot, ok := state.snapshot("acme/api")
	require.True(t, ok)
	assert.Equal(t, core.ExecutionStatusFailed, snapshot.Status)
	assert.Contains(t, snapshot.LastError, "disabled")
}

func TestRunStackControlActionStopRunsComposeDown(t *testing.T) {
	repoRoot := t.TempDir()
	repoPath := filepath.Join(repoRoot, "acme", "api")
	require.NoError(t, os.MkdirAll(repoPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "docker-compose.yml"), []byte("services: {}"), 0o644))

	originalComposeCommand := executeComposeCommand
	t.Cleanup(func() { executeComposeCommand = originalComposeCommand })
	executeComposeCommand = func(repoLocalPath string, cmdEnv, runtimeFileEnv []string, args ...string) error {
		assert.Equal(t, repoPath, repoLocalPath)
		assert.Equal(t, []string{"down", "--remove-orphans"}, args)
		return nil
	}

	state := newExecutionStateManager(fixedTimes(
		time.Date(2026, 4, 24, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 24, 10, 0, 1, 0, time.UTC),
		time.Date(2026, 4, 24, 10, 0, 2, 0, time.UTC),
	))
	reconciler := &Reconciler{
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		executionState: state,
		cfg:            config.Config{TargetDir: repoRoot},
	}

	reconciler.runStackControlAction(context.Background(), "acme", "api", "stop_stack")

	snapshot, ok := state.snapshot("acme/api")
	require.True(t, ok)
	assert.Equal(t, core.ExecutionStatusSucceeded, snapshot.Status)
	assert.Equal(t, core.ExecutionStageComplete, snapshot.Stage)
}

func TestRunStackControlActionRestartRunsComposeRestart(t *testing.T) {
	repoRoot := t.TempDir()
	repoPath := filepath.Join(repoRoot, "acme", "api")
	require.NoError(t, os.MkdirAll(repoPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "docker-compose.yml"), []byte("services: {}"), 0o644))

	runtimePlugin := &testRuntimeFilePlugin{files: []core.RuntimeFile{{
		EnvKey:   "TLS_CERT_FILE",
		Filename: "tls.crt",
		Content:  []byte("cert-data"),
		Mode:     0o600,
	}}}

	originalComposeCommand := executeComposeCommand
	t.Cleanup(func() { executeComposeCommand = originalComposeCommand })
	executeComposeCommand = func(repoLocalPath string, cmdEnv, runtimeFileEnv []string, args ...string) error {
		assert.Equal(t, repoPath, repoLocalPath)
		assert.Equal(t, []string{"restart"}, args)
		require.Len(t, runtimeFileEnv, 1)
		return nil
	}

	state := newExecutionStateManager(fixedTimes(
		time.Date(2026, 4, 24, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 24, 10, 0, 1, 0, time.UTC),
		time.Date(2026, 4, 24, 10, 0, 2, 0, time.UTC),
	))
	reconciler := &Reconciler{
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		registry:       &testPluginRegistry{plugins: []core.Plugin{runtimePlugin}},
		executionState: state,
		cfg:            config.Config{TargetDir: repoRoot},
	}

	reconciler.runStackControlAction(context.Background(), "acme", "api", "restart_stack")

	snapshot, ok := state.snapshot("acme/api")
	require.True(t, ok)
	assert.Equal(t, core.ExecutionStatusSucceeded, snapshot.Status)
}

func TestRunStackControlActionEnableClearsDisabledState(t *testing.T) {
	repoRoot := t.TempDir()
	repoPath := filepath.Join(repoRoot, "acme", "api")
	require.NoError(t, os.MkdirAll(repoPath, 0o755))
	require.NoError(t, saveStackState(repoPath, stackState{Disabled: true}))

	state := newExecutionStateManager(fixedTimes(
		time.Date(2026, 4, 24, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 24, 10, 0, 1, 0, time.UTC),
		time.Date(2026, 4, 24, 10, 0, 2, 0, time.UTC),
	))
	reconciler := &Reconciler{
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		executionState: state,
		cfg:            config.Config{TargetDir: repoRoot},
	}

	reconciler.runStackControlAction(context.Background(), "acme", "api", "enable_stack")

	assert.False(t, isStackDisabled(repoPath))
	snapshot, ok := state.snapshot("acme/api")
	require.True(t, ok)
	assert.Equal(t, core.ExecutionStatusSucceeded, snapshot.Status)
}

func TestRegisterEventsStartEventTriggersStackAction(t *testing.T) {
	repoRoot := t.TempDir()
	repoPath := filepath.Join(repoRoot, "acme", "api")
	require.NoError(t, os.MkdirAll(repoPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "docker-compose.yml"), []byte("services: {}"), 0o644))

	originalComposeCommand := executeComposeCommand
	t.Cleanup(func() { executeComposeCommand = originalComposeCommand })
	called := make(chan []string, 1)
	executeComposeCommand = func(repoLocalPath string, cmdEnv, runtimeFileEnv []string, args ...string) error {
		assert.Equal(t, repoPath, repoLocalPath)
		called <- args
		return nil
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := core.NewModuleManager(logger)
	reconciler := &Reconciler{
		logger:         logger,
		registry:       mgr,
		executionState: newExecutionStateManager(time.Now),
		cfg:            config.Config{TargetDir: repoRoot},
	}
	require.NoError(t, reconciler.registerEvents(mgr))

	mgr.Publish(context.Background(), core.InternalEvent{
		Type:   "stack_start_requested",
		Source: "test",
		Details: map[string]any{
			"owner": "acme",
			"repo":  "api",
		},
	})

	select {
	case args := <-called:
		assert.Equal(t, []string{"up", "-d"}, args)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stack start action")
	}
}
