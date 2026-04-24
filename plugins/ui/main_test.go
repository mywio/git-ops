package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/mywio/git-ops/pkg/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUIPlugin(t *testing.T) {
	// Verify Plugin variable implements interface
	var _ core.Plugin = Plugin

	assert.Equal(t, "ui", Plugin.Name())

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx := context.Background()

	// Use ModuleManager as a dummy registry
	mgr := core.NewModuleManager(logger)

	err := Plugin.Init(ctx, logger, mgr)
	assert.NoError(t, err)

	err = Plugin.Start(ctx)
	assert.NoError(t, err)

	caps := Plugin.Capabilities()
	assert.Contains(t, caps, core.CapabilityUI)
	assert.Contains(t, caps, core.CapabilityAPI)

	status := Plugin.Status()
	assert.Equal(t, core.StatusHealthy, status)

	err = Plugin.Stop(ctx)
	assert.NoError(t, err)
}

func TestUIPluginRequiresDefaultAuthHeader(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx := context.Background()
	mgr := core.NewModuleManager(logger)

	plugin := &UIPlugin{}
	err := plugin.Init(ctx, logger, mgr)
	assert.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/ui/system/info", nil)
	rr := httptest.NewRecorder()

	mgr.GetMuxServer().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Contains(t, rr.Body.String(), "missing authenticated user header")
}

func TestUIPluginAcceptsCustomAuthHeader(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx := context.Background()
	mgr := core.NewModuleManager(logger)
	mgr.SetConfig(map[string]map[string]any{
		"ui": {
			"auth_header": "X-Forwarded-User",
		},
	})

	plugin := &UIPlugin{}
	err := plugin.Init(ctx, logger, mgr)
	assert.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/ui/system", nil)
	req.Header.Set("X-Forwarded-User", "alice")
	rr := httptest.NewRecorder()

	mgr.GetMuxServer().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), "<!doctype html")
}

func TestUIPluginCanDisableAuth(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx := context.Background()
	mgr := core.NewModuleManager(logger)
	mgr.SetConfig(map[string]map[string]any{
		"ui": {
			"disable_auth": true,
		},
	})

	plugin := &UIPlugin{}
	err := plugin.Init(ctx, logger, mgr)
	assert.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/ui/system/info", nil)
	rr := httptest.NewRecorder()

	mgr.GetMuxServer().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestUIPluginPublishesStackActionEvent(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx := context.Background()
	mgr := core.NewModuleManager(logger)

	plugin := &UIPlugin{}
	require.NoError(t, plugin.Init(ctx, logger, mgr))

	events := make(chan core.InternalEvent, 1)
	mgr.Subscribe("stack_restart_requested", func(_ context.Context, event core.InternalEvent) {
		events <- event
	})

	body, err := json.Marshal(map[string]string{
		"owner":  "acme",
		"repo":   "api",
		"action": "restart_stack",
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/ui/stacks/action", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Request-User", "alice")
	rr := httptest.NewRecorder()

	mgr.GetMuxServer().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusAccepted, rr.Code)

	select {
	case event := <-events:
		assert.Equal(t, core.EventTypeName("stack_restart_requested"), event.Type)
		assert.Equal(t, "acme", event.Details["owner"])
		assert.Equal(t, "api", event.Details["repo"])
		assert.Equal(t, "alice", event.Details["requested_by"])
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stack action event")
	}
}

func TestUIPluginRejectsUnknownStackAction(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx := context.Background()
	mgr := core.NewModuleManager(logger)

	plugin := &UIPlugin{}
	require.NoError(t, plugin.Init(ctx, logger, mgr))

	body := []byte(`{"owner":"acme","repo":"api","action":"explode_stack"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/ui/stacks/action", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Request-User", "alice")
	rr := httptest.NewRecorder()

	mgr.GetMuxServer().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestUIPluginRejectsStackActionWithoutAuth(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx := context.Background()
	mgr := core.NewModuleManager(logger)

	plugin := &UIPlugin{}
	require.NoError(t, plugin.Init(ctx, logger, mgr))

	body := []byte(`{"owner":"acme","repo":"api","action":"restart_stack"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/ui/stacks/action", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	mgr.GetMuxServer().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestUIPluginRejectsStackActionWrongMethod(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx := context.Background()
	mgr := core.NewModuleManager(logger)

	plugin := &UIPlugin{}
	require.NoError(t, plugin.Init(ctx, logger, mgr))

	req := httptest.NewRequest(http.MethodGet, "/api/ui/stacks/action", nil)
	req.Header.Set("X-Auth-Request-User", "alice")
	rr := httptest.NewRecorder()

	mgr.GetMuxServer().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}
