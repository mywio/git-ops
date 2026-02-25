package main

import (
	"context"
	"log/slog"

	"github.com/mywio/GHOps/pkg/core"
)

type UIPlugin struct {
	logger *slog.Logger
}

var Plugin = &UIPlugin{}

func (p *UIPlugin) Name() string {
	return "ui"
}

func (p *UIPlugin) Init(ctx context.Context, logger *slog.Logger) error {
	p.logger = logger
	return nil
}

func (p *UIPlugin) Start(ctx context.Context) error {
	p.logger.Info("UI Plugin Started")
	return nil
}

func (p *UIPlugin) Stop(ctx context.Context) error {
	return nil
}

func (p *UIPlugin) Capabilities() []core.Capability {
	return []core.Capability{"dashboard"}
}

func (p *UIPlugin) Status() core.ServiceStatus {
	return core.StatusHealthy
}
