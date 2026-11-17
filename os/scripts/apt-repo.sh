#!/usr/bin/env bash
# apt-repo.sh DEBS_DIR OUT_DIR: a flat, signed APT repository of NoCapOS's
# own packages (published as the assets of the "apt" release, see README.md).
#   deb [signed-by=/usr/share/keyrings/nocapos.gpg] https://github.com/shatheitguy/nocapos/releases/download/apt ./
# Signing: $APT_SIGNING_KEY (secret NOCAPOS_APT_SIGNING_KEY, an armored
# private key without a passphrase). Without it the repository is built
# unsigned, with a warning.
set -euo pipefail
debs="${1:?folder with .deb files}"
out="${2:?output folder}"
mkdir -p "$out"
cp "$debs"/*.deb "$out/"
cd "$out"
# Flat repository: the files sit right next to the index.
apt-ftparchive packages . | sed 's|^Filename: \./|Filename: |' >Packages
gzip -9nkf Packages
xz -9kf Packages
apt-ftparchive \
  -o APT::FTPArchive::Release::Origin=NoCapOS \
  -o APT::FTPArchive::Release::Label=NoCapOS \
  -o APT::FTPArchive::Release::Suite=stable \
  -o APT::FTPArchive::Release::Codename=nocapos \
  -o APT::FTPArchive::Release::Architectures="amd64 arm64 armhf all" \
  -o APT::FTPArchive::Release::Description="NoCapOS's own packages" \
  release . >Release

if [ -z "${APT_SIGNING_KEY:-}" ]; then
  echo "::warning::NOCAPOS_APT_SIGNING_KEY is not set: the APT repository is not signed."
  exit 0
fi
export GNUPGHOME
GNUPGHOME="$(mktemp -d)"
trap 'rm -rf "$GNUPGHOME"' EXIT
printf '%s\n' "$APT_SIGNING_KEY" | gpg --batch --quiet --import
fpr="$(gpg --batch --with-colons --list-secret-keys | awk -F: '/^fpr:/ {print $10; exit}')"
gpg --batch --yes --local-user "$fpr" --clearsign -o InRelease Release
gpg --batch --yes --local-user "$fpr" -abs -o Release.gpg Release
gpg --batch --export "$fpr" >nocapos-archive-keyring.gpg
echo "signed with $fpr"
