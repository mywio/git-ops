# Architecture

## Overview

`git-ops` is a single-process Go runtime that loads plugins from `.so` files and
coordinates them through a shared `ModuleManager`.

At a high level the system does four things:

1. Loads configuration from environment variables and an optional YAML file.
2. Loads and initializes plugins from `plugins/`.
3. Exposes a shared HTTP mux for core and plugin routes.
4. Runs long-lived background workflows, primarily the reconciler.

The runtime is intentionally small. Most behavior lives in plugins rather than
in `main.go`.

## Process Model

Startup flow:

1. `main.go` creates a JSON logger and loads config through `pkg/config`.
2. Environment-backed config (`LoadConfigMapFromEnv`) is merged with file config
   (`LoadConfigFile`) using `MergeConfigMap`.
3. `core.NewModuleManager` creates the shared runtime:
   - module registry
   - event bus
   - HTTP mux
   - shared HTTP client
4. `ModuleManager.LoadPlugins` opens `.so` files from the configured plugins
   directory and looks up the exported `Plugin` symbol.
5. `ModuleManager.Init` calls each plugin’s `Init(ctx, logger, registry)`.
6. `ModuleManager.Start` starts the HTTP server and then starts each plugin.
7. `main.go` waits for `SIGINT`/`SIGTERM` and calls `ModuleManager.Stop`.

Shutdown is reverse-order by module registration so plugins can depend on shared
 services staying available during their own teardown.

## Core Runtime

The core runtime lives in `pkg/core`.

Key types:

- `Module` is the lifecycle contract (`Init`, `Start`, `Stop`).
- `Plugin` extends `Module` with metadata, health, and `Execute`.
- `PluginRegistry` is the shared service surface plugins receive in `Init`.
- `ModuleManager` is the concrete runtime that owns module lifecycle, config,
  HTTP routes, and the event bus.

`ModuleManager` is instance-scoped. Event subscribers and registered event types
are stored on the manager instance rather than in package globals, which keeps
tests isolated and avoids cross-manager event leakage.

Plugin loading can be filtered at runtime with `core.plugins` or the
comma-separated `PLUGINS_ALLOW` environment variable. When the allowlist is
empty, `ModuleManager.LoadPlugins` loads every `.so` in the configured plugins
directory.

## Configuration Model

Configuration is sectioned and plugin-oriented.

Primary types:

- `pkg/config.ConfigMap`
- `pkg/config.Config`

Current runtime behavior:

- env vars are converted into a sectioned config map
- optional YAML config is loaded into the same shape
- the file config overlays env config via `MergeConfigMap`
- plugins read their section from `PluginRegistry.GetConfig()`

The `core` section is shared by the main runtime and several plugins.

Examples:

- `core.token`
- `core.users`
- `core.topics`
- `core.target_dir`
- `core.http_addr`

`TOPIC_FILTER` now accepts a comma-separated list and maps to `Config.Topics`.

## Event Bus

Plugins communicate through an internal event bus owned by `ModuleManager`.

Event primitives:

- `InternalEvent`
- `EventTypeDesc`
- `PayloadField`

Flow:

1. Plugins register event types in `Init()`.
2. Plugins subscribe to exact event names or wildcard patterns.
3. Plugins publish events through `registry.Publish(ctx, event)`.
4. `ModuleManager` validates required payload fields and dispatches matching
   listeners asynchronously.

Important events in the current system include:

- `reconcile_now`
- `reconcile_stack`
- `deploy_start`
- `deploy_success`
- `deploy_failed`
- `execution`
- `stack_commit_changed`
- `stack_locked`
- `stack_health`
- `notify_secret_conflict`
- `notify_compose_env_persistence_risk`

The event bus is the integration surface between deployers, notifiers, audit,
UI-adjacent APIs, and trigger plugins.

## HTTP Surface

The core runtime owns a shared `http.ServeMux`. Plugins register routes on that
shared mux through `registry.GetMuxServer()`.

Stable endpoints are documented in [docs/api.yaml](docs/api.yaml).

Current route groups:

- Core:
  - `GET /api/plugins`
  - `GET /api/plugins/{name}`
- UI plugin:
  - `GET /api/ui/deployments`
  - `GET /api/ui/system/info`
  - `GET /api/ui/logs`
  - `/` for the embedded frontend
- Webhook trigger plugin:
  - `POST /reconcile`

The HTTP server only starts when `core.http_addr` / `CORE_HTTP_ADDR` is set.

## Plugin Roles

The repository currently uses plugins for most product behavior.

