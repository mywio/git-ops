package core

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewNotificationPayloadForExecutionEvent(t *testing.T) {
	timestamp := time.Date(2026, time.March, 21, 10, 15, 0, 0, time.UTC)
	event := InternalEvent{
		Type:      EventTypeExecution,
		Timestamp: timestamp,
		Source:    "reconciler",
		Repo:      "api",
		String:    "Execution failed while applying hooks",
		Details: map[string]any{
			"execution_id":   "exec-1",
			"owner":          "acme",
			"repo":           "api",
			"full_name":      "acme/api",
			"stage":          string(ExecutionStageHooks),
			"status":         string(ExecutionStatusFailed),
			"failure_class":  string(FailureClassValidation),
			"alert_severity": string(AlertSeverityError),
			"last_error":     "hook failed",
		},
	}

	payload := NewNotificationPayload(event)

	assert.Equal(t, EventTypeExecution, payload.EventType)
	assert.Equal(t, timestamp, payload.Timestamp)
	assert.Equal(t, "reconciler", payload.Source)
	assert.Equal(t, "api", payload.Repo)
	assert.Equal(t, "exec-1", payload.ExecutionID)
	assert.Equal(t, "acme/api", payload.FullName)
	assert.Equal(t, string(ExecutionStageHooks), payload.Stage)
	assert.Equal(t, string(ExecutionStatusFailed), payload.Status)
	assert.Equal(t, string(FailureClassValidation), payload.FailureClass)
	assert.Equal(t, string(AlertSeverityError), payload.AlertSeverity)
	assert.Equal(t, "hook failed", payload.LastError)
	assert.Equal(t, "Execution failed: acme/api", payload.Title)
	assert.Equal(t, "Execution ID: exec-1\nStage: hooks\nStatus: failed\nSeverity: error\nFailure Class: validation\nError: hook failed", payload.Body)
	assert.Equal(t, "Execution failed while applying hooks", payload.Message)
	assert.Equal(t, event.Details, payload.Details)
}

func TestNewNotificationPayloadForGenericEvent(t *testing.T) {
	timestamp := time.Date(2026, time.March, 21, 11, 0, 0, 0, time.UTC)
	event := InternalEvent{
		Type:      "notify_secret_conflict",
		Timestamp: timestamp,
		Source:    "reconciler",
		Repo:      "api",
		String:    "Secret DATABASE_URL already provided by env; skipping vault",
		Details: map[string]any{
			"key":    "DATABASE_URL",
			"winner": "env",
			"loser":  "vault",
		},
	}

	payload := NewNotificationPayload(event)

	assert.Equal(t, EventTypeName("notify_secret_conflict"), payload.EventType)
	assert.Equal(t, timestamp, payload.Timestamp)
	assert.Equal(t, "reconciler", payload.Source)
	assert.Equal(t, "api", payload.Repo)
	assert.Contains(t, payload.Title, "notify_secret_conflict")
	assert.Contains(t, payload.Title, "reconciler/api")
	assert.Equal(t, event.String, payload.Message)
	assert.Empty(t, payload.ExecutionID)
	assert.Empty(t, payload.FullName)
	assert.Empty(t, payload.Stage)
	assert.Empty(t, payload.Status)
	assert.Empty(t, payload.FailureClass)
	assert.Empty(t, payload.AlertSeverity)
	assert.Empty(t, payload.LastError)
	assert.Equal(t, event.Details, payload.Details)
	assert.Contains(t, payload.Body, "Event Type: notify_secret_conflict")
	assert.Contains(t, payload.Body, "Source: reconciler")
	assert.Contains(t, payload.Body, "Repo: api")
	assert.Contains(t, payload.Body, "Message: Secret DATABASE_URL already provided by env; skipping vault")
	assert.Contains(t, payload.Body, "Details:")
	assert.Contains(t, payload.Body, "DATABASE_URL")
	assert.Contains(t, payload.Body, "env")
	assert.Contains(t, payload.Body, "vault")
}

func TestNotificationPayloadOmitsZeroTimestampWhenMarshaled(t *testing.T) {
	payload := NewNotificationPayload(InternalEvent{
		Type:   "notify_secret_conflict",
		Source: "reconciler",
		Repo:   "api",
		String: "Secret DATABASE_URL already provided by env; skipping vault",
		Details: map[string]any{
			"key":    "DATABASE_URL",
			"winner": "env",
			"loser":  "vault",
		},
	})

	raw, err := json.Marshal(payload)
	assert.NoError(t, err)
	assert.NotContains(t, string(raw), "\"timestamp\"")
}
