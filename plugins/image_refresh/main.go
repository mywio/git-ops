package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/mywio/git-ops/pkg/core"
)

type imageRefreshScheduler interface {
	Schedule(refreshJobRequest) error
}

type refreshJobKey struct {
	FullName  string
	StackPath string
}

type refreshJobRequest struct {
	Key         refreshJobKey
	Owner       string
	Repo        string
	OldCommit   string
	NewCommit   string
	RetryDelays []time.Duration
}

type imageRefreshConfig struct {
	Enabled            bool      `yaml:"enabled"`
	RetryDelaysMinutes []float64 `yaml:"retry_delays_minutes"`
}

type ImageRefreshPlugin struct {
	logger       *slog.Logger
	registry     core.PluginRegistry
	publishEvent func(context.Context, core.InternalEvent)
	jobs         imageRefreshScheduler
	enabled      bool
	retryDelays  []time.Duration
}

var Plugin core.Plugin = &ImageRefreshPlugin{}

func (p *ImageRefreshPlugin) Name() string {
	return "image_refresh"
}

func (p *ImageRefreshPlugin) Description() string {
	return "Refreshes stack images after commit changes without compose changes"
}

func (p *ImageRefreshPlugin) Capabilities() []core.Capability {
	return []core.Capability{core.CapabilitySystem}
}

func (p *ImageRefreshPlugin) Status() core.ServiceStatus {
	if !p.enabled {
		return core.StatusUnknown
	}
	return core.StatusHealthy
}

func (p *ImageRefreshPlugin) Init(ctx context.Context, logger *slog.Logger, registry core.PluginRegistry) error {
	p.logger = logger
	p.registry = registry
	if p.publishEvent == nil && registry != nil {
		p.publishEvent = registry.Publish
	}

	cfg, err := p.loadConfig(registry)
	if err != nil {
		return err
	}
	p.enabled = cfg.Enabled
	p.retryDelays = retryDurations(cfg.RetryDelaysMinutes)
	if len(p.retryDelays) == 0 {
		p.retryDelays = retryDurations([]float64{0, 1, 2, 4, 8})
	}
	p.ensureJobManager(ctx, logger)

	if registry != nil {
		for _, desc := range imageRefreshEventTypes() {
			if err := registry.RegisterEventType(desc); err != nil {
				return fmt.Errorf("register event type %s: %w", desc.Name, err)
			}
		}
	}

	return nil
}

func (p *ImageRefreshPlugin) loadConfig(registry core.PluginRegistry) (imageRefreshConfig, error) {
	cfg := imageRefreshConfig{
		RetryDelaysMinutes: []float64{0, 1, 2, 4, 8},
	}
	if registry == nil {
		return cfg, nil
	}
	section, ok := registry.GetConfig()["image_refresh"]
	if !ok {
		return cfg, nil
	}
	if err := core.DecodeConfigSection(section, &cfg); err != nil {
		return imageRefreshConfig{}, fmt.Errorf("decode image_refresh config: %w", err)
	}
	return cfg, nil
}

func (p *ImageRefreshPlugin) ensureJobManager(ctx context.Context, logger *slog.Logger) {
	if p.jobs != nil {
		return
	}

	p.jobs = newJobManager(jobManagerConfig{
		Logger:  logger,
		Context: ctx,
		RunAttempt: func(runCtx context.Context, attempt refreshAttempt) attemptResult {
			return p.runRefreshAttempt(runCtx, attempt)
		},
		OnSuperseded: func(req refreshJobRequest) {
			p.publishLifecycleEvent(context.Background(), "image_refresh_superseded", req, 1, firstRetryDelay(req), "superseded by newer commit")
		},
		OnRetrying: func(attempt refreshAttempt) {
			p.publishLifecycleEvent(context.Background(), "image_refresh_retrying", attempt.Request, attempt.Number, attempt.Delay, "retry scheduled")
		},
		OnExhausted: func(req refreshJobRequest) {
			p.publishLifecycleEvent(context.Background(), "image_refresh_exhausted", req, len(req.RetryDelays), lastRetryDelay(req), "retry budget exhausted")
		},
	})
}

