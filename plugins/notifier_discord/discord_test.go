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
	"github.com/stretchr/testify/require"
)

type discordTestRegistry struct {
	config      map[string]map[string]any
	subscribers []string
}

func (r *discordTestRegistry) GetPlugin(name string) (core.Plugin, error) { return nil, nil }
func (r *discordTestRegistry) GetPluginsWithCapability(cap core.Capability) []core.Plugin {
	return nil
}
func (r *discordTestRegistry) ListPlugins() []core.Plugin                            { return nil }
func (r *discordTestRegistry) RegisterEventType(desc core.EventTypeDesc) error       { return nil }
func (r *discordTestRegistry) Publish(ctx context.Context, event core.InternalEvent) {}
func (r *discordTestRegistry) GetMuxServer() *http.ServeMux                          { return http.NewServeMux() }
func (r *discordTestRegistry) Subscribe(pattern string, handler core.Listener) {
	r.subscribers = append(r.subscribers, pattern)
}
func (r *discordTestRegistry) GetHTTPClient() *http.Client          { return http.DefaultClient }
func (r *discordTestRegistry) GetConfig() map[string]map[string]any { return r.config }

func TestDiscordNotifierDefaultsToNotifySubscriptions(t *testing.T) {
	registry := &discordTestRegistry{
		config: map[string]map[string]any{
			"discord": {
				"webhook_url": "https://discord.example/webhook",
			},
		},
	}

	notifier := &DiscordNotifier{}
	err := notifier.Init(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), registry)

	require.NoError(t, err)
	assert.True(t, notifier.enabled)
	assert.Equal(t, []string{"notify_*"}, notifier.subscriptions)
	assert.Equal(t, []string{"notify_*"}, registry.subscribers)
}

func TestDiscordNotifierSendsEmbedPayload(t *testing.T) {
	var payload discordWebhookPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	notifier := &DiscordNotifier{
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		client:     server.Client(),
		webhookURL: server.URL,
		enabled:    true,
	}

	event := core.InternalEvent{
		Type:      core.EventTypeExecution,
		Timestamp: time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC),
		Source:    "reconciler",
		Repo:      "api",
		Message:   "deploy failed",
		Details: map[string]any{
			"full_name":      "acme/api",
			"execution_id":   "exec-1",
			"status":         "failed",
			"stage":          "compose_up",
			"alert_severity": "error",
			"last_error":     "compose failed",
		},
	}

	err := notifier.send(context.Background(), event)

	require.NoError(t, err)
	require.Len(t, payload.Embeds, 1)
	embed := payload.Embeds[0]
	assert.Equal(t, "Execution failed: acme/api", embed.Title)
	assert.Equal(t, 0xEF4444, embed.Color)
	assert.NotEmpty(t, embed.Description)
	require.Len(t, embed.Fields, 4)
	assert.Equal(t, "Event", embed.Fields[0].Name)
	assert.Equal(t, "execution", embed.Fields[0].Value)
}

func TestDiscordNotifierExecuteNotify(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	notifier := &DiscordNotifier{
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		client:     server.Client(),
		webhookURL: server.URL,
		enabled:    true,
	}

	res, err := notifier.Execute(context.Background(), "notify", map[string]any{
		"event": core.InternalEvent{Type: "notify_test", Source: "test"},
	})

	require.NoError(t, err)
	assert.Equal(t, map[string]string{"status": "delivered"}, res)
}
