#!/usr/bin/env bash
# Merge the SPDX JSON SBOM of ui/ into the one of the image:
#   tools/sbom-merge.sh <image.spdx.json> <ui.spdx.json> <out.spdx.json>
# The image scan (syft) lists the Debian packages, the Go modules and ffmpeg but not the UI's npm
# packages, which are compiled into the binary and leave no lockfile in the image. A second scan of
# ui/ (package-lock.json) lists them. The result is the image's document plus the UI's packages,
# files and relationships; the UI's root package (what its scan describes) is contained in the
# image's root package, and the UI scan's own DESCRIBES relationship is dropped, so the document
# still describes the image only. tools/sbom-validate.sh checks the result. Needs jq.
set -euo pipefail

[ $# -eq 3 ] || { echo "usage: sbom-merge.sh <image.spdx.json> <ui.spdx.json> <out.spdx.json>" >&2; exit 2; }
image=$1 extra=$2 out=$3

jq --slurpfile extra "$extra" '
  ($extra[0]) as $e
  | ([.relationships[] | select(.spdxElementId == "SPDXRef-DOCUMENT" and .relationshipType == "DESCRIBES")
      | .relatedSpdxElement][0]) as $imageRoot
  | ([$e.relationships[] | select(.spdxElementId == $e.SPDXID and .relationshipType == "DESCRIBES")
      | .relatedSpdxElement]) as $uiRoots
  | ([.packages[].SPDXID] + [(.files // [])[].SPDXID]) as $have
  | .packages += [$e.packages[] | select(.SPDXID as $i | $have | index($i) | not)]
  | .files = ((.files // []) + [($e.files // [])[] | select(.SPDXID as $i | $have | index($i) | not)])
  | .relationships += [$e.relationships[]
      | select(.relationshipType != "DESCRIBES")]
  | .relationships += [$uiRoots[] | {spdxElementId: $imageRoot, relationshipType: "CONTAINS", relatedSpdxElement: .}]
  | .relationships |= unique
' "$image" > "$out"

jq -e '(.packages | length) > 0' "$out" >/dev/null
