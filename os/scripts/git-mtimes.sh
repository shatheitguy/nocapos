#!/usr/bin/env bash
# git-mtimes.sh DIR: sets every tracked file's time to its last commit's.
# Rugix Bakery reuses a cached layer when nothing it uses is newer than the
# layer; a fresh checkout makes every file "new", so CI first puts the
# times back to when each file really changed.
set -euo pipefail
dir="${1:?folder}"
git ls-files -z -- "$dir" | while IFS= read -r -d '' f; do
  t="$(git log -1 --format=%ct -- "$f")"
  [ -n "$t" ] && touch -h -d "@$t" "$f"
done
# Folders too (Bakery looks at a recipe folder's own time).
find "$dir" -type d ! -path '*/.rugix*' ! -path '*/build*' -exec touch -h -d '@0' {} +
git ls-files -z -- "$dir" | while IFS= read -r -d '' f; do
  d="$(dirname "$f")"; t="$(stat -c %Y "$f")"
  while [ "$d" != "." ] && [ "$d" != "/" ]; do
    [ "$(stat -c %Y "$d")" -ge "$t" ] || touch -h -d "@$t" "$d"
    d="$(dirname "$d")"
  done
done
