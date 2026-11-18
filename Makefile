# One entry point for every check. Tools run inside the pinned development image
# (tools/dev/Dockerfile), so the host needs only Docker, make and git.
#
#   make check          everything CI runs on each commit: format, lint, tests, UI, secrets
#   make help           all targets

SHELL := /bin/bash
.SHELLFLAGS := -euo pipefail -c
.DEFAULT_GOAL := help

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w -X github.com/segun-io/fukku/internal/version.Version=$(VERSION) \
           -X github.com/segun-io/fukku/internal/version.Commit=$(COMMIT)

DEV_IMAGE := fukku-dev:$(shell sha256sum tools/dev/Dockerfile | cut -c1-12)
ENV_COMPOSE := docker compose -f test/env/compose.yaml

# Inside the dev image (FUKKU_DEV=1) commands run directly; outside they run in a container.
ifeq ($(FUKKU_DEV),1)
IN_DEV  :=
DEV_DEP :=
else
TTY     := $(shell [ -t 0 ] && echo -t)
# In a git worktree the repository's metadata lives outside $(CURDIR), and without it the
# secret scan silently skips the history. Mount it read-only at the same path.
GIT_COMMON := $(shell git rev-parse --path-format=absolute --git-common-dir 2>/dev/null)
GIT_MOUNT  := $(if $(GIT_COMMON),$(if $(filter $(CURDIR)/%,$(GIT_COMMON)),,-v $(GIT_COMMON):$(GIT_COMMON):ro))
IN_DEV  := docker run --rm -i $(TTY) -u $(shell id -u):$(shell id -g) -v $(CURDIR):/src $(GIT_MOUNT) $(DEV_IMAGE)
DEV_DEP := dev-image
endif

.PHONY: help
help: ## list targets
	@grep -hE '^[a-z][a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | sort | awk -F':.*## ' '{printf "  %-16s %s\n", $$1, $$2}'

.PHONY: dev-image
dev-image: ## build the pinned development image if it is missing
	@docker image inspect $(DEV_IMAGE) >/dev/null 2>&1 || docker build -t $(DEV_IMAGE) tools/dev

.PHONY: shell
shell: $(DEV_DEP) ## open a shell in the development image
	$(IN_DEV) bash

## ---- checks -------------------------------------------------------------------------

ACTIONLINT := rhysd/actionlint:1.7.12@sha256:b1934ee5f1c509618f2508e6eb47ee0d3520686341fec936f3b79331f9315667

.PHONY: check
check: $(DEV_DEP) ## format check, lint, unit tests, UI checks, workflow lint, secret scan
	$(IN_DEV) make --no-print-directory _check
	$(MAKE) --no-print-directory ci-lint check-tracked

.PHONY: check-tracked
check-tracked: ## fail on a tracked core dump or ELF binary (runs on the host: needs only git and bash)
	@tools/check-tracked.sh

.PHONY: ci-lint
ci-lint: ## lint the GitHub Actions workflows (actionlint, pinned)
	@if [ "$(FUKKU_DEV)" = 1 ]; then echo "ci-lint runs on the host (needs Docker)"; else \
	  docker run --rm -v $(CURDIR):/repo -w /repo $(ACTIONLINT) -color=false && echo "actionlint: ok"; fi

.PHONY: vulncheck
vulncheck: ui-deps ## scan Go modules and npm packages for known vulnerabilities (needs internet; CI job vulncheck)
	$(IN_DEV) tools/vulncheck.sh

.PHONY: _check
_check: fmt-check lint test ui-check e2e-check notices-check secrets
	@echo "check: all passed"

.PHONY: fmt
fmt: $(DEV_DEP) ui-deps ## format Go and UI code in place
	$(IN_DEV) bash -c 'golangci-lint fmt ./... && cd ui && npx biome check --write .'

.PHONY: fmt-check
fmt-check: $(DEV_DEP)
	$(IN_DEV) bash -c 'out=$$(golangci-lint fmt --diff ./...); if [ -n "$$out" ]; then echo "$$out"; echo "Go code is not formatted: run make fmt"; exit 1; fi'

.PHONY: lint
lint: $(DEV_DEP) ## lint Go code
	$(IN_DEV) bash -c 'go mod tidy -diff && go vet ./... && go vet -tags probes ./test/... && golangci-lint run ./...'

.PHONY: test
test: $(DEV_DEP) ## unit tests, including the fixture generators (needs ffmpeg: in the dev image)
	$(IN_DEV) go test -race -count=1 ./...

