#!/usr/bin/env bash
# build-debs.sh VERSION ARCH OUTDIR: builds NoCapOS's own Debian packages.
#   nocapos-core      NoCapOS (alfad), the cap CLI and its service    ARCH
#   nocapos-image     image-install integration: read-only system,     all
#                     saved settings, A/B update health check
#   nocapos-branding  os-release, console banner, boot splash          all
#   nocapos-base      metapackage: the system packages NoCapOS uses    ARCH
#                     (from os/packages/*.list)
#   nocap-desktop     NoCap Desktop (NoCap Shell, sign-in screen)       ARCH
# ARCH is a Debian architecture: amd64, arm64 or armhf. The web UI must be
# built first (frontend: npm run build), so alfad can embed it.
set -euo pipefail

VERSION="${1:?version}"
ARCH="${2:?debian arch: amd64, arm64 or armhf}"
OUT="${3:?output folder}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
PKG="$ROOT/os/packaging"
MAINT="NoCapOS <shatheitguy@users.noreply.github.com>"
HOMEPAGE="https://github.com/shatheitguy/nocapos"
DEBVER="$VERSION"

case "$ARCH" in
  amd64) goarch=amd64; goarm="" ;;
  arm64) goarch=arm64; goarm="" ;;
  armhf) goarch=arm; goarm=7 ;;
  *) echo "unknown arch $ARCH" >&2; exit 2 ;;
esac

mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# ---- helpers -------------------------------------------------------------

# docs PKGNAME STAGE: the copyright file and changelog Debian policy asks for.
docs() {
  local d="$2/usr/share/doc/$1"
  mkdir -p "$d"
  {
    echo "Format: https://www.debian.org/doc/packaging-manuals/copyright-format/1.0/"
    echo "Upstream-Name: NoCapOS"
    echo "Source: $HOMEPAGE"
    echo
    echo "Files: *"
    echo "Copyright: NoCapOS authors"
    echo "License: MIT"
    sed 's/^$/./; s/^/ /' "$ROOT/LICENSE"
  } >"$d/copyright"
  printf '%s (%s) stable; urgency=medium\n\n  * NoCapOS %s.\n\n -- %s  %s\n' \
    "$1" "$DEBVER" "$VERSION" "$MAINT" "$(date -R -u -d @"${SOURCE_DATE_EPOCH:-$(date +%s)}")" |
    gzip -9n >"$d/changelog.gz"
}

# finish PKGNAME STAGE: md5sums, installed size, permissions, then the .deb.
finish() {
  local name="$1" stage="$2" arch
  arch="$(sed -n 's/^Architecture: //p' "$stage/DEBIAN/control")"
  (cd "$stage" && find . -type f ! -path './DEBIAN/*' -printf '%P\0' | sort -z | xargs -0r md5sum) >"$stage/DEBIAN/md5sums"
  [ -s "$stage/DEBIAN/md5sums" ] || rm -f "$stage/DEBIAN/md5sums"
  local size
  size="$(du -sk --exclude=DEBIAN "$stage" | cut -f1)"
  sed -i "/^Installed-Size:/d" "$stage/DEBIAN/control"
  echo "Installed-Size: $size" >>"$stage/DEBIAN/control"
  find "$stage" -type d -exec chmod 755 {} +
  for s in preinst postinst prerm postrm; do
    [ -f "$stage/DEBIAN/$s" ] && chmod 755 "$stage/DEBIAN/$s"
  done
  dpkg-deb --root-owner-group -Zxz --build "$stage" "$OUT/${name}_${DEBVER}_${arch}.deb" >/dev/null
  echo "built ${name}_${DEBVER}_${arch}.deb"
}

# copy_root PKGDIR STAGE: the package's files from os/packaging/PKG/root.
copy_root() {
  if [ -d "$1/root" ]; then cp -a "$1/root/." "$2/"; fi
}

