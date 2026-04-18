# git-ops: Dynamic GitHub Topic Deployer

git-ops is a lightweight, "GitOps-lite" operator written in Go. It automatically discovers, syncs, and deploys Docker Compose stacks from your GitHub repositories based on Topics.

## Quick Start

### Docker (recommended)

```yaml
services:
  git-ops:
    image: ghcr.io/mywio/git-ops:latest
    environment:
      GITHUB_TOKEN: ghp_your_token
      GITHUB_USERS: your-github-username
      TOPIC_FILTER: homelab
      TARGET_DIR: /stacks
      CORE_HTTP_ADDR: 0.0.0.0:8080
      PLUGINS_ALLOW: reconciler,ui,audit
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - /opt/stacks:/stacks
    restart: unless-stopped
```

Then run:

```bash
docker compose up -d
```

Docker is also the recommended approach on Windows and macOS, where the native binary is not supported.

| Plugin | Enable when | Extra config needed |
| :--- | :--- | :--- |
| `reconciler` | Always — core deploy engine | none beyond the three required vars |
| `ui` | You want the web dashboard | `CORE_HTTP_ADDR` |
| `audit` | You want an event history | none |
| `image_refresh` | You want automatic image updates | none |
| `env_forwarder` | You forward env vars to stacks | allowlist in config |
| `file_forwarder` | You forward host files to stacks | file list in config |
| `webhook_trigger` | You want to trigger reconcile via HTTP | optional `WEBHOOK_TOKEN` |
| `notifier_discord` | You want Discord alerts | `DISCORD_WEBHOOK_URL` |
| `notifier_pushover` | You want Pushover alerts | `NOTIFY_PUSHOVER_TOKEN` and `NOTIFY_PUSHOVER_USER` |
| `notifier_webhook` | You want generic webhook alerts | `NOTIFY_WEBHOOK_URL` |
| `google_secret_manager` | You use GCP secrets | Google ADC credentials and `GOOGLE_CLOUD_PROJECT` |
| `mcp` | You use AI tooling | `MCP_API_KEY` |

Add plugins to `PLUGINS_ALLOW` one at a time as you need them.

## Features
- **Modular Plugin Architecture**: Extensible functionality via plugins (Secrets, UI, AI Context, Notifications).
- **GitOps Lite**: Syncs `docker-compose.yml` from GitHub based on Topics.
- **Hook System**: Run scripts before/after deployment.

## Installation

### Build from source

Building from source requires Linux, Go 1.24+, and for the UI plugin Node.js 20.19+.

#### Prerequisites
- Go 1.24+
- Docker & Docker Compose

#### Build
```bash
# Build the core binary
make build

# Build plugins
make plugins
```

The binary will be in `bin/git-ops` and plugins in `bin/plugins/`.

Prebuilt binaries are also published as `git-ops-linux-amd64.tar.gz` release assets.
The official container image is published as `ghcr.io/mywio/git-ops:latest`.

Maintainer-focused verification workflows, including mutation testing, are documented in [docs/maintenance.md](docs/maintenance.md).

## Configuration (Env Vars)

| Variable | Description | Required | Example |
| :--- | :--- | :--- | :--- |
| `GITHUB_TOKEN` | PAT with `repo` scope | Yes | `ghp_123...` |
| `GITHUB_USERS` | Comma-separated users/orgs to scan | Yes | `myuser,myorg` |
| `TOPIC_FILTER` | Comma-separated GitHub topics to watch for | Yes | `homelab-server-1,prod` |
| `TARGET_DIR` | Local path to store stacks | No | `/opt/stacks` |
| `GLOBAL_HOOKS_DIR`| Path to server-wide hooks | No | `/etc/git-ops/hooks` |
| `SYNC_INTERVAL` | Loop frequency | No | `5m` (default) |
| `DRY_RUN` | Log only, no changes | No | `false` |
| `PLUGINS_DIR` | Path to plugins directory | No | `./plugins` (default) |
| `PLUGINS_ALLOW` | Comma-separated plugin `.so` base names to load | No | `reconciler,ui,discord` |
| `CORE_HTTP_ADDR` | Core HTTP bind address for APIs/UI | No | `127.0.0.1:8080` |
| `SECRETS_DIR` | Path to local secrets directory | No | `/etc/git-ops/secrets` |

You can also use a YAML config file (default `config.yaml` or set `CONFIG_FILE`).
See `examples/config.yaml` and `docs/deploy.md`.

## Plugins
git-ops supports dynamically loaded plugins. By default, it looks for `.so` files in the `plugins/` directory relative to the working directory.

Available plugins:
- **Reconciler**: Core GitOps engine — scans GitHub, detects changes, and runs `docker compose up/down`.
- **Google Secret Manager**: Injects secrets from GSM into deployments.
- **Env Forwarder**: Forwards allowlisted environment variables into docker compose.
- **File Forwarder**: Forwards allowlisted host files as runtime file paths into docker compose.
- **MCP**: AI Context integration (Model Context Protocol).
- **UI**: Web Dashboard with deployment list, log streaming, and system info.
- **Pushover Notifier**: Sends deployment alerts via Pushover.
- **Discord Notifier**: Sends deployment alerts to a Discord webhook.
- **Webhook Notifier**: Sends deployment events to a generic webhook endpoint.
- **Webhook Trigger**: Exposes `POST /reconcile` to trigger an immediate reconciliation on demand.
- **Audit**: Subscribes to all events and maintains a queryable audit log (memory or SQLite).

See [docs/plugins/](docs/plugins/) for more details.

## Deployment
See `docs/deploy.md` for build and run instructions.

## How it Works
1.  **Scan:** Periodically queries GitHub for repositories matching the configured users and one or more topics (e.g., `topic:homelab-node-1` or `topic:prod`).
2.  **Reconcile:**
    * **New/Updated:** Downloads `docker-compose.yml` and hook scripts, then runs `docker compose up -d`.
    * **Removed/Archived:** Detects if a repo no longer matches the criteria and runs `docker compose down` + deletes the local folder.
3.  **Hooks:** Executes shell scripts before and after deployment for migrations, secrets, or notifications.

## Directory Structure

**On the Server:**
```text
/opt/stacks/
  ├── myuser/
  │   └── my-app/
  │       ├── docker-compose.yml
  │       └── .deploy/ ...
  └── myorg/
      └── media-server/ ...
```

**In your Repository:**
To add hooks, create a `.deploy` folder in your repo:

```text
my-repo/
├── docker-compose.yml
└── .deploy/
    ├── pre/   # Scripts run BEFORE docker compose up
    │   └── 01-init-env.sh
    └── post/  # Scripts run AFTER docker compose up
        └── 99-slack-notify.sh
```

## Hook Environment Variables

Your scripts receive these variables automatically:

* `REPO_NAME`: Name of the repository (e.g., `my-app`)
* `REPO_OWNER`: Owner of the repository (e.g., `myuser`)
* `TARGET_DIR`: Absolute path to the deployment folder on the server
