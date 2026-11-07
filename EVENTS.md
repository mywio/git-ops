# Events

This file is the consolidated event catalog for the current git-ops runtime.

Notes:

- `audit` subscribes to `*`, so it receives every event listed here.
- notifier plugins subscribe to configurable patterns. With default config they
  subscribe to `notify_*` and normalized `execution` events.
- the MCP plugin subscribes to `deploy_*`.
- payload tables describe `InternalEvent.Details` fields, not top-level event
  fields like `Type`, `Source`, `Timestamp`, `Repo`, or `Message`.

## reconcile_now
**Publisher:** `webhook_trigger` (current in-repo publisher)  
**Subscribers:** `reconciler`, `audit`  
**Description:** Requests an immediate full reconciliation run.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `force` | `bool` | No | Force even if locked. |

## reconcile_stack
**Publisher:** none in-repo currently; intended for external publishers or future trigger plugins  
**Subscribers:** `reconciler`, `audit`  
**Description:** Requests reconciliation for a specific stack.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `owner` | `string` | Yes | Repository owner. |
| `repo` | `string` | Yes | Repository name. |
| `force_type` | `string` | No | Force deploy mode (`bypass_check`, `clean_local_state`, `remove_images`, `restart_only`). |

## deploy_start
**Publisher:** `reconciler`  
**Subscribers:** `mcp`, `audit`  
**Description:** Indicates that stack deployment has started.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `owner` | `string` | Yes | Repository owner. |
| `repo` | `string` | Yes | Repository name. |
| `full_name` | `string` | Yes | Repository full name. |
| `status` | `string` | Yes | Deployment lifecycle status string. |
| `duration` | `string` | Yes | Elapsed deployment duration string. |
| `started_at` | `string` | Yes | RFC3339 deploy start timestamp. |

## deploy_success
**Publisher:** `reconciler`  
**Subscribers:** `mcp`, `audit`  
**Description:** Indicates that stack deployment completed successfully.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `owner` | `string` | Yes | Repository owner. |
| `repo` | `string` | Yes | Repository name. |
| `full_name` | `string` | Yes | Repository full name. |
| `status` | `string` | Yes | Deployment lifecycle status string. |
| `duration` | `time.Duration` | Yes | Elapsed deployment duration string. |
| `started_at` | `string` | Yes | RFC3339 deploy start timestamp. |

## deploy_failed
**Publisher:** `reconciler`  
**Subscribers:** `mcp`, `audit`  
**Description:** Indicates that stack deployment failed.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `owner` | `string` | Yes | Repository owner. |
| `repo` | `string` | Yes | Repository name. |
| `full_name` | `string` | Yes | Repository full name. |
| `status` | `string` | Yes | Deployment lifecycle status string. |
| `duration` | `string` | Yes | Elapsed deployment duration string. |
| `started_at` | `string` | Yes | RFC3339 deploy start timestamp. |

## execution
**Publisher:** `reconciler`  
**Subscribers:** `audit`, `notifier_discord`, `notifier_pushover`, `notifier_webhook`
**Description:** Reports stack execution lifecycle updates across request, running, success, and failure transitions.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `execution_id` | `string` | Yes | Execution identifier. |
| `owner` | `string` | Yes | Repository owner. |
| `repo` | `string` | Yes | Repository name. |
| `full_name` | `string` | Yes | Repository full name. |
| `stage` | `string` | Yes | Execution stage. |
| `status` | `string` | Yes | Execution status. |
| `failure_class` | `string` | No | Failure category when applicable. |
| `alert_severity` | `string` | No | Severity hint for downstream consumers. |
| `node_id` | `string` | No | Node that handled the execution. |
| `trigger` | `string` | No | Trigger source (`reconcile`, `reconcile_stack`, etc.). |
| `error` | `string` | No | Error detail on failures. |

## repository_revision_requested and local_deploy_requested

