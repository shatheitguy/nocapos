#!/usr/bin/env bash
# nocapos-setup-hint.service: until the first admin account exists, NoCapOS
# asks for a setup token that it prints to its log at every start. This puts
# the current one on the console login screen (/run/issue.d, so it's gone
# after a reboot), next to the addresses to open.
set -u
hint=/run/issue.d/50-nocapos-setup.issue
mkdir -p /run/issue.d
token=""
for _ in $(seq 1 60); do
  logs="$(journalctl -b -u nocapos --no-pager -o cat -n 400 2>/dev/null || true)"
  last="$(awk '/"msg":"starting alfad"/ {buf=""} {buf = buf $0 "\n"} END {printf "%s", buf}' <<<"$logs")"
  if grep -q '"alfad ready"' <<<"$last"; then
    token="$(grep -o '"setup_token":"[^"]*"' <<<"$last" | tail -1 | cut -d'"' -f4 || true)"
    break
  fi
  sleep 2
done
if [ -n "$token" ]; then
  printf '  First start: open NoCapOS from a device on this network and create your admin.\n  From outside the local network, setup also asks for this token: %s\n\n' "$token" >"$hint"
else
  rm -f "$hint"
fi
agetty --reload 2>/dev/null || true
