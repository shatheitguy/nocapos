#!/bin/bash
# Seals the image. The device never runs apt: system packages only change
# with a whole new system image from a NoCapOS release, so apt has no
# sources here, and nothing the build left behind stays.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive

# Build-time helpers Rugix's Debian bootstrap brings in.
apt-get purge -y --auto-remove mmdebstrap 2>/dev/null || true

rm -f /etc/apt/apt.conf.d/99mmdebstrap
rm -f /etc/apt/sources.list /etc/apt/sources.list.d/*
cat >/etc/apt/sources.list.d/README <<'EOF'
NoCapOS installs system updates as whole system images (Glow Up), never
with apt. The image's Debian sources are recorded in
/usr/lib/nocapos/build-sources.list.
EOF

apt-get clean -y
rm -rf /var/lib/apt/lists/* /var/cache/apt/* /var/cache/debconf/*-old /var/lib/dpkg/*-old
rm -rf /usr/share/doc/*/[!c]* /usr/share/man/* /usr/share/info/* /usr/share/lintian/*
find /usr/share/doc -mindepth 1 -maxdepth 1 -type d -empty -delete 2>/dev/null || true
rm -rf /var/log/* /var/tmp/* /tmp/* /root/.cache /var/cache/man
find / -xdev -name '__pycache__' -type d -prune -exec rm -rf {} + 2>/dev/null || true
: >/etc/machine-id
rm -f /var/lib/dbus/machine-id /var/lib/systemd/random-seed

echo "Image sealed: $(du -sxh / 2>/dev/null | cut -f1) on disk before compression"
