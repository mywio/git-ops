# notifier_pushover

Sends notification events to the Pushover API.

Capabilities: `NOTIFIER`

Config section: `pushover`  
Keys: `token`, `user`

Behavior:
- If `token` or `user` is missing, the plugin logs a warning and disables itself.
- Subscribes to `notify_*` events.
- Uses the registry-provided HTTP client.
