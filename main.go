package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mywio/git-ops/pkg/config"
	"github.com/mywio/git-ops/pkg/core"
	"github.com/mywio/git-ops/pkg/reconciler"
)

func main() {
	// Setup Logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Load Config
	cfgEnv := config.LoadConfig()
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

	cfg := cfgEnv
	if coreSection, ok := cfgMapFile["core"]; ok {
		cfgFile := config.LoadConfigFromMap(coreSection)
		cfg = config.MergeConfig(cfgFile, cfgEnv)
	}

	// Validation
	if cfg.Token == "" || len(cfg.Users) == 0 || cfg.Topic == "" {
		logger.Error("Missing env vars: GITHUB_TOKEN, GITHUB_USERS, TOPIC_FILTER")
		os.Exit(1)
	}

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

	// Register Modules
	// Core Reconciler
	r := reconciler.NewReconciler(cfg)
	mgr.Register(r)

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

func initCoreEvents() {
	// Register core event types
	core.RegisterEventType(core.EventTypeDesc{
		Name:        "reconcile_now",
		Description: "Request an immediate full reconciliation",
		PayloadSpec: map[string]core.PayloadField{
			"force": {Type: "bool", Description: "Force even if locked", Required: false},
		},
	})
	core.RegisterEventType(core.EventTypeDesc{
		Name:        "deploy_success",
		Description: "Stack deployed successfully",
		PayloadSpec: map[string]core.PayloadField{
			"duration": {Type: "time.Duration", Description: "Deploy time", Required: true},
		},
	})
	//TODO: Add more core events...

	// Core subscribes to its own triggers
	core.Subscribe("reconcile_now", handleReconcileNow)
	// Optional: Subscribe to "*" for logging all events
}

// Example handler
func handleReconcileNow(ctx context.Context, event core.InternalEvent) {
	log.Printf("Handling %s from %s", event.Type, event.Source)
	//Call your doFullReconciliation()
	//After done, Publish(InternalEvent{Type: "reconcile_done", ...})
}
