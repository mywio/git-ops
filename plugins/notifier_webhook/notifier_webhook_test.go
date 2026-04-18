package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mywio/git-ops/pkg/core"
	"github.com/stretchr/testify/assert"
)

func TestNormalizePatternsWebhook(t *testing.T) {
	input := []string{" notify_* ", "", "deploy_*", "notify_*", "  "}
	out := normalizePatterns(input)
	assert.Equal(t, []string{"notify_*", "deploy_*"}, out)
}

func TestParseSubscribePatternsWebhook(t *testing.T) {
	tests := []struct {
		name    string
		section map[string]any
		want    []string
	}{
		{
			name:    "missing",
			section: map[string]any{},
			want:    nil,
		},
		{
			name: "string_list",
			section: map[string]any{
				"subscribe": []string{" notify_* ", "deploy_*"},
			},
			want: []string{"notify_*", "deploy_*"},
		},
		{
			name: "any_list",
			section: map[string]any{
				"subscribe": []any{" notify_*", 123, "deploy_*", "notify_*"},
			},
			want: []string{"notify_*", "123", "deploy_*"},
		},
		{
			name: "csv_string",
			section: map[string]any{
				"subscribe": "notify_*, deploy_*",
			},
			want: []string{"notify_*", "deploy_*"},
		},
		{
			name: "scalar",
			section: map[string]any{
				"subscribe": 42,
			},
			want: []string{"42"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := parseSubscribePatterns(tt.section)
			assert.Equal(t, tt.want, out)
		})
	}
}

func TestWebhookSendUsesNormalizedNotificationPayload(t *testing.T) {
	var received core.NotificationPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()

		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.NoError(t, json.Unmarshal(body, &received))

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	plugin := &WebhookPlugin{
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		url:     server.URL,
		client:  server.Client(),
		enabled: true,
	}

	event := core.InternalEvent{
		Type:      core.EventTypeExecution,
		Source:    "reconciler",
		Repo:      "api",
		Timestamp: core.NewExecutionEvent(core.ExecutionEventInput{}).Timestamp,
		Details: map[string]any{
			"execution_id":   "exec-1",
			"full_name":      "acme/api",
			"stage":          string(core.ExecutionStageHooks),
			"status":         string(core.ExecutionStatusFailed),
			"failure_class":  string(core.FailureClassValidation),
			"alert_severity": string(core.AlertSeverityError),
			"last_error":     "hook failed",
		},
	}

	err := plugin.send(context.Background(), event)
	assert.NoError(t, err)
	assert.Equal(t, core.EventTypeExecution, received.EventType)
	assert.Equal(t, "reconciler", received.Source)
	assert.Equal(t, "api", received.Repo)
	assert.Equal(t, "exec-1", received.ExecutionID)
	assert.Equal(t, "acme/api", received.FullName)
	assert.Equal(t, "Execution failed: acme/api", received.Title)
	assert.Equal(t, "Execution ID: exec-1\nStage: hooks\nStatus: failed\nSeverity: error\nFailure Class: validation\nError: hook failed", received.Body)
	assert.Equal(t, "hook failed", received.LastError)
}

func TestWebhookSendKeepsLegacyMessageAndDetailsFields(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()

		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.NoError(t, json.Unmarshal(body, &received))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	plugin := &WebhookPlugin{
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		url:     server.URL,
		client:  server.Client(),
		enabled: true,
	}

	event := core.InternalEvent{
		Type:      core.EventTypeExecution,
		Source:    "reconciler",
		Repo:      "api",
		Message:   "Execution failed while applying hooks",
		Timestamp: time.Time{},
		Details: map[string]any{
			"execution_id": "exec-1",
			"full_name":    "acme/api",
			"status":       string(core.ExecutionStatusFailed),
		},
	}

	err := plugin.send(context.Background(), event)
	assert.NoError(t, err)
	assert.Equal(t, string(core.EventTypeExecution), received["event_type"])
	assert.Equal(t, "reconciler", received["source"])
	assert.Equal(t, "api", received["repo"])
	assert.Equal(t, "Execution failed while applying hooks", received["message"])
	assert.NotContains(t, received, "timestamp")
	assert.Contains(t, received, "details")

	details, ok := received["details"].(map[string]any)
	if assert.True(t, ok) {
		assert.Equal(t, "exec-1", details["execution_id"])
		assert.Equal(t, "acme/api", details["full_name"])
	}
}
