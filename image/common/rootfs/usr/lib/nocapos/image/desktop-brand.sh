#!/usr/bin/env bash
# Gives the image NoCap Desktop's look and switches it on, at build time:
# the same steps the bundled installer's install_full_desktop runs
# (desktop/setup.sh), minus the ones that need a running system (memory
# tuning, restarting services), which the first boot's install.sh --desktop
# does as an update. So the live session of the ISO and the first start of
# an installed system go straight to the NoCapOS sign-in screen and desktop.
# Run by layer.sh inside the image, after desktop-packages.sh.
set -uo pipefail
BUNDLE_DIR=/usr/lib/nocapos/bundle
ETC=/etc/nocapos
DATA=/var/lib/nocapos
BRANDING=1
case "$(dpkg --print-architecture)" in
  amd64) ARCH=amd64 ;; arm64) ARCH=arm64 ;; *) ARCH=armv7 ;;
esac
export BUNDLE_DIR ETC DATA BRANDING ARCH

if [ ! -f "$BUNDLE_DIR/desktop/lib.sh" ] || [ ! -f "$BUNDLE_DIR/desktop/setup.sh" ]; then
  echo "    desktop: this bundle has no NoCap Desktop files; the first boot sets it up"
  exit 0
fi
command -v gnome-shell >/dev/null 2>&1 || { echo "    desktop: GNOME Shell isn't in the image; the first boot sets it up"; exit 0; }

# The helpers setup.sh borrows from install.sh.
info() { printf '==> %s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
has() { command -v "$1" >/dev/null 2>&1; }
pkg_manager() { echo apt; }
pkg_refresh() { apt-get update -q >/dev/null 2>&1; }
pkg_available() { apt-cache show "$2" >/dev/null 2>&1; }
pkg_install() {
  shift
  DEBIAN_FRONTEND=noninteractive apt-get install -y -q -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold "$@"
}

# shellcheck source=/dev/null
. "$BUNDLE_DIR/desktop/lib.sh"
# shellcheck source=/dev/null
. "$BUNDLE_DIR/desktop/setup.sh"

step() {
  if declare -F "$1" >/dev/null; then
    "$@" || warn "desktop: $1 didn't finish"
  fi
}

mkdir -p "$ETC" "$DATA"
step desk_install_browser apt
step desk_install_assets
step desk_install_extension
step desk_settings
step desk_accent
step desk_units
step desk_hide_updaters
step desk_identity
dm="$(gdm_unit "")"
conf="$(gdm_config "")"
step desk_take_display_manager "$dm"
step desk_session "$conf"
step desk_write_conf "$dm" "$conf"

# Switched on: GDM starts at boot into the NoCapOS session.
systemctl set-default graphical.target >/dev/null 2>&1 || true
systemctl enable --force "$dm" >/dev/null 2>&1 || true
step desk_link_display_manager "$dm"
echo full >"$ETC/desktop-mode"
if [ "$(basename "$(readlink -f /etc/systemd/system/display-manager.service 2>/dev/null)")" != "$dm" ]; then
  warn "desktop: the sign-in screen isn't set to start at boot"
  exit 1
fi
echo "    desktop: NoCap Desktop on ($dm, session $NOCAP_SESSION)"
