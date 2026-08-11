# reconciler

Core GitOps reconciler plugin. Periodically scans GitHub and deploys or removes Docker Compose stacks based on repository topics.

Capabilities: `deployer`, `system`

Config section: `core` (shared with main configuration)

## Behavior

On each sync interval the reconciler builds three lists:

1. **Desired** - repos matching `user:<name> topic:<configured-topic> archived:false` for any configured topic in `TOPIC_FILTER`
2. **Removal (explicit)** - repos tagged `git-ops-remove`
3. **Removal (archived)** - repos matching any configured topic but with `archived:true`

Local stacks that are in the removal list are brought down and deleted.
Local stacks not found in any GitHub query produce a warning but are **not** automatically deleted (divergence guard).

## Hooks

Repository and global hook scripts receive only service context variables:

- `REPO_NAME`
- `REPO_OWNER`
- `TARGET_DIR`

Forwarded secrets are intentionally not injected into hook environments. If a
hook needs credentials, it must source them independently, for example from a
secrets manager integration or from files materialized by a runtime-file plugin.

## Events

| Event | Description |
| :--- | :--- |
| `reconcile_now` | Trigger an immediate full reconciliation |
| `reconcile_stack` | Trigger reconciliation for a specific stack |
| `deploy_start` | Emitted when a stack deployment begins |
| `deploy_success` | Emitted when a stack deploys successfully |
| `deploy_failed` | Emitted when a stack deployment fails |
| `notify_secret_conflict` | Emitted when two secret plugins return the same key |
| `stack_commit_changed` | Emitted after a successful reconcile when the observed repository commit advances |

### `stack_commit_changed`

`stack_commit_changed` keeps commit movement visible for audit and downstream automation. Its `Details` payload includes:

- `owner`
- `repo`
- `full_name`
- `stack_path`
- `old_commit`
- `new_commit`
- `compose_changed`

`Source` and `Timestamp` remain on the top-level event.

When `compose_changed=true`, the reconciler already handled the compose deployment itself. Downstream image-refresh logic should ignore that event and only react when `compose_changed=false`.

## Execute actions

| Action | Parameters | Description |
| :--- | :--- | :--- |
| `list_deployments` | - | Returns managed stacks and running Docker containers with Compose grouping and adoption metadata |
| `system_info` | - | Returns Docker daemon info |
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

## Deployment grouping and adoption

Managed stacks are grouped by GitHub owner. Running containers outside those
stacks are grouped by the Docker Compose project, working-directory, config-file,
and service labels. Containers without Compose labels are placed in a standalone
Docker group.

Compose groups are reported as adoption candidates, but discovery is read-only.
See [the deployment guide](../../docs/deploy.md#adopt-an-existing-compose-project)
for the migration requirements and data-safety checklist.
