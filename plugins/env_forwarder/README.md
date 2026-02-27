# env_forwarder

Forwards allowlisted environment variables into the `docker compose` execution environment.

Capabilities: `secrets`

Config section: `env_forwarder`  
Keys:
- `keys`: list of exact environment variable names
- `prefixes`: list of prefixes to include (e.g., `APP_`)

Behavior:
- Only affects the `docker compose` process environment.
- Missing keys are skipped with a warning and emit `notify_env_forwarder_missing`.
- If multiple secret plugins return the same key, the first plugin wins and a
  `notify_secret_conflict` event is emitted by the reconciler.
