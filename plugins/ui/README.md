# ui

Web dashboard plugin.

Capabilities: `UI`, `API`

Config:

- `disable_auth` — disable the built-in UI header check. Default: `false`
- `auth_header` — header containing the authenticated user identity. Default:
  `X-Auth-Request-User`
- `auth_verify_url` — recommended OAuth2 Proxy `/oauth2/auth` endpoint. The UI
  forwards the request cookie/authorization to this endpoint and trusts only the
  identity returned by OAuth2 Proxy.
- `trust_auth_header` — opt into the legacy proxy-header mode. Default: `false`.
  Do not enable this unless the application is unreachable except through a
  trusted proxy that deletes or overwrites the incoming identity header.

The UI plugin requires a verified identity by default on:

- `/`
- `/ui/*`
- `/api/ui/*`

If `disable_auth` is `false`, configure `auth_verify_url` or explicitly opt into
`trust_auth_header`. Otherwise the UI returns `401 Unauthorized`, even when a
client supplies the identity header directly.

## OAuth2 Proxy With Caddy

With OAuth2 Proxy, enable `--set-xauthrequest=true` so `/oauth2/auth` returns
`X-Auth-Request-User`, then configure:

```yaml
ui:
  auth_verify_url: http://oauth2-proxy:4180/oauth2/auth
  auth_header: X-Auth-Request-User
```

Keep Caddy's `forward_auth` in place for login redirects. The application will
independently verify the resulting session, so a client-supplied identity header
is ignored even if Caddy fails to strip it.

If you intentionally use header-only mode, use a `route` block so header
deletion happens before authentication and the verified response overwrites it:

```caddyfile
route {
    request_header -X-Auth-Request-User
    forward_auth oauth2-proxy:4180 {
        uri /oauth2/auth
        copy_headers X-Auth-Request-User
    }
    reverse_proxy git-ops:8080
}
```

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
  auth_verify_url: http://oauth2-proxy:4180/oauth2/auth
  trust_auth_header: false
```

Build note: the UI plugin embeds `frontend/dist`, so `make ui` must be run before building the plugin. If the UI assets have not been built yet, the Go plugin build will fail because the embedded dist directory is missing.
