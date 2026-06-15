#!/usr/bin/env bash
#
# Uninstaller for the Raspberry Pi 5 Homelab Dashboard.
#
#   sudo ./uninstall.sh
#
set -euo pipefail

APP="homelab-dashboard"
INSTALL_DIR="${INSTALL_DIR:-/opt/$APP}"
SERVICE_FILE="/etc/systemd/system/$APP.service"
SUDOERS_FILE="/etc/sudoers.d/$APP"

say()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }

if [ "$(id -u)" -ne 0 ]; then
  exec sudo bash "$0" "$@"
fi

say "Stopping and disabling the service..."
systemctl disable --now "$APP" >/dev/null 2>&1 || true
rm -f "$SERVICE_FILE"
systemctl daemon-reload

say "Removing sudoers rule..."
rm -f "$SUDOERS_FILE"

if [ -d "$INSTALL_DIR" ]; then
  read -rp "Remove $INSTALL_DIR (config, certs, uploads, state)? [y/N]: " yn </dev/tty || true
  case "${yn:-N}" in
    [Yy]*) rm -rf "$INSTALL_DIR"; say "Removed $INSTALL_DIR." ;;
    *) say "Kept $INSTALL_DIR." ;;
  esac
fi

say "Uninstalled."
