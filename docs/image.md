# Product image

`Dockerfile` builds one image for `linux/amd64` and `linux/arm64`; `fukku web` and `fukku worker`
run from it. Every base image is pinned by version and digest, Debian packages come from the
dated snapshot, the user is `10001:10001`, and `fukku healthcheck` is the health check.

## What is in it

| Part | Source |
| --- | --- |
| `fukku` | built from this repository (Go, static, UI embedded) |
| `ffmpeg`, `ffprobe` | built from the upstream 7.1.5 source tarball (pinned by SHA-256; the release is signed by the FFmpeg release key, fingerprint `FCF9 86EA 15E6 E293 A564 4F10 B432 2F04 D676 58D8`, checked when the pin was chosen), linked statically with libx264 and zlib from the Debian snapshot |
| `mkvmerge` | Debian `mkvtoolnix`, snapshot |
| `tini`, `ca-certificates` | Debian, snapshot |

The ffmpeg build enables every demuxer, decoder, parser and filter (probing and cutting accept
any input) but only the encoders (`libx264`, `aac`, `mjpeg`, `png`), muxers (`mp4`, `mov`,
`matroska`, `webm`, `null`, `image2`, `mjpeg`) and protocols (`file`, `pipe`, `fd`) that preview
clips need. No network, no devices, no ffplay. If a feature needs more, widen the `configure`
lists in the `ffmpeg` stage.

Bumping ffmpeg: change `FFMPEG_VERSION` and `FFMPEG_SHA256` together (check the `.asc` signature
against the release key), rebuild, run `make image image-smoke`.

## Options measured (2026-11-16, linux/amd64, uncompressed size as `docker images` shows it)

| Option | Size | Maintenance | Security updates | Licence and provenance |
| --- | --- | --- | --- | --- |
| A. Debian `ffmpeg` (before; `--no-install-recommends` already applied; its dependency tree is mandatory) | 859 MB | none | Debian, via the snapshot date | GPL build from Debian, signed archive |
| B. Pinned third-party static build (BtbN builds measured: `ffmpeg` 134 MB + `ffprobe` 133 MB per binary, plus `mkvtoolnix`) | about 520 MB | low, but dated release tags are pruned upstream, so a pin can disappear; johnvansickle.com builds are single-maintainer, publish MD5 only | none of its own: wait for the next build | GPL (libx264 etc.) or LGPL variant; provenance is a CI job of a third party, not reproducible by us |
| C. Own minimal static ffmpeg from the pinned upstream source, Debian slim runtime (chosen) | 331 MB | one version and one hash to bump; about 100 s build on amd64, longer for arm64 under emulation | we rebuild on each ffmpeg point release; the x264 and zlib versions follow the snapshot | GPLv3 because of libx264 (compatible with AGPL-3.0-only); the source is the pinned upstream tarball, reproducible from a hash |
| D. Scratch image with `mkvmerge` and its libraries copied in | 185 MB | an `ldd` copy script to keep right; no shell | as C | as C, but no package database, so the SBOM lists fewer packages, and nothing to debug with |

Option C is 331 MB, 528 MB (61%) smaller than 859 MB. D is smaller still but trades away the SBOM's
package inventory and a shell for debugging; it is the next step if size matters again
(`mkvtoolnix` and its Qt, ICU and Boost libraries are now most of the remainder). Option B was
rejected on size and on pin retention, A on size.

Static ffmpeg and ffprobe are 24 MB each. glibc is linked statically (no name resolution is
needed: networking is disabled).

## Source offer

The image contains GPL software (ffmpeg with libx264). Corresponding source: the ffmpeg tarball
named in the `Dockerfile` and the Debian source packages for `x264` at the snapshot date.

## Multi-arch in CI

The `image` job of `ci.yml` runs once per architecture (`amd64`, `arm64`), each through
`.github/actions/build-image`: build, `tools/image-smoke.sh` (architecture, user, `version`,
ffprobe, ffmpeg and mkvmerge in the worker's sandbox, and an x264 encode and probe), then an SBOM
per architecture (`fukku-amd64.spdx.json`, `fukku-arm64.spdx.json`). The arm64 job runs under QEMU
emulation; the private repository has no free native arm runner. Locally: `make image image-smoke`
(your own architecture).
