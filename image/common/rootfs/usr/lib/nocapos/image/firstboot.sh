#!/usr/bin/env bash
# nocapos-firstboot.service: finishes NoCapOS on the first boot of an image.
# NoCapOS itself is already installed and running; this runs the bundled
# installer (the same one the one-line install uses) for everything that
# needs a running system: services, mDNS (nocap.local), the console banner,
# the boot splash and NoCap Desktop's memory tuning. The images carry every
# package already (host-packages.sh), so this needs no downloads; an image
# without them installs them here with the network (without one it does a
# minimal setup and tries again on the next boot).
# Settings: /etc/nocapos/image.conf.
set -u

DONE=/var/lib/nocapos/.image-firstboot-done
BUNDLE=/usr/lib/nocapos/bundle
NOCAP_DOCKER=yes
NOCAP_FIRSTBOOT_ARGS="--desktop"
# shellcheck source=/dev/null
[ -f /etc/nocapos/image.conf ] && . /etc/nocapos/image.conf

online() { curl -fsS --max-time 10 -o /dev/null https://deb.debian.org/ 2>/dev/null; }

echo "NoCapOS first-boot setup: waiting for the network"
net=0
for _ in $(seq 1 45); do
  if online; then net=1; break; fi
  sleep 2
done

# Images built with every package already in them (host-packages.sh writes
# this list): nothing is downloaded. Docker (Debian's docker.io) is there,
# and the installer only checks the rest and sets it up.
PRE=/usr/lib/nocapos/image/preinstalled
args=(--yes)
if [ -s "$PRE" ]; then
  # --minimal: the host packages are there; skip the installer's package
  # step (it would fetch package lists and newer versions).
  args+=(--no-docker --minimal)
  if [ "$NOCAP_DOCKER" = yes ] && [ -x /usr/bin/dockerd ]; then systemctl enable --now docker.service >/dev/null 2>&1 || true; fi
  systemctl enable --now libvirtd.service avahi-daemon.service >/dev/null 2>&1 || true
  if command -v virsh >/dev/null 2>&1; then
    virsh net-autostart default >/dev/null 2>&1 || true
    virsh net-start default >/dev/null 2>&1 || true
  fi
  if [ "$net" -eq 1 ] && command -v flatpak >/dev/null 2>&1; then
    flatpak remote-add --if-not-exists --system flathub https://dl.flathub.org/repo/flathub.flatpakrepo >/dev/null 2>&1 || true
  fi
elif [ "$net" -eq 1 ]; then
  if [ "$NOCAP_DOCKER" = yes ]; then args+=(--install-docker); else args+=(--no-docker); fi
else
  echo "NoCapOS first-boot setup: no network; minimal setup now, the rest on a later boot"
  args+=(--no-docker --minimal)
fi
# shellcheck disable=SC2206 # word splitting of the extra options is wanted
extra=($NOCAP_FIRSTBOOT_ARGS)

bash "$BUNDLE/install.sh" "${args[@]}" "${extra[@]}"
rc=$?

# install.sh restarted NoCapOS, so the first-run setup token is new.
systemctl --no-block restart nocapos-setup-hint.service 2>/dev/null || true
if [ "$rc" -eq 0 ] && { [ "$net" -eq 1 ] || [ -s "$PRE" ]; }; then
  touch "$DONE"
  echo "NoCapOS first-boot setup: done"
fi
exit "$rc"
