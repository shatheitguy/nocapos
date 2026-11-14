#!/usr/bin/env bash
# Checks a built NoCapOS image's system (run by the image builds; also works
# on an installed machine):
#   - Debian stays Debian: the next Debian updates apply cleanly (simulated
#     full-upgrade, no held or kept-back packages), no apt pinning, package
#     sources are the distribution's own, automatic security updates on
#   - NoCapOS's branding files belong to no package (Debian updates never
#     overwrite them) and os-release is diverted, not edited
#   - everything preinstalled works: ZFS (built by DKMS for every kernel in
#     the image), firewall, virtual machines, Docker, NoCap Desktop on
#
#   check-image.sh --chroot DIR   check the system in DIR (mounts what it needs)
#   check-image.sh                check this system
set -uo pipefail

if [ "${1:-}" = --chroot ]; then
  root="${2:?--chroot needs a directory}"
  self="$(readlink -f "$0")"
  cp "$self" "$root/tmp/nocapos-check-image.sh"
  had_resolv=0
  if [ -e "$root/etc/resolv.conf" ] || [ -L "$root/etc/resolv.conf" ]; then
    had_resolv=1
    mv "$root/etc/resolv.conf" "$root/etc/resolv.conf.nocapos-check"
  fi
  cp -L /etc/resolv.conf "$root/etc/resolv.conf"
  mount -t proc proc "$root/proc"
  mount -t sysfs sys "$root/sys"
  mount --bind /dev "$root/dev"
  mount --bind /dev/pts "$root/dev/pts"
  chroot "$root" /bin/bash /tmp/nocapos-check-image.sh
  rc=$?
  umount "$root/dev/pts" "$root/dev" "$root/sys" "$root/proc" 2>/dev/null || umount -l "$root/dev/pts" "$root/dev" "$root/sys" "$root/proc" 2>/dev/null || true
  rm -f "$root/etc/resolv.conf" "$root/tmp/nocapos-check-image.sh"
  if [ "$had_resolv" -eq 1 ]; then mv "$root/etc/resolv.conf.nocapos-check" "$root/etc/resolv.conf"; fi
  exit "$rc"
fi

fails=0
ok() { printf '  ok    %s\n' "$*"; }
bad() { printf '  FAIL  %s\n' "$*"; fails=$((fails + 1)); }
section() { printf '\n== %s\n' "$*"; }
export DEBIAN_FRONTEND=noninteractive

lists_before="$(find /var/lib/apt/lists -maxdepth 1 -name '*Packages*' 2>/dev/null | head -1)"

section "Debian updates"
if apt-get update -q >/tmp/nocapos-check-update.log 2>&1; then ok "apt-get update"; else bad "apt-get update failed:"; tail -20 /tmp/nocapos-check-update.log; fi
sim="$(apt-get -s -o Debug::NoLocking=1 full-upgrade 2>&1)"
src=$?
printf '%s\n' "$sim" | grep -E '^[0-9]+ upgraded' || true
if [ "$src" -ne 0 ]; then bad "apt-get -s full-upgrade failed"; printf '%s\n' "$sim" | tail -30; else ok "simulated full-upgrade resolves"; fi
if printf '%s\n' "$sim" | grep -qiE 'kept back|have been held|broken packages|unmet dependencies'; then
  bad "packages held, kept back or broken:"; printf '%s\n' "$sim" | grep -iE -A3 'kept back|held|broken|unmet'
else
  ok "nothing held, kept back or broken"
fi
removals="$(printf '%s\n' "$sim" | grep -c '^Remv ' || true)"
if [ "$removals" -gt 0 ]; then bad "the upgrade would remove packages:"; printf '%s\n' "$sim" | grep '^Remv '; else ok "the upgrade removes nothing"; fi
holds="$(apt-mark showhold 2>/dev/null)"
if [ -n "$holds" ]; then bad "held packages: $holds"; else ok "no held packages"; fi
if grep -rqsiE '^[[:space:]]*Pin' /etc/apt/preferences /etc/apt/preferences.d/ 2>/dev/null; then
  bad "apt pinning found:"; grep -rsiE -B2 '^[[:space:]]*Pin' /etc/apt/preferences /etc/apt/preferences.d/
