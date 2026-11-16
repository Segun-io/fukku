# Product image

`Dockerfile` builds one image for `linux/amd64` and `linux/arm64`; `fukku web` and `fukku worker`
run from it. Every base image is pinned by version and digest, Debian packages come from the
dated snapshot, the user is `10001:10001`, and `fukku healthcheck` is the health check.

## What is in it

| Part | Source |
| --- | --- |
| `fukku` | built from this repository (Go, static, UI embedded) |
| `ffmpeg`, `ffprobe` | built from the upstream 7.1.5 source tarball (pinned by SHA-256; the release is signed by the FFmpeg release key, fingerprint `FCF9 86EA 15E6 E293 A564 4F10 B432 2F04 D676 58D8`, checked when the pin was chosen), with libx264, libdav1d (AV1) and zlib from the Debian snapshot; libx264 and libdav1d are shared libraries installed in the runtime stage, because Debian ships no static libdav1d |
| `mkvmerge` | Debian `mkvtoolnix`, snapshot |
| `tini`, `ca-certificates` | Debian, snapshot |

The ffmpeg build enables every demuxer, decoder, parser and filter, so probing and decoding
accept what the worker accepts: the demuxers for every container in `upload.videoExtensions`
(`matroska,webm`, `mov,mp4`, `avi`, `mpegts`, the worker's `formatWhitelist`) and decoders for
H.264, HEVC, AV1 (libdav1d), VP8, VP9, MPEG-2, MPEG-4, VC-1, AAC, AC-3, E-AC-3, DTS, TrueHD, MP3,
Opus, Vorbis, FLAC, PCM, SubRip, ASS/SSA, mov_text, WebVTT, PGS and DVD subtitles.
`tools/image-smoke.sh` checks all of those by name, writes a short file in each container it can
(mkv, mp4, m4v, mov, avi, ts), probes it with the worker's flags and decodes one. `.webm` is
checked by its demuxer and decoder names only: no VP8 or VP9 encoder is built.

Only the encoders (`libx264`, `aac`, `mpeg4`, `pcm_s16le`, `mjpeg`, `png`, `subrip`, `ass`,
`movtext`, `webvtt`, plus `wrapped_avframe` and `rawvideo` for null and raw output), muxers (`mp4`,
`mov`, `matroska`, `webm`, `avi`, `mpegts`, `null`, `image2`, `mjpeg`) and protocols (`file`,
`pipe`, `fd`) are limited. No network, no devices, no ffplay. If a feature (M3 previews and
merges) needs another encoder, muxer or filter option, widen the `configure` lists in the
`ffmpeg` stage and add a case to the smoke test.

## Maintenance: ffmpeg is no longer patched by Debian

Debian's security updates no longer cover ffmpeg in this image: it is our own build of one
upstream release. Someone has to follow FFmpeg security releases (the release announcements at
ffmpeg.org, or the security advisories of the 7.1 branch) and bump the pin by hand. The
worker parses untrusted uploads with it, so treat a security release as a prompt to bump soon.
libx264, libdav1d, zlib and the rest still come from the Debian snapshot and move with
`DEBIAN_SNAPSHOT`.

To bump: in `Dockerfile` change the two build arguments `FFMPEG_VERSION` and `FFMPEG_SHA256`
together. Then, in an empty directory:

```sh
v=7.1.6   # the new version
curl -fsSLO https://ffmpeg.org/releases/ffmpeg-$v.tar.xz
curl -fsSLO https://ffmpeg.org/releases/ffmpeg-$v.tar.xz.asc
curl -fsSLO https://ffmpeg.org/ffmpeg-devel.asc
export GNUPGHOME=$(mktemp -d)
gpg --import ffmpeg-devel.asc
gpg --verify ffmpeg-$v.tar.xz.asc ffmpeg-$v.tar.xz
sha256sum ffmpeg-$v.tar.xz                                # goes into FFMPEG_SHA256
```

The signature must be "Good" and the key fingerprint
`FCF9 86EA 15E6 E293 A564 4F10 B432 2F04 D676 58D8`. Then `make image image-smoke`, and let CI
build arm64.

## Options measured (2026-11-16, linux/amd64, uncompressed size as `docker images` shows it)

| Option | Size | Maintenance | Security updates | Licence and provenance |
| --- | --- | --- | --- | --- |
| A. Debian `ffmpeg` (before; `--no-install-recommends` already applied; its dependency tree is mandatory) | 859 MB | none | Debian, via the snapshot date | GPL build from Debian, signed archive |
| B. Pinned third-party static build (BtbN builds measured: `ffmpeg` 134 MB + `ffprobe` 133 MB per binary, plus `mkvtoolnix`) | about 520 MB | low, but dated release tags are pruned upstream, so a pin can disappear; johnvansickle.com builds are single-maintainer, publish MD5 only | none of its own: wait for the next build | GPL (libx264 etc.) or LGPL variant; provenance is a CI job of a third party, not reproducible by us |
| C. Own minimal ffmpeg from the pinned upstream source, Debian slim runtime (chosen) | 330 MB | one version and one hash to bump; about 100 s build on amd64, longer for arm64 under emulation | we rebuild on each ffmpeg point release; the x264, dav1d and zlib versions follow the snapshot | GPLv3 because of libx264 (compatible with AGPL-3.0-only); the source is the pinned upstream tarball, reproducible from a hash |
| D. Scratch image with `mkvmerge` and its libraries copied in | 185 MB | an `ldd` copy script to keep right; no shell | as C | as C, but no package database, so the SBOM lists fewer packages, and nothing to debug with |

Option C is 330 MB, 529 MB (62%) smaller than 859 MB. D is smaller still but trades away the SBOM's
package inventory and a shell for debugging; it is the next step if size matters again
(`mkvtoolnix` and its Qt, ICU and Boost libraries are now most of the remainder). Option B was
rejected on size and on pin retention, A on size.

`ffmpeg` and `ffprobe` are 22 MB each.

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
