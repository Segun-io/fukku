#!/usr/bin/env bash
# Measure Jellyfin's library refresh on generated libraries of growing size (issue #161):
#   make refresh-scale [SCALE_SIZES=200,2000,10000] [SCALE_RUNS=5] [SCALE_SCANS=1]
# By hand only: not in CI, not in make check. It needs Docker and the dev image (make dev-image).
#
# It starts a throwaway Jellyfin from the image pinned in compose.yaml, as its own compose
# project (fukku-refresh-scale, test/env/refreshscale/compose.yaml) on
# 127.0.0.1:${FUKKU_SCALE_PORT:-18196}, generates the films under .cache/refresh-scale, runs
# test/env/refreshscale, and removes the project and its data when it ends, also on failure or
# Ctrl-C. The shared test environment (fukku-test) is not touched. The results go to
# .cache/refresh-scale.json and into docs/compatibility.md, between the refresh-scale markers.
set -euo pipefail
cd "$(dirname "$0")"
repo=$(cd ../.. && pwd)
sizes=${SCALE_SIZES:-200,2000,10000}
runs=${SCALE_RUNS:-5}
scans=${SCALE_SCANS:-1}

image=$(sed -nE 's#^ +image: (jellyfin/jellyfin:[^ @]+@sha256:[0-9a-f]{64})$#\1#p' compose.yaml)
if [ -z "$image" ] || [ "$(printf '%s\n' "$image" | wc -l)" -ne 1 ]; then
  echo "refresh-scale: cannot read the one pinned Jellyfin image from test/env/compose.yaml" >&2
  exit 1
fi
dev_image="fukku-dev:$(sha256sum "$repo/tools/dev/Dockerfile" | cut -c1-12)"
docker image inspect "$dev_image" >/dev/null 2>&1 || { echo "refresh-scale: build the dev image first: make dev-image" >&2; exit 1; }
export FUKKU_SCALE_JELLYFIN_IMAGE=$image FUKKU_SCALE_WORK=$repo/.cache/refresh-scale DEV_IMAGE=$dev_image \
  PUID=$(id -u) PGID=$(id -g)
compose=(docker compose -f refreshscale/compose.yaml)

cleanup() {
  "${compose[@]}" --profile tools down -v --remove-orphans >/dev/null 2>&1 || true
  # Jellyfin runs as the calling user, but remove through the dev image all the same, as env-reset does.
  docker run --rm -v "$repo/.cache:/c" "$dev_image" rm -rf /c/refresh-scale || true
}
# Created first, as the calling user: a bind mount of a missing folder would make it root-owned.
mkdir -p "$repo/.cache"
trap cleanup EXIT
cleanup # what an interrupted earlier run left
mkdir -p "$FUKKU_SCALE_WORK"/{config,cache,data}

"${compose[@]}" run --rm --no-deps tools go build -o .cache/bin/refreshscale ./test/env/refreshscale
"${compose[@]}" run --rm --no-deps tools go build -o .cache/bin/seed ./test/env/seed
"${compose[@]}" up -d jellyfin
# A fresh Jellyfin can answer 503 for a while after /health is up; the wizard is idempotent.
for attempt in 1 2 3 4 5; do
  if "${compose[@]}" run --rm --no-deps tools .cache/bin/refreshscale setup; then break; fi
  [ "$attempt" -eq 5 ] && exit 1
  echo "refresh-scale: retrying the wizard in 10 s ($attempt/5)"; sleep 10
done
# Jellyfin has no setting for a fixed API key and locks its database while running (as up.sh).
"${compose[@]}" stop jellyfin
"${compose[@]}" run --rm --no-deps tools .cache/bin/seed jellyfin-key /jellyfin-config/data/jellyfin.db
"${compose[@]}" up -d jellyfin
"${compose[@]}" run --rm --no-deps tools .cache/bin/refreshscale measure \
  -sizes "$sizes" -runs "$runs" -scans "$scans" -out .cache/refresh-scale.json -doc docs/compatibility.md
echo "refresh-scale: results in .cache/refresh-scale.json and docs/compatibility.md; removing the throwaway Jellyfin"
