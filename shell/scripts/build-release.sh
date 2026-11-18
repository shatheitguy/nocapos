#!/usr/bin/env bash
# Builds NoCap Shell's two programs (nocap-shell, nocap-lock) for a release
# bundle, inside a Debian container of the target architecture (the image
# workflows run it in debian:bookworm for amd64/arm64, so the programs run on
# Debian 12 and 13, Raspberry Pi OS and Ubuntu 24.04; armv7 builds in
# debian:trixie). Machines whose libraries differ build them themselves at
# install time from the bundle's sources (deploy/desktop/setup.sh).
#
#   shell/scripts/build-release.sh OUT_DIR [VERSION]
#
# Runs the bridge tests too; any failure stops the build.
set -euo pipefail
OUT="${1:?usage: build-release.sh OUT_DIR [VERSION]}"
VERSION="${2:-$(cat "$(dirname "$0")/../../VERSION" 2>/dev/null || echo dev)}"
HERE="$(cd "$(dirname "$0")/.." && pwd)"

if [ "$(id -u)" -eq 0 ] && command -v apt-get >/dev/null 2>&1; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -q >/dev/null
  apt-get install -y -q --no-install-recommends make gcc pkg-config libgtk-3-dev libgtk-layer-shell-dev \
    libwebkit2gtk-4.1-dev libsoup-3.0-dev libjson-glib-dev libpolkit-agent-1-dev libwayland-dev wayland-protocols \
    libxkbcommon-dev libcairo2-dev libpango1.0-dev libgdk-pixbuf-2.0-dev libpam0g-dev >/dev/null
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
cp -R "$HERE/." "$work/"
rm -rf "$work/build"
make -C "$work" -j"$(nproc)" NOCAP_VERSION="$VERSION" all test
mkdir -p "$OUT"
install -m 0755 "$work/build/nocap-shell" "$work/build/nocap-lock" "$OUT/"
"$OUT/nocap-shell" --version
echo "==> $OUT: nocap-shell, nocap-lock ($(uname -m))"
