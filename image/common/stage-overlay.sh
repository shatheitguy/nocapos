#!/usr/bin/env bash
# Assembles the NoCapOS layer that goes on top of the Debian base, as a
# directory tree to copy into the image's root filesystem:
#
#   /usr/lib/nocapos/bundle/        the release bundle (alfad, cap, install.sh, ...)
#   /usr/lib/nocapos/image/         image scripts (layer.sh runs once in the chroot)
#   /usr/share/plymouth/themes/nocapos/   boot splash (deploy/plymouth)
#   /usr/share/grub/themes/nocapos/       GRUB theme (amd64, when art is given)
#   /etc/...                        systemd units, apt settings, GRUB defaults
#
#   image/common/stage-overlay.sh VERSION ARCH PLATFORM DEST [ART_DIR]
#     VERSION   NoCapOS release, e.g. 0.8.0 (bundle comes from release v0.8.0)
#     ARCH      amd64 | arm64
#     PLATFORM  pc | rpi
#     DEST      output directory (created)
#     ART_DIR   output of make-art.py (optional; adds the GRUB theme)
#
# The bundle is downloaded from the GitHub release and checked against the
# release's SHA256SUMS; the build stops on any mismatch. To test a bundle
# that isn't released yet, point NOCAP_DIST at a directory holding
# nocapos-linux-ARCH.tar.gz and SHA256SUMS (scripts/package.sh output).
set -euo pipefail

VERSION="${1:?version}"
ARCH="${2:?arch}"
PLATFORM="${3:?platform}"
DEST="${4:?destination}"
ART="${5:-}"
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"
REPO="${NOCAP_REPO:-shatheitguy/nocapos}"
RELEASE_URL="${NOCAP_RELEASE_URL:-https://github.com/$REPO/releases/download/v$VERSION}"

case "$ARCH" in amd64 | arm64) ;; *) echo "unsupported arch: $ARCH" >&2; exit 2 ;; esac
case "$PLATFORM" in pc | rpi) ;; *) echo "unsupported platform: $PLATFORM" >&2; exit 2 ;; esac

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
file="nocapos-linux-$ARCH.tar.gz"

if [ -n "${NOCAP_DIST:-}" ]; then
  echo "==> Using the local bundle in $NOCAP_DIST"
  cp "$NOCAP_DIST/$file" "$NOCAP_DIST/SHA256SUMS" "$tmp/"
else
  echo "==> Downloading $file and SHA256SUMS from $RELEASE_URL"
  curl -fsSL --retry 3 "$RELEASE_URL/$file" -o "$tmp/$file"
  curl -fsSL --retry 3 "$RELEASE_URL/SHA256SUMS" -o "$tmp/SHA256SUMS"
fi
want="$(tr -d '\r' <"$tmp/SHA256SUMS" | awk -v f="$file" '$2 == f || $2 == "*" f {print $1}')"
got="$(sha256sum "$tmp/$file" | awk '{print $1}')"
if [ -z "$want" ] || [ "$want" != "$got" ]; then
  echo "checksum mismatch for $file (expected '${want:-none}', got '$got')" >&2
  exit 1
fi
echo "    checksum verified: $got"

mkdir -p "$tmp/x"
tar -xzf "$tmp/$file" -C "$tmp/x"
src="$(find "$tmp/x" -mindepth 1 -maxdepth 1 -type d | head -1)"
[ -f "$src/alfad" ] && [ -f "$src/install.sh" ] || { echo "the bundle has no alfad/install.sh" >&2; exit 1; }
bundle_ver="$(cat "$src/VERSION" 2>/dev/null || true)"
[ "$bundle_ver" = "$VERSION" ] || echo "warning: bundle says version '$bundle_ver', building image $VERSION" >&2

mkdir -p "$DEST"
# Plain cp -R (no -a): files end up owned by whoever builds (root in the
# build containers), never by the CI checkout's user.
mkdir -p "$DEST/usr/lib/nocapos/bundle"
cp -R "$src/." "$DEST/usr/lib/nocapos/bundle/"
cp -R "$HERE/rootfs/." "$DEST/"
install -m 0755 "$HERE/layer.sh" "$DEST/usr/lib/nocapos/image/layer.sh"
chmod 0755 "$DEST"/usr/lib/nocapos/image/*.sh "$DEST/usr/lib/nocapos/image/os-release-sync"

# Boot splash: the same Plymouth theme install.sh sets up (deploy/plymouth).
if [ -d "$ROOT/deploy/plymouth" ]; then
  mkdir -p "$DEST/usr/share/plymouth/themes/nocapos"
  cp -R "$ROOT/deploy/plymouth/." "$DEST/usr/share/plymouth/themes/nocapos/"
fi

# Logo for os-release's LOGO=nocapos (desktop environments, fastfetch, ...).
mkdir -p "$DEST/usr/share/icons/hicolor/256x256/apps" "$DEST/usr/share/pixmaps"
cp "$ROOT/frontend/public/brand/mark-256.png" "$DEST/usr/share/icons/hicolor/256x256/apps/nocapos.png"
cp "$ROOT/frontend/public/brand/mark-256.png" "$DEST/usr/share/pixmaps/nocapos.png"

# GRUB theme for the installed system (PC only).
if [ -n "$ART" ] && [ "$PLATFORM" = pc ]; then
  mkdir -p "$DEST/usr/share/grub/themes/nocapos"
  cp "$ART/grub-background.png" "$DEST/usr/share/grub/themes/nocapos/background.png"
  cp "$HERE/grub-theme.txt" "$DEST/usr/share/grub/themes/nocapos/theme.txt"
else
  rm -f "$DEST/etc/default/grub.d/nocapos.cfg"
fi

cat >"$DEST/usr/lib/nocapos/image/image-release" <<EOF
# Written when this NoCapOS image was built.
NOCAP_IMAGE_VERSION=$VERSION
NOCAP_IMAGE_ARCH=$ARCH
NOCAP_IMAGE_PLATFORM=$PLATFORM
NOCAP_IMAGE_BUILT=$(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF
echo "==> NoCapOS layer staged in $DEST"
