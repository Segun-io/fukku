#!/usr/bin/env bash
# Run the Playwright tests in the runner container of the test stack (make e2e). Needs the stack
# (make image env-up dev-up) and test/e2e/node_modules (make e2e-deps). Arguments are passed to
# `playwright test`, for example: test/e2e/run.sh smoke.spec.ts -g "home page".
set -euo pipefail
cd "$(dirname "$0")"
env_dir=../env

[ -f "$env_dir/.run/.env" ] && [ -f "$env_dir/.run/dev.env" ] || {
  echo "the stack is not up: make image env-up dev-up" >&2
  exit 1
}
[ -d ../../.cache/e2e-fixtures/hostile ] || { echo "the fixtures are missing: make e2e-fixtures" >&2; exit 1; }
[ -d node_modules/@playwright/test ] || { echo "dependencies are missing: make e2e-deps" >&2; exit 1; }

# The image's browsers only work with the same Playwright version.
version=$(sed -n 's/.*"@playwright\/test": "\([0-9.]*\)".*/\1/p' package.json)
grep -q "mcr.microsoft.com/playwright:v${version}-noble@sha256:" "$env_dir/compose.yaml" || {
  echo "test/env/compose.yaml: the e2e image must be playwright:v${version}-noble (the version in package.json)" >&2
  exit 1
}

sock=${FUKKU_E2E_DOCKER_SOCK:-/var/run/docker.sock}
if [ -z "${FUKKU_E2E_DOCKER_GID:-}" ]; then
  FUKKU_E2E_DOCKER_GID=$(stat -c %g "$sock" 2>/dev/null || stat -f %g "$sock")
fi
export FUKKU_E2E_DOCKER_GID

rm -rf test-results playwright-report
compose=(docker compose --env-file "$env_dir/.run/.env" --env-file "$env_dir/.run/dev.env" -f "$env_dir/compose.yaml" --profile fukku)
exec "${compose[@]}" run --rm --no-deps e2e npx playwright test "$@"
