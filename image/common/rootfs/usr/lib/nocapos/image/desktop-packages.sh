#!/usr/bin/env bash
# Puts NoCap Desktop's packages into the image; desktop-brand.sh then puts
# NoCap Shell in place and switches the desktop on, and the first boot's
# install.sh --desktop (NOCAP_FIRSTBOOT_ARGS in /etc/nocapos/image.conf)
# only updates it. The package list is the installer's own (desktop/lib.sh
# in the bundle). Run by layer.sh inside the image.
set -euo pipefail
BUNDLE=/usr/lib/nocapos/bundle
if [ ! -f "$BUNDLE/desktop/lib.sh" ]; then
  echo "    desktop: this bundle has no NoCap Desktop; the first boot sets up what it has"
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
# No service starts inside the image build.
DEBIAN_FRONTEND=noninteractive apt-get install -y -q --no-install-recommends "${want[@]}"

# Off until desktop-brand.sh has put NoCap Shell in place and switches it
# on (it makes the display-manager.service link itself).
systemctl disable greetd.service >/dev/null 2>&1 || true
rm -f /etc/systemd/system/display-manager.service
systemctl set-default multi-user.target >/dev/null 2>&1 || true
