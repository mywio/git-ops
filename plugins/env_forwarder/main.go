package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mywio/git-ops/pkg/config"
	"github.com/mywio/git-ops/pkg/core"
)

type EnvForwarderPlugin struct {
	logger    *slog.Logger
	keys      []string
	prefixes  []string
	enabled   bool
	env       map[string]string
	statePath string

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
		if coreSection, ok := cfg["core"]; ok {
			coreCfg := config.LoadConfigFromMap(coreSection)
			p.statePath = envForwarderStatePath(coreCfg.TargetDir)
		}
	}
	if p.statePath == "" {
		p.statePath = envForwarderStatePath("")
	}

	if len(p.keys) == 0 && len(p.prefixes) == 0 {
		p.logger.WarnContext(ctx, "env_forwarder has no keys or prefixes configured, disabled")
		p.enabled = false
		p.env = map[string]string{}
		p.setStats(envForwarderStats{
			ConfiguredKeys:     len(p.keys),
			ConfiguredPrefixes: len(p.prefixes),
		})
		return nil
	}

	snapshot, err := p.buildSnapshot()
	if err != nil {
		return err
	}
	p.env = snapshot

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

	secrets := cloneStringMap(p.env)

	for _, prefix := range p.prefixes {
		stats.PrefixMatches[prefix] = 0
		stats.PrefixForwarded[prefix] = 0
	}
	configuredKeys := make(map[string]struct{}, len(p.keys))
	for _, key := range p.keys {
		if key != "" {
			configuredKeys[key] = struct{}{}
		}
	}

	for _, key := range p.keys {
		if key == "" {
			continue
		}
		value, ok := secrets[key]
		if !ok {
			p.logger.Warn("Env var not set", "key", key)
			stats.MissingKeys = append(stats.MissingKeys, key)
			continue
		}
		secrets[key] = value
		stats.ForwardedFromKeys++
	}

	if len(p.prefixes) > 0 {
		for key := range secrets {
			for _, prefix := range p.prefixes {
				if prefix == "" {
					continue
				}
				if strings.HasPrefix(key, prefix) {
					stats.PrefixMatches[prefix]++
					if _, exists := configuredKeys[key]; !exists {
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

func (p *EnvForwarderPlugin) buildSnapshot() (map[string]string, error) {
	persisted, err := loadPersistedEnvForwarderSnapshot(p.statePath)
	if err != nil {
		return nil, err
	}

	live := p.collectLiveEnv()
	snapshot := make(map[string]string, len(persisted)+len(live))
	for key, value := range persisted {
		snapshot[key] = value
	}
	for key, value := range live {
		snapshot[key] = value
	}

	if err := persistEnvForwarderSnapshot(p.statePath, snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (p *EnvForwarderPlugin) collectLiveEnv() map[string]string {
	configuredKeys := make(map[string]struct{}, len(p.keys))
	for _, key := range p.keys {
		configuredKeys[key] = struct{}{}
	}

	out := make(map[string]string)
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := parts[0]
		value := parts[1]
		if _, ok := configuredKeys[key]; ok {
			out[key] = value
			continue
		}
		for _, prefix := range p.prefixes {
			if prefix != "" && strings.HasPrefix(key, prefix) {
				out[key] = value
				break
			}
		}
	}
	return out
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

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func envForwarderStatePath(targetDir string) string {
	baseDir := strings.TrimSpace(targetDir)
	if baseDir == "" {
		baseDir = "."
	}
	return filepath.Join(baseDir, ".git-ops", "env_forwarder_snapshot.json")
}

func loadPersistedEnvForwarderSnapshot(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("read env_forwarder state: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return map[string]string{}, nil
	}

	var snapshot map[string]string
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("decode env_forwarder state: %w", err)
	}
	if snapshot == nil {
		return map[string]string{}, nil
	}
	return snapshot, nil
}

func persistEnvForwarderSnapshot(path string, snapshot map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create env_forwarder state dir: %w", err)
	}

	keys := make([]string, 0, len(snapshot))
	for key := range snapshot {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	ordered := make(map[string]string, len(snapshot))
	for _, key := range keys {
		ordered[key] = snapshot[key]
	}

	data, err := json.Marshal(ordered)
	if err != nil {
		return fmt.Errorf("encode env_forwarder state: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write env_forwarder state: %w", err)
	}
	return nil
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
