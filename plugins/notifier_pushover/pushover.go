// plugins/notifier_pushover/pushover.go
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

type PushoverNotifier struct {
	logger  *slog.Logger
	client  *http.Client
	token   core.Secret
	user    string
	enabled bool
	subscriptions []string
}

type pushoverConfig struct {
	Token string `yaml:"token"`
	User  string `yaml:"user"`
}

func (n *PushoverNotifier) Name() string {
	return "pushover"
}

func (n *PushoverNotifier) Init(ctx context.Context, logger *slog.Logger, registry core.PluginRegistry) error {
	n.logger = logger
	var subscribeProvided bool
	var subscribePatterns []string
	if registry != nil {
		n.client = registry.GetHTTPClient()
		cfg := registry.GetConfig()
		if section, ok := cfg["pushover"]; ok {
			if _, okSub := section["subscribe"]; okSub {
				subscribeProvided = true
			}
			var pushoverCfg pushoverConfig
			if err := core.DecodeConfigSection(section, &pushoverCfg); err != nil {
				n.logger.WarnContext(ctx, "Invalid pushover config", "error", err)
			}
			n.token = core.NewSecret(pushoverCfg.Token)
			n.user = pushoverCfg.User
			subscribePatterns = parseSubscribePatterns(section)
		}
	}
	if n.client == nil {
		n.client = http.DefaultClient
	}
	if n.token.Value == "" || n.user == "" {
		n.logger.WarnContext(ctx, "Pushover token or user not set, notifications disabled")
		n.enabled = false
		return nil
	}
	n.enabled = true
	n.logger.InfoContext(ctx, "Pushover Notifier Initialized")

	if registry != nil {
		if !subscribeProvided {
			subscribePatterns = []string{"notify_*"}
		}
		n.subscriptions = append([]string(nil), subscribePatterns...)
		for _, pattern := range subscribePatterns {
			registry.Subscribe(pattern, n.process)
		}
		if len(subscribePatterns) == 0 {
			n.logger.InfoContext(ctx, "Pushover notifier has no subscriptions configured; skipping event registration")
		}
	}

	return nil
}

func (n *PushoverNotifier) Start(ctx context.Context) error {
	// No background services to start
	return nil
}

func (n *PushoverNotifier) Stop(ctx context.Context) error {
	// No services to stop
	return nil
}

func (n *PushoverNotifier) Description() string {
	return "Pushover notifier for sending notifications via Pushover API"
}

func (n *PushoverNotifier) Capabilities() []core.Capability {
	return []core.Capability{core.CapabilityNotifier}
}

func (n *PushoverNotifier) Status() core.ServiceStatus {
	if n.enabled && n.token.Value != "" && n.user != "" {
		return core.StatusHealthy
	}
	return core.StatusDegraded
}

func (n *PushoverNotifier) process(ctx context.Context, event core.InternalEvent) {
	if !n.enabled || n.token.Value == "" || n.user == "" {
		return
	}
	if err := n.send(ctx, event); err != nil {
		n.logger.ErrorContext(ctx, "Failed to send Pushover notification", "error", err)
	}
}

func (n *PushoverNotifier) Execute(ctx context.Context, action string, params map[string]interface{}) (interface{}, error) {
	//if n.token == "" || n.user == "" {
	//	slog.Debug("Pushover token or user not set, skipping notification")
	//	return nil, nil // silent skip if not set
	//}
	//
	//if action != "notify" {
	//	return nil, fmt.Errorf("unsupported action")
	//}
	//
	//eventRaw, ok := params["event"]
	//if !ok {
	//	return nil, fmt.Errorf("missing event")
	//}
	//
	//event, ok := eventRaw.(core.Event)
	//if !ok {
	//	return nil, fmt.Errorf("invalid event type")
	//}
	//
	//priority := 0
	//if event.Type == core.EventDeployFailed || event.Type == core.EventHookError {
	//	priority = 1 // or higher
	//}
	//
	//details, _ := json.Marshal(event.Details) // Assuming Details is map[string]interface{}
	//
	//payload := map[string]interface{}{
	//	"token":    n.token,
	//	"user":     n.user,
	//	"message":  fmt.Sprintf("[%s] %s\nRepo: %s/%s\n%s", event.Type, event.Message, event.Owner, event.Repo, string(details)),
	//	"title":    "git-ops Notification",
	//	"priority": priority,
	//}
	//
	//data, err := json.Marshal(payload)
	//if err != nil {
	//	return nil, err
	//}
	//
	//resp, err := http.Post("https://api.pushover.net/1/messages.json", "application/json", bytes.NewBuffer(data))
	//if err != nil {
	//	slog.Error("Failed to send Pushover notification", "error", err)
	//	return nil, err
	//}
	//defer resp.Body.Close()
	//
	//if resp.StatusCode != 200 {
	//	slog.Error("Pushover API error", "status", resp.StatusCode)
	//	return nil, fmt.Errorf("pushover API error: %d", resp.StatusCode)
	//}
	//
	//slog.Info("Pushover notification delivered successfully")
	//return map[string]string{"status": "delivered"}, nil
	return nil, nil
}

// Exported symbol that core looks up
var Plugin core.Plugin = &PushoverNotifier{}

type pushoverConfigView struct {
	Token     core.Secret `json:"token"`
	User      string      `json:"user"`
	Subscribe []string    `json:"subscribe,omitempty"`
	Enabled   bool        `json:"enabled"`
}

func (n *PushoverNotifier) Config() any {
	return pushoverConfigView{
		Token:     n.token,
		User:      n.user,
		Subscribe: append([]string(nil), n.subscriptions...),
		Enabled:   n.enabled,
	}
}

func (n *PushoverNotifier) send(ctx context.Context, event core.InternalEvent) error {
	if ctx == nil {
		ctx = context.Background()
	}
	details, err := json.Marshal(event.Details) // Assuming Details is map[string]interface{}
	if err != nil {
		return err
	}

	payload := map[string]interface{}{
		"token":   n.token.Value,
		"user":    n.user,
		"message": fmt.Sprintf("[%s] %s\nRepo: %s/%s\n%s", event.Type, event.String, event.Source, event.Repo, string(details)),
		"title":   "git-ops Notification",
		// TODO: Have a priority map in config. That will map notification types to priority levels.
		//"priority": ,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.pushover.net/1/messages.json", bytes.NewBuffer(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("pushover API error: %d", resp.StatusCode)
	}

	n.logger.InfoContext(ctx, "Pushover notification delivered successfully")
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

// TODO: fix me after the refactor
// Main for standalone testing
//func main() {
//	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
//	n := &PushoverNotifier{}
//	ctx := context.Background()
//	if err := n.Init(ctx, logger, nil); err != nil { // nil registry for testing
//		logger.Error("Init failed", "error", err)
//		return
//	}
//	if err := n.Start(ctx); err != nil {
//		logger.Error("Start failed", "error", err)
//		return
//	}
//
//	// Test Execute
//	//event := core.Event{
//	//	Type:    "test",
//	//	Owner:   "owner",
//	//	Repo:    "repo",
//	//	Message: "message",
//	//	Details: map[string]interface{}{"key": "value"},
//	//}
//	params := map[string]interface{}{"event": event}
//	result, err := n.Execute(ctx, "notify", params)
//	if err != nil {
//		logger.Error("Execute failed", "error", err)
//	} else {
//		logger.Info("Execute result", "result", result)
//	}
//
//	n.Stop(ctx)
//}
