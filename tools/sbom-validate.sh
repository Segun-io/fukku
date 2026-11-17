#!/usr/bin/env bash
# Validate SPDX JSON documents (structure, every referenced element exists, relationships):
#   tools/sbom-validate.sh <file.spdx.json>...
# Uses the pinned python image and the pinned spdx-tools release; needs Docker and the internet (pip).
# spdx-tools and every dependency it pulls come from tools/sbom/requirements.txt, installed with
# --require-hashes --no-deps, so nothing unreviewed is installed. Regenerate it with
# tools/sbom/regenerate.sh (see there for the bump procedure).
set -euo pipefail

[ $# -ge 1 ] || { echo "usage: sbom-validate.sh <file.spdx.json>..." >&2; exit 2; }
PYTHON_IMAGE=python:3.13.18-slim@sha256:e48f59cdba69a3cbd9ed2cf0f348d90d45a8d350d98134cf1070bc106ee9ed90
SPDX_TOOLS=0.8.5
REQS=$(cd "$(dirname "$0")/sbom" && pwd)/requirements.txt
grep -qxF "spdx-tools==$SPDX_TOOLS \\" "$REQS" || { echo "tools/sbom/requirements.txt does not pin spdx-tools==$SPDX_TOOLS: run tools/sbom/regenerate.sh" >&2; exit 1; }

for f in "$@"; do
  dir=$(cd "$(dirname "$f")" && pwd)
  name=$(basename "$f")
  docker run --rm -v "$dir:/sbom:ro" -v "$REQS:/requirements.txt:ro" "$PYTHON_IMAGE" sh -c \
    "pip install -q --root-user-action=ignore --disable-pip-version-check --require-hashes --no-deps --only-binary=:all: -r /requirements.txt && pyspdxtools -i /sbom/$name"
  echo "valid SPDX: $name"
done
