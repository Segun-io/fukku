# Fukku product image: one binary, run as `fukku web` or `fukku worker` (deploy/compose.example.yaml).
# Every base image is pinned by version and digest; Debian packages come from a dated snapshot.

ARG NODE_IMAGE=node:26.13.1-trixie-slim@sha256:e6bedef217677b276c591795e169840edd9096dbf13f29df8ef8d1856b8820bc
ARG GO_IMAGE=golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9
ARG RUNTIME_IMAGE=debian:trixie-20261104-slim@sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2

# ---- UI -------------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM ${NODE_IMAGE} AS ui
WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json ui/.npmrc ./
RUN npm ci --no-audit
COPY ui/ ./
RUN npm run build

# ---- Go binary ------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY ui/embed.go ui/
COPY --from=ui /src/ui/dist ui/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X github.com/segun-io/fukku/internal/version.Version=${VERSION} -X github.com/segun-io/fukku/internal/version.Commit=${COMMIT}" \
      -o /out/fukku ./cmd/fukku

# ---- runtime --------------------------------------------------------------------------
FROM ${RUNTIME_IMAGE}
ARG DEBIAN_SNAPSHOT=20261104T000000Z
RUN sed -i \
      -e "s#http://deb.debian.org/debian-security#http://snapshot.debian.org/archive/debian-security/${DEBIAN_SNAPSHOT}#" \
      -e "s#http://deb.debian.org/debian#http://snapshot.debian.org/archive/debian/${DEBIAN_SNAPSHOT}#" \
      /etc/apt/sources.list.d/debian.sources \
 && apt-get -o Acquire::Check-Valid-Until=false update \
 && apt-get install -y --no-install-recommends \
      ca-certificates=20250419+deb13u1 \
      ffmpeg=7:7.1.5-0+deb13u1 \
      mkvtoolnix=92.0-1+deb13u1 \
      tini=0.19.0-3+b8 \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd --system --gid 10001 fukku \
 && useradd --system --uid 10001 --gid fukku --no-create-home --shell /usr/sbin/nologin fukku \
 && mkdir -p /var/lib/fukku /spool /data \
 && chown fukku:fukku /var/lib/fukku /spool

COPY --from=build /out/fukku /usr/local/bin/fukku

LABEL org.opencontainers.image.title="Fukku" \
      org.opencontainers.image.description="Self-hosted media intake for Radarr and Sonarr" \
      org.opencontainers.image.licenses="AGPL-3.0-only" \
      org.opencontainers.image.source="https://github.com/segun-io/fukku"

USER 10001:10001
EXPOSE 8080
# Web by default: `fukku healthcheck` asks /readyz?local=1 (database, spool, sentinels, intake
# root, worker heartbeat; never Radarr or Sonarr, so a reverse proxy keeps routing to the UI
# while they are down).
# The worker's compose service overrides this with `fukku healthcheck --worker`.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["fukku", "healthcheck"]
ENTRYPOINT ["tini", "--", "fukku"]
CMD ["web"]
