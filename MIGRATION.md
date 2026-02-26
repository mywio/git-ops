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

### Running
```bash
./bin/git-ops
# Ensure bin/plugins exists
```
