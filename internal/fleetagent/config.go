package fleetagent

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ControllerURL              string        `yaml:"controller_url"`
	EnrollmentToken            string        `yaml:"enrollment_token"`
	Pool                       string        `yaml:"pool"`
	NodeName                   string        `yaml:"node_name"`
	StateDir                   string        `yaml:"state_dir"`
	RunnerRoot                 string        `yaml:"runner_root"`
	WorkRoot                   string        `yaml:"work_root"`
	ServerCAFile               string        `yaml:"server_ca_file"`
	HeartbeatInterval          time.Duration `yaml:"-"`
	HeartbeatIntervalRaw       string        `yaml:"heartbeat_interval"`
	AllowedSupplementaryGroups []string      `yaml:"allowed_supplementary_groups"`
	MaxRunners                 int           `yaml:"max_runners"`
}

func LoadConfig(path string) (Config, error) {
	cfg := Config{StateDir: "/var/lib/git-ops-agent", RunnerRoot: "/opt/actions-runner",
		WorkRoot: "/var/lib/git-ops-runner", HeartbeatInterval: 15 * time.Second, MaxRunners: 4}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, err
	}
	if value := strings.TrimSpace(os.Getenv("GHOPS_AGENT_ENROLLMENT_TOKEN")); value != "" {
		cfg.EnrollmentToken = value
	}
	if cfg.NodeName == "" {
		cfg.NodeName, _ = os.Hostname()
	}
	if cfg.HeartbeatIntervalRaw != "" {
		cfg.HeartbeatInterval, err = time.ParseDuration(cfg.HeartbeatIntervalRaw)
		if err != nil || cfg.HeartbeatInterval <= 0 {
			return cfg, fmt.Errorf("invalid heartbeat_interval")
		}
	}
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		return cfg, fmt.Errorf("git-ops-agent supports linux amd64 and arm64")
	}
	if cfg.ControllerURL == "" || cfg.Pool == "" || cfg.NodeName == "" || cfg.ServerCAFile == "" {
		return cfg, fmt.Errorf("controller_url, pool, node_name, and server_ca_file are required")
	}
	for _, path := range []string{cfg.StateDir, cfg.RunnerRoot, cfg.WorkRoot} {
		if !filepath.IsAbs(path) {
			return cfg, fmt.Errorf("agent directories must be absolute")
		}
	}
	if cfg.MaxRunners < 1 {
		return cfg, fmt.Errorf("max_runners must be at least one")
	}
	return cfg, nil
}
