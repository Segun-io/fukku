#!/usr/bin/env bash
# Print the CHANGELOG.md section of one version (the text under "## [<version>]" up to the next
# "## " heading), for the draft release's notes.
#   tools/release-notes.sh <version> [CHANGELOG.md]       version without the leading v
# Fails when the version has no section or the section is empty.
set -euo pipefail

version=${1:?usage: release-notes.sh <version> [changelog]}
changelog=${2:-CHANGELOG.md}

body=$(awk -v want="## [$version]" '
  index($0, want) == 1 { on = 1; next }
  on && /^## / { exit }
  on && /^\[[^]]+\]: / { next } # the link definitions at the end of the file
  on { print }
' "$changelog")

# Trim blank lines at both ends.
body=$(printf '%s\n' "$body" | sed '/./,$!d' | sed -e :a -e '/^[[:space:]]*$/{$d;N;ba' -e '}')
if [ -z "$body" ]; then
  echo "release-notes: no section '## [$version]' with content in $changelog" >&2
  exit 1
fi
printf '%s\n' "$body"
