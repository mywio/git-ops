# File Forwarder Example

This example shows how to forward a host file path into `docker compose` using
the `file_forwarder` plugin.

## Setup
1. Add config:
```yaml
file_forwarder:
  files:
    - env: "TLS_CERT_FILE"
      path: "/etc/git-ops/tls/tls.crt"
      filename: "tls.crt"
      mode: "0600"
      required: true
```

2. Ensure the source file exists on the host running git-ops.

3. Use `${TLS_CERT_FILE}` in your compose file to mount/read the materialized runtime file path.
