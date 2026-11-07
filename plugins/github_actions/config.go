package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mywio/git-ops/pkg/core"
)

type authConfig struct {
	Type           string `yaml:"type" json:"type"`
	AppID          int64  `yaml:"app_id" json:"app_id,omitempty"`
	InstallationID int64  `yaml:"installation_id" json:"installation_id,omitempty"`
	PrivateKeyFile string `yaml:"private_key_file" json:"private_key_file,omitempty"`
	TokenEnv       string `yaml:"token_env" json:"token_env,omitempty"`
	TokenFile      string `yaml:"token_file" json:"token_file,omitempty"`
}

type connectionConfig struct {
	Name              string     `yaml:"name" json:"name"`
	Owners            []string   `yaml:"owners" json:"owners"`
	Auth              authConfig `yaml:"auth" json:"auth"`
	WebhookSecretFile string     `yaml:"webhook_secret_file" json:"webhook_secret_file,omitempty"`
}

type actionsConfig struct {
	Enabled            bool               `yaml:"enabled"`
	StateDB            string             `yaml:"state_db"`
	RefreshIntervalRaw string             `yaml:"refresh_interval"`
	MaxLogDownload     int64              `yaml:"max_log_download"`
	Connections        []connectionConfig `yaml:"connections"`
	RefreshInterval    time.Duration      `yaml:"-"`
}

func decodeActionsConfig(section map[string]any) (actionsConfig, error) {
	cfg := actionsConfig{StateDB: "/var/lib/git-ops/github-actions.db", RefreshInterval: time.Minute, MaxLogDownload: 25 << 20}
	if len(section) > 0 {
		if err := core.DecodeConfigSection(section, &cfg); err != nil {
			return cfg, fmt.Errorf("decode github_actions config: %w", err)
		}
	}
	if cfg.RefreshIntervalRaw != "" {
		value, err := time.ParseDuration(cfg.RefreshIntervalRaw)
		if err != nil || value < 15*time.Second {
			return cfg, fmt.Errorf("github_actions.refresh_interval must be at least 15s")
		}
		cfg.RefreshInterval = value
	}
	if !cfg.Enabled {
		return cfg, nil
	}
	if !filepath.IsAbs(cfg.StateDB) {
		return cfg, fmt.Errorf("github_actions.state_db must be absolute")
	}
	if len(cfg.Connections) == 0 {
		return cfg, fmt.Errorf("github_actions.connections is required")
	}
	owners := map[string]string{}
	names := map[string]bool{}
	for i := range cfg.Connections {
		connection := &cfg.Connections[i]
		connection.Name = strings.TrimSpace(connection.Name)
		if connection.Name == "" || names[connection.Name] {
			return cfg, fmt.Errorf("GitHub connection names must be unique")
		}
		names[connection.Name] = true
		if connection.Auth.Type != "github_app" && connection.Auth.Type != "pat" {
			return cfg, fmt.Errorf("connection %s has unsupported auth type", connection.Name)
		}
		if connection.Auth.Type == "github_app" {
			if connection.Auth.AppID == 0 || connection.Auth.InstallationID == 0 || connection.Auth.PrivateKeyFile == "" {
				return cfg, fmt.Errorf("connection %s requires app_id, installation_id, and private_key_file", connection.Name)
			}
		} else if (connection.Auth.TokenEnv == "") == (connection.Auth.TokenFile == "") {
			return cfg, fmt.Errorf("connection %s PAT requires exactly one token_env or token_file", connection.Name)
		}
		for _, owner := range connection.Owners {
			key := strings.ToLower(strings.TrimSpace(owner))
			if key == "" {
				continue
			}
			if prior := owners[key]; prior != "" {
				return cfg, fmt.Errorf("owner %s is mapped to both %s and %s", owner, prior, connection.Name)
			}
			owners[key] = connection.Name
		}
	}
	return cfg, nil
}

func loadPAT(cfg authConfig) (string, error) {
	if cfg.TokenEnv != "" {
		value := strings.TrimSpace(os.Getenv(cfg.TokenEnv))
		if value == "" {
			return "", fmt.Errorf("PAT environment variable %s is empty", cfg.TokenEnv)
		}
		return value, nil
	}
	data, err := os.ReadFile(cfg.TokenFile)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", fmt.Errorf("PAT file is empty")
	}
	return value, nil
}
