package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/mywio/git-ops/pkg/core"
)

type EnvForwarderPlugin struct {
	logger   *slog.Logger
	keys     []string
	prefixes []string
	enabled  bool
}

type envForwarderConfig struct {
	Keys     []string `yaml:"keys"`
	Prefixes []string `yaml:"prefixes"`
}

var Plugin core.Plugin = &EnvForwarderPlugin{}

func (p *EnvForwarderPlugin) Name() string {
	return "env_forwarder"
}

func (p *EnvForwarderPlugin) Description() string {
	return "Forwards allowlisted environment variables into docker compose execution"
}

func (p *EnvForwarderPlugin) Init(ctx context.Context, logger *slog.Logger, registry core.PluginRegistry) error {
	p.logger = logger

	if registry != nil {
		cfg := registry.GetConfig()
		if section, ok := cfg["env_forwarder"]; ok {
			var ecfg envForwarderConfig
			if err := core.DecodeConfigSection(section, &ecfg); err != nil {
				p.logger.WarnContext(ctx, "Invalid env_forwarder config", "error", err)
			}
			p.keys = normalizeList(ecfg.Keys)
			p.prefixes = normalizeList(ecfg.Prefixes)
		}
	}

	if len(p.keys) == 0 && len(p.prefixes) == 0 {
		p.logger.WarnContext(ctx, "env_forwarder has no keys or prefixes configured, disabled")
		p.enabled = false
		return nil
	}

	p.enabled = true
	p.logger.InfoContext(ctx, "env_forwarder initialized", "keys", len(p.keys), "prefixes", len(p.prefixes))
	return nil
}

func (p *EnvForwarderPlugin) Start(ctx context.Context) error {
	return nil
}

func (p *EnvForwarderPlugin) Stop(ctx context.Context) error {
	return nil
}

func (p *EnvForwarderPlugin) Capabilities() []core.Capability {
	return []core.Capability{core.CapabilitySecrets}
}

func (p *EnvForwarderPlugin) Status() core.ServiceStatus {
	if p.enabled {
		return core.StatusHealthy
	}
	return core.StatusDegraded
}

func (p *EnvForwarderPlugin) Execute(action string, params map[string]interface{}) (interface{}, error) {
	if action != "get_secrets" {
		return nil, fmt.Errorf("unknown action: %s", action)
	}
	if !p.enabled {
		return map[string]string{}, nil
	}

	secrets := make(map[string]string)

	for _, key := range p.keys {
		if key == "" {
			continue
		}
		value, ok := os.LookupEnv(key)
		if !ok {
			p.logger.Warn("Env var not set", "key", key)
			core.Publish(context.Background(), core.InternalEvent{
				Type:   "notify_env_forwarder_missing",
				Source: "env_forwarder",
				String: fmt.Sprintf("Env var %s not set", key),
				Details: map[string]interface{}{
					"key": key,
				},
			})
			continue
		}
		secrets[key] = value
	}

	if len(p.prefixes) > 0 {
		for _, env := range os.Environ() {
			parts := strings.SplitN(env, "=", 2)
			if len(parts) != 2 {
				continue
			}
			key := parts[0]
			value := parts[1]
			for _, prefix := range p.prefixes {
				if prefix == "" {
					continue
				}
				if strings.HasPrefix(key, prefix) {
					if _, exists := secrets[key]; !exists {
						secrets[key] = value
					}
					break
				}
			}
		}
	}

	return secrets, nil
}

func normalizeList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
