package main

import (
	"context"
	"embed"
	"log/slog"
	"net/http"

	"github.com/mywio/git-ops/pkg/core"
)

//go:embed frontend/dist
var frontendFS embed.FS

type UIPlugin struct {
	mux      *http.ServeMux
	logger   *slog.Logger
	registry core.PluginRegistry
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
	p.registry = registry
	if registry != nil {
		p.mux = registry.GetMuxServer()
		p.registerRoutes()
	}
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
	return []core.Capability{core.CapabilityUI, core.CapabilityAPI}
}

func (p *UIPlugin) Status() core.ServiceStatus {
	return core.StatusHealthy
}

func (p *UIPlugin) Execute(ctx context.Context, action string, params map[string]interface{}) (interface{}, error) {
	return nil, nil
}
