# webhook_trigger

Exposes an HTTP endpoint to trigger reconciliation.

Capabilities: `trigger`

Config section: `webhook_trigger`  
Keys: `port`, `token`, `rate_limit`  
Default: `port` falls back to `8082`

Endpoint:
- `POST /reconcile`

Auth:
- If `token` is set, the request must include `Authorization: Bearer <token>`.
- If `rate_limit` is set to a duration like `30s` or `5m`, the endpoint enforces
  a minimum interval between accepted reconciliations.
- `rate_limit=0s` disables throttling.
- Requests inside the interval return `429 Too Many Requests` with `Retry-After`.

Behavior:
- Publishes `reconcile_now` and `webhook_received` events.
