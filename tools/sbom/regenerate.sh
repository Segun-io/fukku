#!/usr/bin/env bash
# Regenerate tools/sbom/requirements.txt (spdx-tools and every transitive dependency, pinned
# by version and sha256 hash) from tools/sbom/requirements.in. Needs Docker and the internet.
#   tools/sbom/regenerate.sh
# The resolver is uv, copied from its pinned image into the pinned Python image that
# tools/sbom-validate.sh also uses, so the file is resolved for the interpreter that installs it.
# To bump spdx-tools: edit requirements.in and SPDX_TOOLS in tools/sbom-validate.sh to the same
# version, run this script, review the diff (new packages, new hashes) and commit all three files.
# To bump uv or the Python image: change the pin below (version and digest together), rerun.
set -euo pipefail
cd "$(dirname "$0")"

UV_IMAGE=ghcr.io/astral-sh/uv:0.13.9@sha256:d3b601f0c7b118bc1753febf7c41cc80981df678445e0254284eecf84db79a7d
PYTHON_IMAGE=python:3.13.18-slim@sha256:e48f59cdba69a3cbd9ed2cf0f348d90d45a8d350d98134cf1070bc106ee9ed90

tag=fukku-sbom-regenerate:$(printf '%s\n%s\n' "$UV_IMAGE" "$PYTHON_IMAGE" | sha256sum | cut -c1-12)
docker build -q -t "$tag" - >/dev/null <<DOCKERFILE
FROM $UV_IMAGE AS uv
FROM $PYTHON_IMAGE
COPY --from=uv /uv /usr/local/bin/uv
DOCKERFILE

docker run --rm -u "$(id -u):$(id -g)" --tmpfs /tmp:exec,mode=1777 -e HOME=/tmp -e UV_CACHE_DIR=/tmp/uv \
  -v "$PWD:/w" -w /w "$tag" \
  uv pip compile --generate-hashes --universal --python-version 3.13 --quiet \
    --custom-compile-command "tools/sbom/regenerate.sh" -o requirements.txt requirements.in
echo "wrote tools/sbom/requirements.txt"
