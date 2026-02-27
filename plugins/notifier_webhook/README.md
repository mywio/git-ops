# notifier_webhook

Sends notification events to a generic webhook endpoint.

Capabilities: `NOTIFIER`

Config section: `webhook`  
Keys: `url`

Behavior:
- If `url` is missing, the plugin logs a warning and disables itself.
- Uses the registry-provided HTTP client.

Payload fields:
`event_type`, `source`, `repo`, `message`, `details`
