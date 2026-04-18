# ui

Web dashboard plugin.

Capabilities: `UI`, `API`

Config: none (requires `CORE_HTTP_ADDR` / `core.http_addr` to be set)

## Auth Proxy Paths

If you are putting git-ops behind Caddy, oauth2-proxy, or another auth layer, use these path rules:

- `/ui/*` — browser UI, protect with auth
- `/api/ui/*` — UI data API, protect with auth
- `/api/plugins` — core plugin metadata, protect as needed
- `/reconcile` — webhook trigger, no proxy auth needed
- `/mcp/*` — MCP endpoints, no proxy auth needed
- `/health` — health check, no auth needed

## API Routes

| Method | Path | Description |
| :--- | :--- | :--- |
| `GET` | `/api/ui/deployments` | Lists all locally managed stacks with runtime status and deployment execution metadata |
| `GET` | `/api/ui/system/info` | Returns Docker daemon info |
| `GET` | `/api/ui/logs?owner=&repo=&lines=` | Streams managed stack logs via SSE |
| `GET` | `/api/ui/logs?container=&lines=` | Streams unmanaged container logs via SSE |

The frontend dashboard SPA is served at `/ui/*`, and `/` redirects to `/ui/system`.

Build note: the UI plugin embeds `frontend/dist`, so `make ui` must be run before building the plugin. If the UI assets have not been built yet, the Go plugin build will fail because the embedded dist directory is missing.