.PHONY: notices
notices: ui-deps ## regenerate THIRD_PARTY_NOTICES.md (UI packages, fonts, Go modules, image components)
	$(IN_DEV) go run ./tools/notices

.PHONY: notices-check
notices-check: ui-deps ## fail if THIRD_PARTY_NOTICES.md is out of date or a licence is not allowed (offline: needs ui/node_modules and the Go module cache)
	$(IN_DEV) go run ./tools/notices -check

.PHONY: ui-deps
ui-deps: $(DEV_DEP)
	$(IN_DEV) bash -c 'cd ui && if [ ! -f node_modules/.package-lock.json ] || [ package-lock.json -nt node_modules/.package-lock.json ]; then npm ci --no-audit; fi'

.PHONY: ui-check
ui-check: ui-deps ## lint, unit tests, type-check and build the UI
	$(IN_DEV) bash -c 'cd ui && npm run lint && npm test && npm run build'

.PHONY: secrets
secrets: $(DEV_DEP) ## scan the working tree (and history, once there is one) for secrets
	$(IN_DEV) bash -c 'gitleaks dir --no-banner --redact --config .gitleaks.toml . && if git rev-parse -q --verify HEAD >/dev/null 2>&1; then gitleaks git --no-banner --redact --config .gitleaks.toml .; elif ! git rev-parse --git-dir >/dev/null 2>&1; then echo "secrets: WARNING: no git metadata visible, history not scanned" >&2; fi'

.PHONY: secrets-staged
secrets-staged: $(DEV_DEP)
	$(IN_DEV) gitleaks git --pre-commit --staged --no-banner --redact --config .gitleaks.toml .

.PHONY: hooks
hooks: ## install the git pre-commit hook (gitleaks on staged changes)
	git config core.hooksPath .githooks

## ---- end-to-end tests (plan §14.6) -------------------------------------------------

.PHONY: e2e-deps
e2e-deps: $(DEV_DEP) ## install the Playwright project's dependencies (no scripts, no browser download)
	$(IN_DEV) bash -c 'cd test/e2e && if [ ! -f node_modules/.package-lock.json ] || [ package-lock.json -nt node_modules/.package-lock.json ]; then npm ci --no-audit; fi'

.PHONY: e2e-fixtures
e2e-fixtures: $(DEV_DEP) ## generate the files the end-to-end tests upload into .cache/e2e-fixtures (never committed)
	$(IN_DEV) go run ./test/fixtures/cmd/genfixtures -quick -out .cache/e2e-fixtures >/dev/null

.PHONY: e2e-check
e2e-check: e2e-deps ## lint and type-check the Playwright project
	$(IN_DEV) bash -c 'cd test/e2e && npm run lint && npm run typecheck'

# The large upload of exit-m1-large-upload.spec.ts (issue #15): 5GiB in make e2e and CI,
# 60GiB in make e2e-large. dev-up raises the stack's upload limits to fit it.
E2E_LARGE_SIZE ?= 5GiB
E2E_MAX_IN_FLIGHT ?= 128GiB
# The tests tagged @online (exit-m2.spec.ts, exit-m3.spec.ts) add titles through the *arr
# metadata services; E2E_ONLINE=0 skips them (CI does on pull requests, as with the probes).
E2E_ONLINE ?= 1

.PHONY: e2e
e2e: export FUKKU_E2E_ONLINE = $(E2E_ONLINE)
e2e: export FUKKU_E2E_LARGE_SIZE = $(E2E_LARGE_SIZE)
e2e: export FUKKU_DEV_MAX_FILE_SIZE = $(E2E_LARGE_SIZE)
e2e: export FUKKU_DEV_MAX_IN_FLIGHT = $(E2E_MAX_IN_FLIGHT)
e2e: ## Playwright through the UI and API against the dev stack; make e2e ARGS='smoke.spec.ts -g home'
	$(MAKE) --no-print-directory image env-up dev-up e2e-deps e2e-fixtures
	test/e2e/run.sh $(ARGS)

.PHONY: e2e-large
e2e-large: ## the large-upload test at 60GiB (by hand: needs about 65GiB free for test/env/.run/data)
	$(MAKE) --no-print-directory e2e E2E_LARGE_SIZE=60GiB ARGS='exit-m1-large-upload.spec.ts $(ARGS)'

## ---- clean-room install test (issue #67) ----------------------------------------------

