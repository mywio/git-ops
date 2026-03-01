# notifier_webhook

Sends notification events to a generic webhook endpoint.

Capabilities: `NOTIFIER`

Config section: `webhook`  
Keys: `url`, `subscribe`

Behavior:
- If `url` is missing, the plugin logs a warning and disables itself.
- Uses the registry-provided HTTP client.
- Subscribes to event patterns from `subscribe` (e.g., `notify_*`, `deploy_*`).
- If `subscribe` is omitted, defaults to `notify_*`. If `subscribe` is empty, no events are subscribed.

Payload fields:
`event_type`, `source`, `repo`, `message`, `details`
