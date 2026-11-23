#!/usr/bin/env bash
# Builds the NoCapOS container template: a Debian 13 root filesystem with
# NoCapOS installed in server mode, packed the way hypervisors import system
# container templates (a zstd-compressed tar of the root filesystem).
#
#   image/lxc/build.sh VERSION ARCH OUTDIR
#     VERSION  NoCapOS release, e.g. 0.14.6 (the bundle comes from release v<VERSION>)
#     ARCH     amd64 | arm64 (built natively: run it on a machine of that architecture)
#     OUTDIR   gets nocapos-<VERSION>-lxc-<ARCH>.tar.zst, its .sha256 and its package list
#
# Runs as root on Debian 13 (or in a privileged debian:trixie container) with
# mmdebstrap, zstd and curl. Environment:
#   NOCAP_DIST      folder with nocapos-linux-<ARCH>.tar.gz and SHA256SUMS
#                   (scripts/package.sh output) instead of the release download
#   NOCAP_SNAPSHOT  Debian snapshot to build from (snapshot.debian.org, e.g.
#                   20261115T000000Z; default: os/config/snapshot.toml's), so
#                   the same inputs give the same packages
#   SOURCE_DATE_EPOCH  timestamps inside the template (default: now)
set -euo pipefail

VERSION="${1:?version}"
ARCH="${2:?arch: amd64 or arm64}"
OUT="${3:?output folder}"
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"
REPO="${NOCAP_REPO:-shatheitguy/nocapos}"
RELEASE_URL="${NOCAP_RELEASE_URL:-https://github.com/$REPO/releases/download/v$VERSION}"
NAME="nocapos-$VERSION-lxc-$ARCH"

case "$ARCH" in amd64 | arm64) ;; *) echo "unsupported arch: $ARCH" >&2; exit 2 ;; esac
case "$VERSION" in *[!0-9A-Za-z.+-]* | '') echo "bad version: $VERSION" >&2; exit 2 ;; esac
[ "$(id -u)" -eq 0 ] || { echo "run as root" >&2; exit 1; }
for t in mmdebstrap zstd curl tar sha256sum; do
  command -v "$t" >/dev/null || { echo "missing $t (apt-get install mmdebstrap zstd curl)" >&2; exit 1; }
done
case "$(dpkg --print-architecture)" in
  "$ARCH") ;;
  *) echo "build the $ARCH template on an $ARCH machine (this one is $(dpkg --print-architecture))" >&2; exit 1 ;;
esac

SNAP="${NOCAP_SNAPSHOT:-$(sed -n 's/^snapshot = "\(.*\)"/\1/p' "$ROOT/os/config/snapshot.toml" | head -1)}"
[[ "$SNAP" =~ ^[0-9]{8}T[0-9]{6}Z$ ]] || { echo "bad Debian snapshot: '$SNAP'" >&2; exit 1; }
export SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(date +%s)}"

mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
STAGE="$WORK/stage"   # copied into the root filesystem as-is
HOOK="$WORK/hook"     # build-time only: /tmp/nocap-build in the chroot
ROOTFS="$WORK/rootfs"

# ---- every package has a reason (the same rule as os/packages) ----------------
bad=0
n=0
while IFS= read -r line || [ -n "$line" ]; do
  n=$((n + 1))
  case "$line" in '' | '#'*) continue ;; esac
  reason=""
  [ "${line%%#*}" != "$line" ] && reason="${line#*#}"
  if [ -z "$(echo "$reason" | tr -d '[:space:]')" ]; then
    echo "packages.list:$n: no reason given for: $line" >&2
    bad=1
  fi
done <"$HERE/packages.list"
[ "$bad" -eq 0 ] || exit 1

# ---- the NoCapOS bundle, checked against the release's SHA256SUMS --------------
file="nocapos-linux-$ARCH.tar.gz"
mkdir -p "$WORK/dl"
if [ -n "${NOCAP_DIST:-}" ]; then
  echo "==> Using the local bundle in $NOCAP_DIST"
  cp "$NOCAP_DIST/$file" "$NOCAP_DIST/SHA256SUMS" "$WORK/dl/"
else
  echo "==> Downloading $file and SHA256SUMS from $RELEASE_URL"
  curl -fsSL --retry 3 "$RELEASE_URL/$file" -o "$WORK/dl/$file"
  curl -fsSL --retry 3 "$RELEASE_URL/SHA256SUMS" -o "$WORK/dl/SHA256SUMS"
fi
want="$(tr -d '\r' <"$WORK/dl/SHA256SUMS" | awk -v f="$file" '$2 == f || $2 == "*" f {print $1}')"
got="$(sha256sum "$WORK/dl/$file" | awk '{print $1}')"
if [ -z "$want" ] || [ "$want" != "$got" ]; then
  echo "checksum mismatch for $file (expected '${want:-none}', got '$got')" >&2
  exit 1
fi
echo "    checksum verified: $got"
mkdir -p "$WORK/x"
tar -xzf "$WORK/dl/$file" -C "$WORK/x"
src="$(find "$WORK/x" -mindepth 1 -maxdepth 1 -type d | head -1)"
[ -f "$src/alfad" ] && [ -f "$src/install.sh" ] || { echo "the bundle has no alfad/install.sh" >&2; exit 1; }