**Publishers:** `webhook_trigger`, then `reconciler`
**Subscribers:** `reconciler`, then `local_deploy`; `audit` receives both
**Description:** Preserves a GitHub default-branch push as an exact revision request,
then routes it after owner/topic and recipe eligibility checks.

Both payloads contain `owner`, `repo`, `ref`, `commit_sha`, `delivery_id`,
and `trigger`. Periodic reconciliation uses the same routed event without a
delivery ID, allowing `local_deploy` to deduplicate by owner/repository/SHA.

## local_deploy lifecycle events

**Publisher:** `local_deploy`
**Subscribers:** `audit` and any notifier configured for `local_deploy_*`
**Description:** The plugin emits `local_deploy_started`, `local_deploy_phase`,
`local_deploy_succeeded`, `local_deploy_failed`,
`local_deploy_superseded`, `local_deploy_rollback_started`,
`local_deploy_rollback_succeeded`, and `local_deploy_rollback_failed`.

Lifecycle payloads include the execution ID, exact commit SHA, prior successful
SHA, trigger, duration, current phase, verification and rollback status, and a
failure classification/error when applicable. The plugin also publishes the
normalized `execution` event at each transition, using the stages `checkout`,
`build`, `deploy`, `verify`, and `rollback`, so existing audit and
notification consumers receive a consistent execution shape.

## stack_commit_changed
**Publisher:** `reconciler`  
**Subscribers:** `image_refresh`, `audit`  
**Description:** Announces that the observed repository commit advanced after a successful reconciliation.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `owner` | `string` | Yes | Repository owner. |
| `repo` | `string` | Yes | Repository name. |
| `full_name` | `string` | Yes | Repository full name. |
| `stack_path` | `string` | Yes | Absolute stack path. |
| `old_commit` | `string` | No | Previously observed commit. |
| `new_commit` | `string` | Yes | Newly observed commit. |
| `compose_changed` | `bool` | Yes | Whether the compose file changed in the successful reconcile path. |

## stack_locked
**Publisher:** `reconciler`  
**Subscribers:** `audit`  
**Description:** Indicates that deploy or prune work was skipped because a lock file is present.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `owner` | `string` | Yes | Repository owner. |
| `repo` | `string` | Yes | Repository name. |
| `full_name` | `string` | Yes | Repository full name. |
| `stack_path` | `string` | Yes | Absolute stack path. |
| `lock_file` | `string` | Yes | Absolute lock file path. |

## stack_health
**Publisher:** `reconciler`  
**Subscribers:** `audit`  
**Description:** Emitted when observed compose runtime health changes for a managed stack.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `owner` | `string` | Yes | Repository owner. |
| `repo` | `string` | Yes | Repository name. |
| `full_name` | `string` | Yes | Repository full name. |
| `status` | `string` | Yes | Derived runtime status (`running`, `partial`, `stopped`, `unknown`). |
| `containers` | `array` | Yes | Array of `{name, state}` objects for the observed containers. |

## notify_secret_conflict
**Publisher:** `reconciler`  
**Subscribers:** `notifier_pushover` (default `notify_*`), `notifier_webhook` (default `notify_*`), `audit`  
**Description:** Signals that multiple secret providers returned the same secret key and one was skipped.  
**Note:** the current registration schema expects stack identity fields, but the emitted payload currently includes only the three fields listed below.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `key` | `string` | Yes | Secret key. |
| `winner` | `string` | Yes | Plugin that won precedence. |
| `skipped` | `string` | Yes | Plugin that was skipped. |

## notify_compose_env_persistence_risk
**Publisher:** `reconciler`  
**Subscribers:** `notifier_pushover` (default `notify_*`), `notifier_webhook` (default `notify_*`), `audit`  
**Description:** Warns that forwarded compose env is referenced in ways that may not persist across container recreation.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `owner` | `string` | Yes | Repository owner. |
| `repo` | `string` | Yes | Repository name. |
| `full_name` | `string` | Yes | Repository full name. |
| `services` | `array` | Yes | Affected compose service names. |
| `keys` | `array` | Yes | Forwarded env keys involved in risky references. |
| `risk_count` | `int` | Yes | Number of grouped risk findings. |
| `findings` | `array` | Yes | Per-service risk details. |