# deps_from_lists LIST...: "Depends" and "Recommends" lines from package
# lists ("a|b" becomes "a | b"; "?name" is only recommended).
deps_from_lists() {
  local deps=() recs=() line entry
  for list in "$@"; do
    while IFS= read -r line || [ -n "$line" ]; do
      entry="${line%%#*}"
      entry="$(echo "$entry" | tr -d '[:space:]')"
      [ -n "$entry" ] || continue
      if [ "${entry#\?}" != "$entry" ]; then
        recs+=("$(echo "${entry#\?}" | sed 's/|/ | /g')")
      else
        deps+=("$(echo "$entry" | sed 's/|/ | /g')")
      fi
    done <"$list"
  done
  local IFS=,
  echo "Depends: ${deps[*]}" | sed 's/,/, /g'
  [ "${#recs[@]}" -eq 0 ] || echo "Recommends: ${recs[*]}" | sed 's/,/, /g'
}

# ---- nocapos-core --------------------------------------------------------

build_core() {
  local stage="$WORK/nocapos-core"
  mkdir -p "$stage/DEBIAN" "$stage/usr/bin"
  copy_root "$PKG/nocapos-core" "$stage"
  if [ ! -d "$ROOT/backend/web/dist" ]; then
    echo "backend/web/dist is missing: build the web UI first (cd frontend && npm run build)" >&2
    exit 1
  fi
  (cd "$ROOT/backend" && CGO_ENABLED=0 GOOS=linux GOARCH=$goarch GOARM=$goarm \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$stage/usr/bin/nocapos" ./cmd/alfad)
  (cd "$ROOT/backend" && CGO_ENABLED=0 GOOS=linux GOARCH=$goarch GOARM=$goarm \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$stage/usr/bin/cap" ./cmd/cap)
  chmod 755 "$stage/usr/bin/nocapos" "$stage/usr/bin/cap"
  chmod 600 "$stage/etc/nocapos/nocapos.env"
  docs nocapos-core "$stage"
  cat >"$stage/DEBIAN/control" <<EOF
Package: nocapos-core
Version: $DEBVER
Architecture: $ARCH
Maintainer: $MAINT
Section: admin
Priority: optional
Homepage: $HOMEPAGE
Depends: ca-certificates, systemd
Description: NoCapOS: a web OS for your home server and desktop
 NoCapOS (alfad) runs the NoCapOS web OS: files, apps, storage, sharing,
 users, network and updates, in the browser and on NoCap Desktop. Also
 contains the cap command-line tool.
EOF
  echo "/etc/nocapos/nocapos.env" >"$stage/DEBIAN/conffiles"
  cat >"$stage/DEBIAN/postinst" <<'EOF'
#!/bin/sh
set -e
if [ "$1" = configure ]; then
  systemctl daemon-reload >/dev/null 2>&1 || true
  systemctl enable nocapos.service >/dev/null 2>&1 || true
  if [ -d /run/systemd/system ] && [ -z "${NOCAP_IMAGE_BUILD:-}" ]; then
    systemctl try-restart nocapos.service >/dev/null 2>&1 || true
  fi
fi
EOF
  cat >"$stage/DEBIAN/prerm" <<'EOF'
#!/bin/sh
set -e
if [ "$1" = remove ] && [ -d /run/systemd/system ]; then
  systemctl stop nocapos.service >/dev/null 2>&1 || true
fi
EOF
  cat >"$stage/DEBIAN/postrm" <<'EOF'
#!/bin/sh
set -e
if [ "$1" = remove ] || [ "$1" = purge ]; then
  systemctl disable nocapos.service >/dev/null 2>&1 || true
  systemctl daemon-reload >/dev/null 2>&1 || true
fi
EOF
  finish nocapos-core "$stage"
}

# ---- nocapos-image -------------------------------------------------------

build_image() {
  local stage="$WORK/nocapos-image"
  mkdir -p "$stage/DEBIAN"
  copy_root "$PKG/nocapos-image" "$stage"
  chmod 755 "$stage"/usr/lib/nocapos/* "$stage"/etc/rugix/hooks/boot/post-init/*
  docs nocapos-image "$stage"
  cat >"$stage/DEBIAN/control" <<EOF
Package: nocapos-image
Version: $DEBVER
Architecture: all
Maintainer: $MAINT
Section: admin
Priority: optional
Homepage: $HOMEPAGE
Depends: nocapos-core (= $DEBVER), cryptsetup, curl, e2fsprogs, util-linux, mawk | gawk
Description: NoCapOS image install integration
 Turns a NoCapOS image into a read-only system with A/B updates (Rugix):
 the data partition driver (with "Lock my drive"), what survives restarts
 and updates, this machine's saved settings in /etc, and the health check
 that keeps a just-updated system only when it works. Only for NoCapOS
 image installs.
EOF
  (cd "$stage" && find etc -type f | sed 's|^|/|' | sort) >"$stage/DEBIAN/conffiles"
  cat >"$stage/DEBIAN/postinst" <<'EOF'
#!/bin/sh
set -e
if [ "$1" = configure ]; then
  systemctl daemon-reload >/dev/null 2>&1 || true
  systemctl enable nocapos-boot-check.service nocapos-etc-save.service nocapos-etc-changed.path nocapos-etc-restore.service >/dev/null 2>&1 || true
fi
EOF
  cat >"$stage/DEBIAN/postrm" <<'EOF'
#!/bin/sh
set -e
if [ "$1" = remove ] || [ "$1" = purge ]; then
  systemctl disable nocapos-boot-check.service nocapos-etc-save.service nocapos-etc-changed.path >/dev/null 2>&1 || true
fi
EOF
  finish nocapos-image "$stage"
}

# ---- nocapos-branding ----------------------------------------------------

build_branding() {
  local stage="$WORK/nocapos-branding"
  mkdir -p "$stage/DEBIAN" "$stage/usr/lib" "$stage/usr/share/plymouth/themes/nocapos" \
    "$stage/usr/share/nocapos/brand" "$stage/usr/share/icons/hicolor/256x256/apps"
  copy_root "$PKG/nocapos-branding" "$stage"
  cat >"$stage/usr/lib/os-release" <<EOF
PRETTY_NAME="NoCapOS $VERSION"
NAME="NoCapOS"
VERSION_ID="$VERSION"
VERSION="$VERSION"
VERSION_CODENAME=trixie
ID=nocapos
ID_LIKE=debian
ANSI_COLOR="1;31"
LOGO=nocapos
HOME_URL="$HOMEPAGE"
DOCUMENTATION_URL="$HOMEPAGE#readme"
SUPPORT_URL="$HOMEPAGE/issues"
BUG_REPORT_URL="$HOMEPAGE/issues"
EOF
  cat >"$stage/etc/lsb-release" <<EOF
DISTRIB_ID=NoCapOS
DISTRIB_RELEASE=$VERSION
DISTRIB_CODENAME=trixie
DISTRIB_DESCRIPTION="NoCapOS $VERSION"
EOF
  cp "$ROOT"/deploy/plymouth/* "$stage/usr/share/plymouth/themes/nocapos/"
  cp "$ROOT"/frontend/public/brand/*.png "$stage/usr/share/nocapos/brand/"
  cp "$ROOT/frontend/public/brand/mark-256.png" "$stage/usr/share/icons/hicolor/256x256/apps/nocapos.png"
  find "$stage/usr/share" -type f -exec chmod 644 {} +
  docs nocapos-branding "$stage"
  cat >"$stage/DEBIAN/control" <<EOF
Package: nocapos-branding
Version: $DEBVER
Architecture: all
Maintainer: $MAINT
Section: misc
Priority: optional
Homepage: $HOMEPAGE
Depends: base-files
Recommends: plymouth
Description: NoCapOS identity and boot branding
 Makes the system identify as NoCapOS (os-release, lsb-release), and adds
 the NoCapOS console banner and the NoCapOS boot splash (logo and loading
 animation).
EOF
  printf '%s\n' /etc/issue /etc/issue.net /etc/lsb-release >"$stage/DEBIAN/conffiles"
  cat >"$stage/DEBIAN/preinst" <<'EOF'
#!/bin/sh
set -e
# base-files owns these; NoCapOS's own versions replace them, and the
# originals are kept next to them (dpkg-divert) and put back on removal.
if [ "$1" = install ] || [ "$1" = upgrade ]; then
  for f in /usr/lib/os-release /etc/issue /etc/issue.net; do
    dpkg-divert --package nocapos-branding --add --rename --divert "$f.debian" "$f" >/dev/null
  done
fi
EOF
  cat >"$stage/DEBIAN/postinst" <<'EOF'
#!/bin/sh
set -e
if [ "$1" = configure ] && command -v plymouth-set-default-theme >/dev/null 2>&1; then
  plymouth-set-default-theme nocapos >/dev/null 2>&1 || true
fi
EOF
  cat >"$stage/DEBIAN/postrm" <<'EOF'
#!/bin/sh
set -e
if [ "$1" = remove ] || [ "$1" = abort-install ] || [ "$1" = disappear ]; then
  for f in /usr/lib/os-release /etc/issue /etc/issue.net; do
    dpkg-divert --package nocapos-branding --remove --rename --divert "$f.debian" "$f" >/dev/null || true
  done
fi
EOF
  finish nocapos-branding "$stage"
}

# ---- nocapos-base --------------------------------------------------------

build_base() {
  local stage="$WORK/nocapos-base" target lists
  case "$ARCH" in
    amd64) target=pc ;;
    arm64) target=pi ;;
    *) echo "nocapos-base: no image target for $ARCH, skipped"; return 0 ;;
  esac
  lists=("$ROOT/os/packages/common.list" "$ROOT/os/packages/desktop.list" "$ROOT/os/packages/$target.list")
  mkdir -p "$stage/DEBIAN"
  docs nocapos-base "$stage"
  {
    echo "Package: nocapos-base"
    echo "Version: $DEBVER"
    echo "Architecture: $ARCH"
    echo "Maintainer: $MAINT"
    echo "Section: metapackages"
    echo "Priority: optional"
    echo "Homepage: $HOMEPAGE"
    deps_from_lists "${lists[@]}"
    echo "Description: NoCapOS system packages (metapackage)"
    echo " Depends on exactly the system packages a NoCapOS image contains:"
    echo " each is listed with the NoCapOS feature that needs it in"
    echo " os/packages/*.list in the NoCapOS source."
  } >"$stage/DEBIAN/control"
  finish nocapos-base "$stage"
}

# ---- nocap-desktop --------------------------------------------------------

# NoCap Desktop: NoCap Shell's programs (nocap-shell, nocap-lock: C, built
# for the image by build.sh in a Debian 13 container; NOCAP_SHELL_BINS points
# at them), its pages (frontend/dist-shell: npm run build:shell) and the
# desktop's files (deploy/desktop). The package's postinst puts them in place
# with the same steps a script install takes (setup-image.sh).
build_desktop() {
  local bins="${NOCAP_SHELL_BINS:-}" ui="$ROOT/frontend/dist-shell"
  if [ -z "$bins" ] || [ ! -x "$bins/nocap-shell" ] || [ ! -x "$bins/nocap-lock" ] || [ ! -f "$ui/shell.html" ]; then
    echo "nocap-desktop: needs NOCAP_SHELL_BINS (nocap-shell, nocap-lock) and frontend/dist-shell, skipped"
    return 0
  fi
  local stage="$WORK/nocap-desktop" b
  b="$stage/usr/share/nocapos/desktop-bundle"
  mkdir -p "$stage/DEBIAN" "$b/shell/bin" "$b/shell/ui" "$b/brand" "$b/wallpapers"
  cp -R "$ROOT/deploy/desktop" "$b/desktop"
  install -m 0755 "$bins/nocap-shell" "$bins/nocap-lock" "$b/shell/bin/"
  cp -R "$ui/." "$b/shell/ui/"
  cp "$ROOT"/frontend/public/brand/*.png "$b/brand/"
  cp "$ROOT"/frontend/public/wallpapers/*.jpg "$b/wallpapers/"
  install -m 0755 "$PKG/nocap-desktop/setup-image.sh" "$b/setup-image.sh"
  find "$b" -type f ! -perm -u+x -exec chmod 644 {} +
  docs nocap-desktop "$stage"
  cat >"$stage/DEBIAN/control" <<EOF
Package: nocap-desktop
Version: $DEBVER
Architecture: $ARCH
Maintainer: $MAINT
Section: x11
Priority: optional
Homepage: $HOMEPAGE
Depends: nocapos-core (= $DEBVER), labwc, greetd, xwayland, libwebkit2gtk-4.1-0, libgtk-layer-shell0, libjson-glib-1.0-0, libpolkit-agent-1-0, libpam0g
Description: NoCap Desktop
 The desktop on the machine's own screen: NoCap's sign-in screen (greetd),
 labwc and NoCap Shell (top bar, dock, launcher, Control Center,
 notifications, admin prompts, lock screen).
EOF
  cat >"$stage/DEBIAN/postinst" <<'EOF'
#!/bin/sh
set -e
if [ "$1" = configure ]; then
  /usr/share/nocapos/desktop-bundle/setup-image.sh
fi
EOF
  finish nocap-desktop "$stage"
}
build_core
build_image
build_branding
build_base
build_desktop
