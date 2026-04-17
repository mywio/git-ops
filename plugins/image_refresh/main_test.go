package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/mywio/git-ops/pkg/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type imageRefreshMockRegistry struct {
	cfg         map[string]map[string]any
	subs        map[string]core.Listener
	registered  []core.EventTypeDesc
}

func (m *imageRefreshMockRegistry) GetPlugin(name string) (core.Plugin, error) { return nil, assert.AnError }
func (m *imageRefreshMockRegistry) GetPluginsWithCapability(cap core.Capability) []core.Plugin { return nil }
func (m *imageRefreshMockRegistry) ListPlugins() []core.Plugin { return nil }
func (m *imageRefreshMockRegistry) RegisterEventType(desc core.EventTypeDesc) error {
	m.registered = append(m.registered, desc)
	return nil
}
func (m *imageRefreshMockRegistry) Publish(ctx context.Context, event core.InternalEvent) {}
func (m *imageRefreshMockRegistry) GetMuxServer() *http.ServeMux { return nil }
func (m *imageRefreshMockRegistry) Subscribe(pattern string, handler core.Listener) {
	if m.subs == nil { m.subs = map[string]core.Listener{} }
	m.subs[pattern] = handler
}
func (m *imageRefreshMockRegistry) GetHTTPClient() *http.Client { return nil }
func (m *imageRefreshMockRegistry) GetConfig() map[string]map[string]any { return m.cfg }

type scheduledRequestRecorder struct {
	requests []refreshJobRequest
}

func (r *scheduledRequestRecorder) Schedule(req refreshJobRequest) error {
	r.requests = append(r.requests, req)
	return nil
}

func TestImageRefreshPluginIgnoresComposeChangingCommitEvents(t *testing.T) {
	registry := &imageRefreshMockRegistry{cfg: map[string]map[string]any{"image_refresh": {"enabled": true, "retry_delays_minutes": []any{0.0, 1.0}}}}
	plugin := &ImageRefreshPlugin{jobs: &scheduledRequestRecorder{}}
	require.NoError(t, plugin.Init(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), registry))
	require.NoError(t, plugin.Start(context.Background()))

	handler := registry.subs["stack_commit_changed"]
	require.NotNil(t, handler)
	handler(context.Background(), core.InternalEvent{Type: core.EventTypeName("stack_commit_changed"), Source: "reconciler", Details: map[string]any{"owner": "acme", "repo": "api", "full_name": "acme/api", "stack_path": "/tmp/acme/api", "old_commit": "old", "new_commit": "new", "compose_changed": true}})

	recorder := plugin.jobs.(*scheduledRequestRecorder)
	assert.Len(t, recorder.requests, 0)
}

func TestImageRefreshPluginSchedulesJobForComposeUnchangedCommitEvent(t *testing.T) {
	registry := &imageRefreshMockRegistry{cfg: map[string]map[string]any{"image_refresh": {"enabled": true, "retry_delays_minutes": []any{0.0, 1.0, 2.0}}}}
	recorder := &scheduledRequestRecorder{}
	plugin := &ImageRefreshPlugin{jobs: recorder}
	require.NoError(t, plugin.Init(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), registry))
	require.NoError(t, plugin.Start(context.Background()))

	handler := registry.subs["stack_commit_changed"]
	require.NotNil(t, handler)
	handler(context.Background(), core.InternalEvent{Type: core.EventTypeName("stack_commit_changed"), Source: "reconciler", Details: map[string]any{"owner": "acme", "repo": "api", "full_name": "acme/api", "stack_path": "/tmp/acme/api", "old_commit": "old", "new_commit": "new", "compose_changed": false}})

	require.Len(t, recorder.requests, 1)
	assert.Equal(t, "acme/api", recorder.requests[0].Key.FullName)
	assert.Equal(t, "/tmp/acme/api", recorder.requests[0].Key.StackPath)
	assert.Equal(t, []time.Duration{0, time.Minute, 2 * time.Minute}, recorder.requests[0].RetryDelays)
}

func TestImageRefreshPluginEmitsScheduledEventWithRequiredFields(t *testing.T) {
	events := make(chan core.InternalEvent, 2)

	registry := &imageRefreshMockRegistry{cfg: map[string]map[string]any{"image_refresh": {"enabled": true, "retry_delays_minutes": []any{0.0, 1.0}}}}
	recorder := &scheduledRequestRecorder{}
	plugin := &ImageRefreshPlugin{
		jobs: recorder,
		publishEvent: func(_ context.Context, event core.InternalEvent) {
			events <- event
		},
	}
	require.NoError(t, plugin.Init(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), registry))
	require.NoError(t, plugin.Start(context.Background()))

	handler := registry.subs["stack_commit_changed"]
	require.NotNil(t, handler)
	handler(context.Background(), core.InternalEvent{Type: core.EventTypeName("stack_commit_changed"), Source: "reconciler", Details: map[string]any{"owner": "acme", "repo": "api", "full_name": "acme/api", "stack_path": "/tmp/acme/api", "old_commit": "old", "new_commit": "new", "compose_changed": false}})

	event := <-events
	assert.Equal(t, core.EventTypeName("image_refresh_scheduled"), event.Type)
	assert.Equal(t, "image_refresh", event.Source)
	assert.False(t, event.Timestamp.IsZero())
	assert.Equal(t, "acme", event.Details["owner"])
	assert.Equal(t, "api", event.Details["repo"])
	assert.Equal(t, "acme/api", event.Details["full_name"])
	assert.Equal(t, "/tmp/acme/api", event.Details["stack_path"])
	assert.Equal(t, "old", event.Details["old_commit"])
	assert.Equal(t, "new", event.Details["new_commit"])
	assert.Equal(t, float64(1), event.Details["attempt"])
	assert.Equal(t, "0s", event.Details["retry_delay"])
}

func TestImageRefreshPluginRegistersPhaseOneEventTypes(t *testing.T) {
	registry := &imageRefreshMockRegistry{cfg: map[string]map[string]any{"image_refresh": {"enabled": true, "retry_delays_minutes": []any{0.0, 1.0}}}}
	plugin := &ImageRefreshPlugin{jobs: &scheduledRequestRecorder{}}
	require.NoError(t, plugin.Init(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), registry))

	registered := map[core.EventTypeName]core.EventTypeDesc{}
	for _, desc := range registry.registered {
		registered[desc.Name] = desc
	}
	for _, name := range []core.EventTypeName{"image_refresh_scheduled", "image_refresh_retrying", "image_refresh_no_update", "image_refresh_update_found", "image_refresh_restarting", "image_refresh_succeeded", "image_refresh_failed", "image_refresh_exhausted", "image_refresh_superseded"} {
		_, ok := registered[name]
		assert.True(t, ok, "missing registered event type %s", name)
	}
	assert.Contains(t, registered["image_refresh_scheduled"].PayloadSpec, "attempt")
	assert.Contains(t, registered["image_refresh_scheduled"].PayloadSpec, "retry_delay")
	assert.Contains(t, registered["image_refresh_scheduled"].PayloadSpec, "old_commit")
	assert.Contains(t, registered["image_refresh_scheduled"].PayloadSpec, "new_commit")
	assert.Contains(t, registered["image_refresh_scheduled"].PayloadSpec, "stack_path")
}


