#!/usr/bin/env bash
# Reclaim disk space from Docker: dangling images, stopped containers, unused
# networks and build cache. Does NOT touch named volumes or in-use data.
set -euo pipefail
echo "Reclaiming Docker space (safe prune — volumes are left untouched)..."
docker system prune -f
echo "Done."
