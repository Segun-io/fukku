#!/usr/bin/env bash
# Merge SPDX JSON documents into the first one: tools/sbom-merge.sh <image.spdx.json> <ui.spdx.json> <out>
# The image scan (syft) lists the Debian packages, the Go modules and ffmpeg but not the UI's npm
# packages, which are compiled into the binary and leave no lockfile in the image. A second scan of
# ui/ (package-lock.json) lists them; this script adds its packages and relationships to the
# image's document, so one SBOM describes the whole image. Needs jq.
set -euo pipefail

[ $# -eq 3 ] || { echo "usage: sbom-merge.sh <image.spdx.json> <ui.spdx.json> <out.spdx.json>" >&2; exit 2; }
image=$1 extra=$2 out=$3

jq --slurpfile extra "$extra" '
  ($extra[0]) as $e
  | ([.packages[].SPDXID] | map({(.): true}) | add) as $have
  | .packages += [$e.packages[] | select($have[.SPDXID] | not)]
  | .relationships += [$e.relationships[]?
      | select(.relationshipType != "DESCRIBES")
      | select(.spdxElementId != $e.SPDXID)]
  | .relationships += [$e.packages[] | select($have[.SPDXID] | not)
      | select(.name != $e.name)
      | {spdxElementId: "SPDXRef-DOCUMENT", relationshipType: "DESCRIBES", relatedSpdxElement: .SPDXID}]
  | .relationships |= unique
' "$image" > "$out"

jq -e '(.packages | length) > 0' "$out" >/dev/null
