package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mywio/git-ops/pkg/core"
	"gopkg.in/yaml.v3"
)

type localDeployConfigInput struct {
	Enabled             bool     `yaml:"enabled"`
	RequireSystemd      *bool    `yaml:"require_systemd"`
	StateDir            string   `yaml:"state_dir"`
	CacheDir            string   `yaml:"cache_dir"`
	LogDir              string   `yaml:"log_dir"`
	GlobalConcurrency   int      `yaml:"global_concurrency"`
	CheckoutRetention   int      `yaml:"checkout_retention"`
	ExecutionRetention  int      `yaml:"execution_retention"`
	SuccessfulRetention int      `yaml:"successful_retention"`
	MaxLogSize          any      `yaml:"max_log_size"`
	AllowedEnvironment  []string `yaml:"allowed_environment"`
}

type localDeployConfig struct {
	Enabled             bool
	RequireSystemd      bool
	StateDir            string
	CacheDir            string
	LogDir              string
	GlobalConcurrency   int
	CheckoutRetention   int
	ExecutionRetention  int
	SuccessfulRetention int
	MaxLogSize          int64
	AllowedEnvironment  []string
}

func defaultLocalDeployConfig() localDeployConfig {
	return localDeployConfig{
		RequireSystemd:      true,
		StateDir:            "/var/lib/git-ops/local-deploy/state",
		CacheDir:            "/var/lib/git-ops/local-deploy/cache",
		LogDir:              "/var/log/git-ops/local-deploy",
		GlobalConcurrency:   1,
		CheckoutRetention:   5,
		ExecutionRetention:  100,
		SuccessfulRetention: 20,
		MaxLogSize:          10 << 20,
	}
}

