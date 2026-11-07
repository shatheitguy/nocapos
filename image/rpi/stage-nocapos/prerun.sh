#!/bin/bash -e
# pi-gen stage: start from the previous stage's root filesystem (Lite).
if [ ! -d "${ROOTFS_DIR}" ]; then
  copy_previous
fi