func (p *ImageRefreshPlugin) Start(ctx context.Context) error {
	if p.registry == nil {
		return nil
	}
	p.registry.Subscribe("stack_commit_changed", p.handleCommitChanged)
	return nil
}

func (p *ImageRefreshPlugin) Stop(ctx context.Context) error {
	if manager, ok := p.jobs.(*jobManager); ok {
		manager.Stop()
	}
	return nil
}

func (p *ImageRefreshPlugin) Execute(ctx context.Context, action string, params map[string]interface{}) (interface{}, error) {
	return nil, fmt.Errorf("unknown action: %s", action)
}

func (p *ImageRefreshPlugin) handleCommitChanged(ctx context.Context, event core.InternalEvent) {
	if !p.enabled {
		return
	}
	if composeChanged, _ := event.Details["compose_changed"].(bool); composeChanged {
		return
	}

	req := refreshJobRequest{
		Key: refreshJobKey{
			FullName:  stringDetail(event, "full_name"),
			StackPath: stringDetail(event, "stack_path"),
		},
		Owner:       stringDetail(event, "owner"),
		Repo:        stringDetail(event, "repo"),
		OldCommit:   stringDetail(event, "old_commit"),
		NewCommit:   stringDetail(event, "new_commit"),
		RetryDelays: append([]time.Duration(nil), p.retryDelays...),
	}

	p.publishLifecycleEvent(ctx, "image_refresh_scheduled", req, 1, firstRetryDelay(req), "")

	if err := p.jobs.Schedule(req); err != nil && p.logger != nil {
		p.logger.ErrorContext(ctx, "Failed to schedule image refresh job", "stack", req.Key.FullName, "error", err)
	}
}

func (p *ImageRefreshPlugin) runRefreshAttempt(ctx context.Context, attempt refreshAttempt) attemptResult {
	execEnv, err := prepareImageRefreshComposeEnvironment(ctx, p.registry, attempt.Request.Owner, attempt.Request.Repo, p.logger)
	if err != nil {
		p.publishLifecycleEvent(ctx, "image_refresh_failed", attempt.Request, attempt.Number, attempt.Delay, err.Error())
		return attemptResult{Status: attemptStatusTerminalFailure}
	}
	defer execEnv.Cleanup()

	if err := runImageRefreshPreflight(attempt.Request.Key.StackPath, execEnv.RuntimeFileEnv); err != nil {
		p.publishLifecycleEvent(ctx, "image_refresh_failed", attempt.Request, attempt.Number, attempt.Delay, err.Error())
		return attemptResult{Status: attemptStatusTerminalFailure}
	}

	updated, err := detectUpdatedImages(attempt.Request.Key.StackPath, execEnv)
	if err != nil {
		p.publishLifecycleEvent(ctx, "image_refresh_failed", attempt.Request, attempt.Number, attempt.Delay, err.Error())
		return attemptResult{Status: attemptStatusRetry}
	}
	if !updated {
		p.publishLifecycleEvent(ctx, "image_refresh_no_update", attempt.Request, attempt.Number, attempt.Delay, "")
		return attemptResult{Status: attemptStatusRetry}
	}

	p.publishLifecycleEvent(ctx, "image_refresh_update_found", attempt.Request, attempt.Number, attempt.Delay, "image update detected")
	p.publishLifecycleEvent(ctx, "image_refresh_restarting", attempt.Request, attempt.Number, attempt.Delay, "restarting stack")
	if err := runComposeUp(attempt.Request.Key.StackPath, execEnv); err != nil {
		p.publishLifecycleEvent(ctx, "image_refresh_failed", attempt.Request, attempt.Number, attempt.Delay, err.Error())
		return attemptResult{Status: attemptStatusTerminalFailure}
	}
	p.publishLifecycleEvent(ctx, "image_refresh_succeeded", attempt.Request, attempt.Number, attempt.Delay, "")
	return attemptResult{Status: attemptStatusSucceeded}
}

