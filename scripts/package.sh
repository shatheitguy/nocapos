#!/usr/bin/env bash
# Builds the Linux release bundles (amd64, arm64, armv7) into dist/ — the same
# output as package.ps1, for Linux/macOS and GitHub Actions.
#   scripts/package.sh 0.3.0
set -euo pipefail
VERSION="${1:-0.0.0-dev}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="$ROOT/dist"
rm -rf "$DIST"; mkdir -p "$DIST"

echo "==> Building the web UI"
(cd "$ROOT/frontend" && npm ci --no-audit --no-fund && npm run build)

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
  cp "$ROOT/deploy/install.sh" "$ROOT/deploy/nocapos.service" "$ROOT/deploy/docker-compose.yml" \
     "$ROOT/deploy/docker-compose.nvidia.yml" "$ROOT/deploy/Dockerfile.bundle" "$stage/"
  printf '%s' "$VERSION" >"$stage/VERSION"
  printf '%s' "$target" >"$stage/ARCH"
  : >"$stage/rootfs/data/.keep"
  cat >"$stage/README.txt" <<EOF
NoCapOS $VERSION for Linux ($target)

Install (recommended): NoCapOS runs as root and manages this server -
Linux users, network, power, storage, a root terminal and one-click
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
