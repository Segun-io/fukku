#!/usr/bin/env bash
# The clean-room install test (issue #67, M4's exit criterion): a clean install from the
# documents on a host that has nothing but a container runtime and Compose.
#
#   FUKKU_IMAGE=<image> test/install/run.sh        (or: make install-test FUKKU_IMAGE=<image>)
#
# FUKKU_IMAGE is the image to install. A registry reference by digest
# (ghcr.io/segun-io/fukku:<tag>@sha256:<digest>) is pulled by the clean host itself, as
# docs/install.md step 2 says. Anything else (fukku:dev) is an image of this machine, which is
# copied into the clean host with `docker save | docker load` (for an image that is not
# published yet); the install then finds it there and does not pull.
#
# A registry reference to a PRIVATE package (before it is made public): the clean host has no
# login of the outer machine, so give it a token that can read the package, in a file or the
# environment, never on the command line. The host logs in with it (scaffolding, like the other
# host provisioning; the token stays in the throwaway host and goes away with it):
#   GHCR_TOKEN_FILE=/path/to/file   or   GHCR_TOKEN=<token>      and optionally GHCR_USER=<login>
#
# What it does, in four parts. Only part 3 is the operator's install.
#   1. SCAFFOLDING, outside the clean host: build the test tools, generate the media at test
#      time, start the clean host (Docker-in-Docker, pinned by digest), and copy the images in.
#   2. SCAFFOLDING, inside it: the apps an operator already has. Radarr, Sonarr and Jellyfin
#      (the pins of test/env/compose.yaml), seeded, and a forward-authentication stub.
#   3. THE INSTALL, test/install/operator.sh: the steps of README.md and docs/install.md, run
#      as the media user, followed by the reverse proxy of docs/reverse-proxy.md (Caddy).
#   4. THE CHECKS (test/install/tools, `check`): /readyz, sign-in through the proxy, a film
#      (flow 1) and a season archive (flow 3) imported through the HTTP API.
#
# Needs: Docker (privileged containers allowed), the development image (made if missing), and
# the internet for two things only: the image pulls that are not cached, and Radarr's and
# Sonarr's metadata services (they add the two free titles). Runs under its own names
# (container fukku-install-host, nothing of the dev stack fukku-test is touched) and removes
# the clean host and its volumes at the end; FUKKU_INSTALL_KEEP=1 keeps it for a look
# (docker exec -it fukku-install-host sh), and the next run removes it.
set -euo pipefail
cd "$(dirname "$0")/../.."
repo=$PWD

: "${FUKKU_IMAGE:?set FUKKU_IMAGE to the image to install, e.g. FUKKU_IMAGE=fukku:dev or a registry reference by digest}"

# Pinned by version and digest. The apps are the pins of the test environment.
DIND_IMAGE='docker:29.9.3-dind@sha256:7bf70dcb85c822e3fa6f2d332143e474b99147b9d7b39102b0cfe8b04a7dd61b'
CADDY_IMAGE='caddy:2.11.9@sha256:4851e54a46b206517c574821bb5cc67f170d2156041e9fbc4c9193b45e84a75b'
pin() { awk -v svc="$1" '$0 ~ "^  "svc":$" {f=1; next} f && /^    image:/ {print $2; exit}' test/env/compose.yaml; }
RADARR_IMAGE=$(pin radarr)
SONARR_IMAGE=$(pin sonarr)
JELLYFIN_IMAGE=$(pin jellyfin)
for v in RADARR_IMAGE SONARR_IMAGE JELLYFIN_IMAGE; do
  [[ ${!v} == *@sha256:* ]] || { echo "install-test: no digest-pinned $v in test/env/compose.yaml" >&2; exit 1; }
done

# Fixed fake keys, valid only inside the clean host.
export RADARR_API_KEY=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
export SONARR_API_KEY=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb

HOST=fukku-install-host
DATA_DIR=/srv/data          # stands for /path/to/data of the documents
APPS_NETWORK=arrnet         # the network the apps share (the `name: media` of compose.yaml)
TOOLS_IMAGE=fukku-install-tools:local
DEV_IMAGE=${DEV_IMAGE:-fukku-dev:$(sha256sum tools/dev/Dockerfile | cut -c1-12)}
work=$repo/.cache/install
started=$SECONDS

phase() { printf '\n######## %s (%ss)\n' "$*" "$((SECONDS - started))"; }
hx() { docker exec -i "$HOST" "$@"; }                                   # root in the clean host
as_media() { docker exec -u media -e HOME=/home/media -w /home/media "$@"; }

