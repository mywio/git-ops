# Plugin Development

This guide is the shortest path to writing a correct new plugin for `git-ops`
without reverse-engineering existing plugin source files.

## What A Plugin Is

A plugin is a Go package compiled with `-buildmode=plugin` that exports a
symbol named `Plugin` implementing `core.Plugin`.

At runtime:

1. `ModuleManager.LoadPlugins` loads `.so` files from the plugins directory.
2. It looks up the exported `Plugin` symbol in each shared object.
3. It calls `Init`, then `Start`, and later `Stop`.

Plugins are the main extension mechanism in this repository. Most product
features are implemented as plugins, not in `main.go`.

## Minimum Interface Contract

Your plugin must implement:

```go
type Plugin interface {
    Name() string
    Init(ctx context.Context, logger *slog.Logger, registry core.PluginRegistry) error
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    Description() string
    Capabilities() []core.Capability
    Status() core.ServiceStatus
    Execute(ctx context.Context, action string, params map[string]interface{}) (interface{}, error)
}
```

And it must export:

```go
var Plugin core.Plugin = &MyPlugin{}
```

## Copy-Paste Template

Use this as the starting point for a new plugin:

```go
package main

import (
    "context"
    "fmt"
    "log/slog"
    "net/http"

    "github.com/mywio/git-ops/pkg/core"
)

type myPluginConfig struct {
    Enabled bool   `yaml:"enabled"`
    Token   string `yaml:"token"`
}

type MyPlugin struct {
    logger   *slog.Logger
    registry core.PluginRegistry
    mux      *http.ServeMux
    cfg      myPluginConfig
    enabled  bool
}

var Plugin core.Plugin = &MyPlugin{}

func (p *MyPlugin) Name() string {
    return "my_plugin"
}

func (p *MyPlugin) Description() string {
    return "Short human-readable summary"
}

func (p *MyPlugin) Capabilities() []core.Capability {
    return []core.Capability{core.CapabilityAPI}
}

func (p *MyPlugin) Init(ctx context.Context, logger *slog.Logger, registry core.PluginRegistry) error {
    p.logger = logger
    p.registry = registry

    if registry != nil {
        if section, ok := registry.GetConfig()["my_plugin"]; ok {
            if err := core.DecodeConfigSection(section, &p.cfg); err != nil {
                return fmt.Errorf("decode my_plugin config: %w", err)
            }
        }
        p.mux = registry.GetMuxServer()
    }

    p.enabled = p.cfg.Enabled

    if registry != nil {
        if err := registry.RegisterEventType(core.EventTypeDesc{
            Name:        "my_plugin_event",
            Description: "Describe the event",
            PayloadSpec: map[string]core.PayloadField{
                "key": {Type: "string", Description: "Example field", Required: true},
            },
        }); err != nil {
            return fmt.Errorf("register my_plugin_event: %w", err)
        }
    }

    return nil
}

func (p *MyPlugin) Start(ctx context.Context) error {
    // Subscribe here, not in Init, so config validation in Init stays independent
    // of event bus readiness.
    if p.registry != nil {
        p.registry.Subscribe("some_event", p.handleEvent)
    }
    return nil
}

func (p *MyPlugin) Stop(ctx context.Context) error {
    return nil
}

func (p *MyPlugin) Status() core.ServiceStatus {
    if !p.enabled {
        return core.StatusUnknown
    }
    return core.StatusHealthy
}

func (p *MyPlugin) Execute(ctx context.Context, action string, params map[string]interface{}) (interface{}, error) {
    switch action {
    case "ping":
        return map[string]any{"ok": true}, nil
    default:
        return nil, fmt.Errorf("unknown action: %s", action)
    }
}

func (p *MyPlugin) handleEvent(ctx context.Context, event core.InternalEvent) {
    p.logger.Info("received event", "type", event.Type)
}

func (p *MyPlugin) Config() any {
    return struct {
        Enabled bool        `json:"enabled"`
        Token   core.Secret `json:"token"`
    }{
        Enabled: p.enabled,
        Token:   core.NewSecret(p.cfg.Token),
    }
}
```

This template shows the current repo conventions:

- store `logger` and `registry` on the struct
- load config in `Init`
- register event types in `Init`
- subscribe in `Start`
- use `Config() any` for UI-safe config exposure
- wrap secrets with `core.Secret`

## Config Loading Pattern

Plugins should read only their own config section unless they intentionally
depend on shared `core` settings.

Example:

```go
if registry != nil {
    if section, ok := registry.GetConfig()["my_plugin"]; ok {
        if err := core.DecodeConfigSection(section, &cfg); err != nil {
            return fmt.Errorf("decode my_plugin config: %w", err)
        }
    }
}
```

Use `core.DecodeConfigSection` for typed config structs rather than manually
casting nested maps.

### Exposing Config Safely

If your plugin wants its config to appear in `/api/plugins?include_config=true`
and the UI, implement:

```go
func (p *MyPlugin) Config() any
```

For secrets:

```go
core.NewSecret(rawValue)
```

`core.Secret` serializes as `REDACTED`, not the underlying value.

## Event Pattern

If your plugin publishes events:

1. register them in `Init()`
2. publish them through `registry.Publish(ctx, event)`

