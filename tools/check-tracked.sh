#!/usr/bin/env bash
# Fails when a tracked file is a core dump or a compiled (ELF) binary: neither belongs in the
# repository, and both have been committed by accident (a crashed tool leaves `core.<pid>`).
# Reads the index, so it also catches a file that is staged but not yet committed. Fast: one
# `git ls-files` and a 4-byte read per file that is not obviously text.
#
# A binary the repository really needs goes into ALLOWED below, one path per line, with a
# reason. There is none today.
set -euo pipefail
cd "$(dirname "$0")/.."

ALLOWED=(
)

allowed() {
  local p
  for p in "${ALLOWED[@]+"${ALLOWED[@]}"}"; do [ "$p" = "$1" ] && return 0; done
  return 1
}

bad=()
while IFS= read -r -d '' f; do
  [ -f "$f" ] && [ ! -L "$f" ] || continue # deleted in the work tree, or a symlink
  allowed "$f" && continue
  base=${f##*/}
  if [[ $base == core || $base =~ ^core\.[0-9]+$ ]]; then
    bad+=("$f: a core dump")
    continue
  fi
  if [ "$(head -c 4 -- "$f" | od -An -tx1 | tr -d ' \n')" = "7f454c46" ]; then
    bad+=("$f: an ELF binary")
  fi
done < <(git ls-files -z)

if [ "${#bad[@]}" -gt 0 ]; then
  printf 'tracked files that must not be committed:\n' >&2
  printf '  %s\n' "${bad[@]}" >&2
  echo 'Remove them with `git rm`. A crashed tool leaves core dumps: delete them, never `git add -A`.' >&2
  echo 'A binary the repository really needs: allow-list it in tools/check-tracked.sh.' >&2
  exit 1
fi
echo "check-tracked: ok"
