#!/usr/bin/env bash
# nocapos-firstboot.service: finishes NoCapOS on the first boot of an image.
# NoCapOS itself is already installed and running; this runs the bundled
# installer (the same one the one-line install uses) for everything that
# needs a live system and the network: Docker, the host packages for
# virtual machines / sharing / media / ZFS, NoCap Desktop when a screen is
# plugged in, mDNS (nocap.local), the console banner and boot splash.
# Settings: /etc/nocapos/image.conf. Without network it does a minimal setup
# and tries again on the next boot.
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

args=(--yes)
if [ "$net" -eq 1 ]; then
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
if [ "$rc" -eq 0 ] && [ "$net" -eq 1 ]; then
  touch "$DONE"
  echo "NoCapOS first-boot setup: done"
fi
exit "$rc"
