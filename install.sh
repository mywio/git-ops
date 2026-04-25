#!/bin/sh
set -eu

REPO="mywio/git-ops"
VERSION="${VERSION:-latest}"
PREFIX="${PREFIX:-/usr/local}"
SYSTEMD=0
SERVICE_USER="${SERVICE_USER:-git-ops}"
SERVICE_GROUP="${SERVICE_GROUP:-git-ops}"
STATE_DIR="${STATE_DIR:-/var/lib/git-ops}"

usage() {
    cat <<EOF
Usage: $0 [--prefix PATH] [--version TAG|latest] [--systemd]

Installs git-ops and its plugin bundle from GitHub Releases.

Options:
  --prefix PATH   Install under PATH instead of /usr/local
  --version TAG   Install a specific release tag (default: latest)
  --systemd       Install /etc/systemd/system/git-ops.service

Environment overrides for --systemd:
  SERVICE_USER    Service account name (default: git-ops)
  SERVICE_GROUP   Service group name (default: git-ops)
  STATE_DIR       Working directory for the service (default: /var/lib/git-ops)
EOF
}

need_cmd() {
    command -v "$1" >/dev/null 2>&1 || {
        echo "error: required command not found: $1" >&2
        exit 1
    }
}

resolve_nologin_shell() {
    for shell_path in /usr/sbin/nologin /sbin/nologin /bin/false; do
        if [ -x "$shell_path" ]; then
            printf '%s\n' "$shell_path"
            return
        fi
    done
    echo "error: could not find a nologin shell" >&2
    exit 1
}

ensure_system_group() {
    group_name="$1"
    if getent group "$group_name" >/dev/null 2>&1; then
        return 0
    fi
    groupadd --system "$group_name"
}

ensure_system_user() {
    user_name="$1"
    group_name="$2"
    home_dir="$3"
    if id "$user_name" >/dev/null 2>&1; then
        return 0
    fi
    useradd \
        --system \
        --gid "$group_name" \
        --home-dir "$home_dir" \
        --create-home \
        --shell "$(resolve_nologin_shell)" \
        "$user_name"
}

confirm_overwrite() {
    target="$1"
    if [ ! -e "$target" ]; then
        return 0
    fi
    if [ ! -t 0 ]; then
        echo "error: $target already exists; rerun interactively to confirm overwrite" >&2
        exit 1
    fi
    printf '%s exists. Overwrite? [y/N] ' "$target" >&2
    read answer
    case "$answer" in
        y|Y|yes|YES) ;;
        *) echo "aborted" >&2; exit 1 ;;
    esac
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --prefix)
            [ "$#" -ge 2 ] || { echo "error: --prefix requires a value" >&2; exit 1; }
            PREFIX="$2"
            shift 2
            ;;
        --version)
            [ "$#" -ge 2 ] || { echo "error: --version requires a value" >&2; exit 1; }
            VERSION="$2"
            shift 2
            ;;
        --systemd)
            SYSTEMD=1
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "error: unknown argument: $1" >&2
            usage >&2
            exit 1
            ;;
    esac
done

need_cmd curl
need_cmd tar
need_cmd install
if [ "$SYSTEMD" -eq 1 ]; then
    need_cmd systemctl
    need_cmd getent
    need_cmd groupadd
    need_cmd useradd
fi

resolve_version() {
    if [ "$VERSION" != "latest" ]; then
        case "$VERSION" in
            v*) printf '%s\n' "$VERSION" ;;
            *) printf 'v%s\n' "$VERSION" ;;
        esac
        return
    fi

    api_url="https://api.github.com/repos/$REPO/releases/latest"
    tag="$(curl -fsSL "$api_url" | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
    if [ -z "$tag" ]; then
        echo "error: failed to resolve latest release from $api_url" >&2
        exit 1
    fi
    printf '%s\n' "$tag"
}

TAG="$(resolve_version)"
ARCHIVE="git-ops-linux-amd64.tar.gz"
TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT INT TERM

DOWNLOAD_URL="https://github.com/$REPO/releases/download/$TAG/$ARCHIVE"
if [ "$VERSION" = "latest" ]; then
    DOWNLOAD_URL="https://github.com/$REPO/releases/latest/download/$ARCHIVE"
fi

echo "Downloading $DOWNLOAD_URL"
curl -fsSL "$DOWNLOAD_URL" -o "$TMPDIR/$ARCHIVE"
tar -xzf "$TMPDIR/$ARCHIVE" -C "$TMPDIR"

BIN_DIR="$PREFIX/bin"
LIB_DIR="$PREFIX/lib/git-ops"
PLUGINS_DIR="$LIB_DIR/plugins"

confirm_overwrite "$BIN_DIR/git-ops"
if [ -d "$PLUGINS_DIR" ] && [ "$(find "$PLUGINS_DIR" -mindepth 1 -maxdepth 1 2>/dev/null | wc -l | tr -d ' ')" -gt 0 ]; then
    confirm_overwrite "$PLUGINS_DIR"
fi

mkdir -p "$BIN_DIR" "$PLUGINS_DIR"
install -m 0755 "$TMPDIR/git-ops" "$BIN_DIR/git-ops"
find "$PLUGINS_DIR" -mindepth 1 -maxdepth 1 -type f -name '*.so' -exec rm -f {} \; 2>/dev/null || true
for plugin in "$TMPDIR"/plugins/*.so; do
    install -m 0644 "$plugin" "$PLUGINS_DIR/"
done

if [ "$SYSTEMD" -eq 1 ]; then
    ensure_system_group "$SERVICE_GROUP"
    ensure_system_user "$SERVICE_USER" "$SERVICE_GROUP" "$STATE_DIR"
    install -d -m 0750 -o "$SERVICE_USER" -g "$SERVICE_GROUP" "$STATE_DIR"

    if ! getent group docker >/dev/null 2>&1; then
        echo "warning: docker group does not exist yet; git-ops.service will reference it via SupplementaryGroups=docker" >&2
    fi

    SERVICE_PATH="/etc/systemd/system/git-ops.service"
    confirm_overwrite "$SERVICE_PATH"
    cat > "$SERVICE_PATH" <<EOF
[Unit]
Description=git-ops
After=network.target docker.service
Wants=docker.service

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_GROUP
SupplementaryGroups=docker
WorkingDirectory=$STATE_DIR
Environment=CONFIG_FILE=/etc/git-ops/config.yaml
Environment=PLUGINS_DIR=$PLUGINS_DIR
ExecStart=$BIN_DIR/git-ops
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
UMask=0027

[Install]
WantedBy=multi-user.target
EOF
fi

cat <<EOF
Installed:
  Binary:  $BIN_DIR/git-ops
  Plugins: $PLUGINS_DIR
  Version: $TAG

Next steps:
1. Create /etc/git-ops/config.yaml or export the required env vars.
2. Required settings:
   - GITHUB_TOKEN
   - GITHUB_USERS
   - TOPIC_FILTER
3. Optional but common:
   - TARGET_DIR
   - CORE_HTTP_ADDR
   - PLUGINS_ALLOW
EOF

if [ "$SYSTEMD" -eq 1 ]; then
    cat <<EOF
4. Review /etc/systemd/system/git-ops.service
5. Ensure /etc/git-ops/config.yaml is readable by $SERVICE_USER or group $SERVICE_GROUP.
6. Ensure TARGET_DIR is writable by $SERVICE_USER (or via ACL/group permissions).
7. Then run:
   systemctl daemon-reload
   systemctl enable --now git-ops
EOF
fi
