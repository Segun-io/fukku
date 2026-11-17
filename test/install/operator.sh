#!/bin/sh
# THE INSTALL: exactly the steps of README.md ("Install in five steps") and docs/install.md
# (steps 1 to 7), in their order, as the operator would type them. test/install/run.sh runs it
# inside the clean host as the user that owns the media (uid 1000), not root, with the host's
# own shell (busybox ash: the documents' commands must not need bash).
#
# Only documented steps are allowed here. What the documents leave to the reader is marked
# "EDIT": the values the documents say to replace. The only things this script takes from
# outside the documents are listed in its environment, and each is a value a real operator
# also has to bring: the image reference, the data folder, the apps' network, the API keys.
#
#   FUKKU_IMAGE        the image to install (a registry reference by digest, or an image
#                      already on the host)
#   DATA_DIR           the host folder that stands for /path/to/data
#   APPS_NETWORK       the Docker network Radarr, Sonarr and Jellyfin share
#   RADARR_API_KEY, SONARR_API_KEY, JELLYFIN_KEY_FILE   the apps' keys (Jellyfin's in a file)
#   CADDY_IMAGE        the proxy image (docs/reverse-proxy.md names caddy:2.11.9)
#   SRC                the repository's files, where `git clone` would have put them
set -eu

step() { printf '\n==== %s\n' "$*"; }

# ---- README.md step 1 / docs/install.md "1. Make the folder" -----------------------------
# (The documents `git clone` the repository for its example files; the clean host has no git,
# so run.sh puts the working tree's copy at ~/fukku-src and this script starts from there. The
# examples it copies are the ones of this very commit.)
step "1. Make the folder"
mkdir -p ~/fukku && cd ~/fukku
cp "$SRC/deploy/compose.example.yaml" compose.yaml
cp "$SRC/deploy/fukku.example.yaml" fukku.yaml
install -d -m 0750 state/data state/spool
install -d -m 0700 secrets
# EDIT: /path/to/data
install -d "$DATA_DIR/intake/jobs" "$DATA_DIR/intake/worker" "$DATA_DIR/intake/.incoming"

# ---- docs/install.md "2. Get the image" ---------------------------------------------------
step "2. Get the image"
# EDIT: the x-image line names the image to install (a release candidate, by digest).
sed -i "s#^x-image: &image .*#x-image: \&image $FUKKU_IMAGE#" compose.yaml
image=$(sed -n 's/^x-image: &image //p' compose.yaml)
docker image inspect "$image" >/dev/null 2>&1 || docker pull "$image"
docker run --rm "$image" version

# ---- docs/install.md "3. Secrets, as files" -----------------------------------------------
step "3. Secrets, as files"
# The documents' `read -rs key` is fed from the environment instead of the keyboard.
printf '%s\n' "$RADARR_API_KEY" | { read -rs key; printf '%s' "$key" > secrets/radarr_api_key; unset key; }
printf '%s\n' "$SONARR_API_KEY" | { read -rs key; printf '%s' "$key" > secrets/sonarr_api_key; unset key; }
read -rs key < "$JELLYFIN_KEY_FILE" || true; printf '%s' "$key" > secrets/jellyfin_api_key; unset key
chmod 0400 secrets/*
# This installation has no Bazarr: "Leave out an app by deleting its FUKKU_..._API_KEY_FILE
# line, its name in secrets: [...], its entry under secrets: at the end of the compose file
# and its section in fukku.yaml."
sed -i -e '/FUKKU_SUBTITLES_API_KEY_FILE/d' -e 's/, bazarr_api_key//' -e '/^  bazarr_api_key:/,/^    file:/d' compose.yaml
sed -i -e '/^subtitles:/,/^$/d' fukku.yaml

# ---- README.md step 3 / docs/install.md "4. Edit the two files" ---------------------------
step "4. Edit the two files"
# EDIT compose.yaml: the user stays 1000:1000 (the media user); every /path/to/data; the
# network the apps share.
sed -i -e "s#/path/to/data#$DATA_DIR#g" -e "s#^    name: media\$#    name: $APPS_NETWORK#" compose.yaml
# EDIT fukku.yaml: publicBaseUrl (https://fukku.example.com), auth.trustedProxies
# (172.29.50.10), auth.adminGroup and groupsHeader, the app URLs (radarr, sonarr, jellyfin),
# the root folders (/data/media/movies, /data/media/tv) and the quality profile (HD-1080p)
# are the example's own values here, so they stay. The sentinel the example names:
touch "$DATA_DIR/.mounted"
# On a disk with less free space than upload.freeSpaceMargin (100 GiB by default) every upload
# is refused; the documents say to lower it there.
free_kib=$(df -Pk "$DATA_DIR" | awk 'NR==2 {print $4}')
if [ "$free_kib" -lt $((150 * 1024 * 1024)) ]; then
  printf '\nupload:\n  freeSpaceMargin: 1GiB\n' >> fukku.yaml
fi

# ---- docs/install.md "5. Check the configuration" -----------------------------------------
step "5. Check the configuration"
docker compose run --rm --no-deps web check-config | tee /tmp/check-config.web
head -n 1 /tmp/check-config.web | grep -q 'valid for role web'
docker compose run --rm --no-deps worker check-config --role worker

# ---- README.md step 4 / docs/install.md "6. Start it" -------------------------------------
step "6. Start it"
docker compose up -d
docker compose ps
n=0
until [ "$(docker compose ps --format '{{.Health}}' | grep -c '^healthy$')" = 2 ]; do
  n=$((n + 1))
  if [ "$n" -gt 60 ]; then
    echo "both containers should be healthy within a minute" >&2
    docker compose ps >&2
    docker compose logs >&2
    exit 1
  fi
  sleep 2
done
docker compose ps
docker compose logs web | grep web_listening

# ---- README.md step 5 / docs/install.md "7. Put the reverse proxy in front" ---------------
step "7. Put the reverse proxy in front"
# docs/reverse-proxy.md, "Caddy": the Caddyfile is the one in the document, taken from it.
mkdir -p ~/proxy && cd ~/proxy
awk '/^```caddyfile/ {f=1; next} /^```/ {f=0} f' "$SRC/docs/reverse-proxy.md" > Caddyfile
# TEST ONLY (reverse-proxy.md, "Validation of these examples"): example.com cannot get a
# certificate, so Caddy makes its own.
awk '{print} /^fukku\.example\.com \{$/ {print "\ttls internal"}' Caddyfile > Caddyfile.test && mv Caddyfile.test Caddyfile
docker run --rm -v "$PWD/Caddyfile:/etc/caddy/Caddyfile:ro" "$CADDY_IMAGE" caddy validate --config /etc/caddy/Caddyfile
# docs/reverse-proxy.md, "Docker: a fixed address for the proxy": the proxy's own compose
# file, on the fukku-frontend network at 172.29.50.10 and on the network of the auth service.
cat > compose.yaml <<YAML
services:
  proxy:
    image: $CADDY_IMAGE
    ports: ["80:80", "443:443"]
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy-data:/data
    networks:
      fukku-frontend:
        ipv4_address: 172.29.50.10
      authnet:
        aliases: [fukku.example.com]   # TEST ONLY: how the check container reaches the proxy
volumes:
  caddy-data:
networks:
  fukku-frontend:
    external: true
  authnet:
    external: true
YAML
docker compose up -d
docker compose ps
printf '\ninstall: done\n'
