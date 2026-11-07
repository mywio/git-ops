# GitHub Actions runner fleet

GHOps can run as a central controller while outbound-connected agents install and supervise persistent GitHub Actions runners on Linux servers.

## Security boundary

A persistent self-hosted runner executes repository workflow code on the server. Docker group membership is effectively root access. Use this feature only with trusted private repositories, restricted organization runner groups, protected default branches, environment approvals, and least-privilege workflow tokens.

Reusable GitHub App keys and PATs stay on the controller. Agents receive only one-hour, task-scoped registration or removal tokens over mTLS. The controller never sends arbitrary shell commands.

## Controller setup

1. Create a server TLS certificate for the dedicated agent listener and a separate CA used to sign agent client certificates.
2. Configure `fleet_controller` with the listener, public URL, CA files, state database, and pools.
3. Configure `github_actions.connections`. Every owner must map to exactly one named connection.
4. Configure `ui.operator_users` and/or `ui.operator_groups`; mutations fail closed without an operator match.
5. Enable the `fleet_controller`, `github_actions`, `webhook_trigger`, and `ui` plugins.
6. Start with `core.dry_run: true` and review the Runner Setup page.

A GitHub App needs Actions read/write, Contents read, Metadata read, repository Administration write for repository runners, and organization Self-hosted runners write for organization runners. PATs need equivalent fine-grained permissions. Classic PATs generally require `repo` and `admin:org` for organization runners.

Configure `push`, `workflow_run`, `workflow_job`, and `ping` webhooks at `/webhooks/github`. GitHub App secrets and manually configured PAT-connection webhook secrets are read from their connection secret files.

Organization runner groups and selected-repository access are intentionally configured in GitHub. GHOps can use an existing group but does not change organization policy.

See `examples/config.yaml` for the full schema.

## Agent setup

Build or install `git-ops-agent` for Linux amd64 or arm64. Run it as a root-owned systemd service with:

The release installer downloads the architecture-specific binary, verifies `SHA256SUMS`, writes the systemd unit, and starts the service. Release binaries and checksums also carry GitHub build-provenance attestations.

```sh
curl --fail --location --output install-agent.sh \
  https://github.com/mywio/git-ops/releases/download/vX.Y.Z/install-agent.sh
chmod 0755 install-agent.sh
GHOPS_AGENT_ENROLLMENT_TOKEN='<one-time-token>' sudo -E ./install-agent.sh \
  vX.Y.Z https://ghops.example.com:8443 server-1 ./controller-server-ca.pem server-1
```

For a manual install, use this strict agent configuration:


```yaml
controller_url: "https://ghops.example.com:8443"
pool: "server-1"
node_name: "server-1"
state_dir: "/var/lib/git-ops-agent"
runner_root: "/opt/actions-runner"
work_root: "/var/lib/git-ops-runner"
server_ca_file: "/etc/git-ops-agent/controller-server-ca.pem"
heartbeat_interval: "15s"
max_runners: 4
allowed_supplementary_groups: ["docker"]
```

From **Agents & Runners**, create a one-time enrollment token for the configured pool. Supply it only for the first start:

```sh
GHOPS_AGENT_ENROLLMENT_TOKEN='<one-time-token>' systemctl start git-ops-agent
```

After enrollment, delete `/etc/git-ops-agent/enrollment.env` (if the installer created it) and restart the service. The generated agent key, certificate, controller CA, and agent ID are stored with mode 0600 beneath the state directory. Agents initiate all connections; no inbound agent port is needed.

## Repository setup

The repository must be private, owned by a configured connection, and have a topic mapped to an agent pool. Add:

```yaml
version: 1

deployments:
  - id: production
    name: Production
    workflow: .github/workflows/deploy.yml
    environment: production
    runner_labels: [self-hosted, ghops, homelab-server-1]
```

The manifest is strict. Unknown fields, duplicate IDs, invalid workflow paths, missing labels, and unavailable pool labels block deployment.

The workflow owns triggers, verification, and rollback:

```yaml
name: Production
on:
  push:
    branches: [main]
  workflow_dispatch: {}

jobs:
  deploy:
    runs-on: [self-hosted, ghops, homelab-server-1]
    environment: production
    permissions:
      contents: read
    steps:
      - uses: actions/checkout@v4
      - run: make deploy
      - run: make verify
```

Once `.ghops/actions.yaml` exists, GHOps skips Compose and local-deploy reconciliation for that repository. Having both `.ghops/actions.yaml` and `.ghops/deploy.yaml` is a blocking configuration error.

## Operations and recovery

The UI supports enrollment, runner install/start/stop/restart/drain/update/remove, workflow dispatch on the default branch, cancellation, and reruns. Removal and cancellation require confirmation.

Agent and GitHub state are persisted in SQLite with WAL mode. Back up the fleet database, Actions database, and agent CA together. Revoking an agent immediately rejects its certificate. An offline runner can be removed from GitHub manually if the host cannot redeem a removal token.

GitHub provides status while jobs run and downloadable logs after completion; GHOps links to GitHub for live log viewing.
