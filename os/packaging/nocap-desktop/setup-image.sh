#!/bin/bash
# setup-image.sh: puts NoCap Desktop in place on a NoCapOS image, with the
# same steps a script install takes (deploy/desktop/setup.sh), minus what an
# image doesn't do: no package installs (the image has them), no build on
# the machine (the bundle's programs are built for the image), nothing
# depending on this machine's memory, screen or earlier desktops.
# Run by the nocap-desktop package's postinst.
set -o pipefail

BUNDLE_DIR=/usr/share/nocapos/desktop-bundle
ETC=/etc/nocapos
DATA=/var/lib/nocapos
BRANDING=1
VERSION="$(dpkg-query -W -f='${Version}' nocap-desktop 2>/dev/null || echo dev)"
ARCH="$(dpkg --print-architecture)"
export BUNDLE_DIR ETC DATA BRANDING VERSION ARCH

# install.sh's helpers that setup.sh uses.
info() { printf '==> %s\n' "$*"; }
warn() { printf 'WARNING: %s\n' "$*" >&2; }
has() { command -v "$1" >/dev/null 2>&1; }
pkg_manager() { echo apt; }
pkg_available() { return 1; }
pkg_install() { return 1; }
pkg_install_group() { return 1; }
pkg_refresh() { return 0; }
nvidia_proprietary() { return 1; }

# shellcheck source=/dev/null
. "$BUNDLE_DIR/desktop/lib.sh"
# shellcheck source=/dev/null
. "$BUNDLE_DIR/desktop/setup.sh"

mkdir -p "$ETC" "$DATA"
# The clipboard service runs cap from where script installs keep it.
[ -e /usr/local/bin/cap ] || { mkdir -p /usr/local/bin; ln -s /usr/bin/cap /usr/local/bin/cap; }

# must STEP...: the image build fails when a step that the screen needs fails.
must() { "$@" || { warn "NoCap Desktop: $1 failed"; exit 1; }; }
may() { "$@" || warn "NoCap Desktop: $1 didn't finish (not needed to start)"; }

must desk_install_assets
must desk_install_shell apt debian
must desk_session_files debian
may desk_accent
must desk_clipboard
may desk_units
may desk_identity
may desk_take_display_manager
must desk_greetd debian
must desk_write_conf
must desk_link_display_manager "$DESK_DM"
systemctl set-default graphical.target >/dev/null 2>&1 || ln -sf /usr/lib/systemd/system/graphical.target /etc/systemd/system/default.target
printf 'PRETTY_HOSTNAME=NoCapOS\n' >/etc/machine-info
may desk_verify
# The install's own notes (for uninstalling on a script install) aren't
# needed on an image: the image is replaced whole.
rm -rf "$DESK_BACKUP"
echo "NoCap Desktop is in place"
