#!/usr/bin/env bash
# Installs NoCapOS onto a ZFS mirror of two disks, from the live session
# ("Try NoCapOS"). Both disks get the same layout and their own bootloader,
# so the server keeps running, and still starts, when either disk fails.
#
#   sudo /usr/lib/nocapos/image/install-zfs-mirror.sh DISK1 DISK2 [--hostname NAME] [--yes]
#
# DISK1/DISK2: whole disks, preferably /dev/disk/by-id/... names. Both are
# erased. Each gets: BIOS boot (1 MiB), EFI system (512 MiB), boot pool
# member (1 GiB), root pool member (the rest). The system is copied from
# the live image, so it is exactly what the regular installer installs.
# Needs a network connection (ZFS is built for the running kernel).
set -euo pipefail

say() { printf '==> %s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

HOSTNAME_NEW=nocap
YES=0
DISKS=()
while [ $# -gt 0 ]; do
  case "$1" in
    --hostname) HOSTNAME_NEW="${2:?--hostname needs a value}"; shift 2 ;;
    --yes) YES=1; shift ;;
    -h|--help) sed -n '2,12p' "$0"; exit 0 ;;
    -*) die "unknown option $1" ;;
    *) DISKS+=("$1"); shift ;;
  esac
done

[ "$(id -u)" -eq 0 ] || die "run as root (sudo)"
[ "${#DISKS[@]}" -eq 2 ] || die "give exactly two disks, e.g. /dev/disk/by-id/ata-A /dev/disk/by-id/ata-B"
[[ "$HOSTNAME_NEW" =~ ^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$ ]] || die "hostname: letters, numbers and - only"

if grep -qs "Raspberry Pi" /proc/device-tree/model; then
  die "the Raspberry Pi's firmware starts from one card or drive only, so a mirrored system drive can't take over by itself. Use the regular image and keep a backup of the card."
fi
ARCH="$(dpkg --print-architecture)"
EFI=0
[ -d /sys/firmware/efi ] && EFI=1
case "$ARCH" in
  amd64) ;;
  arm64) [ "$EFI" -eq 1 ] || die "64-bit ARM boards need UEFI for a mirrored system drive" ;;
  *) die "a mirrored system drive is supported on amd64 and arm64 (UEFI)" ;;
esac

A="$(readlink -f "${DISKS[0]}")"
B="$(readlink -f "${DISKS[1]}")"
[ -b "$A" ] && [ -b "$B" ] || die "both disks must be block devices"
[ "$A" != "$B" ] || die "pick two different disks"
for d in "$A" "$B"; do
  [ "$(lsblk -dno TYPE "$d")" = "disk" ] || die "$d is not a whole disk"
done
# Never the disk the live system runs from.
LIVE_SRC="$(findmnt -no SOURCE /run/live/medium 2>/dev/null || true)"
if [ -n "$LIVE_SRC" ]; then
  LIVE_DISK="/dev/$(lsblk -no PKNAME "$LIVE_SRC" 2>/dev/null | head -1)"
  for d in "$A" "$B"; do
    [ "$d" != "$LIVE_DISK" ] && [ "$d" != "$LIVE_SRC" ] || die "$d holds the live system you started from"
  done
fi
SQUASH=/run/live/medium/live/filesystem.squashfs
[ -f "$SQUASH" ] || die "run this from the NoCapOS live session (no $SQUASH)"

