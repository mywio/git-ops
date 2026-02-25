package main

import (
	"context"
	"log/slog"

	"github.com/mywio/GHOps/pkg/core"
)

type MCPPlugin struct {
	logger *slog.Logger
}

var Plugin = &MCPPlugin{}

func (p *MCPPlugin) Name() string {
	return "mcp"
}

func (p *MCPPlugin) Description() string {
	return "Provides AI context via MCP"
}

func (p *MCPPlugin) Init(ctx context.Context, logger *slog.Logger, registry core.PluginRegistry) error {
	p.logger = logger
	return nil
}

func (p *MCPPlugin) Start(ctx context.Context) error {
	p.logger.Info("MCP Plugin Started")
	return nil
}

func (p *MCPPlugin) Stop(ctx context.Context) error {
	return nil
}

func (p *MCPPlugin) Capabilities() []core.Capability {
	return []core.Capability{"ai-context"}
}

func (p *MCPPlugin) Status() core.ServiceStatus {
	return core.StatusHealthy
}

func (p *MCPPlugin) Execute(action string, params map[string]interface{}) (interface{}, error) {
	return nil, nil
}
