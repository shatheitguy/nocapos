#!/usr/bin/env bash
# Builds the NoCapOS Raspberry Pi image (64-bit; Pi 3, 4, 5, 400, 500,
# Zero 2 W, CM4/CM5): Raspberry Pi OS Lite arm64 built with pi-gen, plus
# the NoCapOS stage (image/rpi/stage-nocapos).
#
#   image/rpi/build.sh VERSION [OUT_DIR]
#
# Needs Docker (pi-gen's build-docker.sh; your user in the docker group, or
# run as root) and git. Fastest on an arm64 Linux
# machine; on x86_64 register qemu-user-static's binfmt handlers first
# (Debian/Ubuntu: apt install qemu-user-static binfmt-support).
# Output: OUT_DIR/nocapos-VERSION-rpi-arm64.img.xz (default OUT_DIR: ./out)
set -euo pipefail

VERSION="${1:?usage: build.sh VERSION [OUT_DIR]}"
OUT="${2:-$PWD/out}"
HERE="$(cd "$(dirname "$0")" && pwd)"
COMMON="$(cd "$HERE/../common" && pwd)"
WORK="${NOCAP_WORK:-$PWD/.pi-gen}"
# pi-gen's 64-bit branch (Raspberry Pi OS trixie). Pin a commit with PIGEN_REF.
PIGEN_REPO="${PIGEN_REPO:-https://github.com/RPi-Distro/pi-gen.git}"
PIGEN_REF="${PIGEN_REF:-arm64}"

mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"
command -v docker >/dev/null || { echo "docker is required" >&2; exit 1; }
SUDO=""
[ "$(id -u)" -eq 0 ] || SUDO=sudo

$SUDO rm -rf "$WORK"
git clone --quiet "$PIGEN_REPO" "$WORK"
git -C "$WORK" checkout --quiet "$PIGEN_REF"
echo "==> pi-gen $(git -C "$WORK" rev-parse --short HEAD) ($PIGEN_REF)"

# Lite base (stages 0-2) without its own image; ours comes from stage-nocapos.
touch "$WORK/stage2/SKIP_IMAGES"
cp -R "$HERE/stage-nocapos" "$WORK/stage-nocapos"
chmod 0755 "$WORK/stage-nocapos/prerun.sh" "$WORK"/stage-nocapos/*/*.sh
bash "$COMMON/stage-overlay.sh" "$VERSION" arm64 rpi "$WORK/stage-nocapos/00-nocapos/files/overlay"

cp "$HERE/config" "$WORK/config"
echo "IMG_NAME=nocapos-$VERSION-rpi-arm64" >>"$WORK/config"

echo "==> pi-gen build-docker.sh (this takes a while)"
(cd "$WORK" && ./build-docker.sh) 2>&1 | tee "$OUT/build-rpi.log"

img="$(find "$WORK/deploy" -maxdepth 1 -name '*.img.xz' | head -1)"
[ -n "$img" ] || { echo "pi-gen produced no image; see $OUT/build-rpi.log" >&2; exit 1; }
cp "$img" "$OUT/nocapos-$VERSION-rpi-arm64.img.xz"
(cd "$OUT" && sha256sum "nocapos-$VERSION-rpi-arm64.img.xz" >"nocapos-$VERSION-rpi-arm64.img.xz.sha256")
echo "==> $OUT/nocapos-$VERSION-rpi-arm64.img.xz"
