package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mywio/git-ops/pkg/core"
)

type composeRefreshConfig struct {
	Enabled   bool   `yaml:"enabled"`
	TargetDir string `yaml:"target_dir"`
}

type ComposeRefreshPlugin struct {
	logger       *slog.Logger
	registry     core.PluginRegistry
	publishEvent func(context.Context, core.InternalEvent)
	enabled      bool
	targetDir    string
}

type refreshResult struct {
	Owner     string `json:"owner"`
	Repo      string `json:"repo"`
	StackPath string `json:"stack_path"`
	Updated   bool   `json:"updated"`
	Status    string `json:"status"`
}

var Plugin core.Plugin = &ComposeRefreshPlugin{}

var runComposeRefreshCommand = func(stackPath string, args ...string) ([]byte, error) {
	cmd := exec.Command("docker", append([]string{"compose"}, args...)...)
	cmd.Dir = stackPath
	cmd.Env = os.Environ()
	return cmd.CombinedOutput()
}

var inspectComposeRefreshImage = func(imageRef string) (string, error) {
	cmd := exec.Command("docker", "image", "inspect", "--format", "{{.Id}}", imageRef)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func (p *ComposeRefreshPlugin) Name() string { return "compose_refresh" }

func (p *ComposeRefreshPlugin) Description() string {
	return "Manually pulls compose images and restarts stacks when image identities change"
}

func (p *ComposeRefreshPlugin) Capabilities() []core.Capability {
	return []core.Capability{core.CapabilityRefreshStackImages, core.CapabilitySystemInfo}
}

func (p *ComposeRefreshPlugin) Status() core.ServiceStatus {
	if !p.enabled {
		return core.StatusUnknown
	}
	return core.StatusHealthy
}

func (p *ComposeRefreshPlugin) Init(ctx context.Context, logger *slog.Logger, registry core.PluginRegistry) error {
	p.logger = logger
	p.registry = registry
	if registry != nil {
		p.publishEvent = registry.Publish
	}

	cfg, err := p.loadConfig(registry)
	if err != nil {
		return err
	}
	p.enabled = cfg.Enabled
	p.targetDir = cfg.TargetDir
	if p.targetDir == "" {
		p.targetDir = "./stacks"
	}

	if registry != nil {
		for _, desc := range composeRefreshEventTypes() {
			if err := registry.RegisterEventType(desc); err != nil {
				return fmt.Errorf("register event type %s: %w", desc.Name, err)
			}
		}
	}
	return nil
}

func (p *ComposeRefreshPlugin) loadConfig(registry core.PluginRegistry) (composeRefreshConfig, error) {
	cfg := composeRefreshConfig{Enabled: true}
	if registry == nil {
		return cfg, nil
	}
	all := registry.GetConfig()
	if coreSection, ok := all["core"]; ok {
		if targetDir, ok := coreSection["target_dir"].(string); ok {
			cfg.TargetDir = targetDir
		}
	}
	if section, ok := all["compose_refresh"]; ok {
		if err := core.DecodeConfigSection(section, &cfg); err != nil {
			return composeRefreshConfig{}, fmt.Errorf("decode compose_refresh config: %w", err)
		}
	}
	return cfg, nil
}

func (p *ComposeRefreshPlugin) Start(ctx context.Context) error { return nil }
func (p *ComposeRefreshPlugin) Stop(ctx context.Context) error  { return nil }

func (p *ComposeRefreshPlugin) Execute(ctx context.Context, action string, params map[string]interface{}) (interface{}, error) {
	switch action {
	case string(core.CapabilitySystemInfo):
		return map[string]interface{}{"enabled": p.enabled, "target_dir": p.targetDir}, nil
	case string(core.CapabilityRefreshStackImages):
		return p.refreshStackImages(ctx, params)
	default:
		return nil, fmt.Errorf("unknown action: %s", action)
	}
}

func (p *ComposeRefreshPlugin) refreshStackImages(ctx context.Context, params map[string]interface{}) (refreshResult, error) {
	owner, repo, err := requiredComposeRefreshRepoParams(params)
	if err != nil {
		return refreshResult{}, err
	}
	if !p.enabled {
		return refreshResult{}, fmt.Errorf("compose_refresh is disabled")
	}

	stackPath := filepath.Join(p.targetDir, owner, repo)
	result := refreshResult{Owner: owner, Repo: repo, StackPath: stackPath, Status: "started"}
	p.publishLifecycle(ctx, "compose_refresh_started", result, "")

	updated, err := p.pullChangedImages(stackPath)
	if err != nil {
		result.Status = "failed"
		p.publishLifecycle(ctx, "compose_refresh_failed", result, err.Error())
		return result, err
	}
	result.Updated = updated
	if !updated {
		result.Status = "no_update"
		p.publishLifecycle(ctx, "compose_refresh_no_update", result, "")
		return result, nil
	}

	if output, err := runComposeRefreshCommand(stackPath, "up", "-d", "--remove-orphans"); err != nil {
		result.Status = "failed"
		err = fmt.Errorf("docker compose up failed: %w: %s", err, strings.TrimSpace(string(output)))
		p.publishLifecycle(ctx, "compose_refresh_failed", result, err.Error())
		return result, err
	}
	result.Status = "updated"
	p.publishLifecycle(ctx, "compose_refresh_succeeded", result, "")
	return result, nil
}

func (p *ComposeRefreshPlugin) pullChangedImages(stackPath string) (bool, error) {
	before, err := collectComposeRefreshImageIdentities(stackPath)
	if err != nil {
		return false, err
	}
	if output, err := runComposeRefreshCommand(stackPath, "pull"); err != nil {
		return false, fmt.Errorf("docker compose pull failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	after, err := collectComposeRefreshImageIdentities(stackPath)
	if err != nil {
		return false, err
	}
	return composeRefreshIdentitiesChanged(before, after), nil
}

func collectComposeRefreshImageIdentities(stackPath string) (map[string]string, error) {
	output, err := runComposeRefreshCommand(stackPath, "config", "--images")
	if err != nil {
		return nil, fmt.Errorf("docker compose config --images failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	identities := map[string]string{}
	for _, line := range strings.Split(string(output), "\n") {
		imageRef := strings.TrimSpace(line)
		if imageRef == "" {
			continue
		}
		identity, err := inspectComposeRefreshImage(imageRef)
		if err != nil {
			identity = ""
		}
		identities[imageRef] = identity
	}
	return identities, nil
}

func composeRefreshIdentitiesChanged(before, after map[string]string) bool {
	if len(before) != len(after) {
		return true
	}
	for ref, beforeID := range before {
		if after[ref] != beforeID {
			return true
		}
	}
	return false
}

func requiredComposeRefreshRepoParams(params map[string]interface{}) (string, string, error) {
	owner, okOwner := params["owner"].(string)
	repo, okRepo := params["repo"].(string)
	owner = strings.TrimSpace(owner)
	repo = strings.TrimSpace(repo)
	if !okOwner || !okRepo || owner == "" || repo == "" {
		return "", "", fmt.Errorf("refresh_stack_images requires 'owner' and 'repo' string parameters")
	}
	return owner, repo, nil
}

func (p *ComposeRefreshPlugin) publishLifecycle(ctx context.Context, eventType core.EventTypeName, result refreshResult, message string) {
	if p.publishEvent == nil {
		return
	}
	details := map[string]any{
		"owner":      result.Owner,
		"repo":       result.Repo,
		"stack_path": result.StackPath,
		"updated":    result.Updated,
		"status":     result.Status,
	}
	if message != "" {
		details["message"] = message
	}
	p.publishEvent(ctx, core.InternalEvent{Type: eventType, Source: p.Name(), Timestamp: time.Now().UTC(), Details: details})
}

func composeRefreshEventTypes() []core.EventTypeDesc {
	payload := map[string]core.PayloadField{
		"owner":      {Type: "string", Description: "Repository owner", Required: true},
		"repo":       {Type: "string", Description: "Repository name", Required: true},
		"stack_path": {Type: "string", Description: "Absolute local stack path", Required: true},
		"updated":    {Type: "bool", Description: "Whether image identities changed", Required: true},
		"status":     {Type: "string", Description: "Refresh status", Required: true},
		"message":    {Type: "string", Description: "Outcome or error detail", Required: false},
	}
	return []core.EventTypeDesc{
		{Name: "compose_refresh_started", Description: "Manual compose image refresh started", PayloadSpec: cloneComposeRefreshPayload(payload)},
		{Name: "compose_refresh_no_update", Description: "Compose pull found no updated images", PayloadSpec: cloneComposeRefreshPayload(payload)},
		{Name: "compose_refresh_succeeded", Description: "Compose pull updated images and stack was restarted", PayloadSpec: cloneComposeRefreshPayload(payload)},
		{Name: "compose_refresh_failed", Description: "Compose image refresh failed", PayloadSpec: cloneComposeRefreshPayload(payload)},
	}
}

func cloneComposeRefreshPayload(src map[string]core.PayloadField) map[string]core.PayloadField {
	dst := make(map[string]core.PayloadField, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
