package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/mywio/git-ops/pkg/core"
)

type DiscordNotifier struct {
	logger        *slog.Logger
	client        *http.Client
	webhookURL    string
	enabled       bool
	subscriptions []string
}

type discordConfig struct {
	WebhookURL string `yaml:"webhook_url"`
}

type discordWebhookPayload struct {
	Embeds []discordEmbed `json:"embeds"`
}

type discordEmbed struct {
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	Color       int            `json:"color,omitempty"`
	Timestamp   string         `json:"timestamp,omitempty"`
	Fields      []discordField `json:"fields,omitempty"`
}

type discordField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

func (n *DiscordNotifier) Name() string {
	return "discord"
}

func (n *DiscordNotifier) Init(ctx context.Context, logger *slog.Logger, registry core.PluginRegistry) error {
	n.logger = logger
	var subscribeProvided bool
	var subscribePatterns []string
	if registry != nil {
		n.client = registry.GetHTTPClient()
		cfg := registry.GetConfig()
		if section, ok := cfg["discord"]; ok {
			if _, okSub := section["subscribe"]; okSub {
				subscribeProvided = true
			}
			var discordCfg discordConfig
			if err := core.DecodeConfigSection(section, &discordCfg); err != nil {
				n.logger.WarnContext(ctx, "Invalid discord config", "error", err)
			}
			n.webhookURL = discordCfg.WebhookURL
			subscribePatterns = parseSubscribePatterns(section)
		}
	}
	if n.client == nil {
		n.client = http.DefaultClient
	}
	if n.webhookURL == "" {
		n.logger.WarnContext(ctx, "Discord webhook URL not set, notifications disabled")
		n.enabled = false
		return nil
	}

	n.enabled = true
	n.logger.InfoContext(ctx, "Discord notifier initialized")
	if registry != nil {
		if !subscribeProvided {
			subscribePatterns = []string{"notify_*"}
		}
		n.subscriptions = append([]string(nil), subscribePatterns...)
		for _, pattern := range subscribePatterns {
			registry.Subscribe(pattern, n.process)
		}
		if len(subscribePatterns) == 0 {
			n.logger.InfoContext(ctx, "Discord notifier has no subscriptions configured; skipping event registration")
		}
	}

	return nil
}

func (n *DiscordNotifier) Start(ctx context.Context) error {
	return nil
}

func (n *DiscordNotifier) Stop(ctx context.Context) error {
	return nil
}

func (n *DiscordNotifier) Description() string {
	return "Discord notifier for sending event notifications via Discord webhooks"
}

func (n *DiscordNotifier) Capabilities() []core.Capability {
	return []core.Capability{core.CapabilityNotifier}
}

func (n *DiscordNotifier) Status() core.ServiceStatus {
	if n.enabled && n.webhookURL != "" {
		return core.StatusHealthy
	}
	return core.StatusDegraded
}

func (n *DiscordNotifier) Execute(ctx context.Context, action string, params map[string]interface{}) (interface{}, error) {
	if !n.enabled || n.webhookURL == "" {
		return nil, nil
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
	if err := n.send(ctx, event); err != nil {
		return nil, err
	}
	return map[string]string{"status": "delivered"}, nil
}

var Plugin core.Plugin = &DiscordNotifier{}

type discordConfigView struct {
	WebhookURL core.Secret `json:"webhook_url"`
	Subscribe  []string    `json:"subscribe,omitempty"`
	Enabled    bool        `json:"enabled"`
}

func (n *DiscordNotifier) Config() any {
	return discordConfigView{
		WebhookURL: core.NewSecret(n.webhookURL),
		Subscribe:  append([]string(nil), n.subscriptions...),
		Enabled:    n.enabled,
	}
}

func (n *DiscordNotifier) process(ctx context.Context, event core.InternalEvent) {
	if !n.enabled || n.webhookURL == "" {
		return
	}
	if err := n.send(ctx, event); err != nil {
		n.logger.ErrorContext(ctx, "Failed to send Discord notification", "error", err)
	}
}

func (n *DiscordNotifier) send(ctx context.Context, event core.InternalEvent) error {
	if ctx == nil {
		ctx = context.Background()
	}
	notification := core.NewNotificationPayload(event)
	payload := discordWebhookPayload{
		Embeds: []discordEmbed{buildDiscordEmbed(notification)},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.webhookURL, bytes.NewBuffer(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("discord webhook status %d", resp.StatusCode)
	}

	n.logger.InfoContext(ctx, "Discord notification delivered successfully")
	return nil
}

func buildDiscordEmbed(notification core.NotificationPayload) discordEmbed {
	embed := discordEmbed{
		Title:       notification.Title,
		Description: notification.Body,
		Color:       discordColor(notification),
	}
	if !notification.Timestamp.IsZero() {
		embed.Timestamp = notification.Timestamp.UTC().Format("2006-01-02T15:04:05.000Z")
	}

	fields := make([]discordField, 0, 4)
	if notification.EventType != "" {
		fields = append(fields, discordField{Name: "Event", Value: string(notification.EventType), Inline: true})
	}
	if notification.FullName != "" {
		fields = append(fields, discordField{Name: "Stack", Value: notification.FullName, Inline: true})
	} else if notification.Repo != "" {
		fields = append(fields, discordField{Name: "Repo", Value: notification.Repo, Inline: true})
	}
	if notification.Status != "" {
		fields = append(fields, discordField{Name: "Status", Value: notification.Status, Inline: true})
	}
	if notification.Stage != "" {
		fields = append(fields, discordField{Name: "Stage", Value: notification.Stage, Inline: true})
	}
	if len(fields) > 0 {
		embed.Fields = fields
	}

	return embed
}

func discordColor(notification core.NotificationPayload) int {
	switch {
	case strings.EqualFold(notification.AlertSeverity, string(core.AlertSeverityError)),
		strings.EqualFold(notification.AlertSeverity, string(core.AlertSeverityCritical)),
		strings.EqualFold(notification.Status, string(core.ExecutionStatusFailed)),
		strings.Contains(strings.ToLower(string(notification.EventType)), "failed"):
		return 0xEF4444
	case strings.EqualFold(notification.AlertSeverity, string(core.AlertSeverityWarning)),
		strings.Contains(strings.ToLower(string(notification.EventType)), "warning"),
		strings.Contains(strings.ToLower(string(notification.EventType)), "risk"),
		strings.Contains(strings.ToLower(string(notification.EventType)), "locked"):
		return 0xF59E0B
	case strings.EqualFold(notification.Status, string(core.ExecutionStatusSucceeded)),
		strings.Contains(strings.ToLower(string(notification.EventType)), "success"),
		strings.Contains(strings.ToLower(string(notification.EventType)), "succeeded"):
		return 0x22C55E
	default:
		return 0x3B82F6
	}
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
