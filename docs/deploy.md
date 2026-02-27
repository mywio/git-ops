# Deployment

This guide covers building and running `git-ops` with plugins.

## Prerequisites
- Linux (Go plugins are not supported on Windows)
- Go 1.24+
- Docker + Docker Compose

## Build
```bash
make build
make plugins
```

Artifacts:
- Core: `bin/git-ops`
- Plugins: `bin/plugins/*.so`

## Configure
Create a YAML config file (default `config.yaml` or set `CONFIG_FILE`):
```yaml
core:
  token: "ghp_123..."
  users: ["myuser", "myorg"]
  topic: "homelab-server-1"
  target_dir: "/opt/stacks"
  interval: "5m"
  plugins_dir: "./bin/plugins"

env_forwarder:
  keys: ["SECRET_API_KEY", "DB_PASSWORD"]
  prefixes: ["APP_"]

google_secret_manager:
  project_id: "my-gcp-project"
```

Notes:
- `env_forwarder` reads environment variables from the host. Ensure they are set
  in the service environment (see systemd example below).
- `google_secret_manager` requires Google ADC credentials.

## Run
```bash
CONFIG_FILE=/etc/git-ops/config.yaml ./bin/git-ops
```

## Update
```bash
git pull
make build
make plugins
systemctl restart git-ops
```

Notes:
- If you are not using systemd, restart your process manager instead.
- Rebuild after updating so plugins and embedded docs stay in sync.

## Auto-update options

### Docker + Watchtower
This requires publishing a container image (e.g., to GHCR) that already contains
the built core binary and plugins.

Example `docker-compose.yml`:
```yaml
services:
  git-ops:
    image: ghcr.io/your-org/git-ops:latest
    environment:
      - CONFIG_FILE=/etc/git-ops/config.yaml
      - SECRET_API_KEY=example
      - DB_PASSWORD=example
    volumes:
      - /etc/git-ops:/etc/git-ops:ro
      - /opt/stacks:/opt/stacks
    restart: unless-stopped

  watchtower:
    image: containrrr/watchtower:latest
    command: --cleanup --interval 300
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    restart: unless-stopped
```

### Systemd timer (git pull + rebuild)
This keeps a source checkout updated and rebuilds on a schedule.

`/etc/systemd/system/git-ops-update.service`:
```ini
[Unit]
Description=Update git-ops

[Service]
Type=oneshot
WorkingDirectory=/opt/git-ops
ExecStart=/usr/bin/git pull
ExecStart=/usr/bin/make build
ExecStart=/usr/bin/make plugins
ExecStart=/bin/systemctl restart git-ops
```

`/etc/systemd/system/git-ops-update.timer`:
```ini
[Unit]
Description=Periodic git-ops update

[Timer]
OnCalendar=hourly
Persistent=true

[Install]
WantedBy=timers.target
```

Enable:
```bash
systemctl daemon-reload
systemctl enable --now git-ops-update.timer
```

## Plugin precedence
Secret plugins are loaded in `.so` filename order. If multiple plugins return the
same key, the first wins and `notify_secret_conflict` is emitted.

Prefixing filenames (e.g., `01_env_forwarder.so`) controls precedence.

## MCP docs
The MCP plugin embeds the `docs/` folder at build time. `make plugins` copies
`docs/` into `plugins/mcp/docs` and embeds it. The docs are served at
`/mcp/docs/` on the MCP HTTP server.

## Systemd example
```ini
[Unit]
Description=git-ops
After=network.target docker.service
Wants=docker.service

[Service]
Type=simple
WorkingDirectory=/opt/git-ops
Environment=CONFIG_FILE=/etc/git-ops/config.yaml
Environment=SECRET_API_KEY=example
Environment=DB_PASSWORD=example
Environment=APP_TOKEN=example
ExecStart=/opt/git-ops/bin/git-ops
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

## Examples
- Env Forwarder: `examples/env_forwarder/`
- Google Secret Manager: `examples/google_secret_manager/`
