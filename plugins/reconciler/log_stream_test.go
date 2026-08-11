package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"testing"
	"time"

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

func TestLogStreamHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_LOG_STREAM_HELPER") != "1" {
		return
	}
	fmt.Println("ready")
	for {
		time.Sleep(time.Second)
	}
}
