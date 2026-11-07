# Fleet controller plugin

Provides the central, durable controller for outbound-mTLS GHOps agents.

Capabilities: `list_agents`, `manage_agents`.

The plugin uses a dedicated TLS listener and SQLite state. Enrollment tokens are one-time and pool-scoped. Agent commands are leased, idempotent typed `runner.*` tasks; arbitrary command execution is not supported.

See [the runner fleet guide](../../docs/github-actions-runners.md).
