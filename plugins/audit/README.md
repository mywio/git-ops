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
| `filter` | map | — | Key/value filter: `type` (event type), `source` (plugin name), `repo` (repo name) |
| `since` | string (RFC3339) or `time.Time` | — | Return only events at or after this time |
| `until` | string (RFC3339) or `time.Time` | — | Return only events at or before this time |

## Example config

```yaml
audit:
  storage: sqlite
  db_path: /var/lib/git-ops/audit.db
  retention_count: 5000
```
