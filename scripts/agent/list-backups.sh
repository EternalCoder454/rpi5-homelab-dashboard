#!/usr/bin/env bash
# List the backups currently stored on the SD backup partition. Read-only.
set -euo pipefail
DIR="${BACKUP_DIR:-/mnt/backups}"
[ -d "$DIR" ] || { echo "$DIR is not mounted. Run the SD setup first." >&2; exit 1; }
echo "Backups in $DIR:"
if ls "$DIR"/*.tar.gz >/dev/null 2>&1; then
  ls -lht "$DIR"/*.tar.gz | awk '{print "- "$9"  ("$5", "$6" "$7" "$8")"}'
else
  echo "(none yet)"
fi
echo
df -h "$DIR" | awk 'NR==2 {print $4" free of "$2" ("$5" used)"}'