## webhook_received
**Publisher:** `webhook_trigger`  
**Subscribers:** `audit`  
**Description:** Records receipt of an incoming reconciliation webhook before any downstream work.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `client_ip` | `string` | No | Caller remote address. |
| `method` | `string` | No | HTTP method used for the webhook request. |
| `user_agent` | `string` | No | Caller user agent string. |

## image_refresh_scheduled
**Publisher:** `image_refresh`  
**Subscribers:** `audit`  
**Description:** A new image-refresh retry cycle was scheduled for a stack.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `owner` | `string` | Yes | Repository owner. |
| `repo` | `string` | Yes | Repository name. |
| `full_name` | `string` | Yes | Full stack name. |
| `stack_path` | `string` | Yes | Absolute local stack path. |
| `old_commit` | `string` | Yes | Previous deployed commit. |
| `new_commit` | `string` | Yes | New deployed commit. |
| `attempt` | `int` | Yes | Attempt number in the retry cycle. |
| `retry_delay` | `string` | Yes | Delay before the attempt runs. |
| `message` | `string` | No | Outcome or detail message. |

## image_refresh_retrying
**Publisher:** `image_refresh`  
**Subscribers:** `audit`  
**Description:** Another delayed image-refresh attempt was scheduled or started.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `owner` | `string` | Yes | Repository owner. |
| `repo` | `string` | Yes | Repository name. |
| `full_name` | `string` | Yes | Full stack name. |
| `stack_path` | `string` | Yes | Absolute local stack path. |
| `old_commit` | `string` | Yes | Previous deployed commit. |
| `new_commit` | `string` | Yes | New deployed commit. |
| `attempt` | `int` | Yes | Attempt number in the retry cycle. |
| `retry_delay` | `string` | Yes | Delay before the attempt runs. |
| `message` | `string` | No | Outcome or detail message. |

## image_refresh_no_update
**Publisher:** `image_refresh`  
**Subscribers:** `audit`  
**Description:** `docker compose pull` completed but image identities did not change.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `owner` | `string` | Yes | Repository owner. |
| `repo` | `string` | Yes | Repository name. |
| `full_name` | `string` | Yes | Full stack name. |
| `stack_path` | `string` | Yes | Absolute local stack path. |
| `old_commit` | `string` | Yes | Previous deployed commit. |
| `new_commit` | `string` | Yes | New deployed commit. |
| `attempt` | `int` | Yes | Attempt number in the retry cycle. |
| `retry_delay` | `string` | Yes | Delay before the attempt runs. |
| `message` | `string` | No | Outcome or detail message. |

## image_refresh_update_found
**Publisher:** `image_refresh`  
**Subscribers:** `audit`  
**Description:** Image pull detected updated image identities for the stack.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `owner` | `string` | Yes | Repository owner. |
| `repo` | `string` | Yes | Repository name. |
| `full_name` | `string` | Yes | Full stack name. |
| `stack_path` | `string` | Yes | Absolute local stack path. |
| `old_commit` | `string` | Yes | Previous deployed commit. |
| `new_commit` | `string` | Yes | New deployed commit. |
| `attempt` | `int` | Yes | Attempt number in the retry cycle. |
| `retry_delay` | `string` | Yes | Delay before the attempt runs. |
| `message` | `string` | No | Outcome or detail message. |

## image_refresh_restarting
**Publisher:** `image_refresh`  
**Subscribers:** `audit`  
**Description:** The plugin is applying refreshed images with `docker compose up -d --remove-orphans`.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `owner` | `string` | Yes | Repository owner. |
| `repo` | `string` | Yes | Repository name. |
| `full_name` | `string` | Yes | Full stack name. |
| `stack_path` | `string` | Yes | Absolute local stack path. |
| `old_commit` | `string` | Yes | Previous deployed commit. |
| `new_commit` | `string` | Yes | New deployed commit. |
| `attempt` | `int` | Yes | Attempt number in the retry cycle. |
| `retry_delay` | `string` | Yes | Delay before the attempt runs. |
| `message` | `string` | No | Outcome or detail message. |

