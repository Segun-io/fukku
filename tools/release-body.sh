#!/usr/bin/env bash
# Print the body of the draft GitHub release: the CHANGELOG notes, the image reference pinned by
# digest, how to verify it, and a pointer to the compatibility table attached to the release.
#   tools/release-body.sh <version> <image-name> <digest> <notes-file>
# <digest> is sha256:<64 hex>; a dry run passes "dry-run" and gets a placeholder. Used by
# release.yml (the release job, and the dry run that shows the result).
set -euo pipefail

version=${1:?usage: release-body.sh <version> <image-name> <digest> <notes-file>}
image=${2:?}
digest=${3:?}
notes=${4:?}

if [ "$digest" = dry-run ]; then
  digest="sha256:<digest known only after the push>"
elif ! printf '%s' "$digest" | grep -Eq '^sha256:[0-9a-f]{64}$'; then
  echo "release-body: '$digest' is not a sha256 digest" >&2
  exit 1
fi
# The signer's identity: this workflow, run for exactly this version's tag.
identity="https://github.com/Segun-io/fukku/.github/workflows/release.yml@refs/tags/v${version}"

cat "$notes"
cat <<EOF

## Image

Pin the image by digest, in \`deploy/compose.example.yaml\` or your own file:

\`\`\`
${image}:${version}@${digest}
\`\`\`

\`linux/amd64\` and \`linux/arm64\` in one manifest. The image is signed with cosign (keyless) and
carries build provenance and an SBOM per architecture. Verify before you run it:

\`\`\`sh
cosign verify \\
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \\
  --certificate-identity '${identity}' \\
  ${image}@${digest}
gh attestation verify oci://${image}@${digest} --repo Segun-io/fukku
\`\`\`

The SBOMs are attached to this release as \`fukku-amd64.spdx.json\` and
\`fukku-arm64.spdx.json\`.

## Compatibility

\`compatibility.md\`, attached to this release, is \`docs/compatibility.md\` as of the tag: what
Fukku relies on in Radarr, Sonarr, Bazarr and Jellyfin, checked by behaviour probes against the
pinned versions.
EOF
