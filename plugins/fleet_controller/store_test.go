package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mywio/git-ops/pkg/fleetapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFleetEnrollmentIsPoolScopedAndSingleUse(t *testing.T) {
	store, err := openFleetStore(filepath.Join(t.TempDir(), "fleet.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.close()) })
	ctx := context.Background()
	require.NoError(t, store.createEnrollment(ctx, "secret", "prod", time.Now().Add(time.Minute)))
	record := agentRecord{ID: "agent-1", Facts: fleetapi.AgentFacts{NodeName: "node", Pool: "prod"}, LastSeen: time.Now()}
	require.NoError(t, store.enroll(ctx, "secret", record))
	assert.Error(t, store.enroll(ctx, "secret", agentRecord{ID: "agent-2", Facts: record.Facts, LastSeen: time.Now()}))
}

func TestFleetTaskLeaseIsBoundToAgent(t *testing.T) {
	store, err := openFleetStore(filepath.Join(t.TempDir(), "fleet.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.close()) })
	ctx := context.Background()
	task := fleetapi.Task{ID: "task-1", Type: "runner.start", Generation: 1, ExpiresAt: time.Now().Add(time.Minute)}
	require.NoError(t, store.queueTask(ctx, task, "agent-1"))
	claimed, err := store.claimTask(ctx, "agent-1", time.Minute)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	assert.True(t, store.taskLeasedTo(ctx, task.ID, "agent-1"))
	assert.False(t, store.taskLeasedTo(ctx, task.ID, "agent-2"))
}
