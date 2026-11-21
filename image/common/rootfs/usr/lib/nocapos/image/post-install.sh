#!/usr/bin/env bash
# Runs inside a freshly installed NoCapOS PC, at the end of either installer:
# the graphical one (a Calamares job) or the text-mode one (debian-installer's
# late_command). Both copy the live system, so NoCapOS, its packages and NoCap
# Desktop are already in place; this makes it an installed system:
#   - Debian's standard package sources (trixie, trixie-updates,
#     trixie-security; main, contrib, non-free-firmware), nothing else
#   - NoCapOS's services and the first-boot setup switched on
#   - live-only leftovers (the installer, its settings) gone
#   - the boot menu rebuilt with NoCapOS's name and theme
#   post-install.sh calamares|d-i
set -u
IMG=/usr/lib/nocapos/image
codename="$(. /etc/os-release && echo "${VERSION_CODENAME:-trixie}")"

# ---------------------------------------------------------------- apt
# One file, Debian's own archive only (the installers write their own, with
# the install medium or backports in it).
rm -f /etc/apt/sources.list.d/debian-live-media.list /etc/apt/sources.list.d/base.list \
  /etc/apt/sources.list.d/nocapos-contrib.list
cat >/etc/apt/sources.list.d/debian.sources <<EOF
# Debian's archive (written by the NoCapOS installer). Security updates
# install themselves every day (unattended-upgrades); the rest is offered in
# Settings → Software Update.
Types: deb
URIs: http://deb.debian.org/debian
Suites: $codename $codename-updates
Components: main contrib non-free-firmware
Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg

Types: deb
URIs: http://security.debian.org/debian-security
Suites: $codename-security
Components: main contrib non-free-firmware
Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg
EOF
cat >/etc/apt/sources.list <<'EOF'
# Debian's package sources are in /etc/apt/sources.list.d/debian.sources.
EOF

# ---------------------------------------------------------------- live leftovers
# The installers remove the live-only packages (live-boot, live-config, the
# graphical installer); their settings and NoCapOS's live-session helpers go
# too.
for p in calamares calamares-settings-debian live-boot live-boot-doc \
  live-config live-config-doc live-config-systemd live-tools; do
  if dpkg-query -W -f='${db:Status-Abbrev}' "$p" 2>/dev/null | grep -q '^i'; then
    DEBIAN_FRONTEND=noninteractive apt-get purge -y -q "$p" >/dev/null 2>&1 || true
  fi
done
DEBIAN_FRONTEND=noninteractive apt-get autoremove --purge -y -q >/dev/null 2>&1 || true
rm -rf /etc/calamares "$IMG/live" /usr/lib/live/config/2000-nocapos
rm -f /etc/sudoers.d/live /etc/xdg/autostart/nocapos-live-welcome.desktop
# The live session signed its user in automatically; the installed system
# shows the sign-in screen.
if [ -f /etc/greetd/config.toml ] && grep -q '^\[initial_session\]' /etc/greetd/config.toml; then
  awk '/^\[initial_session\]/ {skip = 1; next} /^\[/ {skip = 0} !skip' /etc/greetd/config.toml >/etc/greetd/config.toml.tmp &&
    cat /etc/greetd/config.toml.tmp >/etc/greetd/config.toml
  rm -f /etc/greetd/config.toml.tmp
fi

# ---------------------------------------------------------------- NoCapOS
rm -f /var/lib/nocapos/.image-firstboot-done
systemctl enable nocapos.service nocapos-firstboot.service nocapos-setup-hint.service nocapos-images.service >/dev/null 2>&1 || true
"$IMG/os-release-sync" || true
# The first account (made by the installer, in the sudo group) is a NoCapOS
# administrator: NoCapOS signs in with the machine's Linux accounts.

# ---------------------------------------------------------------- boot
# The silent boot for this machine's own hardware (its graphics driver
# early in the initramfs), then the boot menu: nocap-boot checks grub.cfg
# and keeps the previous one if update-grub fails.
if [ -x "$IMG/boot/nocap-boot" ]; then "$IMG/boot/nocap-boot" apply --no-update || true; fi
if command -v update-initramfs >/dev/null 2>&1; then update-initramfs -u -k all >/dev/null 2>&1 || true; fi
if [ -x "$IMG/boot/nocap-boot" ]; then
  NOCAP_GRUB_CHANGED=1 "$IMG/boot/nocap-boot" apply || true
elif command -v update-grub >/dev/null 2>&1; then
  update-grub >/dev/null 2>&1 || true
fi
echo "NoCapOS: installed system ready (${1:-installer})"
exit 0
