package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mywio/git-ops/pkg/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type webhookTestRegistry struct {
	config map[string]map[string]any
	events []core.InternalEvent
}

func (r *webhookTestRegistry) GetPlugin(name string) (core.Plugin, error) { return nil, nil }
func (r *webhookTestRegistry) GetPluginsWithCapability(cap core.Capability) []core.Plugin {
	return nil
}
func (r *webhookTestRegistry) ListPlugins() []core.Plugin { return nil }
func (r *webhookTestRegistry) RegisterEventType(desc core.EventTypeDesc) error {
	return nil
}
func (r *webhookTestRegistry) Publish(ctx context.Context, event core.InternalEvent) {
	r.events = append(r.events, event)
}
func (r *webhookTestRegistry) GetMuxServer() *http.ServeMux                    { return http.NewServeMux() }
func (r *webhookTestRegistry) Subscribe(pattern string, handler core.Listener) {}
func (r *webhookTestRegistry) GetHTTPClient() *http.Client                     { return http.DefaultClient }
func (r *webhookTestRegistry) GetConfig() map[string]map[string]any            { return r.config }

func TestWebhookTriggerAllowsRequestsOutsideMinimumInterval(t *testing.T) {
	registry := &webhookTestRegistry{
		config: map[string]map[string]any{
			"webhook_trigger": {
				"rate_limit": "30s",
			},
		},
	}

	nowValues := []time.Time{
		time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 18, 10, 0, 31, 0, time.UTC),
	}
	index := 0
	plugin := &WebhookTriggerPlugin{
		now: func() time.Time {
			value := nowValues[index]
			index++
			return value
		},
	}
	require.NoError(t, plugin.Init(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), registry))

	first := httptest.NewRequest(http.MethodPost, "/reconcile", nil)
	first.RemoteAddr = "127.0.0.1:1234"
	firstRec := httptest.NewRecorder()
	plugin.handleReconcile(firstRec, first)
	assert.Equal(t, http.StatusAccepted, firstRec.Code)

	second := httptest.NewRequest(http.MethodPost, "/reconcile", nil)
	second.RemoteAddr = "127.0.0.1:1234"
	secondRec := httptest.NewRecorder()
	plugin.handleReconcile(secondRec, second)
	assert.Equal(t, http.StatusAccepted, secondRec.Code)
}

func TestWebhookTriggerRateLimitsAcceptedRequests(t *testing.T) {
	registry := &webhookTestRegistry{
		config: map[string]map[string]any{
			"webhook_trigger": {
				"rate_limit": "30s",
			},
		},
	}

	nowValues := []time.Time{
		time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 18, 10, 0, 10, 0, time.UTC),
	}
	index := 0
	plugin := &WebhookTriggerPlugin{
		now: func() time.Time {
			value := nowValues[index]
			index++
			return value
		},
	}
	require.NoError(t, plugin.Init(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), registry))

	first := httptest.NewRequest(http.MethodPost, "/reconcile", nil)
	first.RemoteAddr = "127.0.0.1:1234"
	firstRec := httptest.NewRecorder()
	plugin.handleReconcile(firstRec, first)
	require.Equal(t, http.StatusAccepted, firstRec.Code)

	second := httptest.NewRequest(http.MethodPost, "/reconcile", nil)
	second.RemoteAddr = "127.0.0.1:1234"
	secondRec := httptest.NewRecorder()
	plugin.handleReconcile(secondRec, second)
	assert.Equal(t, http.StatusTooManyRequests, secondRec.Code)
	assert.Equal(t, "20", secondRec.Header().Get("Retry-After"))

	require.Len(t, registry.events, 2)
	assert.Equal(t, core.EventTypeName("reconcile_now"), registry.events[0].Type)
	assert.Equal(t, core.EventTypeName("webhook_received"), registry.events[1].Type)
}

func TestWebhookTriggerAuthRunsBeforeRateLimit(t *testing.T) {
	registry := &webhookTestRegistry{
		config: map[string]map[string]any{
			"webhook_trigger": {
				"token":      "secret-token",
				"rate_limit": "30s",
			},
		},
	}

	plugin := &WebhookTriggerPlugin{
		now: func() time.Time {
			return time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC)
		},
	}
	require.NoError(t, plugin.Init(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), registry))

	req := httptest.NewRequest(http.MethodPost, "/reconcile", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	plugin.handleReconcile(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Len(t, registry.events, 0)
	assert.True(t, plugin.lastAccepted.IsZero())
}
