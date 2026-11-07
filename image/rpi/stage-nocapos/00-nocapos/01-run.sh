#!/bin/bash -e
# Copies the NoCapOS layer (staged by image/rpi/build.sh into files/overlay)
# into the image.
cp -R files/overlay/. "${ROOTFS_DIR}/"