logged_in=""   # the registry the clean host was logged in to, if any

# The token must not outlive the pull: the clean host is kept with FUKKU_INSTALL_KEEP=1.
logout() {
  if [ -n "$logged_in" ]; then
    as_media "$HOST" docker logout "$logged_in" >/dev/null 2>&1 || true
    logged_in=""
  fi
}

cleanup() {
  local rc=$?
  logout
  if [ "$rc" -ne 0 ] && docker inspect "$HOST" >/dev/null 2>&1; then
    echo "---- the install failed; state of the clean host:" >&2
    hx docker ps -a --format '{{.Names}}\t{{.Status}}' >&2 || true
    as_media "$HOST" sh -c 'cd ~/fukku 2>/dev/null && docker compose logs --tail 40' >&2 || true
  fi
  if [ "${FUKKU_INSTALL_KEEP:-0}" = 1 ]; then
    echo "kept: docker exec -it $HOST sh (remove it with: docker rm -fv $HOST)"
  else
    docker rm -fv "$HOST" >/dev/null 2>&1 || true
  fi
  if [ "$rc" -eq 0 ]; then
    echo; echo "install-test: PASSED in $((SECONDS - started)) s"
  else
    echo; echo "install-test: FAILED after $((SECONDS - started)) s" >&2
  fi
  exit "$rc"
}
trap cleanup EXIT

# ---- 1. SCAFFOLDING outside the clean host -------------------------------------------------
phase "build the test tools and generate the media"
docker image inspect "$DEV_IMAGE" >/dev/null 2>&1 || docker build -t "$DEV_IMAGE" tools/dev
rm -rf "$work"; mkdir -p "$work"
dev=(docker run --rm -u "$(id -u):$(id -g)" -v "$repo:/src" -e CGO_ENABLED=0 "$DEV_IMAGE")
"${dev[@]}" go build -trimpath -o .cache/install/tools ./test/install/tools
tar -C "$work" -cf "$work/tools.tar" tools
# The media are generated now, from the repository's fixtures: nothing is committed.
"${dev[@]}" go run ./test/fixtures/cmd/genfixtures -install -out .cache/install/fixtures >/dev/null

# Images: the apps' by their digests (from the local cache, or pulled), and the one to install.
fetch() { docker image inspect "$1" >/dev/null 2>&1 || docker pull "$1" >/dev/null; }
pinned=("$RADARR_IMAGE" "$SONARR_IMAGE" "$JELLYFIN_IMAGE" "$CADDY_IMAGE")
for i in "${pinned[@]}" "$DIND_IMAGE"; do fetch "$i"; done
images=()
for i in "${pinned[@]}"; do images+=("${i#*@}"); done
preload=true
if [[ $FUKKU_IMAGE == *@sha256:* ]]; then
  preload=false   # a registry reference by digest: the clean host pulls it (install step 2)
else
  docker image inspect "$FUKKU_IMAGE" >/dev/null 2>&1 || { echo "install-test: FUKKU_IMAGE=$FUKKU_IMAGE is not on this machine (build it: make image)" >&2; exit 1; }
  images+=("$FUKKU_IMAGE")
fi

phase "start the clean host: $DIND_IMAGE"
docker rm -fv "$HOST" >/dev/null 2>&1 || true
docker run -d --name "$HOST" --privileged "$DIND_IMAGE" >/dev/null
for _ in $(seq 60); do hx docker info >/dev/null 2>&1 && break; sleep 1; done
hx docker info >/dev/null
hx docker compose version

phase "provision the host: a media user (uid 1000), the data filesystem, the images"
# Not the install: the host's own users and disk, which an operator has before they start.
hx sh -c "adduser -D -u 1000 media && addgroup media docker \
  && install -d -o 1000 -g 1000 $DATA_DIR $DATA_DIR/media/movies $DATA_DIR/media/tv \
       /srv/scaffold/config/radarr /srv/scaffold/config/sonarr /srv/scaffold/config/jellyfin/config /srv/scaffold/config/jellyfin/cache \
       /home/media/fukku-src /srv/fixtures"
