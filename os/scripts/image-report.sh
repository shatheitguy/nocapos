#!/usr/bin/env bash
# image-report.sh IMAGE: the system partition's size (compressed squashfs
# and unpacked) and the package list inside, saved next to the image as
# NAME.packages.tsv (the release manifest and the security refresh use it).
set -euo pipefail
img="${1:?image}"
name="$(basename "$img" .img)"
out="$(dirname "$img")/$name.packages.tsv"
command -v unsquashfs >/dev/null || sudo apt-get install -y -qq squashfs-tools >/dev/null

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
# The system partition is the largest squashfs partition in the image.
sfdisk -d "$img" | sed -n 's/.*start= *\([0-9]*\), size= *\([0-9]*\).*/\1 \2/p' >"$tmp/parts"
found=""
while read -r start size; do
  dd if="$img" of="$tmp/part" bs=512 skip="$start" count="$size" status=none
  if unsquashfs -s "$tmp/part" >/dev/null 2>&1; then
    found=1
    break
  fi
done <"$tmp/parts"
[ -n "$found" ] || { echo "no squashfs system partition found in $img"; exit 1; }

sq_bytes="$(unsquashfs -s "$tmp/part" | sed -n 's/^Filesystem size \([0-9]*\) bytes.*/\1/p')"
[ -n "$sq_bytes" ] || sq_bytes="$(stat -c %s "$tmp/part")"
unsquashfs -q -n -d "$tmp/root" "$tmp/part" usr/share/nocapos/packages.tsv >/dev/null
cp "$tmp/root/usr/share/nocapos/packages.tsv" "$out"
raw="$(unsquashfs -lls "$tmp/part" 2>/dev/null | awk '$3 ~ /^[0-9]+$/ && $1 ~ /^-/ {s += $3} END {printf "%d", s / 1048576}')"
echo "| Part | Size |"
echo "|---|---|"
echo "| System (squashfs, compressed) | $((sq_bytes / 1048576)) MiB |"
echo "| System (unpacked files) | ${raw} MiB |"
echo "| Packages | $(wc -l <"$out") |"
