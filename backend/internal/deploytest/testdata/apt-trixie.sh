#!/usr/bin/env bash
# install.sh's package helpers against a real apt, in a Debian trixie
# container (CI runs it; root inside a throwaway container only):
#
#   docker run --rm -v "$PWD:/src:ro" debian:trixie bash /src/backend/internal/deploytest/testdata/apt-trixie.sh
#
# It breaks the package system the way a Raspberry Pi's was (a kernel
# headers meta-package whose versioned headers package is missing), leaves
# a package half configured, holds the dpkg lock, and checks what the
# helpers do and say about each.
set -euo pipefail
SRC="${SRC:-/src}"
[ "$(id -u)" -eq 0 ] && { [ -f /.dockerenv ] || [ -f /run/.containerenv ]; } || {
  echo "run this in a throwaway container only" >&2
  exit 2
}
export DEBIAN_FRONTEND=noninteractive
W=/tmp/nocap-apt-test
rm -rf "$W" && mkdir -p "$W"
sed -n '/^# >>> package helpers/,/^# <<< package helpers/p' "$SRC/deploy/install.sh" | tr -d '\r' >"$W/helpers.sh"
grep -q 'pkg_install_group()' "$W/helpers.sh" || { echo "FAIL: no package helpers in install.sh"; exit 1; }

FAILS=0
fail() { echo "FAIL: $*"; FAILS=$((FAILS + 1)); }
expect() { # OUTPUT-FILE TEXT...
  local f="$1" t
  shift
  for t in "$@"; do grep -qF -- "$t" "$f" || fail "output lacks: $t"; done
}
reject() {
  local f="$1" t
  shift
  for t in "$@"; do ! grep -qF -- "$t" "$f" || fail "output has: $t"; done
}

# helpers SCRIPT: runs SCRIPT after the helpers, output to $W/out (and shown).
helpers() {
  cat >"$W/run.sh" <<EOF
set -euo pipefail
NAME=NoCapOS
ASSUME_YES="\${ASSUME_YES:-0}"
info() { printf '==> %s\n' "\$*"; }
warn() { printf 'warning: %s\n' "\$*" >&2; }
has() { command -v "\$1" >/dev/null 2>&1; }
. "$W/helpers.sh"
$1
EOF
  bash "$W/run.sh" >"$W/out" 2>&1 || echo "(exit $?)" >>"$W/out"
  sed 's/^/  | /' "$W/out"
}

# deb NAME VERSION [DEPENDS] [POSTINST]: a tiny package.
deb() {
  local d="$W/pkg/$1"
  mkdir -p "$d/DEBIAN"
  {
    echo "Package: $1"
    echo "Version: $2"
    echo "Architecture: all"
    echo "Maintainer: NoCapOS tests <tests@localhost>"
    [ -z "${3:-}" ] || echo "Depends: $3"
    echo "Description: NoCapOS installer test package"
  } >"$d/DEBIAN/control"
  if [ -n "${4:-}" ]; then
    printf '#!/bin/sh\n%s\n' "$4" >"$d/DEBIAN/postinst"
    chmod 755 "$d/DEBIAN/postinst"
  fi
  dpkg-deb --build --root-owner-group "$d" "$W/$1.deb" >/dev/null
}

echo "== refresh"
apt-get update -q >/dev/null
helpers 'apt_refresh && echo REFRESHED'
expect "$W/out" REFRESHED

echo "== the Raspberry Pi state: headers meta-package without its headers"
deb linux-headers-rpi-2712 1:6.12.99-1+rpt1 'linux-headers-6.12.99+rpt-rpi-2712 (= 1:6.12.99-1+rpt1)'
dpkg -i --force-depends "$W/linux-headers-rpi-2712.deb" >/dev/null 2>&1 || true
apt-get check >/dev/null 2>&1 && fail "apt-get check should see the unmet dependency"

echo "== without --yes and nobody to ask: nothing removed, the real error shown"
NOCAP_TTY=/nonexistent-tty helpers 'pkg_install_group apt desktop lean hello fonts-dejavu-core || echo GROUP-FAILED; pkg_summary'
expect "$W/out" \
  "Repairing the package system first" \
  "would remove: linux-headers-rpi-2712" \
  "nothing is removed" \
  "nothing could install while the package system is broken (linux-headers-rpi-2712)" \
  "Unmet dependencies" \
  GROUP-FAILED \
  "Settings → Glow Up → Update everything"
