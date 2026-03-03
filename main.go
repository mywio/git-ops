package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mywio/git-ops/pkg/config"
	"github.com/mywio/git-ops/pkg/core"
)

func main() {
	// Setup Logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Load Config
	cfgMapEnv := config.LoadConfigMapFromEnv()
	configPath := os.Getenv("CONFIG_FILE")
	if configPath == "" {
		configPath = "config.yaml"
	}
	cfgMapFile, err := config.LoadConfigFile(configPath)
	if err != nil {
		logger.Error("Failed to load config file", "path", configPath, "error", err)
	}
	cfgMap := config.MergeConfigMap(cfgMapFile, cfgMapEnv)

	// Setup Module Manager
	mgr := core.NewModuleManager(logger)
	mgr.SetConfig(cfgMap)
	mgr.SetHTTPClient(&http.Client{Timeout: 15 * time.Second})

	// Load Plugins
	pluginsDir := ""
	if coreSection, ok := cfgMap["core"]; ok {
		if v, ok := coreSection["plugins_dir"].(string); ok {
			pluginsDir = v
		}
	}
	if pluginsDir == "" {
		pluginsDir = "plugins"
	}
	if err := mgr.LoadPlugins(pluginsDir); err != nil {
		logger.Error("Failed to load plugins", "error", err)
	}

	// Register Modules (if any core modules remain)

	// Init Modules
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := mgr.Init(ctx); err != nil {
		logger.Error("Failed to initialize modules", "error", err)
		os.Exit(1)
	}

	// Start Modules
	mgr.Start(ctx)

	// Wait for Signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigChan
	logger.Info("Received signal, shutting down...", "signal", sig)

	// Graceful Shutdown
	mgr.Stop(ctx)
	logger.Info("Shutdown complete")
}
