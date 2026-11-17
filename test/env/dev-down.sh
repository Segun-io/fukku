#!/usr/bin/env bash
# Stop the Fukku containers of the dev stack and mediacheck, and remove their network (make
# dev-down). The test environment (Radarr, Sonarr, Bazarr, Jellyfin) and all data stay.
set -euo pipefail
cd "$(dirname "$0")"
env_files=()
[ -f .run/.env ] && env_files+=(--env-file .run/.env)
[ -f .run/dev.env ] && env_files+=(--env-file .run/dev.env)
compose=(docker compose "${env_files[@]}" -f compose.yaml --profile fukku)
"${compose[@]}" rm -sf fukku-proxy fukku-web fukku-worker mediacheck
docker network rm fukku-test_fukku >/dev/null 2>&1 || true
