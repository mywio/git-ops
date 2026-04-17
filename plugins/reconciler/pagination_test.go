package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/go-github/v57/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchReposIntoFollowsGitHubSearchPagination(t *testing.T) {
	server, client, seenPages := newGitHubSearchTestServer(t)
	defer server.Close()

	reconciler := &Reconciler{
		client: client,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	target := make(map[string]*github.Repository)
	reconciler.fetchReposInto(context.Background(), "user:acme topic:deploy archived:false", target)

	require.Len(t, target, 2)
	assert.Contains(t, target, "acme/api")
	assert.Contains(t, target, "acme/web")
	assert.Equal(t, []string{"1", "2"}, seenPages())
}

func TestFetchRemovalIntoFollowsGitHubSearchPagination(t *testing.T) {
	server, client, seenPages := newGitHubSearchTestServer(t)
	defer server.Close()

	reconciler := &Reconciler{
		client: client,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	target := make(map[string]bool)
	reconciler.fetchRemovalInto(context.Background(), "user:acme topic:deploy archived:false", target)

	require.Len(t, target, 2)
	assert.True(t, target["acme/api"])
	assert.True(t, target["acme/web"])
	assert.Equal(t, []string{"1", "2"}, seenPages())
}

func newGitHubSearchTestServer(t *testing.T) (*httptest.Server, *github.Client, func() []string) {
	t.Helper()

	var requests []string
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	baseURL, err := url.Parse(server.URL + "/")
	require.NoError(t, err)

	mux.HandleFunc("/search/repositories", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		if page == "" {
			page = "1"
		}
		requests = append(requests, page)

		w.Header().Set("Content-Type", "application/json")
		if page == "1" {
			w.Header().Set("Link", fmt.Sprintf("<%s/search/repositories?page=2>; rel=\"next\"", server.URL))
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"total_count":        101,
				"incomplete_results": false,
				"items": []map[string]any{
					{
						"name": "api",
						"owner": map[string]any{
							"login": "acme",
						},
					},
				},
			}))
			return
		}

		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"total_count":        101,
			"incomplete_results": false,
			"items": []map[string]any{
				{
					"name": "web",
					"owner": map[string]any{
						"login": "acme",
					},
				},
			},
		}))
	})

	client := github.NewClient(server.Client())
	client.BaseURL = baseURL

	return server, client, func() []string {
		out := make([]string, len(requests))
		copy(out, requests)
		return out
	}
}
