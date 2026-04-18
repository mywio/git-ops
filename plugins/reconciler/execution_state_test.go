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

	history := state.snapshotHistory("acme/api")
	require.Len(t, history, 2)
	assert.Equal(t, second.ExecutionID, history[0].ExecutionID)
	assert.Equal(t, core.ExecutionStatusSucceeded, history[0].Status)
	assert.Equal(t, first.ExecutionID, history[1].ExecutionID)
	assert.Equal(t, core.ExecutionStatusFailed, history[1].Status)
}

func TestExecutionStateHistoryCapsAtTenEntriesPerStack(t *testing.T) {
	times := make([]time.Time, 0, 36)
	base := time.Date(2026, 3, 21, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 36; i++ {
		times = append(times, base.Add(time.Duration(i)*time.Second))
	}
	state := newExecutionStateManager(fixedTimes(times...))

	for i := 0; i < 12; i++ {
		snapshot, acquired := state.acquire("acme/api", "acme", "api", "node-a", "reconcile_stack")
		require.True(t, acquired)

		_, ok := state.markSucceeded("acme/api", core.ExecutionStageComplete)
		require.True(t, ok)
		assert.Contains(t, snapshot.ExecutionID, "acme/api-")
	}

	history := state.snapshotHistory("acme/api")
	require.Len(t, history, 10)
	assert.Equal(t, core.ExecutionStatusSucceeded, history[0].Status)
	assert.Equal(t, core.ExecutionStatusSucceeded, history[9].Status)
	assert.True(t, history[0].UpdatedAt.After(history[9].UpdatedAt))
}

func TestSnapshotHistoryReturnsCopy(t *testing.T) {
	now := fixedTimes(
		time.Date(2026, 3, 21, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 21, 10, 0, 1, 0, time.UTC),
	)
	state := newExecutionStateManager(now)

	_, acquired := state.acquire("acme/api", "acme", "api", "node-a", "manual")
	require.True(t, acquired)
	_, ok := state.markSucceeded("acme/api", core.ExecutionStageComplete)
	require.True(t, ok)

	history := state.snapshotHistory("acme/api")
	require.Len(t, history, 1)
	history[0].Status = core.ExecutionStatusFailed

	again := state.snapshotHistory("acme/api")
	require.Len(t, again, 1)
	assert.Equal(t, core.ExecutionStatusSucceeded, again[0].Status)
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
	history, ok := entry["history"].([]executionSnapshot)
	require.True(t, ok)
	require.Len(t, history, 1)
	assert.Equal(t, snapshot.ExecutionID, history[0].ExecutionID)
	assert.Equal(t, core.ExecutionStatusFailed, history[0].Status)
}

func TestListManagedDeploymentsParsesComposeStatus(t *testing.T) {
	tests := []struct {
		name       string
		containers []composePSContainer
		wantStatus string
	}{
		{
			name: "all running",
			containers: []composePSContainer{
				{State: "running"},
				{State: "running"},
			},
			wantStatus: "running",
		},
		{
			name: "partial running",
			containers: []composePSContainer{
				{State: "running"},
				{State: "exited"},
			},
			wantStatus: "partial",
		},
		{
			name: "all stopped",
			containers: []composePSContainer{
				{State: "exited"},
				{State: "created"},
			},
			wantStatus: "stopped",
		},
		{
			name:       "no parsed containers",
			containers: nil,
			wantStatus: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targetDir := t.TempDir()
			repoPath := filepath.Join(targetDir, "acme", "api")
			require.NoError(t, os.MkdirAll(repoPath, 0755))

			originalListComposePSContainers := listComposePSContainers
			listComposePSContainers = func(path string) ([]composePSContainer, error) {
				assert.Equal(t, repoPath, path)
				return tt.containers, nil
			}
			defer func() {
				listComposePSContainers = originalListComposePSContainers
			}()

			reconciler := &Reconciler{
				cfg:    config.Config{TargetDir: targetDir},
				logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			}

			deployments, err := reconciler.listManagedDeployments()
			require.NoError(t, err)
			require.Len(t, deployments, 1)
			assert.Equal(t, tt.wantStatus, deployments[0]["status"])
		})
	}
}

