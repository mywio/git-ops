package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mywio/git-ops/pkg/core"
)

type poolConfig struct {
	Name             string   `yaml:"name" json:"name"`
	RepositoryTopics []string `yaml:"repository_topics" json:"repository_topics"`
	RunnerLabels     []string `yaml:"runner_labels" json:"runner_labels"`
}

type fleetConfig struct {
	Enabled            bool          `yaml:"enabled"`
	ListenAddr         string        `yaml:"listen_addr"`
	PublicURL          string        `yaml:"public_url"`
	TLSCertFile        string        `yaml:"tls_cert_file"`
	TLSKeyFile         string        `yaml:"tls_key_file"`
	AgentCACertFile    string        `yaml:"agent_ca_cert_file"`
	AgentCAKeyFile     string        `yaml:"agent_ca_key_file"`
	StateDB            string        `yaml:"state_db"`
	EnrollmentTokenTTL time.Duration `yaml:"-"`
	HeartbeatInterval  time.Duration `yaml:"-"`
	OfflineAfter       time.Duration `yaml:"-"`
	Pools              []poolConfig  `yaml:"pools"`
	EnrollmentTTLRaw   string        `yaml:"enrollment_token_ttl"`
	HeartbeatRaw       string        `yaml:"heartbeat_interval"`
	OfflineRaw         string        `yaml:"offline_after"`
}

func defaultFleetConfig() fleetConfig {
	return fleetConfig{
		ListenAddr: ":8443", StateDB: "/var/lib/git-ops/fleet.db",
		EnrollmentTokenTTL: 15 * time.Minute, HeartbeatInterval: 15 * time.Second,
		OfflineAfter: time.Minute,
	}
}

func decodeFleetConfig(section map[string]any) (fleetConfig, error) {
	cfg := defaultFleetConfig()
	if len(section) == 0 {
		return cfg, nil
	}
	if err := core.DecodeConfigSection(section, &cfg); err != nil {
		return cfg, fmt.Errorf("decode fleet_controller config: %w", err)
	}
	for raw, target := range map[string]*time.Duration{
		cfg.EnrollmentTTLRaw: &cfg.EnrollmentTokenTTL,
		cfg.HeartbeatRaw:     &cfg.HeartbeatInterval,
		cfg.OfflineRaw:       &cfg.OfflineAfter,
	} {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		value, err := time.ParseDuration(raw)
		if err != nil || value <= 0 {
			return cfg, fmt.Errorf("invalid fleet duration %q", raw)
		}
		*target = value
	}
	if !cfg.Enabled {
		return cfg, nil
	}
	for name, value := range map[string]string{
		"public_url": cfg.PublicURL, "tls_cert_file": cfg.TLSCertFile,
		"tls_key_file": cfg.TLSKeyFile, "agent_ca_cert_file": cfg.AgentCACertFile,
		"agent_ca_key_file": cfg.AgentCAKeyFile,
	} {
		if strings.TrimSpace(value) == "" {
			return cfg, fmt.Errorf("fleet_controller.%s is required", name)
		}
	}
	if !filepath.IsAbs(cfg.StateDB) {
		return cfg, fmt.Errorf("fleet_controller.state_db must be absolute")
	}
	if len(cfg.Pools) == 0 {
		return cfg, fmt.Errorf("fleet_controller.pools must contain at least one pool")
	}
	seen := map[string]bool{}
	for i := range cfg.Pools {
		pool := &cfg.Pools[i]
		pool.Name = strings.TrimSpace(pool.Name)
		if pool.Name == "" || seen[pool.Name] {
			return cfg, fmt.Errorf("fleet pool names must be non-empty and unique")
		}
		seen[pool.Name] = true
		pool.RepositoryTopics = cleanStrings(pool.RepositoryTopics)
		pool.RunnerLabels = cleanStrings(append(pool.RunnerLabels, "self-hosted", "ghops", pool.Name))
		if len(pool.RepositoryTopics) == 0 {
			return cfg, fmt.Errorf("fleet pool %s requires repository_topics", pool.Name)
		}
	}
	return cfg, nil
}

func cleanStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func (cfg fleetConfig) pool(name string) (poolConfig, bool) {
	for _, pool := range cfg.Pools {
		if pool.Name == name {
			return pool, true
		}
	}
	return poolConfig{}, false
}
