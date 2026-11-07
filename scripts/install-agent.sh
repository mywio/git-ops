#!/bin/sh
set -eu

usage() {
  echo "usage: $0 <version> <controller-url> <pool> <server-ca-file> [node-name]" >&2
  echo "set GHOPS_AGENT_ENROLLMENT_TOKEN for first enrollment" >&2
  exit 2
}

[ "$#" -ge 4 ] && [ "$#" -le 5 ] || usage
[ "$(id -u)" -eq 0 ] || { echo "run this installer as root" >&2; exit 1; }

version=$1
controller_url=$2
pool=$3
server_ca_source=$4
node_name=${5:-$(hostname)}
repository=${GHOPS_RELEASE_REPOSITORY:-mywio/git-ops}

case "$(uname -m)" in
  x86_64|amd64) architecture=amd64 ;;
  aarch64|arm64) architecture=arm64 ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

for command in curl sha256sum install systemctl; do
  command -v "$command" >/dev/null 2>&1 || { echo "required command not found: $command" >&2; exit 1; }
done
[ -r "$server_ca_source" ] || { echo "server CA is not readable: $server_ca_source" >&2; exit 1; }

asset="git-ops-agent-linux-$architecture"
base_url="https://github.com/$repository/releases/download/$version"
temporary=$(mktemp -d)
trap 'rm -rf "$temporary"' EXIT HUP INT TERM

curl --fail --location --proto '=https' --tlsv1.2 --output "$temporary/$asset" "$base_url/$asset"
curl --fail --location --proto '=https' --tlsv1.2 --output "$temporary/SHA256SUMS" "$base_url/SHA256SUMS"
expected=$(awk -v name="$asset" '$2 == "dist/" name || $2 == name { print $1; exit }' "$temporary/SHA256SUMS")
[ -n "$expected" ] || { echo "release checksum does not contain $asset" >&2; exit 1; }
printf '%s  %s\n' "$expected" "$temporary/$asset" | sha256sum --check --status

install -d -m 0750 /etc/git-ops-agent /var/lib/git-ops-agent /var/lib/git-ops-runner /opt/actions-runner
install -m 0755 "$temporary/$asset" /usr/local/bin/git-ops-agent
install -m 0644 "$server_ca_source" /etc/git-ops-agent/controller-server-ca.pem

if [ ! -e /etc/git-ops-agent/config.yaml ]; then
  umask 077
  config_file="$temporary/config.yaml"
  cat > "$config_file" <<EOF
controller_url: "$controller_url"
pool: "$pool"
node_name: "$node_name"
state_dir: "/var/lib/git-ops-agent"
runner_root: "/opt/actions-runner"
work_root: "/var/lib/git-ops-runner"
server_ca_file: "/etc/git-ops-agent/controller-server-ca.pem"
heartbeat_interval: "15s"
max_runners: 4
allowed_supplementary_groups: []
EOF
  install -m 0600 "$config_file" /etc/git-ops-agent/config.yaml
fi

cat > "$temporary/git-ops-agent.service" <<'EOF'
[Unit]
Description=GHOps runner fleet agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/git-ops-agent -config /etc/git-ops-agent/config.yaml
EnvironmentFile=-/etc/git-ops-agent/enrollment.env
Restart=always
RestartSec=5s
UMask=0077

[Install]
WantedBy=multi-user.target
EOF
install -m 0644 "$temporary/git-ops-agent.service" /etc/systemd/system/git-ops-agent.service

if [ -n "${GHOPS_AGENT_ENROLLMENT_TOKEN:-}" ]; then
  umask 077
  printf 'GHOPS_AGENT_ENROLLMENT_TOKEN=%s\n' "$GHOPS_AGENT_ENROLLMENT_TOKEN" > /etc/git-ops-agent/enrollment.env
fi

systemctl daemon-reload
systemctl enable --now git-ops-agent

echo "git-ops-agent $version installed and started"
if [ -e /etc/git-ops-agent/enrollment.env ]; then
  echo "After the agent appears online, delete /etc/git-ops-agent/enrollment.env and restart the service."
fi
