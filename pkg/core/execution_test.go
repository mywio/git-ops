package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewExecutionEventIncludesNormalizedMetadata(t *testing.T) {
	event := NewExecutionEvent(ExecutionEventInput{
		ExecutionID: "exec-123",
		Owner:       "me",
		Repo:        "app",
		Stage:       ExecutionStageComposeUp,
		Status:      ExecutionStatusRunning,
		NodeID:      "node-7",
		Trigger:     "webhook",
		Source:      "reconciler",
		Details: map[string]any{
			"attempt": 2,
		},
	})

	assert.Equal(t, EventTypeExecution, event.Type)
	assert.Equal(t, "reconciler", event.Source)
	assert.Equal(t, "app", event.Repo)
	assert.Equal(t, "exec-123", event.Details["execution_id"])
	assert.Equal(t, "me", event.Details["owner"])
	assert.Equal(t, "app", event.Details["repo"])
	assert.Equal(t, "me/app", event.Details["full_name"])
	assert.Equal(t, string(ExecutionStageComposeUp), event.Details["stage"])
	assert.Equal(t, string(ExecutionStatusRunning), event.Details["status"])
	assert.Equal(t, "node-7", event.Details["node_id"])
	assert.Equal(t, "webhook", event.Details["trigger"])
	assert.Equal(t, "reconciler", event.Details["source"])
	assert.Equal(t, 2, event.Details["attempt"])
}

func TestNewExecutionEventPreservesExplicitFullName(t *testing.T) {
	event := NewExecutionEvent(ExecutionEventInput{
		ExecutionID: "exec-456",
		Owner:       "me",
		Repo:        "app",
		FullName:    "custom/slug",
		Stage:       ExecutionStageFetch,
		Status:      ExecutionStatusRequested,
	})

	assert.Equal(t, "custom/slug", event.Details["full_name"])
	assert.Equal(t, "me", event.Details["owner"])
	assert.Equal(t, "app", event.Details["repo"])
}

func TestNewExecutionEventAlwaysIncludesStableKeys(t *testing.T) {
	event := NewExecutionEvent(ExecutionEventInput{
		ExecutionID: "exec-empty",
	})

	assert.Contains(t, event.Details, "execution_id")
	assert.Contains(t, event.Details, "owner")
	assert.Contains(t, event.Details, "repo")
	assert.Contains(t, event.Details, "full_name")
	assert.Contains(t, event.Details, "stage")
	assert.Contains(t, event.Details, "status")
	assert.Equal(t, "", event.Details["owner"])
	assert.Equal(t, "", event.Details["repo"])
	assert.Equal(t, "", event.Details["full_name"])
	assert.Equal(t, "", event.Details["stage"])
	assert.Equal(t, "", event.Details["status"])
}

func TestExecutionContractEnumValues(t *testing.T) {
	assert.Equal(t, "requested", string(ExecutionStatusRequested))
	assert.Equal(t, "running", string(ExecutionStatusRunning))
	assert.Equal(t, "failed", string(ExecutionStatusFailed))

	assert.Equal(t, "fetch", string(ExecutionStageFetch))
	assert.Equal(t, "compose_up", string(ExecutionStageComposeUp))

	assert.Equal(t, "transient", string(FailureClassTransient))
	assert.Equal(t, "critical", string(AlertSeverityCritical))
}
