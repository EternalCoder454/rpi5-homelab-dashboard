#!/usr/bin/env bash
# Manual Argon fan control. Usage: fan.sh <auto|0-100>
#   auto    restore the saved automatic temperature curve
#   0-100   pin the fan to a fixed speed (forced to 100% at >=75C for safety)
# Mirrors the dashboard's fan handler: the speed is expressed as a curve the
# argononed/argon-go daemon applies, so we don't fight it over I2C.
set -euo pipefail

CONF=/etc/argononed.conf
BAK="${HOMELAB_DIR:-$HOME/homelab}/argon-auto-curve.bak"
SAFETY_TEMP=75

arg="${1:-}"
[ -n "$arg" ] || { echo "usage: fan.sh <auto|0-100>" >&2; exit 2; }

if [ "$arg" = "auto" ]; then
  if [ -s "$BAK" ]; then
    sudo -n cp "$BAK" "$CONF"
  else
    printf '#\n# Argon Fan Speed Configuration (CPU)\n#\n55=30\n60=55\n65=100\n' | sudo -n tee "$CONF" >/dev/null
  fi
  echo "Fan set to the automatic temperature curve."
else
  case "$arg" in *[!0-9]*|'') echo "percent must be an integer 0-100" >&2; exit 2;; esac
  [ "$arg" -le 100 ] || { echo "percent must be 0-100" >&2; exit 2; }
  # Preserve the existing auto curve the first time we switch to manual.
  if [ -f "$CONF" ] && ! grep -q '# dashboard-manual' "$CONF"; then
    sudo -n cp "$CONF" "$BAK" 2>/dev/null || true
  fi
  printf '# Argon Fan Speed Configuration (CPU)\n# dashboard-manual\n0=%s\n%s=100\n' "$arg" "$SAFETY_TEMP" | sudo -n tee "$CONF" >/dev/null
  echo "Fan pinned to ${arg}% (auto-forced to 100% at >=${SAFETY_TEMP}C for safety)."
fi

# argon-go reads the conf live; the python argononed (fallback) needs a nudge.
sudo -n systemctl reset-failed argononed 2>/dev/null || true
sudo -n systemctl try-restart argononed 2>/dev/null || true
echo "Done."
