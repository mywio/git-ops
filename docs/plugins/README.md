# Plugins

Git-Ops uses a modular architecture where functionality is loaded as plugins.
Each plugin is a shared object (`.so`) file loaded at runtime.

Per-plugin docs live with their source. Start here:

- [Google Secret Manager](../../plugins/google_secret_manager/README.md)
- [MCP](../../plugins/mcp/README.md)
- [Notifications](../../plugins/notifications/README.md)
- [Pushover Notifier](../../plugins/notifier_pushover/README.md)
- [Webhook Notifier](../../plugins/notifier_webhook/README.md)
- [UI](../../plugins/ui/README.md)
- [Webhook Trigger](../../plugins/webhook_trigger/README.md)

## Developing Plugins
Plugins must implement the `Plugin` interface defined in `pkg/core/module.go`.

```go
type Plugin interface {
    Module
    Capabilities() []Capability
    Status() ServiceStatus
}
```

Use `registry.GetConfig()` for configuration and `core.DecodeConfigSection` to
decode a section into a struct.

Build plugins with `go build -buildmode=plugin`.
