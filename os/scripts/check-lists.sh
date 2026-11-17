#!/bin/bash
# check-lists.sh: every package in os/packages/*.list says which NoCapOS
# feature needs it ("name  # reason"). CI fails on a line without a reason.
set -euo pipefail
cd "$(dirname "$0")/../packages"
bad=0
for f in *.list; do
  n=0
  while IFS= read -r line || [ -n "$line" ]; do
    n=$((n + 1))
    case "$line" in '' | '#'*) continue ;; esac
    entry="${line%%#*}"
    reason=""
    [ "$entry" != "$line" ] && reason="${line#*#}"
    if [ -z "$(echo "$reason" | tr -d '[:space:]')" ]; then
      echo "$f:$n: no reason given for: $line"
      bad=1
    fi
  done <"$f"
done
[ "$bad" -eq 0 ] && echo "package lists OK"
exit "$bad"
