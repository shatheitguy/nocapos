#!/usr/bin/env bash
# Puts NoCap Desktop's packages (the full desktop) into the image, so the
# first boot only has to brand it and switch it on: firstboot runs the
# bundled install.sh with --desktop (NOCAP_FIRSTBOOT_ARGS in
# /etc/nocapos/image.conf). The package list is the installer's own
# (desktop/lib.sh in the bundle). Run by layer.sh inside the image.
set -euo pipefail
BUNDLE=/usr/lib/nocapos/bundle
if [ ! -f "$BUNDLE/desktop/lib.sh" ]; then
  echo "    desktop: this bundle has no full desktop; the first boot sets up what it has"
  exit 0
fi
# shellcheck source=/dev/null
. "$BUNDLE/desktop/lib.sh"

family="$(desktop_family "")"
case "$family" in debian | ubuntu) ;; *) echo "    desktop: $family isn't an image base; skipped"; exit 0 ;; esac
apt-get update -q >/dev/null
want=()
# The installer's own choice (desktop_select), from what this image's
# repositories have: the same on amd64, arm64 and armhf.
while IFS= read -r line; do
  case "$line" in
    '' | \?*) ;;
    \!*) echo "    desktop: not available here: ${line#\!}" >&2; exit 1 ;;
    *) want+=("$line") ;;
  esac
done < <(
  desktop_packages "$family" | tr -d '?' | tr '|' '\n' | while IFS= read -r alt; do
    [ -n "$alt" ] && apt-cache show "$alt" >/dev/null 2>&1 && echo "$alt"
  done | desktop_select "$family"
)
echo "    desktop: ${want[*]}"
DEBIAN_FRONTEND=noninteractive apt-get install -y -q --no-install-recommends "${want[@]}"

# Off until the first boot has branded it (install.sh switches it on).
for u in gdm3.service gdm.service; do systemctl disable "$u" >/dev/null 2>&1 || true; done
rm -f /etc/systemd/system/display-manager.service
systemctl set-default multi-user.target >/dev/null 2>&1 || true
