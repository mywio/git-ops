# reconciler

Core GitOps reconciler plugin. Periodically scans GitHub and deploys or removes Docker Compose stacks based on repository topics.

Capabilities: `deployer`, `system`

Config section: `core` (shared with main configuration)

## Behavior

On each sync interval the reconciler builds three lists:

1. **Desired** — repos matching `user:<name> topic:<TOPIC_FILTER> archived:false`
2. **Removal (explicit)** — repos tagged `git-ops-remove`
3. **Removal (archived)** — repos matching the topic filter but with `archived:true`

Local stacks that are in the removal list are brought down and deleted.
Local stacks not found in any GitHub query produce a warning but are **not** automatically deleted (divergence guard).

## Events

| Event | Description |
| :--- | :--- |
| `reconcile_now` | Trigger an immediate full reconciliation |
| `reconcile_stack` | Trigger reconciliation for a specific stack |
| `deploy_start` | Emitted when a stack deployment begins |
| `deploy_success` | Emitted when a stack deploys successfully |
| `deploy_failed` | Emitted when a stack deployment fails |
| `notify_secret_conflict` | Emitted when two secret plugins return the same key |

## Execute actions

| Action | Parameters | Description |
| :--- | :--- | :--- |
| `list_deployments` | — | Returns all locally managed stacks with status |
| `system_info` | — | Returns Docker daemon info |
| `stream_logs` | `owner`, `repo`, `lines` | Streams `docker compose logs` as a channel of strings |
| `reconcile_stack` | `owner`, `repo`, `force_type` | Triggers a targeted reconciliation |

### `force_type` values for `reconcile_stack`

| Value | Behavior |
| :--- | :--- |
| *(empty)* | Skip deploy if `docker-compose.yml` is unchanged |
| `bypass_check` | Re-deploy even if file is unchanged |
| `clean_local_state` | Delete the local compose file and `.deploy` folder before deploying |
| `remove_images` | Run `docker compose down --rmi all` before deploying |
| `restart_only` | Run `docker compose restart` (skips file download and change detection) |
