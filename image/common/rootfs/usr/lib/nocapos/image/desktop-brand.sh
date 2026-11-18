#!/usr/bin/env bash
# Puts NoCap Desktop in place in the image and switches it on, at build
# time: the same steps the bundled installer's install_full_desktop runs
# (desktop/setup.sh): NoCap Shell's programs and pages, the session, greetd
# with NoCap's sign-in screen; minus the ones that need a running system
# (memory tuning, restarting services), which the first boot's
# install.sh --desktop does as an update. So the live session of the ISO and
# the first start of an installed system go straight to NoCap Desktop.
# Run by layer.sh inside the image, after desktop-packages.sh.
set -uo pipefail
BUNDLE_DIR=/usr/lib/nocapos/bundle
ETC=/etc/nocapos
DATA=/var/lib/nocapos
BRANDING=1
VERSION="$(cat "$BUNDLE_DIR/VERSION" 2>/dev/null || echo dev)"
case "$(dpkg --print-architecture)" in
  amd64) ARCH=amd64 ;; arm64) ARCH=arm64 ;; *) ARCH=armv7 ;;
esac
export BUNDLE_DIR ETC DATA BRANDING ARCH VERSION

if [ ! -f "$BUNDLE_DIR/desktop/lib.sh" ] || [ ! -f "$BUNDLE_DIR/desktop/setup.sh" ]; then
  echo "    desktop: this bundle has no NoCap Desktop files; the first boot sets it up"
  exit 0
fi
command -v labwc >/dev/null 2>&1 || { echo "    desktop: labwc isn't in the image; the first boot sets it up"; exit 0; }

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
pkg_install_group() {
  shift 3
  DEBIAN_FRONTEND=noninteractive apt-get install -y -q --no-install-recommends "$@"
}
pkg_not_available() { shift; warn "desktop: not available here: $*"; }
guacd_note() { :; }

# shellcheck source=/dev/null
. "$BUNDLE_DIR/desktop/lib.sh"
# shellcheck source=/dev/null
. "$BUNDLE_DIR/desktop/setup.sh"

fail=0
step() {
  if declare -F "$1" >/dev/null; then
    "$@" || { warn "desktop: $1 didn't finish"; fail=1; }
  fi
}

mkdir -p "$ETC" "$DATA"
family="$(desktop_family "")"
step desk_install_assets
step desk_install_shell apt "$family"
step desk_session_files "$family"
step desk_accent
step desk_clipboard
step desk_units
step desk_hide_updaters
step desk_identity
step desk_take_display_manager
step desk_greetd "$family"
step desk_write_conf
[ "$fail" -eq 0 ] || exit 1

# Switched on: greetd starts at boot with NoCap's sign-in screen.
systemctl set-default graphical.target >/dev/null 2>&1 || true
systemctl enable --force "$DESK_DM" >/dev/null 2>&1 || true
step desk_link_display_manager "$DESK_DM"
echo full >"$ETC/desktop-mode"
if [ "$(basename "$(readlink -f /etc/systemd/system/display-manager.service 2>/dev/null)")" != "$DESK_DM" ]; then
  warn "desktop: the sign-in screen isn't set to start at boot"
  exit 1
fi
for f in "$SHELL_LIB/nocap-shell" "$SHELL_LIB/nocap-lock" "$SHELL_UI/shell.html"; do
  [ -e "$f" ] || { warn "desktop: $f is missing"; exit 1; }
done
echo "    desktop: NoCap Desktop on ($DESK_DM, NoCap Shell $("$SHELL_LIB/nocap-shell" --version 2>/dev/null))"
