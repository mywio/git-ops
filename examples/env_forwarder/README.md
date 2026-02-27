# Env Forwarder Example

This example shows a simple service that consumes environment variables
forwarded by the `env_forwarder` plugin.

## Setup
1. Add config:
```yaml
env_forwarder:
  keys:
    - "SECRET_API_KEY"
    - "DB_PASSWORD"
  prefixes:
    - "APP_"
```

2. Ensure the environment variables exist on the host running git-ops:
```
export SECRET_API_KEY="example"
export DB_PASSWORD="example"
export APP_TOKEN="example"
```

3. Add the topic `homelab-server-1` (or your configured topic) to the repo.

## Example compose
See `docker-compose.yml`.
