package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mywio/git-ops/pkg/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUIPluginRequiresExplicitRollbackConfirmation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := core.NewModuleManager(logger)
	manager.SetConfig(map[string]map[string]any{"ui": {"trust_auth_header": true, "operator_users": []string{"alice"}}})
	actionPlugin := &uiActionTestPlugin{capabilities: []core.Capability{core.CapabilityRollbackDeployment}}
	manager.Register(actionPlugin)
	plugin := &UIPlugin{}
	require.NoError(t, plugin.Init(context.Background(), logger, manager))

	request := httptest.NewRequest(http.MethodPost, "/api/ui/stacks/action", bytes.NewBufferString(
		`{"owner":"acme","repo":"api","action":"rollback_deployment","sha":"1234567890123456789012345678901234567890"}`,
	))
	request.Header.Set("X-Auth-Request-User", "alice")
	response := httptest.NewRecorder()
	manager.GetMuxServer().ServeHTTP(response, request)

	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Empty(t, actionPlugin.action)
}

func TestUIPluginDispatchesConfirmedRollbackTarget(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager := core.NewModuleManager(logger)
	manager.SetConfig(map[string]map[string]any{"ui": {"trust_auth_header": true, "operator_users": []string{"alice"}}})
	actionPlugin := &uiActionTestPlugin{capabilities: []core.Capability{core.CapabilityRollbackDeployment}}
	manager.Register(actionPlugin)
	plugin := &UIPlugin{}
	require.NoError(t, plugin.Init(context.Background(), logger, manager))
	sha := "1234567890123456789012345678901234567890"

	request := httptest.NewRequest(http.MethodPost, "/api/ui/stacks/action", bytes.NewBufferString(
		`{"owner":"acme","repo":"api","action":"rollback_deployment","sha":"`+sha+`","confirmed":true}`,
	))
	request.Header.Set("X-Auth-Request-User", "alice")
	response := httptest.NewRecorder()
	manager.GetMuxServer().ServeHTTP(response, request)

	assert.Equal(t, http.StatusAccepted, response.Code)
	assert.Equal(t, "rollback_deployment", actionPlugin.action)
	assert.Equal(t, sha, actionPlugin.params["sha"])
	assert.Equal(t, "alice", actionPlugin.params["requested_by"])
}
