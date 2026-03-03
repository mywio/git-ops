# file_forwarder

Forwards allowlisted host files into the `docker compose` execution context by
materializing temporary runtime files and exporting their paths as environment
variables.

Capabilities: `runtime_files`

Config section: `file_forwarder`

Keys:
- `files`: list of file mappings:
  - `env`: env var name to expose file path to compose (required)
  - `path`: host source file path to read (required)
  - `filename`: output file name for the runtime file (optional; defaults to source basename)
  - `mode`: file mode in octal string (optional; default `0600`)
  - `required`: fail deploy when missing/unreadable (optional; default `true`)

Execute:
- Action: `get_runtime_files`
- Params: `owner`, `repo` (currently unused)
- Returns: `[]core.RuntimeFile`

Notes:
- Files are written to a temp directory only for the deploy execution.
- The reconciler removes generated runtime files after `docker compose` exits.