func TestListDeploymentsIncludesUnmanagedRunningContainers(t *testing.T) {
	targetDir := t.TempDir()
	repoPath := filepath.Join(targetDir, "acme", "api")
	require.NoError(t, os.MkdirAll(repoPath, 0755))

	originalListComposePSContainers := listComposePSContainers
	listComposePSContainers = func(path string) ([]composePSContainer, error) {
		assert.Equal(t, repoPath, path)
		return []composePSContainer{
			{Name: "acme-api-1", State: "running"},
		}, nil
	}
	defer func() {
		listComposePSContainers = originalListComposePSContainers
	}()

	originalListDockerContainers := listDockerContainers
	listDockerContainers = func() ([]dockerPSContainer, error) {
		return []dockerPSContainer{
			{ID: "managed", Names: "acme-api-1", Image: "managed:latest", State: "running", Status: "Up 1 minute"},
			{ID: "unmanaged", Names: "postgres-dev", Image: "postgres:16", State: "running", Status: "Up 2 minutes"},
		}, nil
	}
	defer func() {
		listDockerContainers = originalListDockerContainers
	}()

	reconciler := &Reconciler{
		cfg:    config.Config{TargetDir: targetDir},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	deployments, err := reconciler.listDeployments()
	require.NoError(t, err)
	require.Len(t, deployments, 2)

	assert.Equal(t, "git-ops", deployments[0]["source"])
	assert.Equal(t, "docker", deployments[1]["source"])
	assert.Equal(t, "postgres-dev", deployments[1]["display_name"])
	assert.Equal(t, false, deployments[1]["managed"])
	assert.Equal(t, "postgres-dev", deployments[1]["container"])
}

func TestParseDockerPSOutputIgnoresMalformedLines(t *testing.T) {
	out := []byte("{\"Names\":\"db\",\"State\":\"running\"}\nnot-json\n{\"Names\":\"web\",\"State\":\"running\"}\n")

	containers := parseDockerPSOutput(out)

	require.Len(t, containers, 2)
	assert.Equal(t, []dockerPSContainer{
		{Names: "db", State: "running"},
		{Names: "web", State: "running"},
	}, containers)
}

func TestParseComposePSOutputIgnoresMalformedLines(t *testing.T) {
	out := []byte("{\"State\":\"running\"}\nnot-json\n{\"State\":\"exited\"}\n")

	containers := parseComposePSOutput(out)

	require.Len(t, containers, 2)
	assert.Equal(t, []composePSContainer{
		{State: "running"},
		{State: "exited"},
	}, containers)
}

func TestHealthContainersFromComposeUsesNameThenService(t *testing.T) {
	containers := []composePSContainer{
		{Name: "web-1", State: "running"},
		{Service: "db", State: "exited"},
		{State: "created"},
	}

	health := healthContainersFromCompose(containers)

	require.Len(t, health, 3)
	// healthContainersFromCompose sorts by normalized container name for stable comparisons.
	assert.Equal(t, "db", health[0].Name)
	assert.Equal(t, "created", health[1].State)
	assert.Equal(t, "unknown", health[1].Name)
	assert.Equal(t, "web-1", health[2].Name)
}

func TestPollStackHealthPublishesOnlyOnChange(t *testing.T) {
	targetDir := t.TempDir()
	repoPath := filepath.Join(targetDir, "acme", "api")
	require.NoError(t, os.MkdirAll(repoPath, 0755))

	originalListComposePSContainers := listComposePSContainers
	defer func() {
		listComposePSContainers = originalListComposePSContainers
	}()

	call := 0
	listComposePSContainers = func(path string) ([]composePSContainer, error) {
		assert.Equal(t, repoPath, path)
		call++
		switch call {
		case 1, 2, 3, 4:
			return []composePSContainer{{Name: "web-1", State: "running"}}, nil
		case 5, 6:
			return []composePSContainer{{Name: "web-1", State: "exited"}}, nil
		default:
			return nil, fmt.Errorf("unexpected call")
		}
	}

	events := make(chan core.InternalEvent, 4)
	reconciler := &Reconciler{
		cfg:    config.Config{TargetDir: targetDir},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		publishEvent: func(_ context.Context, event core.InternalEvent) {
			if event.Type == "stack_health" {
				events <- event
			}
		},
		lastHealth: make(map[string]stackHealthSnapshot),
	}

	reconciler.pollStackHealth(context.Background())
	reconciler.pollStackHealth(context.Background())
	reconciler.pollStackHealth(context.Background())

	close(events)
	var published []core.InternalEvent
	for event := range events {
		published = append(published, event)
	}

	require.Len(t, published, 2)
	assert.Equal(t, "running", published[0].Details["status"])
	assert.Equal(t, "stopped", published[1].Details["status"])

	containers, ok := published[1].Details["containers"].([]map[string]string)
	require.True(t, ok)
	require.Len(t, containers, 1)
	assert.Equal(t, "web-1", containers[0]["name"])
	assert.Equal(t, "exited", containers[0]["state"])
}

func TestPollStackHealthPublishesUnknownOnComposeError(t *testing.T) {
	targetDir := t.TempDir()
	repoPath := filepath.Join(targetDir, "acme", "api")
	require.NoError(t, os.MkdirAll(repoPath, 0755))

	originalListComposePSContainers := listComposePSContainers
	defer func() {
		listComposePSContainers = originalListComposePSContainers
	}()

	listComposePSContainers = func(path string) ([]composePSContainer, error) {
		assert.Equal(t, repoPath, path)
		return nil, errors.New("compose unavailable")
	}

	events := make(chan core.InternalEvent, 1)
	reconciler := &Reconciler{
		cfg:    config.Config{TargetDir: targetDir},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		publishEvent: func(_ context.Context, event core.InternalEvent) {
			if event.Type == "stack_health" {
				events <- event
			}
		},
		lastHealth: make(map[string]stackHealthSnapshot),
	}

	reconciler.pollStackHealth(context.Background())

	select {
	case event := <-events:
		assert.Equal(t, "unknown", event.Details["status"])
		containers, ok := event.Details["containers"].([]map[string]string)
		require.True(t, ok)
		assert.Len(t, containers, 0)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stack_health event")
	}
}

func TestFailExecutionPublishesFailedLifecycleWithUnknownFailureClassification(t *testing.T) {
	executionID := fmt.Sprintf("exec-%d", time.Now().UnixNano())
	events := make(chan core.InternalEvent, 1)

	state := newExecutionStateManager(fixedTimes(time.Date(2026, 3, 21, 10, 0, 0, 0, time.UTC)))
	reconciler := &Reconciler{
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
