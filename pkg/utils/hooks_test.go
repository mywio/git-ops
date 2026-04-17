package utils

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecuteHooksTimesOutPerScript(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "sleep.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte("#!/bin/sh\nsleep 2\n"), 0o755))

	start := time.Now()
	err := ExecuteHooks(context.Background(), dir, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), 100*time.Millisecond)
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, elapsed, time.Second)
}

func TestExecuteHooksRespectsCancelledParentContext(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "sleep.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte("#!/bin/sh\nsleep 2\n"), 0o755))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err := ExecuteHooks(ctx, dir, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), 5*time.Minute)
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Less(t, elapsed, time.Second)
}
