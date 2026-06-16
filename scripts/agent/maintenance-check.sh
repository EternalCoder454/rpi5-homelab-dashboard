#!/usr/bin/env bash
# Full maintenance check: take a fresh backup first, then report on the Pi's
# health (temperature, throttling, disk, failed services, pending updates,
# containers). Read-only apart from the backup it creates.
set -uo pipefail

here="$(dirname "$(readlink -f "$0")")"

echo "=== Maintenance Check — $(date '+%Y-%m-%d %H:%M:%S') ==="
echo
echo "## 1. Backup"
if "$here/backup.sh"; then
  echo "Backup step OK."
else
  echo "WARNING: backup step failed (continuing with health check)."
fi
echo
echo "## 2. Health"
echo "Uptime:$(uptime -p | sed 's/^up/ up/')"
echo "Load:  $(cut -d' ' -f1-3 /proc/loadavg)"
temp="$(vcgencmd measure_temp 2>/dev/null | sed 's/temp=//')"
echo "CPU temp: ${temp:-n/a}"
thr="$(vcgencmd get_throttled 2>/dev/null | sed 's/throttled=//')"
if [ -n "$thr" ] && [ "$thr" != "0x0" ]; then
  echo "Throttling: $thr  (non-zero — under-voltage or thermal capping has occurred)"
else
  echo "Throttling: none"
fi
echo
echo "## 3. Disk"
df -h / /mnt/backups /mnt/media 2>/dev/null | awk 'NR==1 || /\/($|mnt)/'
echo
echo "## 4. Failed services"
failed="$(systemctl --failed --no-legend --plain 2>/dev/null | awk '{print "- "$1}')"
[ -n "$failed" ] && echo "$failed" || echo "None"
echo
echo "## 5. Pending updates"
n="$(apt list --upgradable 2>/dev/null | grep -c '/' || true)"
echo "${n:-0} package(s) upgradable"
[ -f /var/run/reboot-required ] && echo "A reboot is pending." || true
echo
echo "## 6. Docker"
docker ps --format '- {{.Names}}: {{.Status}}' 2>/dev/null || echo "Docker not available"
echo
echo "=== Maintenance check complete ==="
