#!/usr/bin/env bash
# build.sh: one entry point for the NoCapOS image build (Linux with Docker).
#   build.sh debs ARCH             NoCapOS's packages into build/debs/ARCH
#                                  and the image recipe (amd64, arm64)
#   build.sh image SYSTEM          build/SYSTEM.img       (pc, pi, pc-test)
#   build.sh bundle SYSTEM         build/SYSTEM.rugixb + .hash
#   build.sh test-key              a throwaway SSH key for the QEMU test
#   build.sh sizes                 a size table of build/ (Markdown)
# VERSION defaults to the repository's VERSION file.
set -euo pipefail

OS="$(cd "$(dirname "$0")/.." && pwd)"
ROOT="$(cd "$OS/.." && pwd)"
VERSION="${VERSION:-$(tr -d '[:space:]' <"$ROOT/VERSION")}"
BUILD="$OS/build"
mkdir -p "$BUILD"

# Bakery runs as root in its container: its files become this user's again.
bakery() {
  local rc=0
  (cd "$OS" && ./run-bakery "$@") || rc=$?
  if [ "$(id -u)" != 0 ] && command -v sudo >/dev/null 2>&1; then
    sudo -n chown -R "$(id -u):$(id -g)" "$BUILD" 2>/dev/null || true
  fi
  return "$rc"
}

cmd="${1:-}"; shift || true
case "$cmd" in
  debs)
    arch="${1:?arch}"
    out="$BUILD/debs/$arch"
    rm -rf "$out"
    # NoCap Shell's programs, built in Debian 13 (the image's libraries).
    shell_out="$BUILD/shell-bin/$arch"
    if [ ! -x "$shell_out/nocap-shell" ]; then
      mkdir -p "$shell_out"
      docker run --rm --platform "linux/$arch" -v "$ROOT:/src:ro" -v "$shell_out:/out" debian:trixie \
        bash -c "bash /src/shell/scripts/build-release.sh /out $VERSION && chown -R $(id -u):$(id -g) /out"
    fi
    [ -f "$ROOT/frontend/dist-shell/shell.html" ] || { echo "frontend/dist-shell is missing: cd frontend && npm run build:shell" >&2; exit 1; }
    NOCAP_SHELL_BINS="$shell_out" bash "$OS/packaging/build-debs.sh" "$VERSION" "$arch" "$out"
    [ -f "$out/nocap-desktop_${VERSION}_${arch}.deb" ] || { echo "NoCap Desktop's package wasn't built" >&2; exit 1; }
    rm -rf "$OS/recipes/nocapos-os/debs"
    mkdir -p "$OS/recipes/nocapos-os/debs"
    cp "$out"/*.deb "$OS/recipes/nocapos-os/debs/"
    ;;
  image)
    sys="${1:?system}"
    bakery bake image "$sys" --release-version "$VERSION" --release-id nocapos \
      --param-file config/snapshot.toml
    # Bakery leaves it in build/SYSTEM/system.img.
    cp --sparse=always "$BUILD/$sys/system.img" "$BUILD/$sys.img"
    ;;
  bundle)
    sys="${1:?system}"
    bakery bake bundle "$sys" "build/$sys.rugixb" --release-version "$VERSION" --release-id nocapos \
      --param-file config/snapshot.toml
    bash "$0" bundle-hash "$sys"
    ;;
  bundle-hash)
    sys="${1:?system}"
    bakery bundler hash "build/$sys.rugixb" | tail -n 1 | tr -d '[:space:]' >"$BUILD/$sys.hash"
    echo "bundle hash: $(cat "$BUILD/$sys.hash")"
    ;;
  test-key)
    rm -f "$OS/test/key" "$OS/test/key.pub"
    ssh-keygen -q -t ed25519 -N '' -C nocapos-qemu-test -f "$OS/test/key"
    mkdir -p "$OS/recipes/nocapos-test-access/files"
    cp "$OS/test/key.pub" "$OS/recipes/nocapos-test-access/files/test-key.pub"
    ;;
  sizes)
    echo "| File | Size |"
    echo "|---|---|"
    for f in "$BUILD"/*.img "$BUILD"/*.img.xz "$BUILD"/*.rugixb; do
      [ -f "$f" ] || continue
      printf '| %s | %s |\n' "$(basename "$f")" "$(du -h --apparent-size "$f" | cut -f1)"
    done
    ;;
  *)
    sed -n '2,10p' "$0"
    exit 2
    ;;
esac
