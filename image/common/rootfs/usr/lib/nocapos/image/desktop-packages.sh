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
while IFS= read -r line; do
  [ -n "$line" ] || continue
  line="${line#\?}"
  for alt in ${line//|/ }; do
    if apt-cache show "$alt" >/dev/null 2>&1; then want+=("$alt"); break; fi
  done
done < <(desktop_packages "$family")
echo "    desktop: ${want[*]}"
DEBIAN_FRONTEND=noninteractive apt-get install -y -q --no-install-recommends "${want[@]}"

# Off until the first boot has branded it (install.sh switches it on).
for u in gdm3.service gdm.service; do systemctl disable "$u" >/dev/null 2>&1 || true; done
rm -f /etc/systemd/system/display-manager.service
systemctl set-default multi-user.target >/dev/null 2>&1 || true
