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
	"sync"
	"time"

	"github.com/mywio/git-ops/pkg/core"
)

type WebhookTriggerPlugin struct {
	port         string
	token        string
	rateLimit    time.Duration
	logger       *slog.Logger
	registry     core.PluginRegistry
	mux          *http.ServeMux
	server       *http.Server
	now          func() time.Time
	rateLimitMu  sync.Mutex
	lastAccepted time.Time
}

type webhookTriggerConfig struct {
	Port      string `yaml:"port"`
	Token     string `yaml:"token"`
	RateLimit string `yaml:"rate_limit"`
}

func (p *WebhookTriggerPlugin) Name() string {
	return "webhook_trigger"
}

func (p *WebhookTriggerPlugin) Init(ctx context.Context, logger *slog.Logger, registry core.PluginRegistry) error {
	p.logger = logger
	p.registry = registry
	if p.now == nil {
		p.now = time.Now
	}

	if registry != nil {
		cfg := registry.GetConfig()
		if section, ok := cfg["webhook_trigger"]; ok {
			var wcfg webhookTriggerConfig
			if err := core.DecodeConfigSection(section, &wcfg); err != nil {
				p.logger.WarnContext(ctx, "Invalid webhook_trigger config", "error", err)
			}
			p.port = wcfg.Port
			p.token = wcfg.Token
			if wcfg.RateLimit != "" {
				rateLimit, err := time.ParseDuration(wcfg.RateLimit)
				if err != nil {
					return fmt.Errorf("parse webhook_trigger rate_limit: %w", err)
				}
				p.rateLimit = rateLimit
			}
		}
	}
	if p.port == "" {
		p.port = "8082"
	}

	if p.token == "" {
		p.logger.WarnContext(ctx, "WEBHOOK_TOKEN not set, endpoint is unsecured (use with caution)")
	} else {
		p.logger.InfoContext(ctx, "Webhook Trigger Plugin Initialized", "port", p.port, "secured", true)
	}

	if registry != nil {
		if err := registry.RegisterEventType(core.EventTypeDesc{
			Name:        "webhook_received",
			Description: "Raw webhook received (before processing)",
		}); err != nil {
			return fmt.Errorf("register event type webhook_received: %w", err)
		}
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
			p.logger.ErrorContext(ctx, "Webhook server shutdown failed", "error", err)
			return err
		}
		p.logger.InfoContext(ctx, "Webhook Trigger server stopped")
	}
	return nil
}

func (p *WebhookTriggerPlugin) Description() string {
	return "Webhook trigger for on-demand reconciliation of git-ops stacks"
}

func (p *WebhookTriggerPlugin) Capabilities() []core.Capability {
	return []core.Capability{core.CapabilityTrigger}
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

func (p *WebhookTriggerPlugin) Execute(ctx context.Context, action string, params map[string]interface{}) (interface{}, error) {
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

	if limited, retryAfter := p.checkRateLimit(); limited {
		w.Header().Set("Retry-After", fmt.Sprintf("%.0f", retryAfter.Seconds()))
		w.WriteHeader(http.StatusTooManyRequests)
		if _, err := fmt.Fprintln(w, `{"status":"rate_limited","message":"Reconciliation trigger rate-limited"}`); err != nil {
			p.logger.Debug("failed to write rate-limited response", "error", err)
		}
		return
	}

	p.logger.Info("Reconciliation trigger received via webhook",
		"client_ip", r.RemoteAddr,
		"user_agent", r.UserAgent())

	if p.registry != nil {
		p.registry.Publish(r.Context(), core.InternalEvent{
			Type:    "reconcile_now",
			Source:  "webhook_trigger",
			Details: map[string]interface{}{"client_ip": r.RemoteAddr},
		})

		// Publish an event (useful for logging/auditing)
		p.registry.Publish(r.Context(), core.InternalEvent{
			Type:   "webhook_received",
			Source: "webhook_trigger",
			Details: map[string]interface{}{
				"client_ip":  r.RemoteAddr,
				"method":     r.Method,
				"user_agent": r.UserAgent(),
			},
		})
	}

	p.logger.Info("Reconciliation triggered successfully via webhook")
	w.WriteHeader(http.StatusAccepted)
	if _, err := fmt.Fprintln(w, `{"status": "accepted", "message": "Reconciliation triggered"}`); err != nil {
		p.logger.Debug("failed to write accepted response", "error", err)
	}
}

func (p *WebhookTriggerPlugin) checkRateLimit() (bool, time.Duration) {
	if p.rateLimit <= 0 {
		return false, 0
	}

	p.rateLimitMu.Lock()
	defer p.rateLimitMu.Unlock()

	now := p.now().UTC()
	if !p.lastAccepted.IsZero() {
		nextAllowed := p.lastAccepted.Add(p.rateLimit)
		if now.Before(nextAllowed) {
			return true, nextAllowed.Sub(now)
		}
	}

	p.lastAccepted = now
	return false, 0
}

// Exported symbol that core looks up
var Plugin core.Plugin = &WebhookTriggerPlugin{}

type webhookTriggerConfigView struct {
	Port      string      `json:"port"`
	Token     core.Secret `json:"token"`
	Secured   bool        `json:"secured"`
	Enabled   bool        `json:"enabled"`
	RateLimit string      `json:"rate_limit"`
	Throttled bool        `json:"throttled"`
}

func (p *WebhookTriggerPlugin) Config() any {
	rateLimit := ""
	if p.rateLimit > 0 {
		rateLimit = p.rateLimit.String()
	}
	return webhookTriggerConfigView{
		Port:      p.port,
		Token:     core.NewSecret(p.token),
		Secured:   p.token != "",
		Enabled:   p.port != "",
		RateLimit: rateLimit,
		Throttled: p.rateLimit > 0,
	}
}

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

	if err := p.Stop(ctx); err != nil {
		logger.Error("Stop failed", "error", err)
	}
}