Major plugin categories:

- Deployment:
  - `reconciler`
- Secrets and runtime materialization:
  - `google_secret_manager`
  - `env_forwarder`
  - `file_forwarder`
- Notifications and audit:
  - `notifier_pushover`
  - `notifier_webhook`
  - `audit`
- Operational APIs and UI:
  - `ui`
  - `mcp`
  - `webhook_trigger`
- Post-deploy automation:
  - `image_refresh`

This split keeps the core runtime generic while allowing feature growth through
capability-specific plugins.

## Reconciler

The reconciler is the primary long-running operational workflow.

Responsibilities:

- discover desired stacks from GitHub search
- discover removal candidates
- reconcile local state under `TARGET_DIR`
- deploy or prune stacks
- track execution state and bounded execution history
- emit deploy and health events

### Desired State Model

For each configured GitHub user/org and each configured topic, the reconciler
searches:

- desired state: `user:<name> topic:<topic> archived:false`
- archived removal state: `user:<name> topic:<topic> archived:true`
- explicit removal: `user:<name> topic:git-ops-remove`

State is keyed by `full_name` (`owner/repo`), so duplicate matches across
multiple topics collapse naturally.

### Deployment Flow

`deployRepoWithExecution()` is the reconciler’s top-level deployment pipeline.

Key phases:

1. fetch compose spec from GitHub
2. check for stack lock
3. apply force-type preconditions
4. handle `restart_only` early-exit mode
5. detect compose changes and dry-run diffs
6. write compose file
7. fetch repo hooks
8. prepare deploy env
9. run pre-hooks
10. preflight and `docker compose up`
11. run post-hooks
12. publish success/failure and update execution state

Supported compose filenames:

- `compose.yaml`
- `docker-compose.yml`

### Execution Tracking

The reconciler tracks per-stack execution state in memory.

Each stack exposes:

- current execution id
- execution status
- execution stage
- last error
- bounded history of the last 10 completed executions

History is returned newest-first through `list_deployments`, which is why the
UI can show recent execution metadata without a separate endpoint.

### Locking

Stacks can be paused with a `.git-ops-lock` file in the stack directory.

When present:

- deploy is skipped
- prune is skipped
- `stack_locked` is emitted

### Health Polling

The reconciler also runs a 60-second health poller.

It inspects `docker compose ps` for each managed stack, derives a high-level
runtime status (`running`, `partial`, `stopped`, `unknown`), and emits
`stack_health` only when a stack’s observed health changes.

## Secrets And Runtime Inputs

There are three main ways runtime inputs reach a deployment:

- `google_secret_manager` returns secret key/value pairs
- `env_forwarder` forwards allowlisted env vars and persists a snapshot for
  restart resilience
- `file_forwarder` materializes allowlisted host files and passes generated file
  paths into the compose environment

Secret conflict resolution is deterministic:

- plugins are loaded in `.so` filename order
- first writer wins
- `notify_secret_conflict` is emitted for collisions

Hooks intentionally do not receive forwarded secrets directly. Hook scripts get:

- `REPO_NAME`
- `REPO_OWNER`
- `TARGET_DIR`

If hooks need credentials, they must source them independently.

## UI And Operator Interfaces

There are three main operator-facing interfaces:

- Web UI:
  - deployments
  - system info
  - live logs
- Webhook trigger:
  - `POST /reconcile`
- MCP plugin:
  - exposes operational tools for AI workflows

The UI plugin embeds the built frontend from `plugins/ui/frontend/dist`, so the
frontend must be built before the Go UI plugin can be built.

## Build And Packaging

Build entrypoints:

- `make build`
- `make build-plugins`
- `make ui`
- `make plugins`

Important details:

- the binary version is injected through `-ldflags`
- UI build requires Node.js `20.19+`
- plugin targets that span multiple source files use `*.go`
- official release artifacts and the published container image target `linux/amd64`
- plugins can be filtered at load time through `core.plugins` / `PLUGINS_ALLOW`

CI currently builds, vets, lints, and tests the repository.

## Design Constraints

Several current design choices are intentional:

- plugin-centric architecture over a large monolith
- instance-scoped event bus for testability
- sectioned config maps for plugin isolation
- additive HTTP routes on a shared mux
- explicit event emission for operational observability
- filesystem-based stack locking for operator transparency

## Current Gaps

This document reflects the current implementation, not the full roadmap.

Still-evolving areas include:

- richer plugin-development docs
- event catalog documentation
- example plugin scaffolding
- fuller API and LLM-oriented documentation
