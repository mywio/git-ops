# mcp

Model Context Protocol plugin that exposes HTTP endpoints for stack discovery and diagnostics.

Capabilities: `MCP`, `API`

Config section: `mcp`  
Keys: `api_key`, `target_dir`  
Default: `target_dir` falls back to `/opt/stacks`

Auth:
- If `api_key` is set, requests must include `X-API-Key: <key>`.

Endpoints:
- `GET /mcp/setup`
- `GET /mcp/stacks`
- `GET /mcp/deployments`
- `GET /mcp/services/{repo}`
- `GET /mcp/logs/{repo}/{service}?lines=100&since=1h`
- `GET /mcp/health/{repo}/{service}`
- `GET /mcp/docs/`

Docs:
- `docs/` is copied into `plugins/mcp/docs` during `make plugins` and embedded at build time.