## image_refresh_succeeded
**Publisher:** `image_refresh`  
**Subscribers:** `audit`  
**Description:** The image-refresh workflow completed successfully.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `owner` | `string` | Yes | Repository owner. |
| `repo` | `string` | Yes | Repository name. |
| `full_name` | `string` | Yes | Full stack name. |
| `stack_path` | `string` | Yes | Absolute local stack path. |
| `old_commit` | `string` | Yes | Previous deployed commit. |
| `new_commit` | `string` | Yes | New deployed commit. |
| `attempt` | `int` | Yes | Attempt number in the retry cycle. |
| `retry_delay` | `string` | Yes | Delay before the attempt runs. |
| `message` | `string` | No | Outcome or detail message. |

## image_refresh_failed
**Publisher:** `image_refresh`  
**Subscribers:** `audit`  
**Description:** A pull, preflight, or restart attempt failed during image refresh.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `owner` | `string` | Yes | Repository owner. |
| `repo` | `string` | Yes | Repository name. |
| `full_name` | `string` | Yes | Full stack name. |
| `stack_path` | `string` | Yes | Absolute local stack path. |
| `old_commit` | `string` | Yes | Previous deployed commit. |
| `new_commit` | `string` | Yes | New deployed commit. |
| `attempt` | `int` | Yes | Attempt number in the retry cycle. |
| `retry_delay` | `string` | Yes | Delay before the attempt runs. |
| `message` | `string` | No | Outcome or error detail. |

## image_refresh_exhausted
**Publisher:** `image_refresh`  
**Subscribers:** `audit`  
**Description:** The image-refresh retry budget was exhausted without success.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `owner` | `string` | Yes | Repository owner. |
| `repo` | `string` | Yes | Repository name. |
| `full_name` | `string` | Yes | Full stack name. |
| `stack_path` | `string` | Yes | Absolute local stack path. |
| `old_commit` | `string` | Yes | Previous deployed commit. |
| `new_commit` | `string` | Yes | New deployed commit. |
| `attempt` | `int` | Yes | Attempt number in the retry cycle. |
| `retry_delay` | `string` | Yes | Delay before the attempt runs. |
| `message` | `string` | No | Outcome or detail message. |

## image_refresh_superseded
**Publisher:** `image_refresh`  
**Subscribers:** `audit`  
**Description:** A newer commit replaced an in-flight retry cycle.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `owner` | `string` | Yes | Repository owner. |
| `repo` | `string` | Yes | Repository name. |
| `full_name` | `string` | Yes | Full stack name. |
| `stack_path` | `string` | Yes | Absolute local stack path. |
| `old_commit` | `string` | Yes | Previous deployed commit. |
| `new_commit` | `string` | Yes | New deployed commit. |
| `attempt` | `int` | Yes | Attempt number in the retry cycle. |
| `retry_delay` | `string` | Yes | Delay before the attempt runs. |
| `message` | `string` | No | Outcome or detail message. |
## Fleet and GitHub Actions events

The fleet and Actions plugins publish `fleet_agent_enrolled`, `fleet_agent_revoked`, `fleet_agent_heartbeat`, `fleet_task_requested`, `fleet_task_completed`, `github_workflow_run`, `github_workflow_job`, `github_actions_manifest_invalid`, and `github_actions_runner_requested`.

Manifest-declared workflow runs also publish the normalized `execution` event with stage `actions`, execution ID `gha:<run-id>:<attempt>`, exact commit SHA, workflow path, conclusion, trigger, and GitHub URL. Audit and notification plugins consume these through their existing wildcard/execution subscriptions.
