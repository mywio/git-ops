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

	"github.com/mywio/git-ops/pkg/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type uiActionTestPlugin struct {
	capabilities []core.Capability
	action       string
	params       map[string]interface{}
}

func (p *uiActionTestPlugin) Name() string                    { return "action_test" }
func (p *uiActionTestPlugin) Description() string             { return "action test plugin" }
func (p *uiActionTestPlugin) Capabilities() []core.Capability { return p.capabilities }
func (p *uiActionTestPlugin) Status() core.ServiceStatus      { return core.StatusHealthy }
func (p *uiActionTestPlugin) Init(context.Context, *slog.Logger, core.PluginRegistry) error {
	return nil
}
func (p *uiActionTestPlugin) Start(context.Context) error { return nil }
func (p *uiActionTestPlugin) Stop(context.Context) error  { return nil }
func (p *uiActionTestPlugin) Execute(_ context.Context, action string, params map[string]interface{}) (interface{}, error) {
	p.action = action
	p.params = params
	return true, nil
}

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

func TestUIPluginExecutesStackActionCapability(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx := context.Background()
	mgr := core.NewModuleManager(logger)
	actionPlugin := &uiActionTestPlugin{capabilities: []core.Capability{core.CapabilityRestartStack}}
	mgr.Register(actionPlugin)

	plugin := &UIPlugin{}
	require.NoError(t, plugin.Init(ctx, logger, mgr))

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
	assert.Equal(t, "restart_stack", actionPlugin.action)
	assert.Equal(t, "acme", actionPlugin.params["owner"])
	assert.Equal(t, "api", actionPlugin.params["repo"])
	assert.Equal(t, "alice", actionPlugin.params["requested_by"])
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
