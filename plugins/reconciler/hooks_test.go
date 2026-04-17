package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-github/v57/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchRepoHooksPreStageFailsWhenHookContentFetchFails(t *testing.T) {
	server, client := newHookContentsTestServer(t)
	defer server.Close()

	reconciler := &Reconciler{
		client: client,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	err := reconciler.fetchRepoHookScriptsForStage(context.Background(), "acme", "api", "pre", t.TempDir())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fetch pre-hook fail.sh")
}

func TestFetchRepoHooksPostStageContinuesWhenHookContentFetchFails(t *testing.T) {
	server, client := newHookContentsTestServer(t)
	defer server.Close()

	localDir := t.TempDir()
	reconciler := &Reconciler{
		client: client,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	err := reconciler.fetchRepoHookScriptsForStage(context.Background(), "acme", "api", "post", localDir)

	require.NoError(t, err)
	content, readErr := os.ReadFile(filepath.Join(localDir, ".deploy", "post", "ok.sh"))
	require.NoError(t, readErr)
	assert.Equal(t, "#!/bin/sh\necho ok\n", string(content))

	_, statErr := os.Stat(filepath.Join(localDir, ".deploy", "post", "fail.sh"))
	assert.ErrorIs(t, statErr, os.ErrNotExist)
}

func newHookContentsTestServer(t *testing.T) (*httptest.Server, *github.Client) {
	t.Helper()

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	baseURL, err := url.Parse(server.URL + "/")
	require.NoError(t, err)

	encodeContent := func(content string) string {
		return base64.StdEncoding.EncodeToString([]byte(content))
	}

	mux.HandleFunc("/repos/acme/api/contents/.deploy/pre", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode([]map[string]any{
			{"type": "file", "name": "ok.sh", "path": ".deploy/pre/ok.sh"},
			{"type": "file", "name": "fail.sh", "path": ".deploy/pre/fail.sh"},
		}))
	})

	mux.HandleFunc("/repos/acme/api/contents/.deploy/post", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode([]map[string]any{
			{"type": "file", "name": "ok.sh", "path": ".deploy/post/ok.sh"},
			{"type": "file", "name": "fail.sh", "path": ".deploy/post/fail.sh"},
		}))
	})

	mux.HandleFunc("/repos/acme/api/contents/.deploy/pre/ok.sh", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"type":     "file",
			"name":     "ok.sh",
			"path":     ".deploy/pre/ok.sh",
			"encoding": "base64",
			"content":  encodeContent("#!/bin/sh\necho ok\n"),
		}))
	})

	mux.HandleFunc("/repos/acme/api/contents/.deploy/post/ok.sh", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"type":     "file",
			"name":     "ok.sh",
			"path":     ".deploy/post/ok.sh",
			"encoding": "base64",
			"content":  encodeContent("#!/bin/sh\necho ok\n"),
		}))
	})

	mux.HandleFunc("/repos/acme/api/contents/.deploy/pre/fail.sh", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})

	mux.HandleFunc("/repos/acme/api/contents/.deploy/post/fail.sh", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})

	client := github.NewClient(server.Client())
	client.BaseURL = baseURL

	return server, client
}