# ---- what goes into the root filesystem ----------------------------------------
# The bundle (without NoCap Desktop's programs and pages: a container has no
# screen), the first-boot pieces and apt settings the OS images use, and the
# container's own settings.
mkdir -p "$STAGE/usr/lib/nocapos/bundle" "$STAGE/usr/lib/nocapos/image" "$STAGE/etc/systemd/system" "$STAGE/etc/apt/apt.conf.d"
cp -R "$src/." "$STAGE/usr/lib/nocapos/bundle/"
rm -rf "$STAGE/usr/lib/nocapos/bundle/shell"
C="$ROOT/image/common/rootfs"
install -m 0755 "$C/usr/lib/nocapos/image/firstboot.sh" "$C/usr/lib/nocapos/image/setup-hint.sh" \
  "$C/usr/lib/nocapos/image/os-release-sync" "$STAGE/usr/lib/nocapos/image/"
install -m 0644 "$C/etc/systemd/system/nocapos-firstboot.service" "$C/etc/systemd/system/nocapos-setup-hint.service" \
  "$STAGE/etc/systemd/system/"
install -m 0644 "$C"/etc/apt/apt.conf.d/* "$STAGE/etc/apt/apt.conf.d/"
cp -R "$HERE/rootfs/." "$STAGE/"
# Settings files: plain 0644 whatever the checkout gave them.
find "$STAGE/etc/nocapos" -type f -exec chmod 0644 {} +
cat >"$STAGE/usr/lib/nocapos/image/image-release" <<EOF
# Written when this NoCapOS container template was built.
NOCAP_IMAGE_VERSION=$VERSION
NOCAP_IMAGE_ARCH=$ARCH
NOCAP_IMAGE_PLATFORM=lxc
NOCAP_IMAGE_BUILT=$(date -u -d "@$SOURCE_DATE_EPOCH" +%Y-%m-%dT%H:%M:%SZ)
NOCAP_IMAGE_DEBIAN_SNAPSHOT=$SNAP
EOF
mkdir -p "$HOOK"
install -m 0755 "$HERE/customize.sh" "$ROOT/os/scripts/resolve-packages.sh" "$HOOK/"
install -m 0644 "$HERE/packages.list" "$HOOK/"

# ---- Debian 13 from the snapshot, then NoCapOS (customize.sh, in the chroot) ---
M="http://snapshot.debian.org/archive"
echo "==> Building the root filesystem (Debian 13, snapshot $SNAP, $ARCH)"
mmdebstrap --mode=root --variant=minbase --arch="$ARCH" --components=main \
  --aptopt='Acquire::Check-Valid-Until "false"' \
  --aptopt='Acquire::Retries "5"' \
  --aptopt='APT::Install-Recommends "false"' \
  --customize-hook="sync-in '$STAGE' /" \
  --customize-hook='mkdir -p "$1/tmp/nocap-build"' \
  --customize-hook="sync-in '$HOOK' /tmp/nocap-build" \
  --chrooted-customize-hook='bash /tmp/nocap-build/customize.sh' \
  --customize-hook='rm -rf "$1/tmp/nocap-build"' \
  trixie "$ROOTFS" \
  "deb $M/debian/$SNAP/ trixie main" \
  "deb $M/debian/$SNAP/ trixie-updates main" \
  "deb $M/debian-security/$SNAP/ trixie-security main"

# The installed system uses Debian's own mirrors (customize.sh wrote them);
# the snapshot was only for building.
[ -f "$ROOTFS/etc/apt/sources.list.d/debian.sources" ] || { echo "customize.sh didn't finish" >&2; exit 1; }
rm -f "$ROOTFS/etc/apt/sources.list"

# The hypervisor makes /dev for each container.
find "$ROOTFS/dev" -mindepth 1 -delete

chroot "$ROOTFS" dpkg-query -W -f='${Package}\t${Version}\n' | sort >"$OUT/$NAME.packages"
echo "==> $(wc -l <"$OUT/$NAME.packages") packages, $(du -sh "$ROOTFS" | cut -f1) unpacked"

# ---- pack -------------------------------------------------------------------------
echo "==> Packing $NAME.tar.zst"
tar --numeric-owner --xattrs --xattrs-include='*' --acls --sort=name \
  --mtime="@$SOURCE_DATE_EPOCH" --clamp-mtime \
  --pax-option=exthdr.name=%d/PaxHeaders/%f,delete=atime,delete=ctime \
  -C "$ROOTFS" -cf - . | zstd -q -19 -T0 -o "$OUT/$NAME.tar.zst" -f
size="$(stat -c %s "$OUT/$NAME.tar.zst")"
# GitHub's release assets stop at 2 GiB.
[ "$size" -lt 2147483648 ] || { echo "the template is too big for a release ($size bytes)" >&2; exit 1; }
(cd "$OUT" && sha256sum "$NAME.tar.zst" >"$NAME.tar.zst.sha256")
ls -lh "$OUT"
echo "==> Done: $OUT/$NAME.tar.zst"
