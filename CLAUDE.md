# CLAUDE.md

## What This Is

`git-ops` is a plugin-based GitOps operator for Docker Compose stacks. It scans
GitHub repositories by topic, syncs compose files into `TARGET_DIR`, and uses
`docker compose` to keep local state aligned with GitHub state.

## Build

```bash
make build
make build-plugins
go test ./...
```

UI build is separate:

```bash
make ui
```

Note: `make ui` requires Node.js `20.19+`.

## Read These First

1. `ARCHITECTURE.md`
2. `pkg/core/module.go`
3. `pkg/core/event.go`
4. `PLUGIN_DEVELOPMENT.md`
5. `EVENTS.md`

## Important Paths

- `main.go`: process bootstrap
- `pkg/core/`: runtime, plugin loader, event bus, HTTP core routes
- `pkg/config/`: env + YAML config loading
- `plugins/reconciler/`: deploy/prune/health/execution logic
- `plugins/ui/`: web UI and `/api/ui/*` routes
- `docs/api.yaml`: stable HTTP API spec

## Testing

```bash
go test ./...
```

If you touch the reconciler heavily:

```bash
go test ./plugins/reconciler
```

## Plugin Notes

- Plugins must export `var Plugin core.Plugin = &MyPlugin{}`
- Multi-file plugins must be built with `*.go`, not only `main.go`
- Register event types in `Init()`
- Subscribe to events in `Init()` so every consumer is ready before startup publishers run
- Use `core.Secret` in `Config()` for redacted config exposure
