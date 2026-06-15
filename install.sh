#!/usr/bin/env bash
#
# Installer for the Raspberry Pi 5 Homelab Dashboard.
#
#   sudo ./install.sh
#
# Environment overrides:
#   INSTALL_DIR=/opt/homelab-dashboard   where to install
#   PORT=8080                            listen port
#   ENABLE_TLS=yes|no                    serve HTTPS (default: prompt, yes)
#   AUTH_USER=... AUTH_PASSWORD=...      non-interactive credentials
#   SKIP_SUDOERS=1                       don't add the sudoers rule
#
set -euo pipefail

REPO_SLUG="EternalCoder454/rpi5-homelab-dashboard"
APP="homelab-dashboard"
INSTALL_DIR="${INSTALL_DIR:-/opt/$APP}"
PORT="${PORT:-8080}"
SERVICE_FILE="/etc/systemd/system/$APP.service"
SUDOERS_FILE="/etc/sudoers.d/$APP"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

say()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[!]\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31m[x]\033[0m %s\n' "$*" >&2; exit 1; }

# --- must run as root; remember the real (non-root) user for the service ----
if [ "$(id -u)" -ne 0 ]; then
  exec sudo -E bash "$0" "$@"
fi
RUN_USER="${SUDO_USER:-root}"
[ "$RUN_USER" = "root" ] && warn "Running the service as root. For better isolation, run this script via 'sudo' from a normal user."

# --- detect architecture ----------------------------------------------------
case "$(uname -m)" in
  aarch64|arm64) ARCH=arm64 ;;
  x86_64|amd64)  ARCH=amd64 ;;
  *) die "Unsupported architecture: $(uname -m)" ;;
esac
say "Installing $APP ($ARCH) to $INSTALL_DIR, service user '$RUN_USER'."

# --- dependencies -----------------------------------------------------------
say "Installing dependencies..."
apt-get update -qq
apt-get install -y -qq arp-scan openssl curl ca-certificates >/dev/null

# --- obtain the binary ------------------------------------------------------
STAGE="$(mktemp -d)"; trap 'rm -rf "$STAGE"' EXIT
PREBUILT="$SCRIPT_DIR/$APP-linux-$ARCH"
if [ -f "$PREBUILT" ]; then
  say "Using bundled binary $PREBUILT"
  install -m 0755 "$PREBUILT" "$STAGE/$APP"
elif command -v go >/dev/null 2>&1; then
  say "Go found ($(go version | awk '{print $3}')) — building from source..."
  ( cd "$SCRIPT_DIR" && CGO_ENABLED=0 go build -ldflags="-s -w" -o "$STAGE/$APP" . )
else
  say "No Go toolchain — downloading the prebuilt release binary..."
  URL="https://github.com/$REPO_SLUG/releases/latest/download/$APP-linux-$ARCH"
  curl -fsSL "$URL" -o "$STAGE/$APP" || die "Download failed ($URL). Install Go and re-run to build from source."
  chmod 0755 "$STAGE/$APP"
fi

# --- lay down files ---------------------------------------------------------
say "Installing files..."
install -d "$INSTALL_DIR"
install -m 0755 "$STAGE/$APP" "$INSTALL_DIR/$APP"
[ -d "$SCRIPT_DIR/static" ] || die "static/ not found next to install.sh — clone the full repository."
rsync -a --delete "$SCRIPT_DIR/static/" "$INSTALL_DIR/static/"
install -m 0644 "$SCRIPT_DIR/config.example.yaml" "$INSTALL_DIR/config.example.yaml"

# --- configuration (preserved on re-install) --------------------------------
if [ -f "$INSTALL_DIR/config.yaml" ]; then
  say "Existing config.yaml found — keeping it."