else
  ok "no apt pinning"
fi
foreign="$(grep -rhsE '^[[:space:]]*(deb|URIs:)' /etc/apt/sources.list /etc/apt/sources.list.d/ |
  grep -vE 'deb\.debian\.org|security\.debian\.org|archive\.raspberrypi\.(com|org)|raspbian\.raspberrypi\.(com|org)|file:/' || true)"
if [ -n "$foreign" ]; then bad "package sources other than the distribution's:"; printf '%s\n' "$foreign"; else ok "package sources: the distribution's own"; fi
if [ -x /usr/bin/unattended-upgrade ] && grep -qs 'Unattended-Upgrade "1"' /etc/apt/apt.conf.d/21nocapos-auto-upgrades; then
  ok "automatic security updates on"
else
  bad "unattended-upgrades missing or off"
fi
kern="$(dpkg-query -W -f='${Package} ${Version}\n' 'linux-image-*' 2>/dev/null | grep -v '^linux-image-[a-z0-9-]* $' || true)"
printf '  kernel packages:\n%s\n' "$(printf '%s\n' "$kern" | sed 's/^/    /')"

section "NoCapOS files belong to no package"
for f in /etc/default/grub.d/nocapos.cfg /usr/share/grub/themes/nocapos/theme.txt \
  /usr/share/plymouth/themes/nocapos/nocapos.plymouth /usr/share/plymouth/themes/nocapos/nocapos.script \
  /etc/apt/apt.conf.d/99nocapos-os-release /etc/apt/apt.conf.d/52nocapos-unattended-upgrades \
  /etc/apt/apt.conf.d/21nocapos-auto-upgrades /usr/lib/nocapos/image/os-release-sync \
  /usr/share/glib-2.0/schemas/90_nocapos.gschema.override /etc/dconf/db/local.d/00-nocapos \
  /etc/dconf/db/gdm.d/90-nocapos /usr/share/wayland-sessions/nocapos.desktop \
  /usr/share/icons/hicolor/256x256/apps/nocapos.png /usr/local/bin/nocapos /etc/systemd/system/nocapos.service; do
  [ -e "$f" ] || continue
  if owner="$(dpkg-query -S "$f" 2>/dev/null)"; then bad "$f is owned by a package: $owner"; else ok "$f"; fi
done
if dpkg-divert --list 2>/dev/null | grep -q 'os-release'; then ok "os-release diverted (dpkg-divert)"; else bad "os-release isn't diverted"; fi
if grep -qs '^NAME="NoCapOS"' /usr/lib/os-release && grep -qs '^ID=debian' /usr/lib/os-release; then
  ok "os-release: NoCapOS, ID=debian"
else
  bad "os-release isn't NoCapOS's"
fi

section "Preinstalled features"
if [ -s /usr/lib/nocapos/image/preinstalled ]; then ok "$(wc -l </usr/lib/nocapos/image/preinstalled) host packages preinstalled"; else bad "no preinstalled package list"; fi
run() { # NAME COMMAND...
  local out
  if out="$("${@:2}" 2>&1)"; then ok "$1: $(printf '%s' "$out" | head -1)"; else bad "$1: $(printf '%s' "$out" | head -3)"; fi
}
zv="$(zfs version 2>&1 | grep -m1 '^zfs-[0-9]' || true)"
if [ -n "$zv" ]; then ok "zfs version: $zv"; else bad "zfs version: $(zfs version 2>&1 | head -2)"; fi
run "ufw" ufw --version
run "virsh" virsh --version
run "docker" docker --version
run "qemu-img" qemu-img --version
run "smbd" smbd --version
run "ffmpeg" ffmpeg -hide_banner -version
run "rclone" rclone version
run "restic" restic version
if grep -qx guacd /usr/lib/nocapos/image/preinstalled 2>/dev/null; then run "guacd" sh -c 'command -v guacd'; fi
run "flatpak" flatpak --version
for build in /lib/modules/*/build; do
  [ -e "$build" ] || continue
  k="$(basename "$(dirname "$build")")"
  if dkms status -k "$k" zfs 2>/dev/null | grep -q ': installed'; then ok "ZFS module built for kernel $k (dkms)"; else bad "ZFS module not built for kernel $k"; dkms status; fi
done
n=0
for m in /lib/modules/*/updates/dkms/zfs.ko*; do [ -e "$m" ] && n=$((n + 1)); done
if [ "$n" -gt 0 ]; then ok "zfs.ko present for $n kernel(s)"; else bad "no zfs.ko in /lib/modules/*/updates/dkms"; fi

