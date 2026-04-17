package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mywio/git-ops/pkg/core"
	"github.com/stretchr/testify/assert"
)

func TestNormalizePatternsPushover(t *testing.T) {
	input := []string{" notify_* ", "", "deploy_*", "notify_*", "  "}
	out := normalizePatterns(input)
	assert.Equal(t, []string{"notify_*", "deploy_*"}, out)
}

func TestParseSubscribePatternsPushover(t *testing.T) {
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

func TestPushoverSendBuildsTitleAndMessageFromNormalizedPayload(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()

		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.NoError(t, json.Unmarshal(body, &received))

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := &PushoverNotifier{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		client: server.Client(),
		token:  core.NewSecret("token-123"),
		user:   "user-456",
		apiURL: server.URL,
	}

	event := core.InternalEvent{
		Type:   core.EventTypeExecution,
		Source: "reconciler",
		Repo:   "api",
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

	err := notifier.send(context.Background(), event)
	assert.NoError(t, err)
	assert.Equal(t, "token-123", received["token"])
	assert.Equal(t, "user-456", received["user"])
	assert.Equal(t, "Execution failed: acme/api", received["title"])
	assert.Equal(t, "Execution ID: exec-1\nStage: hooks\nStatus: failed\nSeverity: error\nFailure Class: validation\nError: hook failed", received["message"])
}

func TestPushoverSendKeepsGenericEventContext(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()

		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.NoError(t, json.Unmarshal(body, &received))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := &PushoverNotifier{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		client: server.Client(),
		token:  core.NewSecret("token-123"),
		user:   "user-456",
		apiURL: server.URL,
	}

	event := core.InternalEvent{
		Type:   core.EventTypeName("notify_secret_conflict"),
		Source: "reconciler",
		Repo:   "api",
		Details: map[string]any{
			"key":    "DATABASE_URL",
			"winner": "env",
			"loser":  "vault",
		},
	}

	err := notifier.send(context.Background(), event)
	assert.NoError(t, err)
	assert.Contains(t, received["title"], "notify_secret_conflict")
	assert.Contains(t, received["title"], "reconciler")
	assert.Contains(t, received["title"], "api")
	assert.Contains(t, received["message"], "notify_secret_conflict")
	assert.Contains(t, received["message"], "reconciler")
	assert.Contains(t, received["message"], "api")
	assert.Contains(t, received["message"], "DATABASE_URL")
	assert.Contains(t, received["message"], "env")
	assert.Contains(t, received["message"], "vault")
}
