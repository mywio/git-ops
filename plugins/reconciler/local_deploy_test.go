package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/go-github/v57/github"
	"github.com/mywio/git-ops/pkg/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepositoryHasLocalDeployRecipeUsesExactSHA(t *testing.T) {
	sha := "1234567890123456789012345678901234567890"
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/repos/acme/api/contents/.ghops/deploy.yaml", func(w http.ResponseWriter, request *http.Request) {
		assert.Equal(t, sha, request.URL.Query().Get("ref"))
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"type": "file", "name": "deploy.yaml", "path": ".ghops/deploy.yaml",
			"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte("version: 1")),
		}))
	})
	baseURL, err := url.Parse(server.URL + "/")
	require.NoError(t, err)
	client := github.NewClient(server.Client())
	client.BaseURL = baseURL
	reconciler := &Reconciler{client: client}

	found, err := reconciler.repositoryHasLocalDeployRecipe(context.Background(), "acme", "api", sha)

	require.NoError(t, err)
	assert.True(t, found)
}

func TestRouteRecipeRevisionPublishesPinnedLocalDeployRequest(t *testing.T) {
	sha := "abcdefabcdefabcdefabcdefabcdefabcdefabcd"
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/repos/acme/api/contents/.ghops/deploy.yaml", func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"type\":\"file\",\"name\":\"deploy.yaml\",\"path\":\".ghops/deploy.yaml\"}"))
	})
	baseURL, err := url.Parse(server.URL + "/")
	require.NoError(t, err)
	client := github.NewClient(server.Client())
	client.BaseURL = baseURL
	var published core.InternalEvent
	reconciler := &Reconciler{
		client:       client,
		publishEvent: func(_ context.Context, event core.InternalEvent) { published = event },
	}
	repository := &github.Repository{
		Name: github.String("api"), DefaultBranch: github.String("main"),
		Owner: &github.User{Login: github.String("acme")},
	}

	routed, err := reconciler.routeRecipeRevision(context.Background(), repository, sha, "periodic_reconcile")

	require.NoError(t, err)
	assert.True(t, routed)
	assert.Equal(t, core.EventTypeLocalDeployRequested, published.Type)
	assert.Equal(t, sha, published.Details["commit_sha"])
	assert.Equal(t, "periodic_reconcile", published.Details["trigger"])
}