else
  say "Creating configuration..."
  AUTH_USER="${AUTH_USER:-}"; AUTH_PASSWORD="${AUTH_PASSWORD:-}"
  if [ -z "$AUTH_USER" ]; then
    read -rp "Admin username [admin]: " AUTH_USER </dev/tty || true
    AUTH_USER="${AUTH_USER:-admin}"
  fi
  while [ -z "$AUTH_PASSWORD" ]; do
    read -rsp "Admin password: " AUTH_PASSWORD </dev/tty; echo >/dev/tty
    read -rsp "Confirm password: " _confirm </dev/tty; echo >/dev/tty
    [ "$AUTH_PASSWORD" = "$_confirm" ] || { warn "Passwords did not match."; AUTH_PASSWORD=""; }
  done
  HASH="$("$INSTALL_DIR/$APP" -hashpw "$AUTH_PASSWORD")"

  ENABLE_TLS="${ENABLE_TLS:-}"
  if [ -z "$ENABLE_TLS" ]; then
    read -rp "Serve over HTTPS with a self-signed cert? [Y/n]: " _yn </dev/tty || true
    case "${_yn:-Y}" in [Nn]*) ENABLE_TLS=no ;; *) ENABLE_TLS=yes ;; esac
  fi

  if [ "$ENABLE_TLS" = "yes" ]; then
    HN="$(hostname)"
    say "Generating self-signed TLS certificate for $HN.local ..."
    openssl req -x509 -newkey rsa:2048 -nodes -days 3650 \
      -keyout "$INSTALL_DIR/key.pem" -out "$INSTALL_DIR/cert.pem" \
      -subj "/CN=$HN.local" \
      -addext "subjectAltName=DNS:$HN,DNS:$HN.local,DNS:localhost,IP:127.0.0.1" >/dev/null 2>&1
    TLS_CERT="cert.pem"; TLS_KEY="key.pem"
  else
    TLS_CERT=""; TLS_KEY=""
  fi

  cat > "$INSTALL_DIR/config.yaml" <<EOF
port: $PORT
upload_dir: ./uploads
exec_allowlist: ["127.0.0.1", "::1", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"]
shell: /bin/bash
auth_user: $AUTH_USER
auth_password_hash: "$HASH"
tls_cert: $TLS_CERT
tls_key: $TLS_KEY
EOF
fi

install -d "$INSTALL_DIR/uploads"
chown -R "$RUN_USER":"$RUN_USER" "$INSTALL_DIR"

# --- sudoers rule for the privileged features -------------------------------
if [ "${SKIP_SUDOERS:-0}" = "1" ]; then
  warn "SKIP_SUDOERS set — Services/System/Network/Filesystem features may not work."
elif [ "$RUN_USER" != "root" ]; then
  say "Adding scoped sudoers rule for '$RUN_USER'..."
  CMDS=""
  for b in systemctl apt-get arp-scan find head cp; do
    p="$(command -v "$b" 2>/dev/null || true)"
    [ -n "$p" ] && CMDS="${CMDS:+$CMDS, }$p"
  done
  TMP_SUDO="$(mktemp)"
  printf '%s ALL=(root) NOPASSWD: %s\n' "$RUN_USER" "$CMDS" > "$TMP_SUDO"
  if visudo -cf "$TMP_SUDO" >/dev/null 2>&1; then
    install -m 0440 "$TMP_SUDO" "$SUDOERS_FILE"
  else
    warn "Generated sudoers rule failed validation; skipping."
  fi
  rm -f "$TMP_SUDO"
fi

# --- systemd service --------------------------------------------------------
say "Installing systemd service..."
sed -e "s|__USER__|$RUN_USER|g" -e "s|__INSTALL_DIR__|$INSTALL_DIR|g" \
  "$SCRIPT_DIR/$APP.service" > "$SERVICE_FILE"
systemctl daemon-reload
systemctl enable --now "$APP" >/dev/null 2>&1
systemctl restart "$APP"

sleep 1
if systemctl is-active --quiet "$APP"; then
  SCHEME="http"; [ -n "${TLS_CERT:-}" ] && SCHEME="https"
  [ -f "$INSTALL_DIR/config.yaml" ] && grep -q '^tls_cert: .\+' "$INSTALL_DIR/config.yaml" && SCHEME="https"
  say "Done. The dashboard is running."
  printf '\n    \033[1;32m%s://%s.local:%s\033[0m\n\n' "$SCHEME" "$(hostname)" "$PORT"
  echo "    Logs:    journalctl -u $APP -f"
  echo "    Restart: sudo systemctl restart $APP"
else
  die "Service failed to start. Check: journalctl -u $APP -e"
fi
