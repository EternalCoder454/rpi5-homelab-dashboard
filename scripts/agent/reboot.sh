#!/usr/bin/env bash
# Reboot the Pi cleanly. The dashboard is unreachable for ~30-60s.
set -euo pipefail
echo "Flushing disk buffers..."
sync
echo "Rebooting now — the dashboard will be unreachable for ~30-60 seconds."
sudo -n systemctl reboot
