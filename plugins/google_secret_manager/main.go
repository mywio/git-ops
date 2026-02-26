package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/mywio/git-ops/pkg/core"
	"google.golang.org/api/iterator"
)

type SecretManagerPlugin struct {
	client    *secretmanager.Client
	logger    *slog.Logger
	projectID string
}

var Plugin = &SecretManagerPlugin{}

func (p *SecretManagerPlugin) Name() string {
	return "google_secret_manager"
}

func (p *SecretManagerPlugin) Description() string {
	return "Injects secrets from Google Secret Manager based on repo owner/name"
}

func (p *SecretManagerPlugin) Init(ctx context.Context, logger *slog.Logger, registry core.PluginRegistry) error {
	p.logger = logger

	// Get Project ID from Env
	p.projectID = os.Getenv("GOOGLE_CLOUD_PROJECT")
	if p.projectID == "" {
		logger.Warn("GOOGLE_CLOUD_PROJECT not set, secret fetching will fail")
	}

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

func (p *SecretManagerPlugin) Execute(action string, params map[string]interface{}) (interface{}, error) {
	if action != "get_secrets" {
		return nil, fmt.Errorf("unknown action: %s", action)
	}

	owner, _ := params["owner"].(string)
	repo, _ := params["repo"].(string)

	if owner == "" || repo == "" {
		return nil, fmt.Errorf("missing owner or repo param")
	}

	if p.projectID == "" {
		return map[string]string{}, fmt.Errorf("GOOGLE_CLOUD_PROJECT not configured")
	}

	// Strategy: List secrets with label "git-ops_repo=<owner>-<repo>"
	// or "git-ops_owner=<owner>" AND "git-ops_repo=<repo>"
	// Filter syntax: labels.key=value
	filter := fmt.Sprintf("labels.git-ops_owner=%s AND labels.git-ops_repo=%s", owner, repo)

	req := &secretmanagerpb.ListSecretsRequest{
		Parent: fmt.Sprintf("projects/%s", p.projectID),
		Filter: filter,
	}

	it := p.client.ListSecrets(context.Background(), req)
	secrets := make(map[string]string)

	for {
		resp, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			p.logger.Error("Failed to list secrets", "error", err)
			return nil, err
		}

		// Get the secret payload (latest version)
		// Secret name format: projects/*/secrets/<name>
		versionName := fmt.Sprintf("%s/versions/latest", resp.Name)
		accessReq := &secretmanagerpb.AccessSecretVersionRequest{
			Name: versionName,
		}

		result, err := p.client.AccessSecretVersion(context.Background(), accessReq)
		if err != nil {
			p.logger.Error("Failed to access secret version", "secret", resp.Name, "error", err)
			continue
		}

		// Extract the Env Var Key from the secret name or a label?
		// Let's assume the secret name is the key (or last part of it)
		// Or utilize a label `git-ops_key`
		// For simplicity, let's use the secret name's last part, uppercase
		parts := strings.Split(resp.Name, "/")
		key := strings.ToUpper(parts[len(parts)-1])

		// If there is a specific label for the key, use it
		if val, ok := resp.Labels["git-ops_env_key"]; ok {
			key = val
		}

		secrets[key] = string(result.Payload.Data)
	}

	return secrets, nil
}
