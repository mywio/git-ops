package main

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/mywio/GHOps/pkg/core"
	"github.com/stretchr/testify/assert"
)

func TestMCPPlugin(t *testing.T) {
	// Verify Plugin variable implements interface
	var _ core.Plugin = Plugin

	assert.Equal(t, "mcp", Plugin.Name())

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx := context.Background()

	// Use ModuleManager as a dummy registry
	mgr := core.NewModuleManager(logger)

	err := Plugin.Init(ctx, logger, mgr)
	assert.NoError(t, err)

	err = Plugin.Start(ctx)
	assert.NoError(t, err)

	caps := Plugin.Capabilities()
	assert.Contains(t, caps, core.Capability("ai-context"))

	status := Plugin.Status()
	assert.Equal(t, core.StatusHealthy, status)

	err = Plugin.Stop(ctx)
	assert.NoError(t, err)
}