# Where `git clone` would have put the repository's files (the clean host has no git).
tar -cf - README.md docs deploy | hx tar -xf - -C /home/media/fukku-src
tar -C "$work/fixtures" -cf - . | hx tar -xf - -C /srv/fixtures
hx chown -R 1000:1000 /home/media/fukku-src
docker save "${images[@]}" | hx docker load >/dev/null
if [ "$preload" = false ] && { [ -n "${GHCR_TOKEN_FILE:-}" ] || [ -n "${GHCR_TOKEN:-}" ]; }; then
  registry=${FUKKU_IMAGE%%/*}
  if [ -n "${GHCR_TOKEN_FILE:-}" ]; then token_in() { cat "$GHCR_TOKEN_FILE"; }; else token_in() { printf '%s' "$GHCR_TOKEN"; }; fi
  # The token goes through stdin only: not in an argument list, a log or a trace.
  token_in | docker exec -i -u media -e HOME=/home/media "$HOST" docker login "$registry" -u "${GHCR_USER:-token}" --password-stdin >/dev/null
  echo "the clean host is logged in to $registry (token from the environment, not shown)"
  logged_in=$registry
fi
# The pinned images were saved by digest (the id of a single-platform image is its digest):
# give each its repository and tag, so that the reference repo:tag@sha256:digest, which is what
# the compose files use, finds it on the host instead of asking a registry. Inspecting the
# whole reference proves the digest is the pinned one.
for ref in "$RADARR_IMAGE" "$SONARR_IMAGE" "$JELLYFIN_IMAGE" "$CADDY_IMAGE"; do
  hx docker tag "${ref#*@}" "${ref%@*}"
  hx docker image inspect "$ref" >/dev/null
done
hx docker import -c 'ENTRYPOINT ["/tools"]' - "$TOOLS_IMAGE" < "$work/tools.tar" >/dev/null

# ---- 2. SCAFFOLDING inside the clean host: the apps the operator already has ---------------
phase "scaffolding: Radarr, Sonarr, Jellyfin and the forward-authentication stub"
docker cp test/install/apps.compose.yaml "$HOST:/srv/scaffold/compose.yaml"
apps_env=(-e "RADARR_IMAGE=$RADARR_IMAGE" -e "SONARR_IMAGE=$SONARR_IMAGE" -e "JELLYFIN_IMAGE=$JELLYFIN_IMAGE"
  -e "TOOLS_IMAGE=$TOOLS_IMAGE" -e "APPS_NETWORK=$APPS_NETWORK" -e "DATA_DIR=$DATA_DIR"
  -e "RADARR_API_KEY=$RADARR_API_KEY" -e "SONARR_API_KEY=$SONARR_API_KEY")
docker exec "${apps_env[@]}" -w /srv/scaffold "$HOST" docker compose up -d --wait
docker exec -e "RADARR_API_KEY=$RADARR_API_KEY" -e "SONARR_API_KEY=$SONARR_API_KEY" "$HOST" \
  docker run --rm --network "$APPS_NETWORK" -e RADARR_API_KEY -e SONARR_API_KEY \
    -v "$DATA_DIR:/data" -v /srv/scaffold:/scaffold "$TOOLS_IMAGE" seed

# ---- 3. THE INSTALL: README.md and docs/install.md, followed literally ---------------------
phase "the install (operator.sh)"
docker cp test/install/operator.sh "$HOST:/srv/scaffold/operator.sh"
as_media -e "FUKKU_IMAGE=$FUKKU_IMAGE" -e "DATA_DIR=$DATA_DIR" -e "APPS_NETWORK=$APPS_NETWORK" \
  -e "CADDY_IMAGE=$CADDY_IMAGE" -e "RADARR_API_KEY=$RADARR_API_KEY" -e "SONARR_API_KEY=$SONARR_API_KEY" \
  -e JELLYFIN_KEY_FILE=/srv/scaffold/jellyfin_api_key -e SRC=/home/media/fukku-src \
  "$HOST" sh /srv/scaffold/operator.sh
logout   # the image is pulled; the token is not needed again

# ---- 4. THE CHECKS ---------------------------------------------------------------------------
phase "the checks"
hx sh -c "docker rm -f check >/dev/null 2>&1; \
  docker create --name check --network $APPS_NETWORK -e RADARR_API_KEY=$RADARR_API_KEY -e SONARR_API_KEY=$SONARR_API_KEY \
    -v $DATA_DIR:/data:ro -v /srv/fixtures:/fixtures:ro $TOOLS_IMAGE check >/dev/null \
  && docker network connect authnet check && docker network connect fukku-frontend check \
  && docker start -a check"
$preload && echo "(the image was copied in with docker save | docker load; a registry reference by digest is pulled by the clean host instead)"
exit 0
