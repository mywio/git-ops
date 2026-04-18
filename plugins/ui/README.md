# ui

Web dashboard plugin.

Capabilities: `UI`, `API`

Config:

- `disable_auth` — disable the built-in UI header check. Default: `false`
- `auth_header` — header containing the authenticated user identity. Default:
  `X-Auth-Request-User`

The UI plugin now requires this header by default on:

- `/`
- `/ui/*`
- `/api/ui/*`

If `disable_auth` is `false` and the configured header is missing or empty, the
UI plugin returns `401 Unauthorized`.

## Auth Proxy Paths

If you are putting git-ops behind Caddy, oauth2-proxy, or another auth layer,
use these path rules:

- `/ui/*` — browser UI, protect with auth and forward the configured user header
- `/api/ui/*` — UI data API, protect with auth and forward the configured user header
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

Example config:

```yaml
ui:
  disable_auth: false
  auth_header: X-Auth-Request-User
```

Build note: the UI plugin embeds `frontend/dist`, so `make ui` must be run before building the plugin. If the UI assets have not been built yet, the Go plugin build will fail because the embedded dist directory is missing.
