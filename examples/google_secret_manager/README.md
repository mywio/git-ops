# Google Secret Manager Example

This example shows a simple service that consumes secrets injected by the
`google_secret_manager` plugin.

## Setup
1. Enable the Google Secret Manager plugin in `config.yaml`:
```yaml
google_secret_manager:
  project_id: "my-gcp-project"
```

2. Create secrets in Google Secret Manager and label them:
```
git-ops_owner = "<repo owner>"
git-ops_repo  = "<repo name>"
git-ops_env_key = "SECRET_API_KEY"   # optional; defaults to secret name uppercased
```

3. Add the topic `homelab-server-1` (or your configured topic) to the repo.

## Example compose
See `docker-compose.yml`. It references environment variables that will be
injected by the plugin into the `docker compose` command.
