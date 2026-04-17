# ui

Web dashboard plugin.

Capabilities: `UI`, `API`

Config: none (requires `CORE_HTTP_ADDR` / `core.http_addr` to be set)

## API Routes

| Method | Path | Description |
| :--- | :--- | :--- |
| `GET` | `/api/ui/deployments` | Lists all locally managed stacks with runtime status and deployment execution metadata |
| `GET` | `/api/ui/system/info` | Returns Docker daemon info |
| `GET` | `/api/ui/logs?owner=&repo=&lines=` | Streams container logs via SSE |

The frontend dashboard (Vite SPA) is served at `/`.

Build note: the UI plugin embeds `frontend/dist`, so `make ui` must be run before building the plugin. If the UI assets have not been built yet, the Go plugin build will fail because the embedded dist directory is missing.
