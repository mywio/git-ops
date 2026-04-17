# Migration Guide

## Migrating from Monolith to Plugin Architecture

git-ops has transitioned to a modular plugin system.

### Changes
- Core binary (`git-ops`) no longer contains all functionality.
- Plugins (Secrets, UI, etc.) must be built separately as `.so` files.
- `Makefile` has been introduced to streamline the build process.

### Build Steps
Previously:
```bash
go build -o git-ops main.go
```

Now:
```bash
make build    # Builds core
make plugins  # Builds plugins
```

### Configuration
- New Environment Variable: `PLUGINS_DIR` (defaults to `./plugins`).
- Ensure the `plugins/` directory (or wherever `PLUGINS_DIR` points to) contains the built `.so` files alongside the `git-ops` binary.

## Multi-topic Topic Filter

`TOPIC_FILTER` now accepts a comma-separated list of GitHub topics instead of a single value.

Previously:
```bash
TOPIC_FILTER=homelab-server-1
```

Now:
```bash
TOPIC_FILTER=homelab-server-1,prod
```

YAML configuration remains backward compatible:
- `topic: "homelab-server-1"` still works
- `topic: ["homelab-server-1", "prod"]` is also supported

### Running
```bash
./bin/git-ops
# Ensure bin/plugins exists
```