func (p *ImageRefreshPlugin) publishLifecycleEvent(ctx context.Context, eventType core.EventTypeName, req refreshJobRequest, attempt int, retryDelay time.Duration, message string) {
	details := map[string]any{
		"owner":       req.Owner,
		"repo":        req.Repo,
		"full_name":   req.Key.FullName,
		"stack_path":  req.Key.StackPath,
		"old_commit":  req.OldCommit,
		"new_commit":  req.NewCommit,
		"attempt":     float64(attempt),
		"retry_delay": retryDelay.String(),
	}
	if message != "" {
		details["message"] = message
	}

	if p.publishEvent != nil {
		p.publishEvent(ctx, core.InternalEvent{
			Type:      eventType,
			Source:    p.Name(),
			Timestamp: time.Now().UTC(),
			Details:   details,
		})
	}
}

func retryDurations(minutes []float64) []time.Duration {
	delays := make([]time.Duration, 0, len(minutes))
	for _, value := range minutes {
		delays = append(delays, time.Duration(value*float64(time.Minute)))
	}
	return delays
}

func stringDetail(event core.InternalEvent, key string) string {
	if value, ok := event.Details[key].(string); ok {
		return value
	}
	return ""
}

func firstRetryDelay(req refreshJobRequest) time.Duration {
	if len(req.RetryDelays) == 0 {
		return 0
	}
	return req.RetryDelays[0]
}

func lastRetryDelay(req refreshJobRequest) time.Duration {
	if len(req.RetryDelays) == 0 {
		return 0
	}
	return req.RetryDelays[len(req.RetryDelays)-1]
}

func imageRefreshEventTypes() []core.EventTypeDesc {
	basePayload := map[string]core.PayloadField{
		"owner":       {Type: "string", Description: "Repository owner", Required: true},
		"repo":        {Type: "string", Description: "Repository name", Required: true},
		"full_name":   {Type: "string", Description: "Full stack name", Required: true},
		"stack_path":  {Type: "string", Description: "Absolute local stack path", Required: true},
		"old_commit":  {Type: "string", Description: "Previous deployed commit", Required: true},
		"new_commit":  {Type: "string", Description: "New deployed commit", Required: true},
		"attempt":     {Type: "int", Description: "Attempt number in the retry cycle", Required: true},
		"retry_delay": {Type: "string", Description: "Delay before the attempt runs", Required: true},
		"message":     {Type: "string", Description: "Outcome or error detail", Required: false},
	}

	return []core.EventTypeDesc{
		{Name: "image_refresh_scheduled", Description: "Image refresh retry cycle scheduled", PayloadSpec: clonePayloadSpec(basePayload)},
		{Name: "image_refresh_retrying", Description: "Image refresh retry attempt started", PayloadSpec: clonePayloadSpec(basePayload)},
		{Name: "image_refresh_no_update", Description: "Image refresh pull found no new image", PayloadSpec: clonePayloadSpec(basePayload)},
		{Name: "image_refresh_update_found", Description: "Image refresh pull found a new image", PayloadSpec: clonePayloadSpec(basePayload)},
		{Name: "image_refresh_restarting", Description: "Image refresh restart in progress", PayloadSpec: clonePayloadSpec(basePayload)},
		{Name: "image_refresh_succeeded", Description: "Image refresh completed successfully", PayloadSpec: clonePayloadSpec(basePayload)},
		{Name: "image_refresh_failed", Description: "Image refresh failed", PayloadSpec: clonePayloadSpec(basePayload)},
		{Name: "image_refresh_exhausted", Description: "Image refresh retries exhausted", PayloadSpec: clonePayloadSpec(basePayload)},
		{Name: "image_refresh_superseded", Description: "Image refresh retry cycle superseded by a newer commit", PayloadSpec: clonePayloadSpec(basePayload)},
	}
}

func clonePayloadSpec(src map[string]core.PayloadField) map[string]core.PayloadField {
	dst := make(map[string]core.PayloadField, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