Example registration:

```go
registry.RegisterEventType(core.EventTypeDesc{
    Name:        "my_event",
    Description: "What happened",
    PayloadSpec: map[string]core.PayloadField{
        "owner": {Type: "string", Description: "Repository owner", Required: true},
    },
})
```

Example publish:

```go
registry.Publish(ctx, core.InternalEvent{
    Type:   "my_event",
    Source: p.Name(),
    Details: map[string]any{
        "owner": "acme",
    },
})
```

Guidelines:

- keep payloads primitive and JSON-friendly
- put domain data in `Details`
- let `ModuleManager.Publish` set the timestamp
- use one event type per distinct lifecycle/state transition

Current event catalog: [EVENTS.md](EVENTS.md)

## Subscription Pattern

Subscribe in `Start()`, not `Init()`, so plugin initialization stays focused on
configuration and setup.

Examples:

- exact event:
  - `registry.Subscribe("stack_commit_changed", p.handleCommitChanged)`
- wildcard:
  - `registry.Subscribe("notify_*", p.process)`
  - `registry.Subscribe("*", p.handleEvent)`

The event bus supports wildcard matching through the shared `ModuleManager`.

## HTTP Route Pattern

Plugins that expose HTTP routes should register them on the shared mux:

```go
if registry != nil {
    p.mux = registry.GetMuxServer()
    p.mux.HandleFunc("/api/my-plugin/health", p.handleHealth)
}
```

Do not start a separate HTTP server unless there is a strong reason. The normal
pattern is to attach routes to the shared mux.

Current examples:

- core plugin metadata routes
- UI routes under `/api/ui/*`
- webhook trigger route at `/reconcile`
- MCP routes under `/mcp/*`

## Execute Pattern

`Execute` is the in-process action surface other plugins or the UI can call.

Keep it explicit:

```go
switch action {
case "my_action":
    return ...
default:
    return nil, fmt.Errorf("unknown action: %s", action)
}
```

Use `Execute` for plugin-local actions, not as a substitute for events.

Rough conventions by capability:

- `DEPLOYER`
  - often exposes actions like `list_deployments`, `system_info`, `stream_logs`
- `SYSTEM`
  - often exposes read-only operational data
- `NOTIFIER`
  - often reacts to events rather than exposing many Execute actions
- `UI` / `API`
  - often register HTTP routes instead of relying heavily on `Execute`

There is no strict capability-to-action contract enforced by the core runtime,
so consistency comes from existing patterns and documentation.

## Capability Reference

Current capabilities from `pkg/core/capabilities.go`:

- `NOTIFIER`: emits notifications for selected events
- `UI`: exposes user-facing HTTP routes or assets
- `API`: exposes machine-facing HTTP routes
- `MCP`: exposes MCP-compatible endpoints or services
- `TRIGGER`: requests reconciliations from external input
- `SECRETS`: returns secret key/value pairs
- `RUNTIME_FILES`: materializes files for compose execution
- `AUDIT`: records or exposes event history
- `DEPLOYER`: deploys or manages stacks
- `SYSTEM`: provides core operational behavior

Pick the smallest accurate capability set. Capabilities are used by other
plugins to discover integrations.

## Build Instructions

Plugins are built with:

```bash
go build -buildmode=plugin -o bin/plugins/my_plugin.so plugins/my_plugin/*.go
```

Rules:

- single-file plugins can build from a single file
- multi-file plugins must use `*.go`
- the output file name controls plugin load order only indirectly through
  alphabetical `.so` ordering

If your plugin also has a frontend or generated assets, add a separate Makefile
target rather than burying extra steps inside `plugins`.

## Directory Layout

Recommended layout:

```text
plugins/
  my_plugin/
    README.md
    main.go
    main_test.go
```

Add more files when needed, but keep the plugin self-contained.

## Testing Guidance

Patterns already used successfully in this repo:

- use overridable `var` function seams for subprocess or filesystem boundaries
- use `httptest.NewServer` for GitHub/API integration tests
- keep pure helper functions separate from IO-heavy paths where possible
- prefer focused tests around one plugin’s public behavior

If your plugin depends on `PluginRegistry`, create a small test double that
implements only the methods you need.

## Common Mistakes

- forgetting to export `var Plugin core.Plugin = &MyPlugin{}`
- subscribing in `Init()` instead of `Start()`
- exposing raw secrets in `Config()`
- building a multi-file plugin with only `main.go`
- registering HTTP routes on a private mux that the shared server never uses
- publishing events without registering them first
- returning opaque `unknown action` errors without validating required params

## Examples To Copy From

Use existing plugins as reference implementations:

- `plugins/reconciler`
  - complex deployer, events, HTTP-backed actions
- `plugins/webhook_trigger`
  - simple trigger plugin with shared mux route
- `plugins/notifier_webhook`
  - configurable event subscriber
- `plugins/image_refresh`
  - lifecycle events, background job orchestration
- `plugins/ui`
  - shared mux routes plus embedded frontend

## Related Docs

- [ARCHITECTURE.md](ARCHITECTURE.md)
- [EVENTS.md](EVENTS.md)
- [docs/api.yaml](docs/api.yaml)
- [docs/plugins/README.md](docs/plugins/README.md)
