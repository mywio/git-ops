# env_forwarder

Forwards allowlisted environment variables into the `docker compose` execution environment.

Capabilities: `secrets`

Config section: `env_forwarder`  
Keys:
- `keys`: list of exact environment variable names
- `prefixes`: list of prefixes to include (e.g., `APP_`)

Behavior:
- Only affects the `docker compose` process environment.
- Snapshots allowlisted values to `TARGET_DIR/.git-ops/env_forwarder_snapshot.json`
  so restarts can reuse the last known values when the new server process starts
  without the original environment.
- Missing keys are skipped with a warning.
- If multiple secret plugins return the same key, the first plugin wins and a
  `notify_secret_conflict` event is emitted by the reconciler.

Execute:
- Action: `get_secrets`
- Params: `owner`, `repo` (currently unused)
- Returns: `map[string]string`
- Action: `get_stats`
- Returns: forwarding stats including per-prefix match/forward counts