# Stable names for the pool members.
byid() {
  local dev="$1" l
  for l in /dev/disk/by-id/*; do
    case "$l" in *-part*|*/wwn-*) continue ;; esac
    [ "$(readlink -f "$l")" = "$dev" ] && { echo "$l"; return; }
  done
  echo "$dev"
}
part() { case "$1" in /dev/disk/by-id/*) echo "$1-part$2" ;; *[0-9]) echo "$1p$2" ;; *) echo "$1$2" ;; esac; }
IDA="$(byid "$A")"
IDB="$(byid "$B")"

SIZE_A="$(lsblk -bdno SIZE "$A")"
SIZE_B="$(lsblk -bdno SIZE "$B")"
echo
echo "NoCapOS will be installed on a mirror of:"
lsblk -dno NAME,SIZE,MODEL,SERIAL "$A" "$B" | sed 's/^/    /'
echo "Everything on both disks will be erased."
if [ "$SIZE_A" != "$SIZE_B" ]; then
  echo "The disks are different sizes; the mirror uses the smaller one's space."
fi
if [ "$YES" -ne 1 ]; then
  read -r -p "Type ERASE to continue: " ok
  [ "$ok" = "ERASE" ] || die "stopped; nothing was changed"
fi

say "Installing ZFS in the live session (this builds it for the running kernel)"
grep -qs contrib /etc/apt/sources.list /etc/apt/sources.list.d/* || sed -i 's/ main\b/ main contrib/' /etc/apt/sources.list
apt-get update -q
DEBIAN_FRONTEND=noninteractive apt-get install -y -q --no-install-recommends \
  "linux-headers-$(uname -r)" zfs-dkms zfsutils-linux gdisk dosfstools squashfs-tools
modprobe zfs

say "Partitioning"
for d in "$A" "$B"; do
  swapoff --all || true
  wipefs -a "$d"
  sgdisk --zap-all "$d"
  sgdisk -a1 -n1:24K:+1000K -t1:EF02 -c1:biosboot "$d"
  sgdisk -n2:1M:+512M -t2:EF00 -c2:efi "$d"
  sgdisk -n3:0:+1G -t3:BF01 -c3:boot "$d"
  sgdisk -n4:0:0 -t4:BF00 -c4:root "$d"
done
partprobe "$A" "$B" || true
udevadm settle
sleep 2

say "Creating the boot and root pools (mirrored)"
zpool create -f -o ashift=12 -o autotrim=on -o compatibility=grub2 -o cachefile=/etc/zfs/zpool.cache \
  -O devices=off -O acltype=posixacl -O xattr=sa -O compression=lz4 -O normalization=formD -O relatime=on \
  -O canmount=off -O mountpoint=/boot -R /mnt \
  bpool mirror "$(part "$IDA" 3)" "$(part "$IDB" 3)"
zpool create -f -o ashift=12 -o autotrim=on \
  -O acltype=posixacl -O xattr=sa -O dnodesize=auto -O compression=lz4 -O normalization=formD -O relatime=on \
  -O canmount=off -O mountpoint=/ -R /mnt \
  rpool mirror "$(part "$IDA" 4)" "$(part "$IDB" 4)"
zfs create -o canmount=off -o mountpoint=none rpool/ROOT
zfs create -o canmount=off -o mountpoint=none bpool/BOOT
zfs create -o canmount=noauto -o mountpoint=/ rpool/ROOT/nocapos
zfs mount rpool/ROOT/nocapos
zfs create -o mountpoint=/boot bpool/BOOT/nocapos
zfs create rpool/home
zfs create -o mountpoint=/root rpool/home/root
zfs create -o canmount=off rpool/var
zfs create rpool/var/lib
zfs create rpool/var/log
zfs create rpool/var/cache
zfs create rpool/var/tmp
chmod 1777 /mnt/var/tmp
chmod 700 /mnt/root

say "Copying the system"
unsquashfs -f -d /mnt "$SQUASH" >/dev/null
mkdir -p /mnt/etc/zfs
cp /etc/zfs/zpool.cache /mnt/etc/zfs/

say "Setting up the EFI partitions"
ESP_A="$(part "$IDA" 2)"
ESP_B="$(part "$IDB" 2)"
mkfs.vfat -F 32 -n NOCAPEFI "$ESP_A"
mkfs.vfat -F 32 -n NOCAPEFI2 "$ESP_B"
mkdir -p /mnt/boot/efi /mnt/boot/efi2
UUID_A="$(blkid -s UUID -o value "$ESP_A")"
UUID_B="$(blkid -s UUID -o value "$ESP_B")"
cat >/mnt/etc/fstab <<EOF
# NoCapOS on a ZFS mirror: pools mount themselves; both EFI partitions are listed.
UUID=$UUID_A /boot/efi vfat umask=0077,nofail,x-systemd.device-timeout=10s 0 1
UUID=$UUID_B /boot/efi2 vfat umask=0077,nofail,x-systemd.device-timeout=10s 0 1
EOF
mount "$ESP_A" /mnt/boot/efi
mount "$ESP_B" /mnt/boot/efi2

echo "$HOSTNAME_NEW" >/mnt/etc/hostname
printf '127.0.0.1\tlocalhost\n127.0.1.1\t%s\n' "$HOSTNAME_NEW" >/mnt/etc/hosts

for fs in dev proc sys run; do mount --make-private --rbind "/$fs" "/mnt/$fs"; done
cleanup() {
  for fs in run sys proc dev; do umount -lf "/mnt/$fs" 2>/dev/null || true; done
}
trap cleanup EXIT

if [ "$ARCH" = amd64 ]; then
  if [ "$EFI" -eq 1 ]; then GRUB_PKGS="grub-efi-amd64 grub-efi-amd64-signed shim-signed"; else GRUB_PKGS="grub-pc"; fi
  KERNEL_PKGS="linux-image-amd64 linux-headers-amd64"
  EFI_TARGET=x86_64-efi
else
  GRUB_PKGS="grub-efi-arm64"
  KERNEL_PKGS="linux-image-arm64 linux-headers-arm64"
  EFI_TARGET=arm64-efi
fi

say "Installing ZFS boot support and the bootloader in the new system"
cp /etc/apt/sources.list /mnt/etc/apt/sources.list
chroot /mnt /bin/bash -euo pipefail -c "
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -q
  apt-get purge -y -q live-boot live-boot-initramfs-tools live-config live-config-systemd live-tools 2>/dev/null || true
  apt-get install -y -q --no-install-recommends $KERNEL_PKGS zfs-dkms zfsutils-linux zfs-initramfs $GRUB_PKGS
  echo REMAKE_INITRD=yes >/etc/dkms/zfs.conf
  mkdir -p /etc/default/grub.d
  printf 'GRUB_CMDLINE_LINUX=\"root=ZFS=rpool/ROOT/nocapos\"\n' >/etc/default/grub.d/10-nocapos-zfs.cfg
  mkdir -p /etc/zfs/zfs-list.cache
  touch /etc/zfs/zfs-list.cache/bpool /etc/zfs/zfs-list.cache/rpool
  systemctl enable zfs-import-cache.service zfs-mount.service zfs-zed.service zfs.target || true
  update-initramfs -c -k all || update-initramfs -u -k all
  update-grub
"

if [ "$EFI" -eq 1 ]; then
  chroot /mnt grub-install --target="$EFI_TARGET" --efi-directory=/boot/efi --bootloader-id=nocapos --recheck
  chroot /mnt grub-install --target="$EFI_TARGET" --efi-directory=/boot/efi2 --bootloader-id=nocapos-mirror --recheck
  # Fallback copies, used when the firmware forgets its boot entries.
  chroot /mnt grub-install --target="$EFI_TARGET" --efi-directory=/boot/efi --removable --no-nvram --recheck
  chroot /mnt grub-install --target="$EFI_TARGET" --efi-directory=/boot/efi2 --removable --no-nvram --recheck
else
  chroot /mnt grub-install --target=i386-pc --recheck "$A"
  chroot /mnt grub-install --target=i386-pc --recheck "$B"
fi

say "Creating the console user 'nocap'"
chroot /mnt /bin/bash -c "id nocap >/dev/null 2>&1 || useradd -m -s /bin/bash -G sudo nocap"
if [ "$YES" -ne 1 ]; then
  chroot /mnt passwd nocap
else
  chroot /mnt passwd -l nocap
fi

say "Turning on NoCapOS"
chroot /mnt /bin/bash -c "rm -f /var/lib/nocapos/.image-firstboot-done; systemctl enable nocapos.service nocapos-firstboot.service nocapos-setup-hint.service; /usr/lib/nocapos/image/os-release-sync || true"

zfs snapshot -r rpool/ROOT/nocapos@install
zfs snapshot -r bpool/BOOT/nocapos@install

say "Finishing"
umount /mnt/boot/efi /mnt/boot/efi2
cleanup
trap - EXIT
zfs umount -a || true
zpool export bpool
zpool export rpool
echo
echo "Done. Remove the USB stick and restart. NoCapOS now runs on a mirror:"
echo "if either disk fails, it keeps running and starts from the other one."
