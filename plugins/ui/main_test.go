package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/mywio/git-ops/pkg/core"
	"github.com/stretchr/testify/assert"
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
