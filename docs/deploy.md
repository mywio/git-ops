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

## Plugin precedence
Secret plugins are loaded in `.so` filename order. If multiple plugins return the
same key, the first wins and `notify_secret_conflict` is emitted.

Prefixing filenames (e.g., `01_env_forwarder.so`) controls precedence.

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
