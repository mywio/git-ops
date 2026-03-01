package core

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

type testPlugin struct {
	name string
}

func (p *testPlugin) Name() string { return p.name }
func (p *testPlugin) Init(ctx context.Context, logger *slog.Logger, registry PluginRegistry) error {
	return nil
}
func (p *testPlugin) Start(ctx context.Context) error { return nil }
func (p *testPlugin) Stop(ctx context.Context) error  { return nil }
func (p *testPlugin) Description() string             { return "test plugin" }
func (p *testPlugin) Capabilities() []Capability      { return []Capability{CapabilityAPI} }
func (p *testPlugin) Status() ServiceStatus           { return StatusHealthy }
func (p *testPlugin) Execute(ctx context.Context, action string, params map[string]interface{}) (interface{}, error) {
	return nil, nil
}
func (p *testPlugin) Config() any {
	return map[string]any{"token": Secret{Value: "abc"}}
}

func TestPluginsAPIList_NoConfig(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := NewModuleManager(logger)
	mgr.Register(&testPlugin{name: "test"})

	req := httptest.NewRequest(http.MethodGet, "/api/plugins", nil)
	rr := httptest.NewRecorder()
	mgr.handlePlugins(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var out []pluginInfo
	err := json.NewDecoder(rr.Body).Decode(&out)
	assert.NoError(t, err)
	assert.Len(t, out, 1)
	assert.Equal(t, "test", out[0].Name)
	assert.Nil(t, out[0].Config)
}

func TestPluginsAPIList_WithConfig(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := NewModuleManager(logger)
	mgr.Register(&testPlugin{name: "test"})

	req := httptest.NewRequest(http.MethodGet, "/api/plugins?include_config=true", nil)
	rr := httptest.NewRecorder()
	mgr.handlePlugins(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var out []pluginInfo
	err := json.NewDecoder(rr.Body).Decode(&out)
	assert.NoError(t, err)
	assert.Len(t, out, 1)
	cfg, ok := out[0].Config.(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "REDACTED", cfg["token"])
}

func TestPluginsAPIDetail(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := NewModuleManager(logger)
	mgr.Register(&testPlugin{name: "test"})

	req := httptest.NewRequest(http.MethodGet, "/api/plugins/test", nil)
	rr := httptest.NewRecorder()
	mgr.handlePlugin(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var out pluginInfo
	err := json.NewDecoder(rr.Body).Decode(&out)
	assert.NoError(t, err)
	assert.Equal(t, "test", out.Name)
	cfg, ok := out.Config.(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "REDACTED", cfg["token"])
}
