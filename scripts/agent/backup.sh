#!/usr/bin/env bash
# Create a timestamped backup of the dashboard's state and key system config on
# the SD backup partition (/mnt/backups), keeping the most recent $KEEP archives.
# Bulk data (uploads/media) is deliberately excluded — this backs up the things
# that are painful to recreate, not the things that are already on disk twice.
set -euo pipefail

BACKUP_DIR="${BACKUP_DIR:-/mnt/backups}"
KEEP="${KEEP:-10}"
HOMELAB="${HOMELAB_DIR:-$HOME/homelab}"

if [ ! -d "$BACKUP_DIR" ]; then
  echo "Backup target $BACKUP_DIR is not mounted. Run the SD setup first." >&2
  exit 1
fi

ts="$(date +%Y%m%d-%H%M%S)"
out="$BACKUP_DIR/$(hostname)-$ts.tar.gz"

stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
mkdir -p "$stage/homelab" "$stage/etc"

echo "Collecting dashboard state..."
for f in config.yaml hub.json network.json assistant.json cert.pem key.pem; do
  [ -e "$HOMELAB/$f" ] && cp -a "$HOMELAB/$f" "$stage/homelab/" || true
done

echo "Collecting system config..."
for f in /etc/fstab /etc/argononed.conf /etc/samba/smb.conf; do
  [ -e "$f" ] && cp -a "$f" "$stage/etc/" 2>/dev/null || true
done

echo "Writing archive -> $out"
tar -czf "$out" -C "$stage" .
echo "Backup complete: $out ($(du -h "$out" | cut -f1))"

# Rotate: keep the newest $KEEP, delete the rest.
mapfile -t old < <(ls -1t "$BACKUP_DIR"/*.tar.gz 2>/dev/null | tail -n +"$((KEEP + 1))")
if [ "${#old[@]}" -gt 0 ]; then
  echo "Pruning ${#old[@]} old backup(s) (keeping $KEEP)..."
  rm -f "${old[@]}"
fi
echo "Backups retained: $(ls -1 "$BACKUP_DIR"/*.tar.gz 2>/dev/null | wc -l)"
