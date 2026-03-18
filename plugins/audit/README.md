# audit

Subscribes to all internal events and maintains a queryable audit log.

Capabilities: `audit`

Config section: `audit`

| Key | Description | Default |
| :--- | :--- | :--- |
| `storage` | Storage backend: `memory` or `sqlite` | `memory` |
| `db_path` | SQLite database file path (only used when `storage: sqlite`) | `data/audit.db` |
| `retention_count` | Maximum number of events to retain (0 = unlimited) | `1000` |

## Execute actions

| Action | Parameters | Description |
| :--- | :--- | :--- |
| `last_events` | `limit`, `offset`, `order`, `filter` | Returns stored events |

### `last_events` parameters

| Parameter | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `limit` | int | `100` | Maximum number of events to return |
| `offset` | int | `0` | Number of events to skip |
| `order` | string | `desc` | Sort order: `asc` or `desc` |
| `filter` | map | — | Key/value pairs to filter events by |

## Example config

```yaml
audit:
  storage: sqlite
  db_path: /var/lib/git-ops/audit.db
  retention_count: 5000
```
