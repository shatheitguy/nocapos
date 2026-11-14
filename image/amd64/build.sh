#!/usr/bin/env bash
# Builds the NoCapOS PC image: a hybrid ISO (USB stick or DVD, BIOS and UEFI)
# that boots into a live NoCap Desktop with the graphical NoCapOS installer
# (Debian's Calamares in NoCapOS's look), plus debian-installer in text mode
# for servers. Debian stable (trixie) base, built with live-build.
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
# The guacd container image (save-guacd.sh), fetched without a Docker daemon.
command -v skopeo >/dev/null || need+=(skopeo)
command -v zstd >/dev/null || need+=(zstd)
python3 -c 'import PIL' 2>/dev/null || need+=(python3 python3-pil)
[ -f /usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf ] || need+=(fonts-dejavu-core)
python3 -c 'import yaml' 2>/dev/null || need+=(python3-yaml)
if [ "${#need[@]}" -gt 0 ]; then
  echo "==> Installing build tools: ${need[*]}"
  apt-get update -q
  DEBIAN_FRONTEND=noninteractive apt-get install -y -q --no-install-recommends "${need[@]}"
fi
echo "==> live-build $(lb --version 2>/dev/null | head -1)"

# The graphical installer's settings must parse (a broken file only shows
# when someone boots the ISO).
python3 - "$HERE/calamares" <<'PY'
import pathlib, sys, yaml
bad = 0
for f in sorted(pathlib.Path(sys.argv[1]).rglob("*")):
    if f.suffix in (".conf", ".desc"):
        try:
            yaml.safe_load(f.read_text())
        except yaml.YAMLError as e:
            print(f"bad YAML in {f}: {e}", file=sys.stderr)
            bad = 1
sys.exit(bad)
PY

LIVE_APPEND="boot=live components quiet splash hostname=nocap username=nocap"
INSTALL_APPEND="vga=788 quiet --- quiet"

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
  --debian-installer-gui false \
  --firmware-binary false \
  --firmware-chroot true \
  --memtest memtest86+ \
  --chroot-squashfs-compression-type xz \
  --checksums sha256 \
  --image-name nocapos \
  --iso-application "NoCapOS" \
  --iso-publisher "NoCapOS; https://github.com/shatheitguy/nocapos" \
  --iso-volume "NoCapOS ${VERSION}" \
  --bootappend-live "$LIVE_APPEND" \
  --bootappend-install "quiet"
# Smaller root filesystem image: bigger blocks and the x86 code filter.
export MKSQUASHFS_OPTIONS="-b 1M -Xbcj x86"

# Our config tree (package list, hooks, preseed) over the generated one.
cp -R "$HERE/config/." config/

# Artwork: boot menus, installer banner, GRUB theme, graphical installer.
python3 "$COMMON/make-art.py" config/nocapos-art
printf '%s\n' "$LIVE_APPEND" >config/nocapos-art/boot-append-live
printf '%s\n' "$INSTALL_APPEND" >config/nocapos-art/boot-append-install
mkdir -p config/includes.installer/usr/share/graphics
for f in logo_installer.png logo_debian.png; do
  cp config/nocapos-art/installer-banner.png "config/includes.installer/usr/share/graphics/$f"
done
for f in logo_installer_dark.png logo_debian_dark.png; do
  cp config/nocapos-art/installer-banner-dark.png "config/includes.installer/usr/share/graphics/$f"
done

# The graphical installer: Debian's Calamares with NoCapOS's settings and
# branding in /etc/calamares (live session only).
cal=config/includes.chroot_after_packages/etc/calamares
mkdir -p "$cal"
cp -R "$HERE/calamares/." "$cal/"
short="$(echo "$VERSION" | cut -d. -f1-2)"
sed -i -e "s/@VERSION@/$VERSION/g" -e "s/@SHORT_VERSION@/$short/g" "$cal/branding/nocapos/branding.desc"
cp config/nocapos-art/calamares-logo.png "$cal/branding/nocapos/logo.png"
cp config/nocapos-art/calamares-welcome.png "$cal/branding/nocapos/welcome.png"
chmod 0755 config/includes.chroot_after_packages/usr/lib/live/config/2000-nocapos \
  config/includes.chroot_after_packages/usr/lib/nocapos/image/live/nocapos-install \
  config/includes.chroot_after_packages/usr/lib/nocapos/image/live/live-welcome

# The NoCapOS layer (release bundle, checked against SHA256SUMS).
bash "$COMMON/stage-overlay.sh" "$VERSION" amd64 pc config/includes.chroot_after_packages config/nocapos-art
chmod 0755 config/hooks/normal/*.hook.*

echo "==> lb build (this takes a while)"
lb build 2>&1 | tee "$OUT/build-amd64.log"
iso="$(find . -maxdepth 1 -name "*.iso" | head -1)"
[ -n "$iso" ] || { echo "lb build produced no ISO; see $OUT/build-amd64.log" >&2; exit 1; }
mv "$iso" "$OUT/nocapos-$VERSION-amd64.iso"
(cd "$OUT" && sha256sum "nocapos-$VERSION-amd64.iso" >"nocapos-$VERSION-amd64.iso.sha256")
size="$(stat -c %s "$OUT/nocapos-$VERSION-amd64.iso")"
echo "==> $OUT/nocapos-$VERSION-amd64.iso ($((size / 1048576)) MiB)"

# Checks inside the built system: Debian updates apply cleanly, NoCapOS's
# files belong to no Debian package, every preinstalled feature works.
bash "$COMMON/rootfs/usr/lib/nocapos/image/check-image.sh" --chroot "$WORK/chroot" 2>&1 | tee "$OUT/check-amd64.log"

# GitHub release assets must stay under 2 GiB.
if [ "$size" -ge 2147483648 ]; then
  echo "the ISO is $((size / 1048576)) MiB, over the 2 GiB release asset limit" >&2
  exit 1
fi
