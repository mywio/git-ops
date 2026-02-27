# google_secret_manager

Injects secrets from Google Secret Manager into deployments based on repo labels.

Capabilities: `secrets`

Config section: `google_secret_manager`  
Keys: `project_id` (or `project`)

Notes:
- Requires Google Cloud Application Default Credentials.
- Secrets are selected by labels `git-ops_owner` and `git-ops_repo`.
- Optional label `git-ops_env_key` overrides the env var key name. Otherwise the
  secret name (last path segment) is used and uppercased.

Execute:
- Action: `get_secrets`
- Params: `owner`, `repo`
- Returns: map of env var name to secret value
