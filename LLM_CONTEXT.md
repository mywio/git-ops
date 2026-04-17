# LLM Context

## Project Summary

`git-ops` is a Go service with dynamically loaded plugins. The core runtime is
small; most behavior lives in plugins.

Primary runtime pieces:

- `pkg/core/module.go`: plugin lifecycle, registry, event bus, shared mux
- `pkg/config/config.go`: sectioned config loading and typed core config
- `plugins/reconciler/`: GitHub discovery, deploy/prune, execution tracking,
  locks, dry-run diffs, health polling

## Stable Interfaces

- Plugin interface: `pkg/core/module.go`
- Event model: `pkg/core/event.go`
- Execution lifecycle event: `pkg/core/execution.go`
- HTTP API: `docs/api.yaml`
- Event catalog: `EVENTS.md`
- Plugin authoring: `PLUGIN_DEVELOPMENT.md`

## Build And Test

```bash
make build
make build-plugins
go test ./...
```

UI assets:

```bash
make ui
```

Requires Node.js `20.19+`.

## Current Conventions

- Plugins load config from `registry.GetConfig()[section]`
- Plugins publish through `registry.Publish`
- Plugins subscribe in `Start()`
- HTTP routes attach to the shared mux from `registry.GetMuxServer()`
- UI-safe config exposure uses `Config() any`
- Secrets in config views use `core.Secret`

## Start Here For Code Changes

1. `ARCHITECTURE.md`
2. `EVENTS.md`
3. `PLUGIN_DEVELOPMENT.md`
4. `plugins/reconciler/main.go`
5. `pkg/core/module.go`
