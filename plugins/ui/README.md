# ui

Web dashboard plugin.

Capabilities: `UI`, `API`

Config: none (requires `CORE_HTTP_ADDR` / `core.http_addr` to be set)

## API Routes

| Method | Path | Description |
| :--- | :--- | :--- |
| `GET` | `/api/ui/deployments` | Lists all locally managed stacks and their status |
| `GET` | `/api/ui/system/info` | Returns Docker daemon info |
| `GET` | `/api/ui/logs?owner=&repo=&lines=` | Streams container logs via SSE |

The frontend dashboard (Vite SPA) is served at `/`.
