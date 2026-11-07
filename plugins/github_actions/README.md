# GitHub Actions plugin

Discovers private topic-eligible repositories, validates `.ghops/actions.yaml`, presents workflows and runs, projects declared workflows into deployments, and brokers persistent runner lifecycle through `fleet_controller`.

Named GitHub App and PAT connections are resolved by owner. Credentials remain on the controller, are redacted from APIs, and short-lived registration/removal tokens are redeemed by the assigned agent task only.

See [the runner fleet guide](../../docs/github-actions-runners.md).
