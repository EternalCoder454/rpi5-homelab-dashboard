#!/usr/bin/env bash
# Update and upgrade all apt packages non-interactively, then autoremove leftovers.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive

echo "Refreshing package lists..."
sudo -n apt-get update

echo "Upgrading packages (full-upgrade)..."
sudo -n apt-get -y -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold full-upgrade

echo "Removing unused packages..."
sudo -n apt-get -y autoremove --purge

echo "Update complete."
if [ -f /var/run/reboot-required ]; then
  echo "NOTE: a reboot is required to finish applying updates."
fi
