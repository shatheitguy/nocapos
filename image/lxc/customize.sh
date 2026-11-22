#!/usr/bin/env bash
# Runs once inside the container template's root filesystem (build.sh,
# mmdebstrap's chrooted customize hook), with the build's apt sources. The
# NoCapOS layer and the first-boot pieces are already copied in (build.sh's
# stage); this installs the packages and puts NoCapOS in place the way
# deploy/install.sh's native mode does. Everything that needs a running
# system (starting services, the console banner with the addresses, mDNS)
# is done on the first boot by nocapos-firstboot.service.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
B=/tmp/nocap-build
BUNDLE=/usr/lib/nocapos/bundle
IMG=/usr/lib/nocapos/image
# shellcheck source=/dev/null
. "$IMG/image-release"
echo "==> NoCapOS $NOCAP_IMAGE_VERSION container template ($NOCAP_IMAGE_ARCH)"

# Nothing starts while packages install in the chroot.
printf '#!/bin/sh\nexit 101\n' >/usr/sbin/policy-rc.d
chmod 755 /usr/sbin/policy-rc.d

# ---------------------------------------------------------------- packages
mapfile -t pkgs < <(bash "$B/resolve-packages.sh" "$B/packages.list")
echo "Installing ${#pkgs[@]} packages: ${pkgs[*]}"
apt-get install -y --no-install-recommends -o Dpkg::Options::=--force-confold "${pkgs[@]}"

# Started by NoCapOS when the feature is turned on (Sharing, Shield).
for unit in smbd nmbd samba-ad-dc ufw; do
  systemctl disable "$unit" 2>/dev/null || true
done
# On from the start: NoCapOS, the network the host sets up, mDNS, Docker.
systemctl enable networking.service avahi-daemon.service docker.service

# avahi-daemon limits itself to a few processes, which an unprivileged
# container counts against the host's mapped user and then refuses to start.
sed -i 's/^[[:space:]]*rlimit-nproc[[:space:]]*=/#&/' /etc/avahi/avahi-daemon.conf

# ---------------------------------------------------------------- NoCapOS
# Same paths and settings as install.sh's native mode, so the first-boot run
# of the installer (and every later update, from Settings or `cap update`)
# treats it as an update.
chown -R root:root /usr/lib/nocapos
install -m 0755 "$BUNDLE/alfad" /usr/local/bin/nocapos
if [ -f "$BUNDLE/cap" ]; then install -m 0755 "$BUNDLE/cap" /usr/local/bin/cap; fi
install -d -m 0755 /etc/nocapos
install -d -m 0700 /var/lib/nocapos
install -d -m 0750 /srv/nocapos
cat >/etc/nocapos/nocapos.env <<'EOF'
# NoCapOS settings — see README for all ALFA_* options. Restart after edits:
#   systemctl restart nocapos
ALFA_LISTEN_ADDR=:443
ALFA_TLS=auto
ALFA_DATA_DIR=/var/lib/nocapos
ALFA_FILE_ROOTS="NoCap Drive=/srv/nocapos"
# Whole filesystem in Files as "System" (core OS folders are protected).
ALFA_SYSTEM_ROOT=true
EOF
chmod 600 /etc/nocapos/nocapos.env
# NoCap Desktop stays off in a container (no screen), across updates too.
echo user-off >/etc/nocapos/desktop-mode
install -m 0644 "$BUNDLE/nocapos.service" /etc/systemd/system/nocapos.service
systemctl enable nocapos.service nocapos-firstboot.service nocapos-setup-hint.service
# The packages are in the template: the first boot only sets NoCapOS up,
# without downloading anything (firstboot.sh reads this list).
printf '%s\n' "${pkgs[@]}" >"$IMG/preinstalled"

# ---------------------------------------------------------------- identity
"$IMG/os-release-sync" --divert
echo nocap >/etc/hostname
cat >/etc/hosts <<'EOF'
127.0.0.1       localhost
127.0.1.1       nocap
::1             localhost ip6-localhost ip6-loopback
ff02::1         ip6-allnodes
ff02::2         ip6-allrouters
EOF
# Console banner until the first boot's installer writes its own (with the
# container's address). agetty reads backslashes as escapes.
{
  cat <<'LOGO' | sed 's/\\/\\\\/g'
  _   _        ____             ___  ____
 | \ | | ___  / ___|__ _ _ __  / _ \/ ___|
 |  \| |/ _ \| |   / _` | '_ \| | | \___ \
 | |\  | (_) | |__| (_| | |_) | |_| |___) |
 |_| \_|\___/ \____\__,_| .__/ \___/|____/
                        |_|
LOGO
  echo
  echo "  Open NoCapOS in a browser on your network:"
  echo "    https://\\4"
  echo "    http://nocap.local"
  echo
  echo "  \\S{PRETTY_NAME}  \\n  \\l"
  echo
} >/etc/issue
echo "NoCapOS" >/etc/issue.net
printf '\nNoCapOS — manage this server from https://<its address>/ (or http://nocap.local)\n\n' >/etc/motd

# ---------------------------------------------------------------- updates
# Debian's own mirrors from here on (the build used a fixed snapshot);
# security updates install themselves (52nocapos-unattended-upgrades).
cat >/etc/apt/sources.list.d/debian.sources <<'EOF'
Types: deb
URIs: http://deb.debian.org/debian
Suites: trixie trixie-updates
Components: main
Signed-By: /usr/share/keyrings/debian-archive-keyring.pgp

Types: deb
URIs: http://security.debian.org/debian-security
Suites: trixie-security
Components: main
Signed-By: /usr/share/keyrings/debian-archive-keyring.pgp
EOF
[ -f /usr/share/keyrings/debian-archive-keyring.pgp ] || sed -i 's/keyring\.pgp$/keyring.gpg/' /etc/apt/sources.list.d/debian.sources

# ---------------------------------------------------------------- clean up
# Each container makes its own machine id and keys on its first boot.
apt-get clean
rm -rf /var/lib/apt/lists/* /var/cache/apt/*.bin /var/cache/debconf/*-old /var/lib/dpkg/*-old
: >/etc/machine-id
rm -f /var/lib/dbus/machine-id /etc/ssh/ssh_host_* /var/lib/systemd/random-seed
find /var/log -type f -delete
rm -f /usr/sbin/policy-rc.d
echo "==> NoCapOS container layer done"
