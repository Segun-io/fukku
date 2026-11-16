# Fukku product image: one binary, run as `fukku web` or `fukku worker` (deploy/compose.example.yaml).
# Every base image is pinned by version and digest; Debian packages come from a dated snapshot.

ARG NODE_IMAGE=node:26.13.1-trixie-slim@sha256:e6bedef217677b276c591795e169840edd9096dbf13f29df8ef8d1856b8820bc
ARG GO_IMAGE=golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9
ARG FFMPEG_VERSION=7.1.5
ARG FFMPEG_SHA256=de668509caf9e35e3cd162473441fdb29538c6d96ed080292b3cf9e6fc5d558f
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

# ---- ffmpeg and ffprobe ---------------------------------------------------------------
# Built from the pinned upstream source for the target architecture (under emulation when
# cross-building) and linked statically, so the runtime stage needs none of Debian's ffmpeg
# dependency tree. Only what the worker uses is enabled: every demuxer, decoder, parser and
# filter (probing and clip cutting accept any input), but encoders and muxers only for preview
# clips (H.264 and AAC in MP4, Matroska, stills). Widen the lists here if a feature needs more.
# GPL because of libx264; Fukku is AGPL-3.0-only. See docs/image.md.
FROM ${GO_IMAGE} AS ffmpeg
ARG FFMPEG_VERSION
ARG FFMPEG_SHA256
ARG DEBIAN_SNAPSHOT=20261104T000000Z
RUN sed -i \
      -e "s#http://deb.debian.org/debian-security#http://snapshot.debian.org/archive/debian-security/${DEBIAN_SNAPSHOT}#" \
      -e "s#http://deb.debian.org/debian#http://snapshot.debian.org/archive/debian/${DEBIAN_SNAPSHOT}#" \
      /etc/apt/sources.list.d/debian.sources \
 && apt-get -o Acquire::Check-Valid-Until=false update \
 && apt-get install -y --no-install-recommends \
      libx264-dev=2:0.164.3108+git31e19f9-2+b1 \
      nasm=2.16.03-1 \
      xz-utils=5.8.1-1+deb13u2 \
      zlib1g-dev=1:1.3.dfsg+really1.3.1-1+b1 \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /build
RUN curl -fsSL -o ffmpeg.tar.xz "https://ffmpeg.org/releases/ffmpeg-${FFMPEG_VERSION}.tar.xz" \
 && echo "${FFMPEG_SHA256}  ffmpeg.tar.xz" | sha256sum -c - \
 && tar -xf ffmpeg.tar.xz --strip-components=1
RUN ./configure \
      --prefix=/out --enable-gpl --enable-libx264 --enable-zlib \
      --disable-autodetect --disable-doc --disable-ffplay --disable-network --disable-debug \
      --disable-shared --enable-static --pkg-config-flags=--static --extra-ldflags=-static \
      --disable-encoders --enable-encoder=libx264,aac,mjpeg,png \
      --disable-muxers --enable-muxer=mp4,mov,matroska,webm,null,image2,mjpeg \
      --disable-protocols --enable-protocol=file,pipe,fd \
      --disable-devices \
 && make -j"$(nproc)" \
 && make install \
 && strip /out/bin/ffmpeg /out/bin/ffprobe \
 && /out/bin/ffmpeg -version | head -n 1 \
 && /out/bin/ffprobe -version | head -n 1

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
      mkvtoolnix=92.0-1+deb13u1 \
      tini=0.19.0-3+b8 \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd --system --gid 10001 fukku \
 && useradd --system --uid 10001 --gid fukku --no-create-home --shell /usr/sbin/nologin fukku \
 && mkdir -p /var/lib/fukku /spool /data \
 && chown fukku:fukku /var/lib/fukku /spool

COPY --from=ffmpeg /out/bin/ffmpeg /out/bin/ffprobe /usr/local/bin/
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
