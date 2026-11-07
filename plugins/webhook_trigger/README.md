# webhook_trigger

Exposes an HTTP endpoint to trigger reconciliation.

Capabilities: `trigger`

Config section: `webhook_trigger`  
Keys: `port`, `token`, `github_secret`, `rate_limit`
Default: `port` falls back to `8082`

Endpoints:
- `POST /reconcile`
- `POST /webhooks/github`

Auth:
- If `token` is set, the request must include `Authorization: Bearer <token>`.
- If `rate_limit` is set to a duration like `30s` or `5m`, the endpoint enforces
  a minimum interval between accepted reconciliations.
- `rate_limit=0s` disables throttling.
- Requests inside the interval return `429 Too Many Requests` with `Retry-After`.

Behavior:
- Publishes `reconcile_now` and `webhook_received` events.
- The GitHub endpoint requires `github_secret` and validates
  `X-Hub-Signature-256` before parsing or processing the payload.
- Only `push` events for the repository-reported default branch are queued.
  Deleted refs, other branches, and duplicate `X-GitHub-Delivery` IDs are
  acknowledged with `202` and ignored.
- Accepted pushes publish `repository_revision_requested` with the exact
  `after` SHA, ref, owner, repository, delivery ID, and `github_push` trigger.

The bearer token continues to protect only the legacy `/reconcile` endpoint;
GitHub deliveries use HMAC authentication instead.
