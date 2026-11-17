# Development and CI toolchain. `make check` and the pre-commit hook run inside this image,
# so the host only needs a container runtime, make and git. Every tool is pinned.
#
# Bump procedure: change a version and its digest together, rebuild with `make dev-image`.

ARG GO_IMAGE=golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9
ARG NODE_IMAGE=node:26.13.1-trixie-slim@sha256:e6bedef217677b276c591795e169840edd9096dbf13f29df8ef8d1856b8820bc
ARG GOLANGCI_IMAGE=golangci/golangci-lint:v2.15.0@sha256:fbfcfd12a6f8d7f1b97b3362ca80fc2a244d46863148c73cb88e7793af96c481
# govulncheck: a Go module, pinned by version (the module proxy's checksum database verifies it).
ARG GOVULNCHECK_VERSION=v1.9.0
ARG GITLEAKS_IMAGE=zricethezav/gitleaks:v8.30.1@sha256:c00b6bd0aeb3071cbcb79009cb16a60dd9e0a7c60e2be9ab65d25e6bc8abbb7f

FROM ${NODE_IMAGE} AS node
FROM ${GOLANGCI_IMAGE} AS golangci
FROM ${GITLEAKS_IMAGE} AS gitleaks

FROM ${GO_IMAGE} AS govulncheck
ARG GOVULNCHECK_VERSION
RUN GOBIN=/out CGO_ENABLED=0 go install golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}

FROM ${GO_IMAGE}

# Debian packages come from a dated snapshot so the versions below stay installable.
ARG DEBIAN_SNAPSHOT=20261104T000000Z
RUN sed -i \
      -e "s#http://deb.debian.org/debian-security#http://snapshot.debian.org/archive/debian-security/${DEBIAN_SNAPSHOT}#" \
      -e "s#http://deb.debian.org/debian#http://snapshot.debian.org/archive/debian/${DEBIAN_SNAPSHOT}#" \
      /etc/apt/sources.list.d/debian.sources \
 && apt-get -o Acquire::Check-Valid-Until=false update \
 && apt-get install -y --no-install-recommends \
      ffmpeg=7:7.1.5-0+deb13u1 \
      mkvtoolnix=92.0-1+deb13u1 \
      sqlite3 \
      zip \
 && rm -rf /var/lib/apt/lists/*

COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -s ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
 && ln -s ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx
COPY --from=golangci /usr/bin/golangci-lint /usr/local/bin/golangci-lint
COPY --from=gitleaks /usr/bin/gitleaks /usr/local/bin/gitleaks
COPY --from=govulncheck /out/govulncheck /usr/local/bin/govulncheck

# The image runs as the calling user (make passes -u), so caches live in the mounted repo.
ENV FUKKU_DEV=1 \
    HOME=/tmp \
    GOCACHE=/src/.cache/go-build \
    GOMODCACHE=/src/.cache/go-mod \
    GOLANGCI_LINT_CACHE=/src/.cache/golangci \
    npm_config_cache=/src/.cache/npm \
    GOFLAGS=-buildvcs=false \
    GIT_CONFIG_COUNT=1 \
    GIT_CONFIG_KEY_0=safe.directory \
    GIT_CONFIG_VALUE_0=*
WORKDIR /src
