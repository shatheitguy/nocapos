#!/usr/bin/env bash
# Builds the NoCapOS PC image: a hybrid ISO (USB stick or DVD, BIOS and UEFI)
# that boots a live NoCapOS and installs it with debian-installer.
# Debian stable (trixie) base, built with live-build.
#
#   sudo image/amd64/build.sh VERSION [OUT_DIR]
#
# Run as root on Debian 13 (or in a privileged debian:trixie container, as
# the image workflow does). Missing build tools are installed with apt.
# Output: OUT_DIR/nocapos-VERSION-amd64.iso (default OUT_DIR: ./out)
set -euo pipefail

VERSION="${1:?usage: build.sh VERSION [OUT_DIR]}"
OUT="${2:-$PWD/out}"
HERE="$(cd "$(dirname "$0")" && pwd)"
COMMON="$(cd "$HERE/../common" && pwd)"
WORK="${NOCAP_WORK:-/var/tmp/nocapos-live}"
DIST=trixie

[ "$(id -u)" -eq 0 ] || { echo "run as root" >&2; exit 1; }
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"

need=()
command -v lb >/dev/null || need+=(live-build)
command -v debootstrap >/dev/null || need+=(debootstrap)
command -v curl >/dev/null || need+=(curl ca-certificates)
command -v cpio >/dev/null || need+=(cpio)
python3 -c 'import PIL' 2>/dev/null || need+=(python3 python3-pil)
[ -f /usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf ] || need+=(fonts-dejavu-core)
if [ "${#need[@]}" -gt 0 ]; then
  echo "==> Installing build tools: ${need[*]}"
  apt-get update -q
  DEBIAN_FRONTEND=noninteractive apt-get install -y -q --no-install-recommends "${need[@]}"
fi
echo "==> live-build $(lb --version 2>/dev/null | head -1)"

if [ -d "$WORK" ]; then (cd "$WORK" && lb clean --purge >/dev/null 2>&1 || true); fi
rm -rf "$WORK"
mkdir -p "$WORK"
cd "$WORK"

lb config \
  --mode debian \
  --distribution "$DIST" \
  --architecture amd64 \
  --archive-areas "main contrib non-free-firmware" \
  --security true \
  --updates true \
  --binary-images iso-hybrid \
  --debian-installer live \
  --debian-installer-distribution "$DIST" \
  --debian-installer-gui true \
  --firmware-binary true \
  --firmware-chroot true \
  --memtest none \
  --checksums sha256 \
  --image-name nocapos \
  --iso-application "NoCapOS" \
  --iso-publisher "NoCapOS; https://github.com/shatheitguy/nocapos" \
  --iso-volume "NoCapOS ${VERSION}" \
  --bootappend-live "boot=live components quiet splash hostname=nocap username=nocap" \
  --bootappend-install "quiet"

# Our config tree (package list, hooks, preseed) over the generated one.
cp -R "$HERE/config/." config/

# Artwork: boot menus, installer banner, GRUB theme.
python3 "$COMMON/make-art.py" config/nocapos-art
mkdir -p config/includes.installer/usr/share/graphics
for f in logo_installer.png logo_debian.png; do
  cp config/nocapos-art/installer-banner.png "config/includes.installer/usr/share/graphics/$f"
done
for f in logo_installer_dark.png logo_debian_dark.png; do
  cp config/nocapos-art/installer-banner-dark.png "config/includes.installer/usr/share/graphics/$f"
done

# The NoCapOS layer (release bundle, checked against SHA256SUMS).
bash "$COMMON/stage-overlay.sh" "$VERSION" amd64 pc config/includes.chroot_after_packages config/nocapos-art
chmod 0755 config/hooks/normal/*.hook.*

echo "==> lb build (this takes a while)"
lb build 2>&1 | tee "$OUT/build-amd64.log"
iso="$(find . -maxdepth 1 -name "*.iso" | head -1)"
[ -n "$iso" ] || { echo "lb build produced no ISO; see $OUT/build-amd64.log" >&2; exit 1; }
mv "$iso" "$OUT/nocapos-$VERSION-amd64.iso"
(cd "$OUT" && sha256sum "nocapos-$VERSION-amd64.iso" >"nocapos-$VERSION-amd64.iso.sha256")
echo "==> $OUT/nocapos-$VERSION-amd64.iso"
