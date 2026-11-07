package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/mywio/git-ops/internal/fleetagent"
	"github.com/mywio/git-ops/pkg/core"
)

func main() {
	configFile := flag.String("config", "/etc/git-ops-agent/config.yaml", "agent configuration file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(core.Version)
		return
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := fleetagent.LoadConfig(*configFile)
	if err != nil {
		logger.Error("load agent config", "error", err)
		os.Exit(1)
	}
	agent, err := fleetagent.New(cfg, logger)
	if err != nil {
		logger.Error("initialize agent", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := agent.Run(ctx); err != nil && ctx.Err() == nil {
		logger.Error("agent stopped", "error", err)
		os.Exit(1)
	}
}
