package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/mywio/git-ops/pkg/core"
)

type WebhookPlugin struct {
	logger *slog.Logger
	url    string
	client *http.Client
}

type webhookConfig struct {
	URL string `yaml:"url"`
}
func (p *WebhookPlugin) Name() string {
	return "webhook"
}

func (p *WebhookPlugin) Init(ctx context.Context, logger *slog.Logger, registry core.PluginRegistry) error {
	p.logger = logger
	if registry != nil {
		cfg := registry.GetConfig()
		if section, ok := cfg["webhook"]; ok {
			var wcfg webhookConfig
			if err := core.DecodeConfigSection(section, &wcfg); err != nil {
				p.logger.Warn("Invalid webhook config", "error", err)
			}
			p.url = wcfg.URL
		}
		p.client = registry.GetHTTPClient()
	}
	if p.client == nil {
		p.client = http.DefaultClient
	}
	if p.url == "" {
		p.logger.Warn("NOTIFY_WEBHOOK_URL not set, webhook notifications disabled")
		return nil
	}

	p.logger.Info("Webhook Plugin Initialized", "url", p.url)
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
	if p.url != "" {
		return core.StatusHealthy
	}
	return core.StatusUnhealthy
}

func (p *WebhookPlugin) Execute(action string, params map[string]interface{}) (interface{}, error) {
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

	payload := map[string]interface{}{
		"event_type": event.Type,
		"source":     event.Source,
		"repo":       event.Repo,
		"message":    event.String,
		"details":    event.Details,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		p.logger.Error("Failed to send webhook", "error", err)
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		p.logger.Error("Webhook failed", "status", resp.StatusCode)
		return nil, fmt.Errorf("webhook status %d", resp.StatusCode)
	}

	p.logger.Info("Webhook delivered successfully")
	return map[string]string{"status": "delivered"}, nil
}

var Plugin core.Plugin = &WebhookPlugin{}

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
	//result, err := p.Execute("notify", params)
	//if err != nil {
	//	logger.Error("Execute failed", "error", err)
	//} else {
	//	logger.Info("Execute result", "result", result)
	//}

	p.Stop(ctx)
}
