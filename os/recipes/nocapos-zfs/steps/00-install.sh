#!/bin/bash
# ZFS's kernel module is built here, once, for exactly the kernels in the
# image. The device never compiles anything: dkms, the compiler and the
# kernel headers are removed again before the layer is sealed.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive

echo "packages/build-only.list" >>"$LAYER_REBUILD_IF_CHANGED"
mapfile -t build < <(bash "${RUGIX_PROJECT_DIR}/scripts/resolve-packages.sh" "${RUGIX_PROJECT_DIR}/packages/build-only.list")
apt-get install -y --no-install-recommends "${build[@]}"

zver="$(dpkg-query -W -f='${Version}' zfs-dkms | sed 's/-[^-]*$//')"
kernels=()
for d in /lib/modules/*; do
  k="$(basename "$d")"
  [ -d "/usr/src/linux-headers-$k" ] || [ -e "$d/build" ] || continue
  kernels+=("$k")
done
[ "${#kernels[@]}" -gt 0 ] || { echo "no kernel with headers found" >&2; exit 1; }

for k in "${kernels[@]}"; do
  echo "Building ZFS $zver for $k"
  if ! dkms status -m zfs -v "$zver" -k "$k" | grep -q installed; then
    dkms install "zfs/$zver" -k "$k"
  fi
  # Keep the built modules outside dkms's tree, so removing dkms leaves them.
  mkdir -p "/lib/modules/$k/extra/zfs"
  find "/lib/modules/$k/updates" -name '*.ko*' -path '*dkms*' -exec mv -f {} "/lib/modules/$k/extra/zfs/" \;
done

# The module is built: drop dkms (forgetting the module first, so its
# removal scripts don't delete it), the compiler and the kernel headers.
rm -rf /var/lib/dkms/zfs
apt-get purge -y zfs-dkms dkms build-essential 'linux-headers-*'
apt-get autoremove -y --purge
for k in "${kernels[@]}"; do
  depmod -a "$k"
  modinfo -k "$k" zfs >/dev/null || { echo "ZFS module missing for $k" >&2; exit 1; }
  echo "ZFS module ready for $k"
done
