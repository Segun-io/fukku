#!/usr/bin/env bash
# Run the behaviour probes inside the test environment: run-probes.sh offline|online [go test args]
# FUKKU_TEST_SIMULATE_METADATA_OUTAGE=1 makes every lookup and add behave as if the *arr
# metadata services were unreachable: those probes end as skipped, without touching the internet.
set -euo pipefail
cd "$(dirname "$0")"
mode=${1:-offline}; shift || true
[ -f .run/.env ] || { echo "the test environment is not up: make env-up" >&2; exit 1; }
online=0; [ "$mode" = online ] && online=1
# A full run starts from empty results (older ones could mix versions); a run that selects
# probes (e.g. -run TestSonarr) replaces only theirs. A simulated outage keeps earlier results, so
# the skipped ones can name the verdict they replace.
if [ "$#" -eq 0 ] && [ "${FUKKU_TEST_SIMULATE_METADATA_OUTAGE:-0}" != 1 ]; then find .run/results -name '[A-Z]*.json' -delete 2>/dev/null || true; fi
docker compose --env-file .run/.env -f compose.yaml run --rm -e FUKKU_TEST_ONLINE=$online \
  -e FUKKU_TEST_SIMULATE_METADATA_OUTAGE="${FUKKU_TEST_SIMULATE_METADATA_OUTAGE:-0}" tools \
  go test -tags probes -count=1 -timeout 60m -v ./test/probes/ "$@"
