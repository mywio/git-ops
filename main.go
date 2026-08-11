package main

import (
	"context"
	"flag"
	"fmt"
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
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(core.Version)
		return
	}

	// Setup Logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	logger.Info("git-ops starting", "version", core.Version)

	// Load Config
	cfgMapEnv := config.LoadConfigMapFromEnv()
	configPath := os.Getenv("CONFIG_FILE")
	if configPath == "" {
		configPath = "config.yaml"
	}
	cfgMapFile, err := config.LoadConfigFile(configPath)
	if err != nil {
		logger.Error("Failed to load config file", "path", configPath, "error", err)
		os.Exit(1)
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
		os.Exit(1)
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
	if err := mgr.Start(ctx); err != nil {
		logger.Error("Failed to start modules", "error", err)
		os.Exit(1)
	}

	// Wait for Signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigChan
	logger.Info("Received signal, shutting down...", "signal", sig)

	// Graceful Shutdown
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	mgr.Stop(shutdownCtx)
	logger.Info("Shutdown complete")
}
