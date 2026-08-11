// Package main contains a development-template plugin that demonstrates the
// minimum git-ops plugin patterns. It is a scaffold, not a production plugin.
package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/mywio/git-ops/pkg/core"
)

type exampleConfig struct {
	Enabled bool   `yaml:"enabled"`
	Message string `yaml:"message"`
	Token   string `yaml:"token"`
}

type ExamplePlugin struct {
	logger       *slog.Logger
	registry     core.PluginRegistry
	cfg          exampleConfig
	enabled      bool
	publishEvent func(context.Context, core.InternalEvent)
}

// Plugin is the exported symbol discovered by the plugin loader.
var Plugin core.Plugin = &ExamplePlugin{}

func (p *ExamplePlugin) Name() string {
	return "example"
}

func (p *ExamplePlugin) Description() string {
	return "Development template plugin demonstrating current git-ops plugin patterns"
}

func (p *ExamplePlugin) Capabilities() []core.Capability {
	return nil
}

func (p *ExamplePlugin) Init(ctx context.Context, logger *slog.Logger, registry core.PluginRegistry) error {
	p.logger = logger
	p.registry = registry
	if p.publishEvent == nil && registry != nil {
		p.publishEvent = registry.Publish
	}

	if registry != nil {
		if section, ok := registry.GetConfig()["example"]; ok {
			if err := core.DecodeConfigSection(section, &p.cfg); err != nil {
				return fmt.Errorf("decode example config: %w", err)
			}
		}
		if err := registry.RegisterEventType(core.EventTypeDesc{
			Name:        "example_event",
			Description: "Example plugin lifecycle event",
			PayloadSpec: map[string]core.PayloadField{
				"message": {Type: "string", Description: "Example message", Required: true},
			},
		}); err != nil {
			return fmt.Errorf("register example_event: %w", err)
		}
		registry.Subscribe("reconcile_now", p.handleReconcileNow)
	}

	p.enabled = p.cfg.Enabled
	return nil
}

func (p *ExamplePlugin) Start(ctx context.Context) error {
	return nil
}

func (p *ExamplePlugin) Stop(ctx context.Context) error {
	return nil
}

func (p *ExamplePlugin) Status() core.ServiceStatus {
	if !p.enabled {
		return core.StatusUnknown
	}
	return core.StatusHealthy
}

func (p *ExamplePlugin) Execute(ctx context.Context, action string, params map[string]interface{}) (interface{}, error) {
	switch action {
	case "ping":
		return map[string]any{
			"ok":      true,
			"message": p.message(),
		}, nil
	default:
		return nil, fmt.Errorf("unknown action: %s", action)
	}
}

func (p *ExamplePlugin) Config() any {
	return struct {
		Enabled bool        `json:"enabled"`
		Message string      `json:"message"`
		Token   core.Secret `json:"token"`
	}{
		Enabled: p.enabled,
		Message: p.cfg.Message,
		Token:   core.NewSecret(p.cfg.Token),
	}
}

func (p *ExamplePlugin) handleReconcileNow(ctx context.Context, event core.InternalEvent) {
	if !p.enabled {
		return
	}
	if p.logger != nil {
		p.logger.InfoContext(ctx, "Example plugin received reconcile_now", "source", event.Source)
	}
	if p.publishEvent != nil {
		p.publishEvent(ctx, core.InternalEvent{
			Type:   "example_event",
			Source: p.Name(),
			Details: map[string]any{
				"message": p.message(),
			},
			Message: "Example plugin observed reconcile_now",
		})
	}
}

func (p *ExamplePlugin) message() string {
	if p.cfg.Message != "" {
		return p.cfg.Message
	}
	return "hello from example plugin"
}
