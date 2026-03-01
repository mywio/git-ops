# Plugins

Git-Ops uses a modular architecture where functionality is loaded as plugins.
Each plugin is a shared object (`.so`) file loaded at runtime.

Per-plugin docs live with their source. Start here:

- [Google Secret Manager](../../plugins/google_secret_manager/README.md)
- [Env Forwarder](../../plugins/env_forwarder/README.md)
- [MCP](../../plugins/mcp/README.md)
- [Notifications](../../plugins/notifications/README.md)
- [Pushover Notifier](../../plugins/notifier_pushover/README.md)
- [Webhook Notifier](../../plugins/notifier_webhook/README.md)
- [UI](../../plugins/ui/README.md)
- [Webhook Trigger](../../plugins/webhook_trigger/README.md)

## Secret Precedence
Secret plugins are ordered by plugin load order (sorted by `.so` file name).
If multiple plugins return the same key, the first one wins. A
`notify_secret_conflict` event is emitted. Prefixing a plugin file name
(e.g., `01_env_forwarder.so`) controls precedence.

## Developing Plugins
Plugins must implement the `Plugin` interface defined in `pkg/core/module.go`.

```go
type Plugin interface {
    Module
    Description() string
    Capabilities() []Capability
    Status() ServiceStatus
    Execute(ctx context.Context, action string, params map[string]interface{}) (interface{}, error)
}
```

Use `registry.GetConfig()` for configuration and `core.DecodeConfigSection` to
decode a section into a struct.

Build plugins with `go build -buildmode=plugin`.

## Core Plugin API
If `core.http_addr` / `CORE_HTTP_ADDR` is set, core exposes:
- `GET /api/plugins` (list plugins; `include_config=true` to include config)
- `GET /api/plugins/{name}` (plugin details with config if available)

Plugins can optionally implement `core.ConfigProvider` to expose a UI-safe config view.
Use `core.Secret` for sensitive fields.
