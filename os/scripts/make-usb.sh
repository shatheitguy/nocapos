#!/usr/bin/env bash
# make-usb.sh PC_IMAGE USB_IMAGE: the NoCapOS USB stick image (the
# installer) from the PC system image. Same system, plus:
#   - the "nocapos-installer" flag on the startup partition, so the stick
#     starts the full-screen setup instead of NoCapOS;
#   - no first-boot partitioning (the stick keeps its size);
#   - a small spare slot (5) and a 768 MiB data partition (6) holding the
#     pristine startup partition (nocapos-install/config.img), which the
#     setup writes onto the new drive.
# Needs: sfdisk, mtools (mcopy, mdel), mkfs.ext4 with -d.
set -euo pipefail
src="${1:?pc image}"
out="${2:?usb image}"
DATA_MIB=768
SPARE_MIB=8

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

cp --sparse=always "$src" "$out"

# Partition 1's offset and size (sectors), from the image's own table.
read -r p1_start p1_size < <(sfdisk -d "$src" | awk -F'[=,]' '/^[^ ]*1 :/ {gsub(/ /,""); print $2, $4}')
[ -n "$p1_start" ] || { echo "no partition 1 in $src" >&2; exit 1; }

# The pristine startup partition, before anything touches it.
mkdir -p "$tmp/data/nocapos-install"
dd if="$src" of="$tmp/data/nocapos-install/config.img" bs=512 skip="$p1_start" count="$p1_size" status=none
chmod 600 "$tmp/data/nocapos-install/config.img"

# The stick: installer flag on, first-boot partitioning off.
off=$((p1_start * 512))
echo "NoCapOS USB stick: starts the setup" >"$tmp/flag"
mcopy -o -i "$out@@$off" "$tmp/flag" ::nocapos-installer
mdel -i "$out@@$off" ::.rugix/bootstrap 2>/dev/null || true
mdel -i "$out@@$off" ::rugix/bootstrap 2>/dev/null || true

# Append the spare slot and the data partition.
size_bytes="$(stat -c %s "$out")"
end_mib=$(((size_bytes + 1048575) / 1048576))
truncate -s $(((end_mib + SPARE_MIB + DATA_MIB + 2) * 1048576)) "$out"
sfdisk --relocate gpt-bak-std "$out" >/dev/null 2>&1 || true
printf 'size=%sMiB, type=0FC63DAF-8483-4772-8E79-3D69D8477DE4\nsize=%sMiB, type=0FC63DAF-8483-4772-8E79-3D69D8477DE4\n' "$SPARE_MIB" "$DATA_MIB" |
  sfdisk --append --no-reread --no-tell-kernel "$out" >/dev/null
sfdisk -d "$out"

read -r p6_start p6_size < <(sfdisk -d "$out" | awk -F'[=,]' '/^[^ ]*6 :/ {gsub(/ /,""); print $2, $4}')
[ -n "$p6_start" ] || { echo "the data partition wasn't made" >&2; exit 1; }
mkfs.ext4 -q -F -L data -d "$tmp/data" "$tmp/data.img" "$((p6_size / 2))k"
dd if="$tmp/data.img" of="$out" bs=512 seek="$p6_start" conv=notrunc,sparse status=none
echo "USB stick image: $out ($(du -h --apparent-size "$out" | cut -f1))"
