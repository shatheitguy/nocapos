#!/usr/bin/env bash
# nocapos-images.service: loads the container images that came with the OS
# image (/usr/lib/nocapos/images/*.tar.zst, e.g. the remote desktop engine
# guacd) into Docker, then deletes each archive to free the space. Nothing
# is downloaded. An archive that doesn't load stays for the next boot; until
# then NoCapOS downloads the image when it's first needed, as on any other
# install.
set -u

DIR=/usr/lib/nocapos/images
shopt -s nullglob
files=("$DIR"/*.tar.zst)
[ "${#files[@]}" -gt 0 ] || exit 0

# Docker switched off (image.conf NOCAP_DOCKER=no, or by hand): leave the
# archives for when it's on.
if ! systemctl is-enabled --quiet docker.service 2>/dev/null && ! systemctl is-active --quiet docker.service 2>/dev/null; then
  echo "Docker is off; bundled images stay in $DIR"
  exit 0
fi
systemctl start docker.service 2>/dev/null || true
up=0
for _ in $(seq 1 60); do
  if docker info >/dev/null 2>&1; then up=1; break; fi
  sleep 2
done
[ "$up" -eq 1 ] || { echo "Docker didn't start; trying again on the next boot" >&2; exit 0; }

rc=0
for f in "${files[@]}"; do
  echo "Loading $(basename "$f")"
  if out="$(zstd -dc "$f" | docker load 2>&1)"; then
    printf '%s\n' "$out"
    loaded="$(printf '%s\n' "$out" | sed -n 's/^Loaded image: //p' | head -1)"
    if [ -n "$loaded" ] && docker image inspect "$loaded" >/dev/null 2>&1; then
      rm -f "$f"
      echo "Loaded $loaded; removed the archive"
      continue
    fi
  else
    printf '%s\n' "$out" >&2
  fi
  echo "Couldn't load $(basename "$f"); it stays for the next boot" >&2
  rc=1
done
rmdir "$DIR" 2>/dev/null || true
exit "$rc"
