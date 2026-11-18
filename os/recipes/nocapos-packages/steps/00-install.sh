#!/bin/bash
# Installs packages/common.list, desktop.list and the target's own list.
# Every line there names the NoCapOS feature that needs the package.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive

files=()
for l in common desktop "${RECIPE_PARAM_TARGET}"; do
  files+=("${RUGIX_PROJECT_DIR}/packages/$l.list")
  # Bakery rebuilds this layer when a list changes.
  echo "packages/$l.list" >>"$LAYER_REBUILD_IF_CHANGED"
done
echo "scripts/resolve-packages.sh" >>"$LAYER_REBUILD_IF_CHANGED"

mapfile -t pkgs < <(bash "${RUGIX_PROJECT_DIR}/scripts/resolve-packages.sh" "${files[@]}")
echo "Installing ${#pkgs[@]} packages: ${pkgs[*]}"
apt-get install -y --no-install-recommends "${pkgs[@]}"

# Services NoCapOS starts itself when a feature is turned on.
for unit in smbd nmbd samba-ad-dc libvirtd libvirt-guests ufw; do
  systemctl disable "$unit" 2>/dev/null || true
done
