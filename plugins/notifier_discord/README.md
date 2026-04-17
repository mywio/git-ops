# notifier_discord

Sends notifications to Discord via webhook embeds.

Capabilities: `NOTIFIER`

Config section: `discord`  
Keys:
- `webhook_url`
- `subscribe` (optional, defaults to `notify_*`)

Behavior:
- Uses the shared `core.NotificationPayload` formatter.
- Sends a Discord embed instead of plain text.
- Colors embeds by severity:
  - green for success
  - red for failures/errors
  - yellow for warnings
