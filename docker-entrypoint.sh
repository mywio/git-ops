#!/bin/sh
set -eu

SERVICE_USER="${SERVICE_USER:-git-ops}"
STATE_DIR="${STATE_DIR:-/var/lib/git-ops}"
DOCKER_SOCK="${DOCKER_SOCK:-/var/run/docker.sock}"

ensure_docker_group_membership() {
    if [ ! -S "$DOCKER_SOCK" ]; then
        return 0
    fi

    sock_gid="$(stat -c '%g' "$DOCKER_SOCK")"
    if [ "$sock_gid" = "0" ]; then
        return 0
    fi

    existing_group="$(getent group "$sock_gid" | cut -d: -f1 || true)"
    if [ -z "$existing_group" ]; then
        existing_group="docker-host"
        groupadd --gid "$sock_gid" "$existing_group"
    fi

    if id -nG "$SERVICE_USER" | tr ' ' '\n' | grep -Fx "$existing_group" >/dev/null 2>&1; then
        return 0
    fi

    usermod -aG "$existing_group" "$SERVICE_USER"
}

install -d -m 0750 -o "$SERVICE_USER" -g "$SERVICE_USER" "$STATE_DIR"
ensure_docker_group_membership

exec gosu "$SERVICE_USER" "$@"
