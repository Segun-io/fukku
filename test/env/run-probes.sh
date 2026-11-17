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
set +e
docker compose --env-file .run/.env -f compose.yaml run --rm -T -e FUKKU_TEST_ONLINE=$online \
  -e FUKKU_TEST_SIMULATE_METADATA_OUTAGE="${FUKKU_TEST_SIMULATE_METADATA_OUTAGE:-0}" tools \
  go test -tags probes -count=1 -timeout 60m -v ./test/probes/ "$@" 2>&1 | tee .run/go-test.log
status=${PIPESTATUS[0]}
set -e
# The exit status goes into the log; the gate (make probes-gate) reads it. A run that did not get
# this far leaves no status line, and the gate fails it.
echo "go test exit status: $status" >> .run/go-test.log
# A differs verdict fails its test, so go test exits non-zero on findings the gate accepts.
# This script succeeds once the run is recorded: the gate judges it, and fails a crash, a
# build failure or a failed test without a result.
echo "go test exited with status $status; the probe gate judges the run"