func decodeLocalDeployConfig(section map[string]any) (localDeployConfig, error) {
	cfg := defaultLocalDeployConfig()
	if len(section) == 0 {
		return cfg, nil
	}
	normalized, err := normalizeLocalDeploySection(section)
	if err != nil {
		return cfg, err
	}
	data, err := yaml.Marshal(normalized)
	if err != nil {
		return cfg, err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var input localDeployConfigInput
	if err := decoder.Decode(&input); err != nil {
		return cfg, fmt.Errorf("decode local_deploy config: %w", err)
	}
	cfg.Enabled = input.Enabled
	if input.RequireSystemd != nil {
		cfg.RequireSystemd = *input.RequireSystemd
	}
	if input.StateDir != "" {
		cfg.StateDir = input.StateDir
	}
	if input.CacheDir != "" {
		cfg.CacheDir = input.CacheDir
	}
	if input.LogDir != "" {
		cfg.LogDir = input.LogDir
	}
	if input.GlobalConcurrency != 0 {
		cfg.GlobalConcurrency = input.GlobalConcurrency
	}
	if input.CheckoutRetention != 0 {
		cfg.CheckoutRetention = input.CheckoutRetention
	}
	if input.ExecutionRetention != 0 {
		cfg.ExecutionRetention = input.ExecutionRetention
	}
	if input.SuccessfulRetention != 0 {
		cfg.SuccessfulRetention = input.SuccessfulRetention
	}
	if input.MaxLogSize != nil {
		size, err := parseByteSize(input.MaxLogSize)
		if err != nil {
			return cfg, fmt.Errorf("local_deploy.max_log_size: %w", err)
		}
		cfg.MaxLogSize = size
	}
	cfg.AllowedEnvironment = uniqueEnvironmentNames(input.AllowedEnvironment)
	if cfg.GlobalConcurrency < 1 {
		return cfg, fmt.Errorf("local_deploy.global_concurrency must be at least 1")
	}
	if cfg.CheckoutRetention < 1 || cfg.ExecutionRetention < 1 || cfg.SuccessfulRetention < 1 {
		return cfg, fmt.Errorf("local_deploy retention values must be at least 1")
	}
	if cfg.SuccessfulRetention > 20 {
		return cfg, fmt.Errorf("local_deploy.successful_retention must not exceed 20")
	}
	if cfg.MaxLogSize < 1024 {
		return cfg, fmt.Errorf("local_deploy.max_log_size must be at least 1024 bytes")
	}
	for _, dir := range []string{cfg.StateDir, cfg.CacheDir, cfg.LogDir} {
		if !filepath.IsAbs(dir) {
			return cfg, fmt.Errorf("local_deploy directories must be absolute: %s", dir)
		}
	}
	return cfg, nil
}

func parseByteSize(value any) (int64, error) {
	switch typed := value.(type) {
	case int:
		return int64(typed), nil
	case int64:
		return typed, nil
	case uint64:
		if typed > uint64(^uint64(0)>>1) {
			return 0, fmt.Errorf("size overflows int64")
		}
		return int64(typed), nil
	case string:
		raw := strings.TrimSpace(strings.ToUpper(typed))
		multiplier := int64(1)
		for suffix, scale := range map[string]int64{"KIB": 1 << 10, "MIB": 1 << 20, "GIB": 1 << 30, "KB": 1000, "MB": 1000 * 1000, "GB": 1000 * 1000 * 1000} {
			if strings.HasSuffix(raw, suffix) {
				multiplier = scale
				raw = strings.TrimSpace(strings.TrimSuffix(raw, suffix))
				break
			}
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid byte size %q", typed)
		}
		return n * multiplier, nil
	default:
		return 0, fmt.Errorf("must be a byte count or size string")
	}
}

func uniqueEnvironmentNames(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || strings.Contains(value, "=") {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

type revisionRequest struct {
	Owner          string `json:"owner"`
	Repo           string `json:"repo"`
	Ref            string `json:"ref"`
	SHA            string `json:"sha"`
	DeliveryID     string `json:"delivery_id,omitempty"`
	Trigger        string `json:"trigger"`
	RequestedBy    string `json:"requested_by,omitempty"`
	ManualRollback bool   `json:"manual_rollback,omitempty"`
}

type deploymentExecution struct {
	ID                 string               `json:"id"`
	SHA                string               `json:"sha"`
	PreviousSuccessful string               `json:"previous_success_sha,omitempty"`
	Trigger            string               `json:"trigger"`
	Status             core.ExecutionStatus `json:"status"`
	Phase              core.ExecutionStage  `json:"phase"`
	FailureClass       core.FailureClass    `json:"failure_class,omitempty"`
	Error              string               `json:"error,omitempty"`
	Verification       string               `json:"verification"`
	LogPath            string               `json:"log_path"`
	StartedAt          time.Time            `json:"started_at"`
	FinishedAt         *time.Time           `json:"finished_at,omitempty"`
	RollbackTarget     string               `json:"rollback_target,omitempty"`
	RollbackStatus     string               `json:"rollback_status,omitempty"`
	RollbackCommands   []string             `json:"rollback_commands,omitempty"`
}

type successfulRevision struct {
	SHA              string    `json:"sha"`
	ExecutionID      string    `json:"execution_id"`
	DeployedAt       time.Time `json:"deployed_at"`
	Verification     string    `json:"verification"`
	RollbackCommands []string  `json:"rollback_commands,omitempty"`
}

type repositoryState struct {
	Owner                 string                `json:"owner"`
	Repo                  string                `json:"repo"`
	CurrentSHA            string                `json:"current_sha,omitempty"`
	PreviousSuccessfulSHA string                `json:"previous_success_sha,omitempty"`
	Pending               *revisionRequest      `json:"pending,omitempty"`
	Active                *deploymentExecution  `json:"active,omitempty"`
	History               []deploymentExecution `json:"history,omitempty"`
	Successful            []successfulRevision  `json:"successful,omitempty"`
}

func stateFile(root, owner, repo string) string {
	return filepath.Join(root, "repositories", owner, repo, "state.json")
}

func loadRepositoryStates(root string) ([]repositoryState, error) {
	var states []repositoryState
	base := filepath.Join(root, "repositories")
	err := filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() || entry.Name() != "state.json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var state repositoryState
		if err := json.Unmarshal(data, &state); err != nil {
			return fmt.Errorf("decode %s: %w", path, err)
		}
		states = append(states, state)
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return states, err
}

func persistRepositoryState(root string, state repositoryState) error {
	path := stateFile(root, state.Owner, state.Repo)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
