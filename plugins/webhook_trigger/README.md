# webhook_trigger

Exposes an HTTP endpoint to trigger reconciliation.

Capabilities: `trigger`

Config section: `webhook_trigger`  
Keys: `port`, `token`  
Default: `port` falls back to `8082`

Endpoint:
- `POST /reconcile`

Auth:
- If `token` is set, the request must include `Authorization: Bearer <token>`.

Behavior:
- Publishes `reconcile_now` and `webhook_received` events.
- Signals `core.TriggerReconcile`.
