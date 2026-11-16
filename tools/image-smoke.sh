#!/usr/bin/env bash
# Smoke test of the product image, used by CI and `make image-smoke`.
#   tools/image-smoke.sh <image> [<arch>]     arch is amd64 or arm64; default: the image's own
# The worker's own sandbox is reproduced (no network, read-only root, no capabilities), so a
# missing library or a tool that needs the network fails here.
set -euo pipefail

image=${1:?usage: image-smoke.sh <image> [amd64|arm64]}
want=${2:-}
got=$(docker image inspect --format '{{.Architecture}}' "$image")
if [ -n "$want" ] && [ "$got" != "$want" ]; then
  echo "image architecture is $got, expected $want" >&2
  exit 1
fi
echo "smoke test: $image ($got)"

user=$(docker image inspect --format '{{.Config.User}}' "$image")
if [ "$user" != 10001:10001 ]; then
  echo "image runs as '$user', expected 10001:10001" >&2
  exit 1
fi

docker run --rm "$image" version

docker run --rm --network none --read-only --cap-drop ALL --tmpfs /tmp --entrypoint sh "$image" -c '
  set -e
  ffprobe -version | head -n 1
  ffmpeg -version | head -n 1
  mkvmerge --version
  # Encode a one-frame clip like a preview, then probe it: exercises libx264 and the mp4 muxer.
  head -c 28800 /dev/zero | ffmpeg -v error -f rawvideo -pix_fmt yuv420p -s 160x120 -i - -c:v libx264 -f mp4 /tmp/probe.mp4
  test "$(ffprobe -v error -show_entries stream=codec_name -of csv=p=0 /tmp/probe.mp4)" = h264
  echo "tools ok"
'
