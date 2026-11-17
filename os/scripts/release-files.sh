#!/usr/bin/env bash
# release-files.sh TARGET: a target's release files in os/build/release/:
#   nocapos-TARGET.img.xz       flashable image (Pi: flash to the card)
#   nocapos-pc-usb.img.xz       PC only: the USB stick with the setup
#   nocapos-TARGET.rugixb       system update bundle (signed when the
#                               signing secrets are set; .signed marks it)
#   nocapos-TARGET.rugixb.hash  its Rugix bundle hash (for the manifest)
#   nocapos-TARGET.packages.tsv what's inside (for the security refresh)
set -euo pipefail
t="${1:?pc or pi}"
OS="$(cd "$(dirname "$0")/.." && pwd)"
B="$OS/build"
R="$B/release"
mkdir -p "$R"

bash "$OS/scripts/signing.sh" sign "$B/$t.rugixb"
bash "$OS/scripts/build.sh" bundle-hash "$t"
cp "$B/$t.rugixb" "$R/nocapos-$t.rugixb"
cp "$B/$t.hash" "$R/nocapos-$t.rugixb.hash"
[ -f "$B/$t.rugixb.signed" ] && touch "$R/nocapos-$t.rugixb.signed"
[ -f "$B/$t.packages.tsv" ] || bash "$OS/scripts/image-report.sh" "$B/$t.img" >/dev/null
cp "$B/$t.packages.tsv" "$R/nocapos-$t.packages.tsv"
xz -T0 -6 -c "$B/$t.img" >"$R/nocapos-$t.img.xz"
if [ "$t" = pc ]; then
  command -v mcopy >/dev/null || sudo apt-get install -y -qq mtools >/dev/null
  bash "$OS/scripts/make-usb.sh" "$B/pc.img" "$B/pc-usb.img"
  xz -T0 -6 -c "$B/pc-usb.img" >"$R/nocapos-pc-usb.img.xz"
fi
ls -la "$R"
