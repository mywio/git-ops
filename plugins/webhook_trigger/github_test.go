package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mywio/git-ops/pkg/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const webhookTestSecret = "github-webhook-secret"

func newGitHubWebhookPlugin(t *testing.T) (*WebhookTriggerPlugin, *webhookTestRegistry) {
	t.Helper()
	registry := &webhookTestRegistry{config: map[string]map[string]any{
		"webhook_trigger": {"github_secret": webhookTestSecret},
	}}
	plugin := &WebhookTriggerPlugin{}
	require.NoError(t, plugin.Init(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), registry))
	return plugin, registry
}

func signedGitHubRequest(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewBufferString(body))
	request.Header.Set("X-GitHub-Event", "push")
	request.Header.Set("X-GitHub-Delivery", "delivery-1")
	mac := hmac.New(sha256.New, []byte(webhookTestSecret))
	_, _ = mac.Write([]byte(body))
	request.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	return request
}

func TestGitHubWebhookQueuesExactDefaultBranchSHA(t *testing.T) {
	plugin, registry := newGitHubWebhookPlugin(t)
	sha := "1234567890123456789012345678901234567890"
	body := `{"ref":"refs/heads/main","after":"` + sha + `","repository":{"name":"api","default_branch":"main","owner":{"login":"acme"}}}`
	response := httptest.NewRecorder()

	plugin.handleGitHub(response, signedGitHubRequest(body))

	assert.Equal(t, http.StatusAccepted, response.Code)
	require.Len(t, registry.events, 2)
	assert.Equal(t, core.EventTypeRepositoryRevisionRequested, registry.events[0].Type)
	request, ok := core.RepositoryRevisionFromEvent(registry.events[0])
	require.True(t, ok)
	assert.Equal(t, sha, request.CommitSHA)
	assert.Equal(t, "delivery-1", request.DeliveryID)
	assert.Equal(t, "github_push", request.Trigger)
}

func TestGitHubWebhookRejectsInvalidSignature(t *testing.T) {
	plugin, registry := newGitHubWebhookPlugin(t)
	request := signedGitHubRequest(`{"ref":"refs/heads/main"}`)
	request.Header.Set("X-Hub-Signature-256", "sha256=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	response := httptest.NewRecorder()

	plugin.handleGitHub(response, request)

	assert.Equal(t, http.StatusUnauthorized, response.Code)
	assert.Empty(t, registry.events)
}

func TestGitHubWebhookIgnoresBranchesAndDuplicateDeliveries(t *testing.T) {
	plugin, registry := newGitHubWebhookPlugin(t)
	body := `{"ref":"refs/heads/feature","after":"1234567890123456789012345678901234567890","repository":{"name":"api","default_branch":"main","owner":{"login":"acme"}}}`
	first := httptest.NewRecorder()
	plugin.handleGitHub(first, signedGitHubRequest(body))
	second := httptest.NewRecorder()
	plugin.handleGitHub(second, signedGitHubRequest(body))

	assert.Equal(t, http.StatusAccepted, first.Code)
	assert.Equal(t, http.StatusAccepted, second.Code)
	assert.Contains(t, second.Body.String(), "duplicate delivery")
	assert.Empty(t, registry.events)
}

func TestGitHubWebhookIgnoresDeletedDefaultBranch(t *testing.T) {
	plugin, registry := newGitHubWebhookPlugin(t)
	body := `{"ref":"refs/heads/main","after":"0000000000000000000000000000000000000000","deleted":true,"repository":{"name":"api","default_branch":"main","owner":{"login":"acme"}}}`
	response := httptest.NewRecorder()

	plugin.handleGitHub(response, signedGitHubRequest(body))

	assert.Equal(t, http.StatusAccepted, response.Code)
	assert.Empty(t, registry.events)
}
