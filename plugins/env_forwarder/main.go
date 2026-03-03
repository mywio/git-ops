package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mywio/git-ops/pkg/core"
)

type EnvForwarderPlugin struct {
	logger   *slog.Logger
	keys     []string
	prefixes []string
	enabled  bool

	statsMu sync.RWMutex
	stats   envForwarderStats
}

type envForwarderConfig struct {
	Keys     []string `yaml:"keys"`
	Prefixes []string `yaml:"prefixes"`
}

type envForwarderStats struct {
	ConfiguredKeys        int
	ConfiguredPrefixes    int
	ForwardedTotal        int
	ForwardedFromKeys     int
	ForwardedFromPrefixes int
	MissingKeys           []string
	PrefixMatches         map[string]int
	PrefixForwarded       map[string]int
	LastUpdated           time.Time
}

type envForwarderStatsView struct {
	ConfiguredKeys        int            `json:"configured_keys"`
	ConfiguredPrefixes    int            `json:"configured_prefixes"`
	ForwardedTotal        int            `json:"forwarded_total"`
	ForwardedFromKeys     int            `json:"forwarded_from_keys"`
	ForwardedFromPrefixes int            `json:"forwarded_from_prefixes"`
	MissingKeys           []string       `json:"missing_keys,omitempty"`
	PrefixMatches         map[string]int `json:"prefix_matches,omitempty"`
	PrefixForwarded       map[string]int `json:"prefix_forwarded,omitempty"`
	LastUpdated           string         `json:"last_updated,omitempty"`
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
		p.setStats(envForwarderStats{
			ConfiguredKeys:     len(p.keys),
			ConfiguredPrefixes: len(p.prefixes),
		})
		return nil
	}

	p.enabled = true
	p.setStats(envForwarderStats{
		ConfiguredKeys:     len(p.keys),
		ConfiguredPrefixes: len(p.prefixes),
	})
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

func (p *EnvForwarderPlugin) Execute(ctx context.Context, action string, params map[string]interface{}) (interface{}, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	switch action {
	case "get_secrets":
		secrets, stats := p.collectSecrets(ctx)
		p.setStats(stats)
		return secrets, nil
	case "get_stats":
		return p.getStatsView(), nil
	default:
		return nil, fmt.Errorf("unknown action: %s", action)
	}
}

type envForwarderConfigView struct {
	Keys     []string              `json:"keys,omitempty"`
	Prefixes []string              `json:"prefixes,omitempty"`
	Enabled  bool                  `json:"enabled"`
	Stats    envForwarderStatsView `json:"stats"`
}

func (p *EnvForwarderPlugin) Config() any {
	return envForwarderConfigView{
		Keys:     append([]string(nil), p.keys...),
		Prefixes: append([]string(nil), p.prefixes...),
		Enabled:  p.enabled,
		Stats:    p.getStatsView(),
	}
}

func (p *EnvForwarderPlugin) collectSecrets(ctx context.Context) (map[string]string, envForwarderStats) {
	stats := envForwarderStats{
		ConfiguredKeys:     len(p.keys),
		ConfiguredPrefixes: len(p.prefixes),
		MissingKeys:        []string{},
		PrefixMatches:      map[string]int{},
		PrefixForwarded:    map[string]int{},
	}

	if !p.enabled {
		return map[string]string{}, stats
	}

	secrets := make(map[string]string)
	envMap := make(map[string]string)
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}
		envMap[parts[0]] = parts[1]
	}

	for _, prefix := range p.prefixes {
		stats.PrefixMatches[prefix] = 0
		stats.PrefixForwarded[prefix] = 0
	}

	for _, key := range p.keys {
		if key == "" {
			continue
		}
		value, ok := envMap[key]
		if !ok {
			p.logger.Warn("Env var not set", "key", key)
			core.Publish(ctx, core.InternalEvent{
				Type:   "notify_env_forwarder_missing",
				Source: "env_forwarder",
				String: fmt.Sprintf("Env var %s not set", key),
				Details: map[string]interface{}{
					"key": key,
				},
			})
			stats.MissingKeys = append(stats.MissingKeys, key)
			continue
		}
		secrets[key] = value
		stats.ForwardedFromKeys++
	}

	if len(p.prefixes) > 0 {
		for key, value := range envMap {
			for _, prefix := range p.prefixes {
				if prefix == "" {
					continue
				}
				if strings.HasPrefix(key, prefix) {
					stats.PrefixMatches[prefix]++
					if _, exists := secrets[key]; !exists {
						secrets[key] = value
						stats.ForwardedFromPrefixes++
						stats.PrefixForwarded[prefix]++
					}
					break
				}
			}
		}
	}

	stats.ForwardedTotal = len(secrets)
	stats.LastUpdated = time.Now().UTC()
	return secrets, stats
}

func (p *EnvForwarderPlugin) setStats(stats envForwarderStats) {
	p.statsMu.Lock()
	defer p.statsMu.Unlock()
	p.stats = stats
}

func (p *EnvForwarderPlugin) getStatsView() envForwarderStatsView {
	p.statsMu.RLock()
	defer p.statsMu.RUnlock()

	lastUpdated := ""
	if !p.stats.LastUpdated.IsZero() {
		lastUpdated = p.stats.LastUpdated.Format(time.RFC3339)
	}
	return envForwarderStatsView{
		ConfiguredKeys:        p.stats.ConfiguredKeys,
		ConfiguredPrefixes:    p.stats.ConfiguredPrefixes,
		ForwardedTotal:        p.stats.ForwardedTotal,
		ForwardedFromKeys:     p.stats.ForwardedFromKeys,
		ForwardedFromPrefixes: p.stats.ForwardedFromPrefixes,
		MissingKeys:           append([]string(nil), p.stats.MissingKeys...),
		PrefixMatches:         cloneStringIntMap(p.stats.PrefixMatches),
		PrefixForwarded:       cloneStringIntMap(p.stats.PrefixForwarded),
		LastUpdated:           lastUpdated,
	}
}

func cloneStringIntMap(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
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