section "Bundled container images"
# The remote desktop engine (guacd) ships as a docker-load archive and is
# loaded on the first boot (nocapos-images.service), which then deletes it.
arch="$(dpkg --print-architecture)"
gz="/usr/lib/nocapos/images/guacd-$arch.tar.zst"
if [ -f "$gz" ]; then
  if zstd -q -t "$gz" 2>/dev/null; then ok "guacd archive intact: $(basename "$gz") ($(($(stat -c %s "$gz") / 1048576)) MiB)"; else bad "guacd archive corrupt: $gz"; fi
  entries="$(zstd -dc "$gz" 2>/dev/null | tar -t 2>/dev/null)"
  if printf '%s\n' "$entries" | grep -qx manifest.json; then
    ok "guacd archive lists $(printf '%s\n' "$entries" | grep -c .) entries"
    manifest="$(zstd -dc "$gz" | tar -xO manifest.json 2>/dev/null)"
    tag="$(printf '%s' "$manifest" | grep -oE '"RepoTags": *\[ *"[^"]*"' | sed 's/.*"\([^"]*\)"$/\1/')"
    config="$(printf '%s' "$manifest" | grep -oE '"Config": *"[^"]*"' | sed 's/.*"\([^"]*\)"$/\1/')"
    case "$tag" in guacamole/guacd:*) ok "guacd archive tag: $tag" ;; *) bad "guacd archive tag: '${tag:-none}'" ;; esac
    if [ -n "$config" ] && zstd -dc "$gz" | tar -xO "$config" 2>/dev/null | grep -Eq "\"architecture\": *\"$arch\""; then
      ok "guacd archive is for $arch"
    else
      bad "guacd archive isn't for $arch"
    fi
  else
    bad "guacd archive has no manifest.json"
  fi
  if systemctl is-enabled --quiet nocapos-images.service 2>/dev/null; then ok "nocapos-images.service on"; else bad "nocapos-images.service not enabled"; fi
  # Where a Docker daemon runs (an installed machine; not the build chroot),
  # check that it loads.
  if docker info >/dev/null 2>&1; then
    if out="$(zstd -dc "$gz" | docker load 2>&1)"; then ok "docker load: $(printf '%s' "$out" | tail -1)"; else bad "docker load: $(printf '%s' "$out" | tail -2)"; fi
  else
    echo "  (no Docker daemon here; docker load not tried)"
  fi
elif [ -n "$(docker images -q 'guacamole/guacd' 2>/dev/null)" ]; then
  ok "guacd image loaded into Docker"
else
  bad "no guacd archive ($gz) and no guacd image in Docker"
fi

section "NoCap Desktop"
dm="$(basename "$(readlink -f /etc/systemd/system/display-manager.service 2>/dev/null)" 2>/dev/null)"
case "$dm" in gdm3.service | gdm.service) ok "sign-in screen at boot: $dm" ;; *) bad "no GDM display-manager link (got '${dm:-none}')" ;; esac
[ "$(readlink -f /etc/systemd/system/default.target 2>/dev/null)" = "$(readlink -f /lib/systemd/system/graphical.target)" ] &&
  ok "boots to graphical.target" || bad "default target isn't graphical.target"
[ -f /usr/share/wayland-sessions/nocapos.desktop ] && ok "NoCapOS session" || bad "NoCapOS session file missing"
command -v gnome-shell >/dev/null && ok "GNOME Shell" || bad "gnome-shell missing"

if [ -z "$lists_before" ]; then rm -rf /var/lib/apt/lists/* 2>/dev/null || true; fi
apt-get clean >/dev/null 2>&1 || true

echo
if [ "$fails" -gt 0 ]; then
  echo "image check: $fails problem(s)"
  exit 1
fi
echo "image check: all good"