dpkg -s linux-headers-rpi-2712 >/dev/null 2>&1 || fail "the headers package was removed without asking"
dpkg -s hello >/dev/null 2>&1 && fail "hello can't have installed on a broken system"

echo "== with --yes: removed, said so, and the install works"
ASSUME_YES=1 helpers '
pkg_install_group apt test full hello
host_feature_packages apt media "nocap-no-such-package"
host_feature_packages apt discovery "wsdd/wsdd2"
host_feature_packages apt remote-desktop "guacd libguac-client-rdp0 libguac-client-vnc0"
pkg_summary'
expect "$W/out" \
  "Removed to repair the package system: linux-headers-rpi-2712" \
  "package system repaired" \
  "media: nocap-no-such-package (not in this system's repositories)"
reject "$W/out" "discovery: wsdd (" "warning: remote-desktop"
dpkg -s hello >/dev/null 2>&1 || fail "hello didn't install after the repair"
dpkg -s linux-headers-rpi-2712 >/dev/null 2>&1 && fail "the broken headers package should be gone"
if apt-cache policy wsdd | grep -q 'Candidate: [0-9]'; then
  dpkg -s wsdd >/dev/null 2>&1 || fail "wsdd didn't install"
fi

echo "== a half-configured package that configure -a can finish"
deb nocap-test-postinst 1.0 '' '[ -e /tmp/nocap-postinst-ok ] || { echo "the first try fails" >&2; exit 1; }'
dpkg -i "$W/nocap-test-postinst.deb" >/dev/null 2>&1 && fail "the postinst should fail the first time"
dpkg-query -W -f='${db:Status-Abbrev}' nocap-test-postinst | grep -q '^iF' || fail "nocap-test-postinst should be half configured"
helpers 'apt_health && echo HEALTHY || echo BROKEN: $APT_BROKEN_PKGS'
expect "$W/out" "BROKEN: nocap-test-postinst"
touch /tmp/nocap-postinst-ok
helpers 'apt_prepare; pkg_summary'
expect "$W/out" "broken: nocap-test-postinst" "package system repaired"
dpkg-query -W -f='${db:Status-Abbrev}' nocap-test-postinst | grep -q '^ii' || fail "nocap-test-postinst should be configured now"

echo "== one that can't be finished: the summary names it"
rm -f /tmp/nocap-postinst-ok
deb nocap-test-postinst2 1.0 '' 'echo "DKMS make.log: build failed" >&2; exit 10'
dpkg -i "$W/nocap-test-postinst2.deb" >/dev/null 2>&1 || true
helpers 'pkg_install_group apt media full cowsay && echo GROUP-OK; pkg_summary'
# apt installs cowsay but reports dpkg's error over the other package: cowsay counts as installed.
expect "$W/out" \
  "The package system is still broken (nocap-test-postinst2)" \
  "package system: nocap-test-postinst2 (still broken:" \
  GROUP-OK
reject "$W/out" "media: cowsay"
dpkg -s cowsay >/dev/null 2>&1 || fail "cowsay should be installed"
dpkg --purge nocap-test-postinst2 >/dev/null 2>&1 || true

echo "== a package tool holding the lock"
flock /var/lib/dpkg/lock-frontend sleep 3 &
sleep 0.5
APT_LOCK_POLL=1 helpers 'apt_wait_lock && echo FREE'
expect "$W/out" "Waiting for another package tool to finish (" "pid" "apt is free again" FREE
wait

echo "== a lock file that is only open is not a busy package tool"
# (Debian's shutdown helper for automatic updates keeps one open all the time.)
sleep 3 </var/lib/dpkg/lock-frontend &
APT_LOCK_POLL=1 helpers 'apt_wait_lock && echo FREE'
expect "$W/out" FREE
reject "$W/out" "Waiting for another package tool"
wait

if [ "$FAILS" -gt 0 ]; then
  echo "$FAILS check(s) failed"
  exit 1
fi
echo "all package helper checks passed"
