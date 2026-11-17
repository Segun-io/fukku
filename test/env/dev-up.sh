#!/usr/bin/env bash
# Start the Fukku containers of the dev stack next to the test environment (make dev-up).
# Needs `make image` (fukku:dev) and `make env-up`. Idempotent.
set -euo pipefail
cd "$(dirname "$0")"
[ -f .run/.env ] || { echo "the test environment is not up: make env-up" >&2; exit 1; }
image=${FUKKU_IMAGE:-fukku:dev}
docker image inspect "$image" >/dev/null 2>&1 || { echo "image $image is missing: make image" >&2; exit 1; }

# The proxy is the .10 host of the subnet (a /24).
subnet=${FUKKU_DEV_SUBNET:-172.30.77.0/24}
case "$subnet" in
  *.*.*.0/24) ;;
  *) echo "FUKKU_DEV_SUBNET must be a /24 such as 172.30.77.0/24, got $subnet" >&2; exit 1 ;;
esac
proxy_ip="${subnet%.0/24}.10"

# The upload limits of the stack (256MiB and 1GiB unless set); make e2e raises them for the
# large-upload test.
for size in "${FUKKU_DEV_MAX_FILE_SIZE:-256MiB}" "${FUKKU_DEV_MAX_IN_FLIGHT:-1GiB}"; do
  [[ "$size" =~ ^[0-9]+([KMGT]i?B)?$ ]] || { echo "FUKKU_DEV_MAX_FILE_SIZE and FUKKU_DEV_MAX_IN_FLIGHT are sizes such as 5GiB, got $size" >&2; exit 1; }
done

# .run/data/media/versions is versions.root of fukku.yaml (second versions, #56).
mkdir -p .run/fukku/data .run/fukku/spool .run/data/intake .run/data/media/versions
touch .run/data/.mounted
cat > .run/dev.env <<ENV
FUKKU_IMAGE=$image
FUKKU_DEV_SUBNET=$subnet
FUKKU_DEV_PROXY_IP=$proxy_ip
FUKKU_DEV_MAX_FILE_SIZE=${FUKKU_DEV_MAX_FILE_SIZE:-256MiB}
FUKKU_DEV_MAX_IN_FLIGHT=${FUKKU_DEV_MAX_IN_FLIGHT:-1GiB}
ENV
compose=(docker compose --env-file .run/.env --env-file .run/dev.env -f compose.yaml --profile fukku)

# The proxy is a tiny Go program, built in the dev image like the seeder; so is mediacheck,
# which measures media for the e2e tests (the M3 exit criteria).
"${compose[@]}" run --rm --no-deps -e CGO_ENABLED=0 tools go build -o .cache/bin/proxy ./test/env/proxy
"${compose[@]}" run --rm --no-deps -e CGO_ENABLED=0 tools go build -o .cache/bin/mediacheck ./test/env/mediacheck

"${compose[@]}" up -d --wait fukku-web fukku-worker fukku-proxy
# Recreated on every run so it runs the binary just built (it keeps no state).
"${compose[@]}" up -d --wait --force-recreate mediacheck
echo "Fukku is up: UI http://127.0.0.1:18080 (sign in as user1, user2 or admin1 with /__test/login?user=<name>), proxy control API http://127.0.0.1:18081"