.PHONY: install-test
install-test: $(DEV_DEP) ## clean-room install from README and docs/install.md on a Docker-in-Docker host; make install-test FUKKU_IMAGE=fukku:dev (by hand before a release; needs internet for Radarr and Sonarr metadata)
	@if [ "$(FUKKU_DEV)" = 1 ]; then echo "install-test runs on the host (needs Docker)"; exit 1; fi
	@test -n "$(FUKKU_IMAGE)" || { echo "set FUKKU_IMAGE, e.g. make install-test FUKKU_IMAGE=fukku:dev (make image builds it) or a registry reference by digest"; exit 2; }
	DEV_IMAGE=$(DEV_IMAGE) FUKKU_IMAGE='$(FUKKU_IMAGE)' test/install/run.sh

## ---- build ------------------------------------------------------------------------

.PHONY: ui
ui: ui-deps ## build the UI into ui/dist (embedded by the Go build)
	$(IN_DEV) bash -c 'cd ui && npm run build'

.PHONY: build
build: ui ## build bin/fukku for this machine
	$(IN_DEV) go build -trimpath -ldflags '$(LDFLAGS)' -o bin/fukku ./cmd/fukku

.PHONY: image
image: ## build the product image as fukku:dev
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t fukku:dev .

.PHONY: ffmpeg-check
ffmpeg-check: $(DEV_DEP) ## fail if the pinned ffmpeg is behind its line's latest release (needs internet)
	$(IN_DEV) go run ./tools/ffmpegcheck

.PHONY: image-smoke
image-smoke: ## smoke-test fukku:dev (what CI runs on each architecture)
	tools/image-smoke.sh fukku:dev

## ---- test environment (plan §14) --------------------------------------------------

.PHONY: env-up
env-up: ## start Radarr, Sonarr, Bazarr and Jellyfin and seed them (idempotent)
	test/env/up.sh

.PHONY: env-down
env-down: ## stop the test environment, keep its data
	$(ENV_COMPOSE) down

.PHONY: env-reset
env-reset: ## stop the test environment and delete its data
	$(ENV_COMPOSE) down -v --remove-orphans
	docker run --rm -v $(CURDIR)/test/env:/env $(DEV_IMAGE) rm -rf /env/.run

.PHONY: dev-up
dev-up: ## run Fukku next to the test environment behind a test auth proxy (needs image and env-up)
	test/env/dev-up.sh

.PHONY: dev-down
dev-down: ## stop Fukku and its proxy; the test environment keeps running
	test/env/dev-down.sh

.PHONY: dev-logs
dev-logs: ## follow the logs of Fukku and its proxy
	docker compose --env-file test/env/.run/.env --env-file test/env/.run/dev.env -f test/env/compose.yaml --profile fukku logs -f fukku-web fukku-worker fukku-proxy

.PHONY: probes
probes: $(DEV_DEP) ## run the behaviour probes that need no internet, then judge them with the gate
	test/env/run-probes.sh offline
	@$(MAKE) --no-print-directory probes-gate

.PHONY: probes-online
probes-online: $(DEV_DEP) ## run every behaviour probe, including those that add titles (internet), then judge them
	test/env/run-probes.sh online
	@$(MAKE) --no-print-directory probes-gate

.PHONY: probes-gate
probes-gate: $(DEV_DEP) ## CI rule on probe results: fail on new differs or error, warn on accepted ones and upstream skips
	$(IN_DEV) go run ./test/probes/cmd/compat -gate -results test/env/.run/results -gotest test/env/.run/go-test.log

.PHONY: compat
compat: $(DEV_DEP) ## write the latest probe results into docs/compatibility.md
	$(IN_DEV) go run ./test/probes/cmd/compat -results test/env/.run/results -doc docs/compatibility.md

# Not a probe and not in CI: it needs a library of thousands of films, so it runs its own
# throwaway Jellyfin (issue #161). SCALE_SIZES, SCALE_RUNS and SCALE_SCANS are passed through.
.PHONY: refresh-scale
refresh-scale: $(DEV_DEP) ## by hand: time Jellyfin's library refresh on 200, 2000 and 10000 generated films in a throwaway Jellyfin, write the table into docs/compatibility.md
	@if [ "$(FUKKU_DEV)" = 1 ]; then echo "refresh-scale runs on the host (needs Docker)"; exit 1; fi
	test/env/refresh-scale.sh

.PHONY: fixtures
fixtures: $(DEV_DEP) ## generate sample media and hostile archives into .cache/fixtures, to look at
	$(IN_DEV) go run ./test/fixtures/cmd/genfixtures -out .cache/fixtures
