# mcp

Model Context Protocol plugin. Exposes git-ops stacks and deployments as MCP tools that Claude (and other MCP clients) can call directly.

Capabilities: `MCP`, `API`

Config section: `mcp`

| Key | Description | Default |
| :--- | :--- | :--- |
| `api_key` | Bearer token required for all requests | *(none — unsecured)* |
| `target_dir` | Path where stacks are deployed | `/opt/stacks` |

Requires `CORE_HTTP_ADDR` / `core.http_addr` to be set so the shared HTTP server is available.

## Connecting to Claude Code

```bash
claude mcp add --transport http \
  --header "Authorization: Bearer $MCP_API_KEY" \
  git-ops \
  http://<your-server>:<port>/mcp
```

If `api_key` is not set, omit the `--header` flag.

## MCP tools

| Tool | Description | Required params |
| :--- | :--- | :--- |
| `list_stacks` | All deployed stacks with last sync and deploy status | — |
| `list_deployments` | Recent deployment events with status and timing | — |
| `get_services` | Docker Compose service list for a stack | `repo` |
| `get_logs` | Container logs for a service | `repo`, `service` |
| `get_health` | Docker health check status for a service | `repo`, `service` |
| `get_setup_info` | Instructions for adding a new repo to git-ops | — |

### `get_logs` optional params
- `lines` — number of lines to return (default: `100`)
- `since` — duration filter (e.g. `1h`, `30m`)

`list_stacks` returns the canonical stack identifier used everywhere else in the plugin: `owner/repo`.

## Authentication

Both formats are accepted for all endpoints:
- `Authorization: Bearer <token>` — used by Claude Code
- `X-API-Key: <token>` — legacy REST clients

## Legacy REST endpoints

The original REST endpoints remain available for other tooling:

- `GET /mcp/setup`
- `GET /mcp/stacks`
- `GET /mcp/deployments`
- `GET /mcp/services/{owner}/{repo}`
- `GET /mcp/logs/{owner}/{repo}/{service}?lines=100&since=1h`
- `GET /mcp/health/{owner}/{repo}/{service}`
- `GET /mcp/docs/`

## Docs

`docs/` is copied into `plugins/mcp/docs` during `make plugins` and embedded at build time. The docs are served at `/mcp/docs/`.
