# Plugins

Git-Ops uses a modular architecture where functionalities are loaded as plugins.
Each plugin is a shared object (`.so`) file loaded at runtime.

## Available Plugins

### Google Secret Manager
Injects secrets from Google Secret Manager into your deployment environment.
- **Capabilities**: `secrets`
- **Configuration**: Uses Google Cloud Application Default Credentials.

### MCP (Model Context Protocol)
Provides context to AI agents about the deployment state.
- **Capabilities**: `ai-context`

### UI
Provides a web-based dashboard for monitoring and managing deployments.
- **Capabilities**: `dashboard`

### Notifications
Sends notifications to Pushover or Webhooks on deployment events.
- **Capabilities**: `notifications`

## Developing Plugins
Plugins must implement the `Plugin` interface defined in `pkg/core/module.go`.

```go
type Plugin interface {
    Module
    Capabilities() []Capability
    Status() ServiceStatus
}
```

Build plugins with `go build -buildmode=plugin`.
