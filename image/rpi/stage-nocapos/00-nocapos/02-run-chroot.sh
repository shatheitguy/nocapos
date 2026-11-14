#!/bin/bash -e
# Puts the NoCapOS layer in place inside the image (with every host package
# and NoCap Desktop preinstalled), then checks it: updates apply cleanly,
# branding files belong to no package, ZFS built for the Pi kernels, the
# preinstalled tools work.
/usr/lib/nocapos/image/layer.sh
/usr/lib/nocapos/image/check-image.sh
