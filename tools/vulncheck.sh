#!/usr/bin/env bash
# Dependency vulnerability scan: `make vulncheck` locally, the `vulncheck` job of ci.yml on pull
# requests and weekly. It needs the internet (the Go and npm advisory databases), so it is not
# part of `make check`, which must pass offline and does not change with the day's advisories.
# Runs inside the dev image (govulncheck and npm are pinned there).
#
# What fails the build:
#   - Go: a vulnerability in a function the code calls (govulncheck's own rule: exit 3).
#     Vulnerable packages that are imported or required but never called are listed and warn.
#   - UI (ui/): a high or critical advisory in a production dependency. Those are bundled into
#     the binary. Moderate and low ones, and anything in dev dependencies, warn.
#   - End-to-end tests (test/e2e): warn only; nothing of it ships.
# A scan that cannot run (no network, database unreachable) fails: it must not pass silently.
set -euo pipefail
cd "$(dirname "$0")/.."

fail=0
warn() { echo "WARNING: $*" >&2; if [ -n "${GITHUB_ACTIONS:-}" ]; then echo "::warning::$*"; fi; }

echo "== govulncheck (Go modules, called functions fail) =="
govulncheck ./... || fail=1

for dir in ui test/e2e; do
  [ -f "$dir/package-lock.json" ] || continue
  if [ "$dir" = ui ]; then
    echo "== npm audit: ui, production dependencies (high and critical fail) =="
    (cd "$dir" && npm audit --omit=dev --audit-level=high) || fail=1
  fi
  # Everything, for the record: moderate and low advisories and dev dependencies only warn.
  echo "== npm audit: $dir, all dependencies (warn only) =="
  (cd "$dir" && npm audit) || warn "$dir: npm audit reports advisories (see above)"
done

if [ "$fail" -ne 0 ]; then
  echo "vulncheck: FAILED (a called Go vulnerability or a high/critical production npm advisory)" >&2
  exit 1
fi
echo "vulncheck: passed"
