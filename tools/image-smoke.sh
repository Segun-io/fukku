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

# The licence texts and third-party notices travel with the image (Dockerfile, runtime stage).
docker run --rm --entrypoint sh "$image" -c '
  for f in LICENSE THIRD_PARTY_NOTICES.md fonts/OFL-dm-mono.txt fonts/OFL-instrument-sans.txt fonts/OFL-schibsted-grotesk.txt ffmpeg/LICENSE.md ffmpeg/COPYING.GPLv2; do
    [ -s "/usr/share/doc/fukku/$f" ] || { echo "missing /usr/share/doc/fukku/$f in the image" >&2; exit 1; }
  done
  grep -q "react" /usr/share/doc/fukku/THIRD_PARTY_NOTICES.md
'
echo "licence texts and notices present"

# No setuid or setgid file may be left (Dockerfile, runtime stage): without no-new-privileges
# they would let the non-root user climb. -xdev keeps find out of /proc and /sys.
# Run as root so that no directory is unreadable, and let errors show.
left=$(docker run --rm --user 0 --entrypoint find "$image" / -xdev -type f -perm /6000)
if [ -n "$left" ]; then
  echo "setuid or setgid files left in the image:" >&2
  echo "$left" >&2
  exit 1
fi
echo "no setuid or setgid files"

# QEMU user-mode emulation (an image of the other architecture, as the arm64 build on an amd64
# runner) ignores RLIMIT_AS, so the checks that the limit is in force cannot pass there.
case "$(uname -m)" in
  x86_64) host=amd64 ;;
  aarch64 | arm64) host=arm64 ;;
  *) host=$(uname -m) ;;
esac
emulated=0
if [ "$got" != "$host" ]; then
  emulated=1
  echo "emulated ($got image on a $host host): skipping the checks that RLIMIT_AS is enforced"
fi

docker run --rm -e EMULATED=$emulated --network none --read-only --cap-drop ALL --tmpfs /tmp --entrypoint sh "$image" -c '
  set -e
  ffprobe -version | head -n 1
  ffmpeg -version | head -n 1
  mkvmerge --version

  # The worker starts every tool under prlimit (worker.toolMemoryLimit): it must exist, set
  # the limit in the child, and make a tool past it fail instead of grow.
  prlimit --as=4294967296 -- ffprobe -version | head -n 1
  if [ "$EMULATED" = 0 ]; then
    [ "$(prlimit --as=4294967296 -- sh -c "ulimit -v")" = 4194304 ] || { echo "prlimit did not set RLIMIT_AS" >&2; exit 1; }
    if prlimit --as=33554432 -- ffmpeg -version >/dev/null 2>&1; then
      echo "ffmpeg ran under a 32 MiB address-space limit" >&2
      exit 1
    fi
  fi
  echo "prlimit ok"

  # What the worker accepts (internal/media/probe.go formatWhitelist, upload.videoExtensions):
  # every demuxer and decoder it can meet must be in this build. Names are as ffprobe lists them.
  demuxers=$(ffprobe -v error -demuxers)
  # (the first four are the upload list; mpeg, asf, flv and ogg are the defaults of worker.libraryFormats)
  for d in matroska,webm mov,mp4 avi mpegts mpeg asf flv ogg; do
    echo "$demuxers" | grep -Eq " $d[ ,]" || { echo "missing demuxer $d" >&2; exit 1; }
  done
  decoders=$(ffprobe -v error -decoders)
  for d in h264 hevc av1 libdav1d vp8 vp9 mpeg2video mpeg4 vc1 \
           aac ac3 eac3 dca truehd mp3 opus vorbis flac pcm_s16le pcm_s24le pcm_f32le \
           subrip ass ssa mov_text webvtt pgssub dvdsub; do
    echo "$decoders" | grep -q " $d " || { echo "missing decoder $d" >&2; exit 1; }
  done

  # One short file per accepted container, probed the way the worker probes: the file on
  # descriptor 3, format and protocol whitelists, no other input.
  probe() { # file, expected codec names in stream order
    found=$(ffprobe -v error -print_format json -show_format -show_streams \
      -format_whitelist matroska,webm,mov,mp4,avi,mpegts -protocol_whitelist fd -fd 3 -i fd: 3<"$1" |
      grep "\"codec_name\"" | sed "s/.*: \"\(.*\)\".*/\1/" | tr "\n" " ")
    [ "$found" = "$2" ] || { echo "$1: streams [$found], expected [$2]" >&2; exit 1; }
    echo "probe ok: $1 ($found)"
  }
  printf "1\n00:00:00,000 --> 00:00:01,000\nhello\n" > /tmp/s.srt
  head -c 57600 /dev/zero > /tmp/v.raw   # three 160x120 yuv420p frames
  head -c 4000 /dev/zero > /tmp/a.raw
  video="-f rawvideo -pix_fmt yuv420p -s 160x120 -r 10 -i /tmp/v.raw"
  audio="-f s16le -ar 8000 -ac 1 -i /tmp/a.raw"
  enc="ffmpeg -v error -y"
  $enc $video $audio -i /tmp/s.srt -map 0 -map 1 -map 2 -c:v libx264 -c:a aac -c:s subrip /tmp/t.mkv
  probe /tmp/t.mkv "h264 aac subrip "
  $enc $video $audio -i /tmp/s.srt -map 0 -map 1 -map 2 -c:v libx264 -c:a aac -c:s mov_text /tmp/t.mp4
  probe /tmp/t.mp4 "h264 aac mov_text "
  $enc $video $audio -c:v libx264 -c:a aac -f mp4 /tmp/t.m4v
  probe /tmp/t.m4v "h264 aac "
  $enc $video $audio -c:v libx264 -c:a aac -f mov /tmp/t.mov
  probe /tmp/t.mov "h264 aac "
  $enc $video $audio -c:v mpeg4 -c:a pcm_s16le /tmp/t.avi
  probe /tmp/t.avi "mpeg4 pcm_s16le "
  $enc $video $audio -c:v libx264 -c:a aac /tmp/t.ts
  probe /tmp/t.ts "h264 aac "
  # .webm needs a VP8 or VP9 encoder, left out on purpose: its demuxer is the matroska,webm one
  # checked above, and the VP8, VP9, Opus and Vorbis decoders are listed.

  # Decode what was written (the preview path): all streams, to null.
  ffmpeg -v error -i /tmp/t.mkv -f null -
  echo "tools ok"
'
