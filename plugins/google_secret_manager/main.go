package main

import (
	"context"
	"log/slog"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"github.com/mywio/GHOps/pkg/core"
)

type SecretManagerPlugin struct {
	client *secretmanager.Client
	logger *slog.Logger
}

var Plugin = &SecretManagerPlugin{}

func (p *SecretManagerPlugin) Name() string {
	return "google_secret_manager"
}

func (p *SecretManagerPlugin) Init(ctx context.Context, logger *slog.Logger) error {
	p.logger = logger
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		return err
	}
	p.client = client
	return nil
}

func (p *SecretManagerPlugin) Start(ctx context.Context) error {
	p.logger.Info("Secret Manager Plugin Started")
	return nil
}

func (p *SecretManagerPlugin) Stop(ctx context.Context) error {
	if p.client != nil {
		return p.client.Close()
	}
	return nil
}

func (p *SecretManagerPlugin) Capabilities() []core.Capability {
	return []core.Capability{"secrets"}
}

func (p *SecretManagerPlugin) Status() core.ServiceStatus {
	if p.client == nil {
		return core.StatusUnhealthy
	}
	return core.StatusHealthy
}
