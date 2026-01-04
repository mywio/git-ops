# GHOps: Dynamic GitHub Topic Deployer

GHOps is a lightweight, "GitOps-lite" operator written in Go. It automatically discovers, syncs, and deploys Docker Compose stacks from your GitHub repositories based on Topics.

## How it Works
1.  **Scan:** Periodically queries GitHub for repositories matching a specific User and Topic (e.g., `topic:homelab-node-1`).
2.  **Reconcile:**
    * **New/Updated:** Downloads `docker-compose.yml` and hook scripts, then runs `docker compose up -d`.
    * **Removed/Archived:** Detects if a repo no longer matches the criteria and runs `docker compose down` + deletes the local folder.
3.  **Hooks:** Executes shell scripts before and after deployment for migrations, secrets, or notifications.

## Configuration (Env Vars)

| Variable | Description | Required | Example |
| :--- | :--- | :--- | :--- |
| `GITHUB_TOKEN` | PAT with `repo` scope | Yes | `ghp_123...` |
| `GITHUB_USERS` | Comma-separated users/orgs to scan | Yes | `myuser,myorg` |
| `TOPIC_FILTER` | The GitHub Topic to watch for | Yes | `homelab-server-1` |
| `TARGET_DIR` | Local path to store stacks | No | `/opt/stacks` |
| `GLOBAL_HOOKS_DIR`| Path to server-wide hooks | No | `/etc/ghops/hooks` |
| `SYNC_INTERVAL` | Loop frequency | No | `5m` (default) |
| `DRY_RUN` | Log only, no changes | No | `false` |

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