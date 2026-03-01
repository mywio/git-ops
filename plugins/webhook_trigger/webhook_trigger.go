// plugins/webhook_trigger/webhook_trigger.go
// Plugin for exposing an HTTP endpoint to trigger reconciliation (e.g., from GitHub Actions/webhooks)

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/mywio/git-ops/pkg/core"
)

type WebhookTriggerPlugin struct {
	port   string
	token  string
	logger *slog.Logger
	mux    *http.ServeMux
	server *http.Server
}

type webhookTriggerConfig struct {
	Port  string `yaml:"port"`
	Token string `yaml:"token"`
}

func (p *WebhookTriggerPlugin) Name() string {
	return "webhook_trigger"
}

func (p *WebhookTriggerPlugin) Init(ctx context.Context, logger *slog.Logger, registry core.PluginRegistry) error {
	p.logger = logger

	if registry != nil {
		cfg := registry.GetConfig()
		if section, ok := cfg["webhook_trigger"]; ok {
			var wcfg webhookTriggerConfig
			if err := core.DecodeConfigSection(section, &wcfg); err != nil {
				p.logger.Warn("Invalid webhook_trigger config", "error", err)
			}
			p.port = wcfg.Port
			p.token = wcfg.Token
		}
	}
	if p.port == "" {
		p.port = "8082"
	}

	if p.token == "" {
		p.logger.Warn("WEBHOOK_TOKEN not set, endpoint is unsecured (use with caution)")
	} else {
		p.logger.Info("Webhook Trigger Plugin Initialized", "port", p.port, "secured", true)
	}

	if registry != nil {
		registry.RegisterEventType(core.EventTypeDesc{
			Name:        "webhook_received",
			Description: "Raw webhook received (before processing)",
		})
		p.mux = registry.GetMuxServer()
	} else {
		p.mux = http.NewServeMux()
	}
	p.mux.HandleFunc("/reconcile", p.handleReconcile)

	return nil
}

func (p *WebhookTriggerPlugin) Start(_ context.Context) error {
	// We do not need to do anything, to "start"
	return nil
}

func (p *WebhookTriggerPlugin) Stop(ctx context.Context) error {
	if p.server != nil {
		if err := p.server.Shutdown(ctx); err != nil {
			p.logger.Error("Webhook server shutdown failed", "error", err)
			return err
		}
		p.logger.Info("Webhook Trigger server stopped")
	}
	return nil
}

func (p *WebhookTriggerPlugin) Description() string {
	return "Webhook trigger for on-demand reconciliation of git-ops stacks"
}

func (p *WebhookTriggerPlugin) Capabilities() []core.Capability {
	// Assuming core defines CapabilityTrigger or similar
	return []core.Capability{core.Capability("trigger")}
}

func (p *WebhookTriggerPlugin) Status() core.ServiceStatus {
	if p.port == "" {
		return core.StatusDegraded
	}
	if p.token == "" {
		return core.StatusUnhealthy
	}
	return core.StatusHealthy
}

func (p *WebhookTriggerPlugin) Execute(action string, params map[string]interface{}) (interface{}, error) {
	return nil, fmt.Errorf("webhook_trigger plugin does not support Execute actions (use HTTP endpoint)")
}

// HTTP handler for /reconcile
func (p *WebhookTriggerPlugin) handleReconcile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Optional token auth
	if p.token != "" {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != p.token {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	p.logger.Info("Reconciliation trigger received via webhook",
		"client_ip", r.RemoteAddr,
		"user_agent", r.UserAgent())

	core.Publish(r.Context(), core.InternalEvent{
		Type:    "reconcile_now",
		Source:  "webhook_trigger",
		Details: map[string]interface{}{"client_ip": r.RemoteAddr},
	})

	// Publish an event (useful for logging/auditing)
	core.Publish(r.Context(), core.InternalEvent{
		Type:   "webhook_received",
		Source: "webhook_trigger",
		Details: map[string]interface{}{
			"client_ip":  r.RemoteAddr,
			"method":     r.Method,
			"user_agent": r.UserAgent(),
		},
	})

	// Trigger reconciliation
	select {
	case core.TriggerReconcile <- struct{}{}:
		p.logger.Info("Reconciliation triggered successfully via webhook")
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintln(w, `{"status": "accepted", "message": "Reconciliation triggered"}`)
	default:
		// Channel is full → already triggering
		p.logger.Debug("Reconciliation already in progress, webhook request accepted but ignored")
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintln(w, `{"status": "accepted", "message": "Reconciliation already in progress"}`)
	}
}

// Exported symbol that core looks up
var Plugin core.Plugin = &WebhookTriggerPlugin{}

// Main for standalone testing
func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	p := &WebhookTriggerPlugin{}
	ctx := context.Background()

	if err := p.Init(ctx, logger, nil); err != nil {
		logger.Error("Init failed", "error", err)
		return
	}

	if err := p.Start(ctx); err != nil {
		logger.Error("Start failed", "error", err)
		return
	}

	logger.Info("Webhook trigger running (press Ctrl+C to stop)")
	<-ctx.Done()

	p.Stop(ctx)
}
