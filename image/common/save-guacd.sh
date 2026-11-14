#!/usr/bin/env bash
# Saves the guacd container image pinned in backend/internal/rdp/guacd.ref
# (name:tag@digest) for one architecture as a docker-load archive,
# compressed with zstd, for the OS images to preload.
#
#   image/common/save-guacd.sh REF_FILE ARCH OUT.tar.zst
#     ARCH  amd64 | arm64
#
# Uses skopeo when it's there (no Docker daemon needed: the PC build runs in
# a plain container), otherwise docker. Checks that the archive is for ARCH
# and carries the name:tag alfad looks for; any problem stops the build.
set -euo pipefail

REF_FILE="${1:?guacd.ref}"
ARCH="${2:?arch}"
OUT="${3:?output .tar.zst}"

ref="$(grep -v '^[[:space:]]*#' "$REF_FILE" | grep -m1 -E '[^[:space:]]' | tr -d '[:space:]')"
case "$ref" in
  *:*@sha256:*) ;;
  *) echo "save-guacd: $REF_FILE must hold name:tag@sha256:<digest>, got '$ref'" >&2; exit 1 ;;
esac
digest="${ref#*@}"
named="${ref%@*}"      # name:tag
name="${named%:*}"
command -v zstd >/dev/null || { echo "save-guacd: zstd is required" >&2; exit 1; }
command -v python3 >/dev/null || { echo "save-guacd: python3 is required" >&2; exit 1; }

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
tar="$tmp/guacd.tar"

echo "==> guacd image $named ($digest, linux/$ARCH)"
if command -v skopeo >/dev/null; then
  skopeo --insecure-policy --override-os linux --override-arch "$ARCH" copy --retry-times 3 -q \
    "docker://$name@$digest" "docker-archive:$tar:$named"
elif command -v docker >/dev/null; then
  docker pull -q --platform "linux/$ARCH" "$name@$digest"
  docker tag "$name@$digest" "$named"
  docker save -o "$tar" "$named"
  docker rmi "$named" "$name@$digest" >/dev/null 2>&1 || true
else
  echo "save-guacd: needs skopeo or docker" >&2
  exit 1
fi

# The archive must be loadable as name:tag and built for ARCH. skopeo
# writes the full name (docker.io/guacamole/guacd:tag); the archive gets
# the short name docker save uses, the one alfad asks Docker for.
python3 - "$tar" "$named" "$ARCH" <<'PY'
import io, json, os, sys, tarfile
path, named, arch = sys.argv[1:]

def short(tag):
    for p in ("docker.io/library/", "docker.io/"):
        if tag.startswith(p):
            return tag[len(p):]
    return tag

with tarfile.open(path) as t:
    manifest = json.load(t.extractfile("manifest.json"))
tags = [tag for m in manifest for tag in (m.get("RepoTags") or [])]
if named not in tags:
    if named not in [short(x) for x in tags]:
        sys.exit(f"save-guacd: archive tags are {tags}, expected {named}")
    for m in manifest:
        m["RepoTags"] = [short(x) for x in (m.get("RepoTags") or [])]
    data = json.dumps(manifest).encode()
    with tarfile.open(path) as src, tarfile.open(path + ".new", "w", format=tarfile.PAX_FORMAT) as dst:
        for m in src.getmembers():
            if m.name == "repositories":
                continue  # optional; its full name would disagree with RepoTags
            if m.name == "manifest.json":
                m.size = len(data)
                dst.addfile(m, io.BytesIO(data))
            else:
                dst.addfile(m, src.extractfile(m) if m.isfile() else None)
    os.replace(path + ".new", path)

with tarfile.open(path) as t:
    manifest = json.load(t.extractfile("manifest.json"))
    tags = [tag for m in manifest for tag in (m.get("RepoTags") or [])]
    if named not in tags:
        sys.exit(f"save-guacd: archive tags are {tags}, expected {named}")
    config = json.load(t.extractfile(manifest[0]["Config"]))
    if config.get("os") != "linux" or config.get("architecture") != arch:
        sys.exit(f"save-guacd: archive is {config.get('os')}/{config.get('architecture')}, expected linux/{arch}")
    layers = len(manifest[0]["Layers"])
print(f"    {named}: linux/{arch}, {layers} layers")
PY

mkdir -p "$(dirname "$OUT")"
# -19 --long=27: small, and still decompresses with zstd's default memory limit.
zstd -q -19 --long=27 -T0 -f "$tar" -o "$OUT"
chmod 0644 "$OUT"
zstd -q -t "$OUT"
echo "    $(basename "$OUT"): $(($(stat -c %s "$tar") / 1048576)) MiB tar, $(($(stat -c %s "$OUT") / 1048576)) MiB zstd"
