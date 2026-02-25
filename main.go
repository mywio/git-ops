package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/mywio/GHOps/pkg/config"
	"github.com/mywio/GHOps/pkg/core"
	"github.com/mywio/GHOps/pkg/reconciler"
)

func main() {
	// 1. Setup Logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// 2. Load Config
	cfg := config.LoadConfig()

	// 3. Validation
	if cfg.Token == "" || len(cfg.Users) == 0 || cfg.Topic == "" {
		logger.Error("Missing env vars: GITHUB_TOKEN, GITHUB_USERS, TOPIC_FILTER")
		os.Exit(1)
	}

	// 4. Setup Module Manager
	mgr := core.NewModuleManager(logger)

	// Load Plugins
	pluginsDir := os.Getenv("PLUGINS_DIR")
	if pluginsDir == "" {
		pluginsDir = "plugins"
	}
	if err := mgr.LoadPlugins(pluginsDir); err != nil {
		logger.Error("Failed to load plugins", "error", err)
	}

	// 5. Register Modules
	// Core Reconciler
	r := reconciler.NewReconciler(cfg)
	mgr.Register(r)

	// 6. Init Modules
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := mgr.Init(ctx); err != nil {
		logger.Error("Failed to initialize modules", "error", err)
		os.Exit(1)
	}

	// 7. Start Modules
	mgr.Start(ctx)

	// 8. Wait for Signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigChan
	logger.Info("Received signal, shutting down...", "signal", sig)

	// 9. Graceful Shutdown
	mgr.Stop(ctx)
	logger.Info("Shutdown complete")
}
