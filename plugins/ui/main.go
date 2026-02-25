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

func (p *UIPlugin) Description() string {
	return "Web Dashboard UI"
}

func (p *UIPlugin) Init(ctx context.Context, logger *slog.Logger, registry core.PluginRegistry) error {
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

func (p *UIPlugin) Execute(action string, params map[string]interface{}) (interface{}, error) {
	return nil, nil
}
