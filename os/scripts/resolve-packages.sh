#!/bin/bash
# resolve-packages.sh LIST...: prints the package names to install from
# NoCapOS package lists (packages/*.list), one per line. Runs inside the
# image being built, against its apt sources.
#   name     # reason     required: fails the build when apt doesn't have it
#   a|b      # reason     the first of them apt has
#   ?name    # reason     optional: skipped when apt doesn't have it
set -euo pipefail

has_candidate() {
  local c
  c="$(apt-cache policy "$1" 2>/dev/null | sed -n 's/^ *Candidate: //p' | head -1)"
  [ -n "$c" ] && [ "$c" != "(none)" ]
}

missing=0
for list in "$@"; do
  while IFS= read -r line || [ -n "$line" ]; do
    entry="${line%%#*}"
    entry="$(echo "$entry" | tr -d '[:space:]')"
    [ -n "$entry" ] || continue
    optional=0
    if [ "${entry#\?}" != "$entry" ]; then
      optional=1
      entry="${entry#\?}"
    fi
    pick=""
    IFS='|' read -ra alts <<<"$entry"
    for a in "${alts[@]}"; do
      if has_candidate "$a"; then pick="$a"; break; fi
    done
    if [ -n "$pick" ]; then
      echo "$pick"
    elif [ "$optional" -eq 1 ]; then
      echo "optional package not available, skipped: $entry" >&2
    else
      echo "required package not available: $entry ($list)" >&2
      missing=1
    fi
  done <"$list"
done
exit "$missing"
