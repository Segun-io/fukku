#!/usr/bin/env bash
# Start and seed the local test environment (make env-up). Idempotent.
set -euo pipefail
cd "$(dirname "$0")"
repo=$(cd ../.. && pwd)
dev_image="fukku-dev:$(sha256sum "$repo/tools/dev/Dockerfile" | cut -c1-12)"

mkdir -p .run/{radarr,sonarr,bazarr/config,jellyfin/config,jellyfin/cache,results} \
         .run/data/media/{movies,tv} .run/data/intake .run/data/blackhole/{radarr,sonarr}/{nzb,watch}
cat > .run/.env <<ENV
PUID=$(id -u)
PGID=$(id -g)
DEV_IMAGE=$dev_image
ENV
compose=(docker compose --env-file .run/.env -f compose.yaml)

# Build the seeder once; `go run` would hide its exit codes. The fake indexer is built the
# same way and run by the dev image.
"${compose[@]}" run --rm --no-deps tools go build -o .cache/bin/seed ./test/env/seed
"${compose[@]}" run --rm --no-deps -e CGO_ENABLED=0 tools go build -o .cache/bin/indexer ./test/env/indexer
seed=(run --rm tools .cache/bin/seed)

# Bazarr reads its API key and connections from config.yaml, so patch it while it is stopped.
"${compose[@]}" stop bazarr >/dev/null 2>&1 || true
"${compose[@]}" run --rm --no-deps -v "$PWD/.run/bazarr/config:/bazarr-config" tools \
  .cache/bin/seed bazarr-config /bazarr-config/config.yaml

"${compose[@]}" up -d radarr sonarr bazarr jellyfin
# Recreated on every run so it runs the binary just built (it keeps no state worth keeping).
"${compose[@]}" up -d --wait --force-recreate indexer
set +e
"${compose[@]}" "${seed[@]}" apps
rc=$?
set -e
if [ "$rc" -eq 3 ]; then
  # Jellyfin has no setting for a fixed API key and locks its database while running.
  "${compose[@]}" stop jellyfin
  "${compose[@]}" run --rm --no-deps tools .cache/bin/seed jellyfin-key /jellyfin-config/data/jellyfin.db
  "${compose[@]}" up -d jellyfin
  # Jellyfin can restart itself once after the wizard; the seeder is idempotent, so retry.
  for attempt in 1 2 3 4 5; do
    if "${compose[@]}" "${seed[@]}" apps; then break; fi
    [ "$attempt" -eq 5 ] && exit 1
    echo "seed: retrying in 10 s ($attempt/5)"; sleep 10
  done
elif [ "$rc" -ne 0 ]; then
  exit "$rc"
fi
echo "test environment ready: radarr :17878, sonarr :18989, bazarr :16767, jellyfin :18096 (127.0.0.1); fake indexer and blackhole download clients seeded"
