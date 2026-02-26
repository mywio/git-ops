package main

import (
	"context"
	"log/slog"

	"github.com/mywio/git-ops/pkg/core"
)

type NotificationPlugin struct {
	logger *slog.Logger
}

var Plugin = &NotificationPlugin{}

func (p *NotificationPlugin) Name() string {
	return "notifications"
}

func (p *NotificationPlugin) Description() string {
	return "Sends notifications (Pushover, Webhooks)"
}

func (p *NotificationPlugin) Init(ctx context.Context, logger *slog.Logger, registry core.PluginRegistry) error {
	p.logger = logger
	return nil
}

func (p *NotificationPlugin) Start(ctx context.Context) error {
	p.logger.Info("Notification Plugin Started")
	return nil
}

func (p *NotificationPlugin) Stop(ctx context.Context) error {
	return nil
}

func (p *NotificationPlugin) Capabilities() []core.Capability {
	return []core.Capability{"notifications"}
}

func (p *NotificationPlugin) Status() core.ServiceStatus {
	return core.StatusHealthy
}

func (p *NotificationPlugin) Execute(action string, params map[string]interface{}) (interface{}, error) {
	return nil, nil
}
