package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-github/v57/github"
	"github.com/mywio/git-ops/pkg/config"
	"github.com/mywio/git-ops/pkg/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutionStateAcquireRejectsOverlappingRunsForSameStack(t *testing.T) {
	now := fixedTimes(
		time.Date(2026, 3, 21, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 21, 10, 0, 1, 0, time.UTC),
	)
	state := newExecutionStateManager(now)

	first, acquired := state.acquire("acme/api", "acme", "api", "node-a", "manual")
	require.True(t, acquired)
	assert.Equal(t, "acme/api", first.FullName)
	assert.Equal(t, core.ExecutionStatusRequested, first.Status)
	assert.Equal(t, core.ExecutionStageRequested, first.Stage)
	assert.Equal(t, "node-a", first.NodeID)
	assert.Equal(t, "manual", first.Trigger)
	assert.NotEmpty(t, first.ExecutionID)

	current, ok := state.snapshot("acme/api")
	require.True(t, ok)
	assert.Equal(t, first.ExecutionID, current.ExecutionID)

	second, acquired := state.acquire("acme/api", "acme", "api", "node-a", "manual")
	assert.False(t, acquired)
	assert.Equal(t, first.ExecutionID, second.ExecutionID)
}

func TestExecutionStateCompletesAndAllowsFutureRuns(t *testing.T) {
	now := fixedTimes(
		time.Date(2026, 3, 21, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 21, 10, 0, 1, 0, time.UTC),
		time.Date(2026, 3, 21, 10, 0, 2, 0, time.UTC),
		time.Date(2026, 3, 21, 10, 0, 3, 0, time.UTC),
		time.Date(2026, 3, 21, 10, 0, 4, 0, time.UTC),
	)
	state := newExecutionStateManager(now)

	first, acquired := state.acquire("acme/api", "acme", "api", "node-a", "reconcile_stack")
	require.True(t, acquired)

	running, ok := state.markRunning("acme/api", core.ExecutionStageFetch)
	require.True(t, ok)
	assert.Equal(t, first.ExecutionID, running.ExecutionID)
	assert.Equal(t, core.ExecutionStatusRunning, running.Status)
	assert.Equal(t, core.ExecutionStageFetch, running.Stage)

	failed, ok := state.markFailed("acme/api", core.ExecutionStageHooks, errors.New("hook failed"))
	require.True(t, ok)
	assert.Equal(t, core.ExecutionStatusFailed, failed.Status)
	assert.Equal(t, core.ExecutionStageHooks, failed.Stage)
	assert.Equal(t, "hook failed", failed.LastError)

	second, acquired := state.acquire("acme/api", "acme", "api", "node-a", "reconcile_stack")
	require.True(t, acquired)
	assert.NotEqual(t, first.ExecutionID, second.ExecutionID)

	succeeded, ok := state.markSucceeded("acme/api", core.ExecutionStageComplete)
	require.True(t, ok)
	assert.Equal(t, core.ExecutionStatusSucceeded, succeeded.Status)
	assert.Equal(t, core.ExecutionStageComplete, succeeded.Stage)
	assert.Empty(t, succeeded.LastError)
}

func TestListManagedDeploymentsIncludesExecutionMetadata(t *testing.T) {
	targetDir := t.TempDir()
	repoPath := filepath.Join(targetDir, "acme", "api")
	require.NoError(t, os.MkdirAll(repoPath, 0755))

	now := fixedTimes(
		time.Date(2026, 3, 21, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 21, 10, 0, 1, 0, time.UTC),
		time.Date(2026, 3, 21, 10, 0, 2, 0, time.UTC),
	)
	state := newExecutionStateManager(now)
	snapshot, acquired := state.acquire("acme/api", "acme", "api", "node-a", "manual")
	require.True(t, acquired)
	_, ok := state.markFailed("acme/api", core.ExecutionStageComposeUp, errors.New("compose failed"))
	require.True(t, ok)

	reconciler := &Reconciler{
		cfg:            config.Config{TargetDir: targetDir},
		logger:         slog.New(slog.NewTextHandler(os.Stdout, nil)),
		executionState: state,
	}

	deployments, err := reconciler.listManagedDeployments()
	require.NoError(t, err)
	require.Len(t, deployments, 1)

	entry := deployments[0]
	assert.Equal(t, "acme", entry["owner"])
	assert.Equal(t, "api", entry["repo"])
	assert.Equal(t, repoPath, entry["path"])
	assert.Equal(t, snapshot.ExecutionID, entry["execution_id"])
	assert.Equal(t, string(core.ExecutionStatusFailed), entry["execution_status"])
	assert.Equal(t, string(core.ExecutionStageComposeUp), entry["execution_stage"])
	assert.Equal(t, "compose failed", entry["last_error"])
}

func TestFailExecutionPublishesFailedLifecycleWithUnknownFailureClassification(t *testing.T) {
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
		executionState: state,
	}
	_, acquired := state.acquire("acme/api", "acme", "api", "node-a", "manual")
	require.True(t, acquired)

	snapshot, ok := state.snapshot("acme/api")
	require.True(t, ok)
	snapshot.ExecutionID = executionID
	state.snapshots["acme/api"] = snapshot

	reconciler.failExecution(context.Background(), "acme/api", core.ExecutionStageHooks, errors.New("hook failed"))

	select {
	case event := <-events:
		assert.Equal(t, string(core.ExecutionStatusFailed), event.Details["status"])
		assert.Equal(t, string(core.FailureClassUnknown), event.Details["failure_class"])
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for execution event")
	}
}

func TestProcessLocalStateSkipsPruneWhenExecutionActive(t *testing.T) {
	targetDir := t.TempDir()
	repoPath := filepath.Join(targetDir, "acme", "api")
	require.NoError(t, os.MkdirAll(repoPath, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "docker-compose.yml"), []byte("services: {}"), 0644))

	state := newExecutionStateManager(fixedTimes(time.Date(2026, 3, 21, 10, 0, 0, 0, time.UTC)))
	_, acquired := state.acquire("acme/api", "acme", "api", "node-a", "manual")
	require.True(t, acquired)

	reconciler := &Reconciler{
		cfg:            config.Config{TargetDir: targetDir},
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		executionState: state,
	}

	reconciler.processLocalState(context.Background(), map[string]*github.Repository{}, map[string]bool{"acme/api": true})

	_, err := os.Stat(repoPath)
	assert.NoError(t, err)
}

func TestExecuteRejectsReconcileStackAfterStopBegins(t *testing.T) {
	reconciler := &Reconciler{
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		stopCh:  make(chan struct{}),
		started: true,
	}

	require.NoError(t, reconciler.Stop(context.Background()))

	res, err := reconciler.Execute(context.Background(), "reconcile_stack", map[string]interface{}{
		"owner": "acme",
		"repo":  "api",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "stopping")
	assert.Nil(t, res)
}

func fixedTimes(values ...time.Time) func() time.Time {
	index := 0
	return func() time.Time {
		if index >= len(values) {
			return values[len(values)-1]
		}
		value := values[index]
		index++
		return value
	}
}
