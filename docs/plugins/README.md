# Plugins

Git-Ops uses a modular architecture where functionality is loaded as plugins.
Each plugin is a shared object (`.so`) file loaded at runtime.

## Configuration

Plugins read configuration from the registry (not environment variables). The core
loader merges a YAML config file with environment defaults and exposes it under
section names.

Config file path:
`CONFIG_FILE` (default `config.yaml`)

Example:
```yaml
core:
  token: "ghp_123..."
  users: ["myuser", "myorg"]
  topic: "homelab-server-1"
  target_dir: "./stacks"
  interval: "5m"
  dry_run: false
  plugins_dir: "./plugins"

pushover:
  token: "push_token"
  user: "push_user"
```

## Available Plugins

### Google Secret Manager
Injects secrets from Google Secret Manager into your deployment environment.
Capabilities: `secrets`
Config section: `google_secret_manager`
Keys: `project_id` (or `project`)
Notes: Uses Google Cloud Application Default Credentials.

### MCP (Model Context Protocol)
Provides context to AI agents about the deployment state.
Capabilities: `MCP`, `API`
Config section: `mcp`
Keys: `api_key`, `target_dir`

### UI
Provides a web-based dashboard for monitoring and managing deployments.
Capabilities: `UI`, `API`
Config section: `core`
Keys: `plugins_dir` (optional; used for plugin loading)

### Pushover Notifier
Sends notifications to Pushover on deployment events.
Capabilities: `NOTIFIER`
Config section: `pushover`
Keys: `token`, `user`

### Webhook Notifier
Sends notifications to a generic webhook endpoint.
Capabilities: `NOTIFIER`
Config section: `webhook`
Keys: `url`

### Webhook Trigger
Exposes an HTTP endpoint to trigger reconciliation.
Capabilities: `trigger`
Config section: `webhook_trigger`
Keys: `port`, `token`

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
