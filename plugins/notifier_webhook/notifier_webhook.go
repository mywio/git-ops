package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/mywio/git-ops/pkg/core"
)

type WebhookPlugin struct {
	logger *slog.Logger
	url    string
	client *http.Client
	enabled bool
}

type webhookConfig struct {
	URL string `yaml:"url"`
}

func (p *WebhookPlugin) Name() string {
	return "webhook"
}

func (p *WebhookPlugin) Init(ctx context.Context, logger *slog.Logger, registry core.PluginRegistry) error {
	p.logger = logger
	var subscribeProvided bool
	var subscribePatterns []string
	if registry != nil {
		cfg := registry.GetConfig()
		if section, ok := cfg["webhook"]; ok {
			if _, ok := section["subscribe"]; ok {
				subscribeProvided = true
			}
			var wcfg webhookConfig
			if err := core.DecodeConfigSection(section, &wcfg); err != nil {
				p.logger.Warn("Invalid webhook config", "error", err)
			}
			p.url = wcfg.URL
			subscribePatterns = parseSubscribePatterns(section)
		}
		p.client = registry.GetHTTPClient()
	}
	if p.client == nil {
		p.client = http.DefaultClient
	}
	if p.url == "" {
		p.logger.Warn("NOTIFY_WEBHOOK_URL not set, webhook notifications disabled")
		p.enabled = false
		return nil
	}

	p.enabled = true
	p.logger.Info("Webhook Plugin Initialized", "url", p.url)
	if registry != nil {
		if !subscribeProvided {
			subscribePatterns = []string{"notify_*"}
		}
		for _, pattern := range subscribePatterns {
			registry.Subscribe(pattern, p.process)
		}
		if len(subscribePatterns) == 0 {
			p.logger.InfoContext(ctx, "Webhook notifier has no subscriptions configured; skipping event registration")
		}
	}
	return nil
}

func (p *WebhookPlugin) Start(ctx context.Context) error {
	// No background services to start
	return nil
}

func (p *WebhookPlugin) Stop(ctx context.Context) error {
	// No services to stop
	return nil
}

func (p *WebhookPlugin) Description() string { return "Generic webhook notifier" }

func (p *WebhookPlugin) Capabilities() []core.Capability {
	return []core.Capability{core.CapabilityNotifier}
}

func (p *WebhookPlugin) Status() core.ServiceStatus {
	if p.enabled && p.url != "" {
		return core.StatusHealthy
	}
	return core.StatusUnhealthy
}

func (p *WebhookPlugin) Execute(ctx context.Context, action string, params map[string]interface{}) (interface{}, error) {
	if p.url == "" {
		p.logger.Debug("Webhook URL not set, skipping notification")
		return nil, nil // silent skip if not set
	}

	if action != "notify" {
		return nil, fmt.Errorf("unsupported action")
	}

	eventRaw, ok := params["event"]
	if !ok {
		return nil, fmt.Errorf("missing event")
	}

	event, ok := eventRaw.(core.InternalEvent)
	if !ok {
		return nil, fmt.Errorf("invalid event type")
	}

	if err := p.send(ctx, event); err != nil {
		return nil, err
	}
	return map[string]string{"status": "delivered"}, nil
}

var Plugin core.Plugin = &WebhookPlugin{}

func (p *WebhookPlugin) process(ctx context.Context, event core.InternalEvent) {
	if !p.enabled || p.url == "" {
		return
	}
	if err := p.send(ctx, event); err != nil {
		p.logger.ErrorContext(ctx, "Webhook notification failed", "error", err)
	}
}

func (p *WebhookPlugin) send(ctx context.Context, event core.InternalEvent) error {
	if ctx == nil {
		ctx = context.Background()
	}
	payload := map[string]interface{}{
		"event_type": event.Type,
		"source":     event.Source,
		"repo":       event.Repo,
		"message":    event.String,
		"details":    event.Details,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewBuffer(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook status %d", resp.StatusCode)
	}

	p.logger.InfoContext(ctx, "Webhook delivered successfully")
	return nil
}

func normalizePatterns(values []string) []string {
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

func parseSubscribePatterns(section map[string]any) []string {
	raw, ok := section["subscribe"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return normalizePatterns(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, fmt.Sprint(item))
		}
		return normalizePatterns(out)
	case string:
		parts := strings.Split(v, ",")
		return normalizePatterns(parts)
	default:
		return normalizePatterns([]string{fmt.Sprint(v)})
	}
}

// Main for standalone testing
func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	p := &WebhookPlugin{}
	ctx := context.Background()
	if err := p.Init(ctx, logger, nil); err != nil { // nil registry for testing
		logger.Error("Init failed", "error", err)
		return
	}
	if err := p.Start(ctx); err != nil {
		logger.Error("Start failed", "error", err)
		return
	}

	// Test Execute
	//event := core.Event{
	//	Type:    "test",
	//	Owner:   "owner",
	//	Repo:    "repo",
	//	Message: "message",
	//	Details: map[string]interface{}{"key": "value"},
	//}
	//params := map[string]interface{}{"event": event}
	//result, err := p.Execute(ctx, "notify", params)
	//if err != nil {
	//	logger.Error("Execute failed", "error", err)
	//} else {
	//	logger.Info("Execute result", "result", result)
	//}

	p.Stop(ctx)
}
