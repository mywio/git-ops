package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-github/v57/github"
	"github.com/mywio/git-ops/pkg/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommitTrackerEmitsEventAfterSuccessfulNoOpCommitAdvance(t *testing.T) {
	repo := &github.Repository{Name: github.String("api"), Owner: &github.User{Login: github.String("acme")}, DefaultBranch: github.String("main")}

	targetDir := t.TempDir()
	stackPath := filepath.Join(targetDir, "acme", "api")
	reconciler, events := newCommitTrackingTestReconciler(t)
	_, acquired := reconciler.executionState.acquire("acme/api", "acme", "api", "node-a", "reconcile")
	require.True(t, acquired)

	reconciler.completeSuccessfulStack(context.Background(), "acme/api", repo, stackPath, "sha-1", false)

	commitEvent := waitForEventType(t, events, core.EventTypeName("stack_commit_changed"))
	require.NotNil(t, commitEvent)
	assert.Equal(t, "reconciler", commitEvent.Source)
	assert.False(t, commitEvent.Timestamp.IsZero())
	assert.Equal(t, "acme", commitEvent.Details["owner"])
	assert.Equal(t, "api", commitEvent.Details["repo"])
	assert.Equal(t, "acme/api", commitEvent.Details["full_name"])
	assert.Equal(t, stackPath, commitEvent.Details["stack_path"])
	assert.Equal(t, "", commitEvent.Details["old_commit"])
	assert.Equal(t, "sha-1", commitEvent.Details["new_commit"])
	assert.Equal(t, false, commitEvent.Details["compose_changed"])
}

func TestCommitTrackerEmitsEventAfterSuccessfulComposeChangingDeploy(t *testing.T) {
	repo := &github.Repository{Name: github.String("api"), Owner: &github.User{Login: github.String("acme")}, DefaultBranch: github.String("main")}

	targetDir := t.TempDir()
	stackPath := filepath.Join(targetDir, "acme", "api")
	reconciler, events := newCommitTrackingTestReconciler(t)
	_, acquired := reconciler.executionState.acquire("acme/api", "acme", "api", "node-a", "reconcile")
	require.True(t, acquired)

	reconciler.completeSuccessfulStack(context.Background(), "acme/api", repo, stackPath, "sha-2", true)

	commitEvent := waitForEventType(t, events, core.EventTypeName("stack_commit_changed"))
	require.NotNil(t, commitEvent)
	assert.Equal(t, true, commitEvent.Details["compose_changed"])
	assert.Equal(t, "sha-2", commitEvent.Details["new_commit"])
}

func TestCommitTrackerDoesNotEmitWhenCommitUnchanged(t *testing.T) {
	repo := &github.Repository{Name: github.String("api"), Owner: &github.User{Login: github.String("acme")}, DefaultBranch: github.String("main")}

	targetDir := t.TempDir()
	stackPath := filepath.Join(targetDir, "acme", "api")
	reconciler, events := newCommitTrackingTestReconciler(t)

	_, acquired := reconciler.executionState.acquire("acme/api", "acme", "api", "node-a", "reconcile")
	require.True(t, acquired)
	reconciler.completeSuccessfulStack(context.Background(), "acme/api", repo, stackPath, "sha-1", false)
	_ = waitForEventType(t, events, core.EventTypeName("stack_commit_changed"))

	_, acquired = reconciler.executionState.acquire("acme/api", "acme", "api", "node-a", "reconcile")
	require.True(t, acquired)
	reconciler.completeSuccessfulStack(context.Background(), "acme/api", repo, stackPath, "sha-1", false)

	assert.NoError(t, ensureNoEventType(events, core.EventTypeName("stack_commit_changed"), 200*time.Millisecond))
}

func TestCommitTrackerUsesTopLevelTimestampAndSourceInsteadOfDetailsFields(t *testing.T) {
	repo := &github.Repository{Name: github.String("api"), Owner: &github.User{Login: github.String("acme")}, DefaultBranch: github.String("main")}

	targetDir := t.TempDir()
	stackPath := filepath.Join(targetDir, "acme", "api")
	reconciler, events := newCommitTrackingTestReconciler(t)
	_, acquired := reconciler.executionState.acquire("acme/api", "acme", "api", "node-a", "reconcile")
	require.True(t, acquired)

	reconciler.completeSuccessfulStack(context.Background(), "acme/api", repo, stackPath, "sha-3", false)

	commitEvent := waitForEventType(t, events, core.EventTypeName("stack_commit_changed"))
	require.NotNil(t, commitEvent)
	_, hasTimestamp := commitEvent.Details["event_timestamp"]
	_, hasSource := commitEvent.Details["source"]
	assert.False(t, hasTimestamp)
	assert.False(t, hasSource)
	assert.Equal(t, "reconciler", commitEvent.Source)
	assert.False(t, commitEvent.Timestamp.IsZero())
}

func newCommitTrackingTestReconciler(t *testing.T) (*Reconciler, chan core.InternalEvent) {
	t.Helper()
	events := make(chan core.InternalEvent, 10)

	return &Reconciler{
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		executionState: newExecutionStateManager(fixedTimes(time.Date(2026, 3, 22, 10, 0, 0, 0, time.UTC), time.Date(2026, 3, 22, 10, 0, 1, 0, time.UTC), time.Date(2026, 3, 22, 10, 0, 2, 0, time.UTC))),
		commitTracker:  newCommitTracker(),
		publishEvent: func(_ context.Context, event core.InternalEvent) {
			events <- event
		},
	}, events
}

func waitForEventType(t *testing.T, events <-chan core.InternalEvent, eventType core.EventTypeName) *core.InternalEvent {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Type == eventType {
				return &event
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", eventType)
		}
	}
}

func ensureNoEventType(events <-chan core.InternalEvent, eventType core.EventTypeName, wait time.Duration) error {
	deadline := time.After(wait)
	for {
		select {
		case event := <-events:
			if event.Type == eventType {
				return assert.AnError
			}
		case <-deadline:
			return nil
		}
	}
}
