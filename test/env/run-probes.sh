#!/usr/bin/env bash
# Run the behaviour probes inside the test environment: run-probes.sh offline|online [go test args]
set -euo pipefail
cd "$(dirname "$0")"
mode=${1:-offline}; shift || true
[ -f .run/.env ] || { echo "the test environment is not up: make env-up" >&2; exit 1; }
online=0; [ "$mode" = online ] && online=1
# A full run starts from empty results (older ones could mix versions); a run that selects
# probes (e.g. -run TestSonarr) replaces only theirs.
if [ "$#" -eq 0 ]; then find .run/results -name '[A-Z]*.json' -delete 2>/dev/null || true; fi
docker compose --env-file .run/.env -f compose.yaml run --rm -e FUKKU_TEST_ONLINE=$online tools \
  go test -tags probes -count=1 -timeout 60m -v ./test/probes/ "$@"
