# image_refresh

Commit-triggered image refresh plugin. It listens for reconciler `stack_commit_changed` events and retries `docker compose pull` for stacks whose git commit advanced without a compose-file change.

Capabilities: `system`

Config section: `image_refresh`

## Behavior

- Global enablement only in phase 1.
- Subscribes only to `stack_commit_changed`.
- Ignores events where `compose_changed=true` because the reconciler already performed the compose deployment.
- For `compose_changed=false`, starts a replaceable per-stack retry cycle.
- Each attempt runs `docker compose pull`.
- Only when local image identities change does it run `docker compose up -d --remove-orphans`.
- A newer commit event supersedes any incomplete retry cycle for the same stack.
- Plugin restart drops in-memory pending jobs in phase 1.

## Config

```yaml
image_refresh:
  enabled: true
  retry_delays_minutes: [0, 1, 2, 4, 8]
```

`retry_delays_minutes` is authoritative for the retry schedule.

## Events

| Event | Description |
| :--- | :--- |
| `image_refresh_scheduled` | A retry cycle was created for a stack |
| `image_refresh_retrying` | Another delayed attempt was scheduled |
| `image_refresh_no_update` | `docker compose pull` completed but image identities did not change |
| `image_refresh_update_found` | Local image identities changed after pull |
| `image_refresh_restarting` | The plugin is running `docker compose up -d --remove-orphans` |
| `image_refresh_succeeded` | The image refresh completed successfully |
| `image_refresh_failed` | A pull/preflight/restart attempt failed |
| `image_refresh_exhausted` | The retry budget was consumed without success |
| `image_refresh_superseded` | A newer commit replaced an incomplete retry cycle |

## Notes

This plugin is commit-triggered, not registry-polled. It is meant to bridge the delay between a source commit landing and the matching container image becoming available in the registry.
