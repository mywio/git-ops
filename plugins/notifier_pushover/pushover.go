// plugins/notifier_pushover/pushover.go
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

type PushoverNotifier struct {
	logger  *slog.Logger
	client  *http.Client
	token   string
	user    string
	enabled bool
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
	if registry != nil {
		n.client = registry.GetHTTPClient()
		cfg := registry.GetConfig()
		if section, ok := cfg["pushover"]; ok {
			var pcfg pushoverConfig
			if err := core.DecodeConfigSection(section, &pcfg); err != nil {
				n.logger.WarnContext(ctx, "Invalid pushover config", "error", err)
			}
			n.token = pcfg.Token
			n.user = pcfg.User
		}
	}
	if n.client == nil {
		n.client = http.DefaultClient
	}
	if n.token == "" || n.user == "" {
		n.logger.WarnContext(ctx, "Pushover token or user not set, notifications disabled")
		n.enabled = false
		return nil
	}
	n.enabled = true
	n.logger.InfoContext(ctx, "Pushover Notifier Initialized")

	// TODO: move this to config?
	// We want to register to all notifications
	if registry != nil {
		registry.Subscribe("notify_*", n.process)
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
	if n.enabled && n.token != "" && n.user != "" {
		return core.StatusHealthy
	}
	return core.StatusDegraded
}

func (n *PushoverNotifier) process(ctx context.Context, event core.InternalEvent) {
	if !n.enabled || n.token == "" || n.user == "" {
		return
	}
	details, err := json.Marshal(event.Details) // Assuming Details is map[string]interface{}
	if err != nil {
		n.logger.ErrorContext(ctx, "Failed to marshal event details",
			slog.Any("event", event))
		return
	}

	payload := map[string]interface{}{
		"token":   n.token,
		"user":    n.user,
		"message": fmt.Sprintf("[%s] %s\nRepo: %s/%s\n%s", event.Type, event.String, event.Source, event.Repo, string(details)),
		"title":   "git-ops Notification",
		// TODO: Have a priority map in config. That will map notification types to priority levels.
		//"priority": ,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		n.logger.ErrorContext(ctx, "Failed to marshal payload",
			slog.Any("event", event),
			slog.Any("payload", payload),
		)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.pushover.net/1/messages.json", bytes.NewBuffer(data))
	if err != nil {
		n.logger.ErrorContext(ctx, "Failed to create Pushover request", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		n.logger.ErrorContext(ctx, "Failed to send Pushover notification", "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		n.logger.ErrorContext(ctx, "Pushover API error", "status", resp.StatusCode)
		return
	}

	n.logger.InfoContext(ctx, "Pushover notification delivered successfully")
	return
}

func (n *PushoverNotifier) Execute(action string, params map[string]interface{}) (interface{}, error) {
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
//	result, err := n.Execute("notify", params)
//	if err != nil {
//		logger.Error("Execute failed", "error", err)
//	} else {
//		logger.Info("Execute result", "result", result)
//	}
//
//	n.Stop(ctx)
//}
