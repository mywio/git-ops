# compose_refresh

Manual Docker Compose image refresh plugin. It runs `docker compose pull` for a managed stack and only runs `docker compose up -d --remove-orphans` when local image identities change after the pull.

Capabilities: `refresh_stack_images`, `system_info`

Config section: `compose_refresh`

## Config

```yaml
compose_refresh:
  enabled: true
  target_dir: ./stacks
```

If `target_dir` is omitted, the plugin uses `core.target_dir`, then falls back to `./stacks`.

## Actions

| Action | Description |
| :--- | :--- |
| `refresh_stack_images` | Pulls compose images for `owner`/`repo` and restarts the stack if image identities changed |
| `system_info` | Returns enabled state and target directory |

## Events

| Event | Description |
| :--- | :--- |
| `compose_refresh_started` | Manual image refresh started |
| `compose_refresh_no_update` | Pull completed but image identities did not change |
| `compose_refresh_succeeded` | Pull updated one or more images and compose up completed |
| `compose_refresh_failed` | Pull, image inspection, or compose up failed |
