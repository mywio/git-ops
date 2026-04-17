package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-github/v57/github"
	"github.com/mywio/git-ops/pkg/config"
	"github.com/mywio/git-ops/pkg/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReconcilerValidateConfigAggregatesMissingRequiredFields(t *testing.T) {
	reconciler := &Reconciler{}

	err := reconciler.validateConfig()

	require.Error(t, err)
	assert.Equal(t, "configuration errors:\n  - GITHUB_TOKEN is required\n  - GITHUB_USERS is required\n  - TOPIC_FILTER is required", err.Error())
}

func TestReconcilerValidateConfigMissingSingleRequiredField(t *testing.T) {
	tests := []struct {
		name    string
		cfg     func(*Reconciler)
		wantErr string
	}{
		{
			name: "missing token",
			cfg: func(r *Reconciler) {
				r.cfg.Users = []string{"acme"}
				r.cfg.Topics = []string{"deploy"}
			},
			wantErr: "configuration errors:\n  - GITHUB_TOKEN is required",
		},
		{
			name: "missing users",
			cfg: func(r *Reconciler) {
				r.cfg.Token = "token"
				r.cfg.Users = []string{"", " "}
				r.cfg.Topics = []string{"deploy"}
			},
			wantErr: "configuration errors:\n  - GITHUB_USERS is required",
		},
		{
			name: "missing topic",
			cfg: func(r *Reconciler) {
				r.cfg.Token = "token"
				r.cfg.Users = []string{"acme"}
			},
			wantErr: "configuration errors:\n  - TOPIC_FILTER is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reconciler := &Reconciler{}
			tt.cfg(reconciler)

			err := reconciler.validateConfig()

			require.Error(t, err)
			assert.Equal(t, tt.wantErr, err.Error())
		})
	}
}

func TestReconcilerValidateConfigAcceptsNonEmptyUsers(t *testing.T) {
	reconciler := &Reconciler{}
	reconciler.cfg.Token = "token"
	reconciler.cfg.Users = []string{"", " acme "}
	reconciler.cfg.Topics = []string{"deploy"}

	err := reconciler.validateConfig()

	require.NoError(t, err)
}

func TestReconcilerInitReturnsComposeAvailabilityError(t *testing.T) {
	originalCheck := checkDockerComposeAvailable
	checkDockerComposeAvailable = func() error {
		return errors.New("docker compose not available: exec: \"docker\": executable file not found in $PATH")
	}
	defer func() {
		checkDockerComposeAvailable = originalCheck
	}()

	reconciler := &Reconciler{}
	reconciler.cfg.Token = "token"
	reconciler.cfg.Users = []string{"acme"}
	reconciler.cfg.Topics = []string{"deploy"}

	err := reconciler.Init(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), nil)

	require.Error(t, err)
	assert.Equal(t, "docker compose not available: exec: \"docker\": executable file not found in $PATH", err.Error())
}

func TestDetectComposeChangeLogsFullAdditionForDryRunFirstDeploy(t *testing.T) {
	var logBuffer bytes.Buffer
	reconciler := &Reconciler{
		cfg:            config.Config{DryRun: true},
		logger:         slog.New(slog.NewJSONHandler(&logBuffer, nil)),
		executionState: newExecutionStateManager(fixedTimes(time.Date(2026, 4, 17, 10, 0, 0, 0, time.UTC), time.Date(2026, 4, 17, 10, 0, 1, 0, time.UTC))),
		commitTracker:  newCommitTracker(),
	}

	_, acquired := reconciler.executionState.acquire("acme/api", "acme", "api", "node-a", "manual")
	require.True(t, acquired)

	spec := composeSpec{
		content:          "services:\n  app:\n    image: nginx:latest\n",
		currentCommitSHA: "abc123",
		repoLocalPath:    t.TempDir(),
		filePath:         filepath.Join(t.TempDir(), "compose.yaml"),
	}
	repo := &github.Repository{
		Name:          github.String("api"),
		Owner:         &github.User{Login: github.String("acme")},
		DefaultBranch: github.String("main"),
	}

	continued := reconciler.detectComposeChange(context.Background(), "acme/api", repo, spec, "", reconciler.logger)

	assert.False(t, continued)
	assert.Contains(t, logBuffer.String(), "DryRun: compose diff")
	assert.Contains(t, logBuffer.String(), "+ services:")
	assert.Contains(t, logBuffer.String(), "+   app:")
	assert.Contains(t, logBuffer.String(), "+     image: nginx:latest")
}

func TestDeployRepoWithExecutionSkipsLockedStack(t *testing.T) {
	server, client := newComposeSpecTestServer(t, map[string]string{
		"compose.yaml": "services:\n  app:\n    image: nginx:latest\n",
	})
	defer server.Close()

	originalFetchSHA := fetchRepoDefaultBranchSHA
	fetchRepoDefaultBranchSHA = func(context.Context, *github.Client, *github.Repository) (string, error) {
		return "abc123", nil
	}
	defer func() {
		fetchRepoDefaultBranchSHA = originalFetchSHA
	}()

	targetDir := t.TempDir()
	stackPath := filepath.Join(targetDir, "acme", "api")
	require.NoError(t, os.MkdirAll(stackPath, 0o755))
	lockPath := filepath.Join(stackPath, ".git-ops-lock")
	require.NoError(t, os.WriteFile(lockPath, []byte("locked"), 0o644))

	events := make(chan core.InternalEvent, 2)
	reconciler := &Reconciler{
		client:         client,
		cfg:            config.Config{TargetDir: targetDir},
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		publishEvent:   func(_ context.Context, event core.InternalEvent) { events <- event },
		executionState: newExecutionStateManager(fixedTimes(time.Date(2026, 4, 17, 11, 0, 0, 0, time.UTC), time.Date(2026, 4, 17, 11, 0, 1, 0, time.UTC))),
	}
	_, acquired := reconciler.executionState.acquire("acme/api", "acme", "api", "node-a", "manual")
	require.True(t, acquired)

	repo := &github.Repository{
		Name:          github.String("api"),
		Owner:         &github.User{Login: github.String("acme")},
		DefaultBranch: github.String("main"),
	}

	reconciler.deployRepoWithExecution(context.Background(), "acme/api", repo, "")

	select {
	case event := <-events:
		assert.Equal(t, core.EventTypeName("stack_locked"), event.Type)
		assert.Equal(t, "acme/api", event.Details["full_name"])
		assert.Equal(t, lockPath, event.Details["lock_file"])
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stack_locked event")
	}

	snapshot, ok := reconciler.executionState.snapshot("acme/api")
	require.True(t, ok)
	assert.Equal(t, core.ExecutionStatusSucceeded, snapshot.Status)
}
