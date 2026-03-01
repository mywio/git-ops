# notifier_pushover

Sends notification events to the Pushover API.

Capabilities: `NOTIFIER`

Config section: `pushover`  
Keys: `token`, `user`, `subscribe`

Behavior:
- If `token` or `user` is missing, the plugin logs a warning and disables itself.
- Subscribes to event patterns from `subscribe` (e.g., `notify_*`, `deploy_*`).
- If `subscribe` is omitted, defaults to `notify_*`. If `subscribe` is empty, no events are subscribed.
- Uses the registry-provided HTTP client.
