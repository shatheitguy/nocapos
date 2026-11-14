#!/usr/bin/env bash
# Puts every host package NoCapOS uses into the image, so nothing has to be
# downloaded on the first boot or the first use of a feature: the installer's
# own package table (PKG_TABLE in the bundle's install.sh: virtual machines,
# sharing, remote desktop, mDNS, disks, media, backups, Linux apps), plus
# Docker, the firewall, ZFS (built with DKMS for every kernel in the image)
# and the drive-pool tools. All from Debian's own archive (main, contrib,
# non-free-firmware), nothing rebuilt. Run by layer.sh inside the image.
set -euo pipefail
BUNDLE=/usr/lib/nocapos/bundle
export DEBIAN_FRONTEND=noninteractive

arch="$(dpkg --print-architecture)"
# Installable: has a candidate version (apt-cache show also answers for
# names other packages merely mention).
avail() {
  local c
  c="$(apt-cache policy "$1" 2>/dev/null | awk '/Candidate:/ {print $2; exit}')"
  [ -n "$c" ] && [ "$c" != "(none)" ]
}
apt_install() {
  apt-get install -y -q --no-install-recommends \
    -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold "$@"
}

# contrib (ZFS lives there) on Debian's own archive. The PC image's build
# already has it; Raspberry Pi OS lists Debian with main only on some
# releases.
apt-get update -q >/dev/null
if ! avail zfs-dkms; then
  codename="$(. /etc/os-release && echo "${VERSION_CODENAME:-trixie}")"
  echo "    packages: adding Debian's contrib component for ZFS"
  cat >/etc/apt/sources.list.d/nocapos-contrib.list <<EOF
# NoCapOS: Debian's contrib component (OpenZFS). Debian's own archive.
deb http://deb.debian.org/debian $codename contrib
deb http://deb.debian.org/debian $codename-updates contrib
deb http://security.debian.org/debian-security $codename-security contrib
EOF
  apt-get update -q >/dev/null
fi

# ZFS's licence note is shown, not asked; say "seen" so nothing waits.
echo "zfs-dkms zfs-dkms/note-incompatible-licenses note" | debconf-set-selections

want=()
skipped=()
add() {
  local p
  for p in "$@"; do
    if avail "$p"; then want+=("$p"); else skipped+=("$p"); fi
  done
}

# The installer's table (apt column), so the image and install.sh never
# drift apart. Virtual machines on ARM use the ARM emulator and firmware.
table="$(sed -n "/^PKG_TABLE='/,/^'/p" "$BUNDLE/install.sh" | sed '1d;$d')"
[ -n "$table" ] || { echo "host-packages: no PKG_TABLE in $BUNDLE/install.sh" >&2; exit 1; }
while IFS= read -r line; do
  [ -n "$line" ] || continue
  list="$(cut -d'|' -f2 <<<"$line")"
  [ "$list" = - ] && continue
  for p in $list; do
    case "$arch:$p" in
      arm64:qemu-system-x86) p=qemu-system-arm ;;
      arm64:ovmf) p=qemu-efi-aarch64 ;;
    esac
    add "$p"
  done
done <<<"$table"

# Docker from Debian (the App Store), the firewall (Shield), drive pools.
# (Debian 13 ships the docker command in docker-cli, only recommended by
# docker.io, and these installs skip recommends.)
add docker.io ufw mergerfs snapraid
if avail docker-cli; then add docker-cli; fi
if avail docker-compose; then add docker-compose; fi

# ZFS: the kernel headers of every kernel flavour in the image, so DKMS
# builds the module now and again after each Debian kernel update.
headers=()
while IFS= read -r img; do
  flavour="${img#linux-image-}"
  if avail "linux-headers-$flavour"; then headers+=("linux-headers-$flavour"); fi
done < <(dpkg-query -W -f='${db:Status-Abbrev} ${Package}\n' 'linux-image-*' 2>/dev/null |
  awk '$1 == "ii" {print $2}' | grep -E '^linux-image-(amd64|arm64|rpi-[a-z0-9]+)$' || true)
[ "${#headers[@]}" -gt 0 ] || { echo "host-packages: no kernel headers package for this image's kernel" >&2; exit 1; }
add "${headers[@]}" dkms zfs-dkms zfsutils-linux

[ "${#skipped[@]}" -eq 0 ] || echo "    packages: not in this image's archive, skipped: ${skipped[*]}"
echo "    packages: ${want[*]}"
# Package installs mustn't start services in the image (live-build and
# pi-gen keep a policy-rc.d in place already; this covers both).
apt_install "${want[@]}"

# Build ZFS for every kernel that has its headers (the image's kernels, not
# the build machine's), and fail the build if one didn't get it.
for build in /lib/modules/*/build; do
  [ -e "$build" ] || continue
  kver="$(basename "$(dirname "$build")")"
  echo "    zfs: building the module for kernel $kver"
  dkms autoinstall -k "$kver"
  if ! dkms status -k "$kver" zfs 2>/dev/null | grep -q ': installed'; then
    dkms status >&2 || true
    echo "host-packages: ZFS didn't build for kernel $kver" >&2
    exit 1
  fi
done
dkms status

# Services as after install.sh (enable_host_services, ensure_docker); the
# rest keep Debian's defaults, the same as on any other install.
systemctl enable docker.service libvirtd.service avahi-daemon.service >/dev/null 2>&1 || true

# The unprivileged account streamed Linux apps run as (install.sh's
# setup_apps_account).
if ! id nocap-apps >/dev/null 2>&1; then
  useradd --system --user-group --create-home --home-dir /var/lib/nocap-apps \
    --shell /usr/sbin/nologin --comment "NoCapOS apps" nocap-apps
fi
mkdir -p /var/lib/nocap-apps/homes
chown nocap-apps:nocap-apps /var/lib/nocap-apps /var/lib/nocap-apps/homes
chmod 0755 /var/lib/nocap-apps
chmod 0711 /var/lib/nocap-apps/homes

# Tells firstboot (and the checks) that the packages are in the image.
printf '%s\n' "${want[@]}" >/usr/lib/nocapos/image/preinstalled
