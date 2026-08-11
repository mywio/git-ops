package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mywio/git-ops/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStreamContainerLogsStopsProcessWhenContextIsCancelled(t *testing.T) {
	originalNewLogCommand := newLogCommand
	t.Cleanup(func() { newLogCommand = originalNewLogCommand })
	newLogCommand = func(ctx context.Context, _ ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestLogStreamHelperProcess")
		cmd.Env = append(os.Environ(), "GO_WANT_LOG_STREAM_HELPER=1")
		return cmd
	}

	reconciler := &Reconciler{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx, cancel := context.WithCancel(context.Background())
	logLines, err := reconciler.streamContainerLogs(ctx, "api", "10")
	require.NoError(t, err)

	select {
	case line := <-logLines:
		assert.Equal(t, "ready", line)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for helper process output")
	}

	cancel()
	select {
	case _, ok := <-logLines:
		assert.False(t, ok, "log channel should close after cancellation")
	case <-time.After(2 * time.Second):
		t.Fatal("log process did not stop after context cancellation")
	}
}

func TestStreamManagedLogsUsesPersistedComposeEnvironment(t *testing.T) {
	targetDir := t.TempDir()
	repoPath := filepath.Join(targetDir, "acme", "petrol-api")
	require.NoError(t, os.MkdirAll(repoPath, 0o755))
	require.NoError(t, persistComposeEnv(repoPath, map[string]string{
		"PETROL_ADMIN_KEY":  "admin-value",
		"PETROL_SYSTEM_KEY": "system-value",
	}))

	originalNewLogCommand := newLogCommand
	t.Cleanup(func() { newLogCommand = originalNewLogCommand })
	newLogCommand = func(ctx context.Context, args ...string) *exec.Cmd {
		assert.Equal(t, []string{"compose", "logs", "-f", "--tail", "10"}, args)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestLogStreamHelperProcess")
		cmd.Env = append(os.Environ(),
			"GO_WANT_LOG_STREAM_HELPER=1",
			"LOG_STREAM_ENV_KEY=PETROL_ADMIN_KEY",
		)
		return cmd
	}

	reconciler := &Reconciler{
		cfg:    config.Config{TargetDir: targetDir},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	ctx, cancel := context.WithCancel(context.Background())
	logLines, err := reconciler.streamLogs(ctx, "acme", "petrol-api", "10")
	require.NoError(t, err)

	select {
	case line := <-logLines:
		assert.Equal(t, "PETROL_ADMIN_KEY=admin-value", line)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for managed log helper output")
	}

	cancel()
	select {
	case _, ok := <-logLines:
		assert.False(t, ok, "managed log channel should close after cancellation")
	case <-time.After(2 * time.Second):
		t.Fatal("managed log process did not stop after context cancellation")
	}
}

func TestEnvironmentWithOverridesReplacesExistingValues(t *testing.T) {
	merged := environmentWithOverrides(
		[]string{"UNCHANGED=value", "PETROL_ADMIN_KEY=stale"},
		[]string{"PETROL_ADMIN_KEY=persisted", "PETROL_SYSTEM_KEY=system"},
	)

	assert.Equal(t, []string{
		"UNCHANGED=value",
		"PETROL_ADMIN_KEY=persisted",
		"PETROL_SYSTEM_KEY=system",
	}, merged)
}

func TestLogStreamHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_LOG_STREAM_HELPER") != "1" {
		return
	}
	if key := os.Getenv("LOG_STREAM_ENV_KEY"); key != "" {
		fmt.Printf("%s=%s\n", key, os.Getenv(key))
	} else {
		fmt.Println("ready")
	}
	for {
		time.Sleep(time.Second)
	}
}
