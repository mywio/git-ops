# Local Deploy plugin

`local_deploy` deploys an opted-in repository at an exact commit. It is a host-only
plugin: the official GHOps container image does not support it.

## Host requirements

Run GHOps as a systemd service. The service account needs:

- Git credentials for each repository and recursive submodule.
- Docker daemon access and the Docker Compose plugin.
- Every executable named by an opted-in repository recipe.
- Write access to the configured state, cache, and log directories.

Repository recipes are trusted code. Docker access is effectively privileged host
access, so only allow trusted repository owners and protect their default branches.

Enable the plugin together with `reconciler` and `webhook_trigger`:

```yaml
core:
  plugins: [reconciler, local_deploy, webhook_trigger, ui, audit]
  dry_run: true

webhook_trigger:
  github_secret: "replace-with-a-long-random-secret"
  token: "bearer-token-for-the-legacy-reconcile-endpoint"

local_deploy:
  enabled: true
  require_systemd: true
  state_dir: /var/lib/git-ops/local-deploy/state
  cache_dir: /var/lib/git-ops/local-deploy/cache
  log_dir: /var/log/git-ops/local-deploy
  global_concurrency: 1
  checkout_retention: 5
  execution_retention: 100
  successful_retention: 20
  max_log_size: 10MiB
  allowed_environment: [PATH, APP_DEPLOY_TOKEN]
```

Start with `core.dry_run: true`. Dry-run mode authenticates and routes webhooks,
creates the mirror and exact checkout, validates the recipe and required executables,
and does not execute any recipe phase or update the current successful revision.

Set the GitHub webhook URL to `https://your-ghops.example/webhooks/github`, content
type to JSON, the same secret as `webhook_trigger.github_secret`, and select push
events. Only pushes to the default branch are accepted.

## Recipe

Create `.ghops/deploy.yaml`:

```yaml
version: 1

checkout:
  submodules: recursive
  lfs: false

phases:
  build:
    - command: make
      args: [build]
      working_directory: .
      timeout: 30m
  deploy:
    - command: make
      args: [deploy]
      timeout: 10m
  verify:
    - command: make
      args: [verify]
      timeout: 2m
  rollback:
    - command: make
      args: [rollback]
      timeout: 10m

health:
  compose:
    files: [compose.yaml]
    project_name: my-app
    timeout: 2m
    interval: 5s
    allow_completed: []
```

Parsing is strict. Unknown fields, unsupported versions, invalid or over-24-hour
timeouts, empty required phases, and absolute or traversing paths are rejected.
Commands execute directly as an argv vector without an implicit shell. A maintainer
may explicitly choose a shell as the command, but that choice is visible in the
recipe.

The child environment contains only fixed `GHOPS_*` metadata and variables named
in `allowed_environment`. Values of allowed variables are redacted from bounded
durable logs. Git credentials are obtained from the systemd account's normal Git
credential configuration; tokens are never added to command arguments.

## Failure and rollback behavior

Build failures stop without rollback. Deploy, verification, and Compose-health
failures run the previous successful revision's rollback recipe, then re-run that
revision's verification and health checks. A first-deploy failure is critical
because no safe rollback target exists. A rollback failure is also critical.

Manual rollback can select any of the last 20 retained successful revisions in the
web UI. GHOps shows the current and target SHAs plus the stored rollback command
summary and requires explicit confirmation. Older retained revisions are recreated
from the authenticated mirror when their checkout has been cleaned up.

On restart, an active execution is recorded as interrupted and failed. Pending work
is retained and starts safely. Rapid pushes coalesce to the newest pending SHA; if a
new SHA arrives during build, the current command finishes and the superseded
revision never enters deploy.
