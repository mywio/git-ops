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
	"path/filepath"
	"testing"

	"github.com/google/go-github/v57/github"
	"github.com/mywio/git-ops/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchComposeSpecPrefersComposeYAML(t *testing.T) {
	server, client := newComposeSpecTestServer(t, map[string]string{
		"compose.yaml":        "services:\n  app:\n    image: nginx:latest\n",
		"docker-compose.yml":  "services:\n  app:\n    image: busybox:latest\n",
	})
	defer server.Close()

	originalFetchSHA := fetchRepoDefaultBranchSHA
	fetchRepoDefaultBranchSHA = func(context.Context, *github.Client, *github.Repository) (string, error) {
		return "abc123", nil
	}
	defer func() {
		fetchRepoDefaultBranchSHA = originalFetchSHA
	}()

	reconciler := &Reconciler{
		client: client,
		cfg:    config.Config{TargetDir: "/tmp/stacks"},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	repo := &github.Repository{
		Name:          github.String("api"),
		Owner:         &github.User{Login: github.String("acme")},
		DefaultBranch: github.String("main"),
	}

	spec, ok := reconciler.fetchComposeSpec(context.Background(), "acme/api", repo, reconciler.logger)

	require.True(t, ok)
	assert.Equal(t, "services:\n  app:\n    image: nginx:latest\n", spec.content)
	assert.Equal(t, filepath.Join("/tmp/stacks", "acme", "api", "compose.yaml"), spec.filePath)
	assert.Equal(t, "abc123", spec.currentCommitSHA)
}

func TestFetchComposeSpecFallsBackToDockerComposeYAML(t *testing.T) {
	server, client := newComposeSpecTestServer(t, map[string]string{
		"docker-compose.yml": "services:\n  app:\n    image: busybox:latest\n",
	})
	defer server.Close()

	originalFetchSHA := fetchRepoDefaultBranchSHA
	fetchRepoDefaultBranchSHA = func(context.Context, *github.Client, *github.Repository) (string, error) {
		return "def456", nil
	}
	defer func() {
		fetchRepoDefaultBranchSHA = originalFetchSHA
	}()

	reconciler := &Reconciler{
		client: client,
		cfg:    config.Config{TargetDir: "/tmp/stacks"},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	repo := &github.Repository{
		Name:          github.String("api"),
		Owner:         &github.User{Login: github.String("acme")},
		DefaultBranch: github.String("main"),
	}

	spec, ok := reconciler.fetchComposeSpec(context.Background(), "acme/api", repo, reconciler.logger)

	require.True(t, ok)
	assert.Equal(t, "services:\n  app:\n    image: busybox:latest\n", spec.content)
	assert.Equal(t, filepath.Join("/tmp/stacks", "acme", "api", "docker-compose.yml"), spec.filePath)
	assert.Equal(t, "def456", spec.currentCommitSHA)
}

func newComposeSpecTestServer(t *testing.T, files map[string]string) (*httptest.Server, *github.Client) {
	t.Helper()

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	baseURL, err := url.Parse(server.URL + "/")
	require.NoError(t, err)

	mux.HandleFunc("/repos/acme/api/contents/compose.yaml", func(w http.ResponseWriter, r *http.Request) {
		respondWithComposeFile(t, w, "compose.yaml", files)
	})
	mux.HandleFunc("/repos/acme/api/contents/docker-compose.yml", func(w http.ResponseWriter, r *http.Request) {
		respondWithComposeFile(t, w, "docker-compose.yml", files)
	})

	client := github.NewClient(server.Client())
	client.BaseURL = baseURL
	return server, client
}

func respondWithComposeFile(t *testing.T, w http.ResponseWriter, name string, files map[string]string) {
	t.Helper()

	content, ok := files[name]
	if !ok {
		http.NotFound(w, nil)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
		"type":     "file",
		"name":     name,
		"path":     name,
		"encoding": "base64",
		"content":  base64.StdEncoding.EncodeToString([]byte(content)),
	}))
}
