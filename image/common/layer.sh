#!/usr/bin/env bash
# Runs once inside the image's root filesystem (live-build chroot hook on PC,
# pi-gen 01-run-chroot.sh on Raspberry Pi), after stage-overlay.sh's tree was
# copied in. Puts NoCapOS in place the way deploy/install.sh's native mode
# does, minus anything that needs a running system (starting the service,
# Docker, host packages): nocapos-firstboot.service runs the bundled
# installer for that on the first real boot.
set -euo pipefail

IMG=/usr/lib/nocapos/image
BUNDLE=/usr/lib/nocapos/bundle
# shellcheck source=/dev/null
. "$IMG/image-release"

for d in /usr/lib/nocapos /usr/share/plymouth/themes/nocapos /usr/share/grub/themes/nocapos; do
  if [ -e "$d" ]; then chown -R root:root "$d"; fi
done

echo "==> NoCapOS $NOCAP_IMAGE_VERSION ($NOCAP_IMAGE_PLATFORM, $NOCAP_IMAGE_ARCH)"

# ---------------------------------------------------------------- NoCapOS
# Same paths and settings as install.sh's native mode, so the first-boot
# run of the installer (and every later update) treats it as an update.
install -m 0755 "$BUNDLE/alfad" /usr/local/bin/nocapos
if [ -f "$BUNDLE/cap" ]; then install -m 0755 "$BUNDLE/cap" /usr/local/bin/cap; fi
install -d -m 0755 /etc/nocapos
install -d -m 0700 /var/lib/nocapos
install -d -m 0750 /srv/nocapos
if [ ! -f /etc/nocapos/nocapos.env ]; then
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
fi
chmod 600 /etc/nocapos/nocapos.env
if [ ! -f /etc/nocapos/image.conf ]; then
  cat >/etc/nocapos/image.conf <<'EOF'
# NoCapOS image settings, read by nocapos-firstboot.service.
# Install Docker (for the App Store) on the first boot: yes | no
NOCAP_DOCKER=yes
# Extra options for the bundled installer on the first boot. "--desktop" sets
# up NoCap Desktop, the full desktop on the screen; use "--no-desktop" for a
# headless server or "--desktop=kiosk" for NoCapOS full screen only (see:
# bash /usr/lib/nocapos/bundle/install.sh --help).
NOCAP_FIRSTBOOT_ARGS="--desktop"
EOF
fi
install -m 0644 "$BUNDLE/nocapos.service" /etc/systemd/system/nocapos.service
systemctl enable nocapos.service nocapos-firstboot.service nocapos-setup-hint.service

# ---------------------------------------------------------------- identity
"$IMG/os-release-sync" --divert
echo nocap >/etc/hostname
if grep -q '^127\.0\.1\.1' /etc/hosts 2>/dev/null; then
  sed -i 's/^127\.0\.1\.1.*/127.0.1.1\tnocap/' /etc/hosts
else
  printf '127.0.1.1\tnocap\n' >>/etc/hosts
fi

# Console banner until the first boot's installer writes its own (with the
# addresses of each network card). agetty reads backslashes as escapes.
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
printf '\nNoCapOS — manage this machine from https://%s/ (or http://nocap.local)\n\n' "$(cat /etc/hostname)" >/etc/motd

# ---------------------------------------------------------------- boot splash
if command -v plymouth-set-default-theme >/dev/null 2>&1 && [ -f /usr/share/plymouth/themes/nocapos/nocapos.plymouth ]; then
  plymouth-set-default-theme nocapos
fi
# Raspberry Pi: the kernel command line lives in cmdline.txt (one line).
for f in /boot/firmware/cmdline.txt /boot/cmdline.txt; do
  [ -f "$f" ] || continue
  line="$(tr -d '\r\n' <"$f")"
  for t in quiet splash loglevel=3 logo.nologo plymouth.ignore-serial-consoles; do
    case " $line " in *" $t "*) ;; *) line="$line $t" ;; esac
  done
  printf '%s\n' "$line" >"$f"
  break
done
# Raspberry Pi firmware: no rainbow square before the splash.
for f in /boot/firmware/config.txt /boot/config.txt; do
  [ -f "$f" ] || continue
  grep -q '^[[:space:]]*disable_splash=' "$f" || printf '\n[all]\ndisable_splash=1\n' >>"$f"
  break
done
if command -v update-initramfs >/dev/null 2>&1 && ls /boot/initrd.img-* >/dev/null 2>&1; then
  update-initramfs -u -k all || echo "warning: update-initramfs failed; the splash shows later in the boot" >&2
fi

# ---------------------------------------------------------------- NoCap Desktop
# The images come with the full desktop: its packages now, the NoCapOS look
# and the switch-on on the first boot (install.sh --desktop).
"$IMG/desktop-packages.sh" || echo "warning: the desktop packages didn't install; the first boot installs them" >&2

# ---------------------------------------------------------------- updates
# Security updates install themselves (52nocapos-unattended-upgrades); the
# rest is offered in Settings → Software Update.
if [ ! -x /usr/bin/unattended-upgrade ]; then
  echo "warning: unattended-upgrades is not installed; automatic security updates are off" >&2
fi

echo "==> NoCapOS layer done"
