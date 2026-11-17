#!/usr/bin/env bash
# Builds the Linux release bundles (amd64, arm64, armv7) into dist/ — the same
# output as package.ps1, for Linux/macOS and GitHub Actions.
#   scripts/package.sh 0.3.0
#
# NoCap Desktop's native programs (nocap-shell, nocap-lock) are C: set
# NOCAP_SHELL_BINS to a folder holding amd64/, arm64/ and armv7/ builds of
# them (shell/scripts/build-release.sh; the release workflow builds them in
# containers of each architecture). An architecture without them still gets
# the sources, and install.sh builds them on the machine.
set -euo pipefail
VERSION="${1:-0.0.0-dev}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="$ROOT/dist"
rm -rf "$DIST"; mkdir -p "$DIST"

echo "==> Building the web UI and NoCap Desktop's pages"
(cd "$ROOT/frontend" && npm ci --no-audit --no-fund && npm run build && npm run build:shell)
SHELL_BINS="${NOCAP_SHELL_BINS:-}"

sums=()
for target in amd64 arm64 armv7; do
  arch=$target; arm=""
  [ "$target" = armv7 ] && { arch=arm; arm=7; }
  name="nocapos-$VERSION-linux-$target"
  stage="$DIST/$name"
  mkdir -p "$stage/rootfs/data"
  echo "==> Building $target"
  (cd "$ROOT/backend" && CGO_ENABLED=0 GOOS=linux GOARCH=$arch GOARM=$arm \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$stage/alfad" ./cmd/alfad)
  (cd "$ROOT/backend" && CGO_ENABLED=0 GOOS=linux GOARCH=$arch GOARM=$arm \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$stage/cap" ./cmd/cap)
  cp "$ROOT/deploy/install.sh" "$ROOT/deploy/uninstall.sh" "$ROOT/deploy/nocapos.service" "$ROOT/deploy/docker-compose.yml" \
     "$ROOT/deploy/docker-compose.nvidia.yml" "$ROOT/deploy/Dockerfile.bundle" "$stage/"
  cp -R "$ROOT/deploy/plymouth" "$stage/plymouth" # boot splash theme
  cp -R "$ROOT/deploy/desktop" "$stage/desktop"   # NoCap Desktop: full desktop + boot branding
  # NoCap Shell: NoCap OS's pages for the screen, the programs for this
  # architecture, and their sources (built on the machine when needed).
  mkdir -p "$stage/shell/src"
  cp -R "$ROOT/frontend/dist-shell" "$stage/shell/ui"
  cp -R "$ROOT/shell/Makefile" "$ROOT/shell/PROTOCOL.md" "$ROOT/shell/host" "$ROOT/shell/lock" "$ROOT/shell/protocols" "$ROOT/shell/test" "$stage/shell/src/"
  if [ -n "$SHELL_BINS" ] && [ -x "$SHELL_BINS/$target/nocap-shell" ] && [ -x "$SHELL_BINS/$target/nocap-lock" ]; then
    mkdir -p "$stage/shell/bin"
    install -m 0755 "$SHELL_BINS/$target/nocap-shell" "$SHELL_BINS/$target/nocap-lock" "$stage/shell/bin/"
  else
    echo "    (no NoCap Shell build for $target: installs build it on the machine)"
  fi
  mkdir -p "$stage/brand" "$stage/wallpapers"
  cp "$ROOT"/frontend/public/brand/*.png "$stage/brand/"
  cp "$ROOT"/frontend/public/wallpapers/*.jpg "$stage/wallpapers/"
  printf '%s' "$VERSION" >"$stage/VERSION"
  printf '%s' "$target" >"$stage/ARCH"
  : >"$stage/rootfs/data/.keep"
  cat >"$stage/README.txt" <<EOF
NoCapOS $VERSION for Linux ($target)

Install (recommended): NoCapOS runs as root and manages this server -
Linux users, network, power, storage, a host terminal and one-click
Docker apps (Docker is installed if missing):
    sudo bash install.sh

Run NoCapOS itself in Docker instead (apps only, no host control):
    sudo bash install.sh --docker

Options:  --storage /path/for/files   --port 8443   --no-docker   --yes
Remove:   sudo bash install.sh --uninstall   (your files are never deleted)
EOF
  file="nocapos-linux-$target.tar.gz"
  tar -czf "$DIST/$file" -C "$DIST" "$name"
  rm -rf "$stage"
  sums+=("$(cd "$DIST" && sha256sum "$file")")
  echo "    packaged $file"
done
printf '%s\n' "${sums[@]}" >"$DIST/SHA256SUMS"
cp "$ROOT/deploy/install.sh" "$DIST/install.sh"
echo "==> Done: $DIST"
