package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

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

func TestFailExecutionPublishesFailureClassificationForPreflightError(t *testing.T) {
	executionID := fmt.Sprintf("exec-%d", time.Now().UnixNano())
	events := make(chan core.InternalEvent, 1)
	originalPublish := publishInternalEvent
	publishInternalEvent = func(_ context.Context, event core.InternalEvent) {
		if event.Details["execution_id"] == executionID {
			events <- event
		}
	}
	defer func() {
		publishInternalEvent = originalPublish
	}()

	state := newExecutionStateManager(fixedTimes(time.Date(2026, 3, 21, 10, 0, 0, 0, time.UTC)))
	reconciler := &Reconciler{
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		executionState: state,
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
	originalPublish := publishInternalEvent
	publishInternalEvent = func(_ context.Context, event core.InternalEvent) {
		if event.Details["full_name"] == "acme/api" && event.Details["status"] == string(core.ExecutionStatusFailed) {
			events <- event
		}
	}
	defer func() {
		publishInternalEvent = originalPublish
	}()

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
	originalPublish := publishInternalEvent
	publishInternalEvent = func(_ context.Context, event core.InternalEvent) {
		if event.Details["full_name"] == "acme/api" && event.Details["status"] == string(core.ExecutionStatusFailed) {
			events <- event
		}
	}
	defer func() {
		publishInternalEvent = originalPublish
	}()

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
