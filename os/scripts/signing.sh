#!/usr/bin/env bash
# signing.sh: update bundle signing in CI (see README.md, Signing).
#   signing.sh root           puts the update root certificate (secret
#                             NOCAPOS_UPDATE_ROOT_CERT, PEM, in $ROOT_CERT)
#                             into the image, which then only installs
#                             bundles signed for it
#   signing.sh sign BUNDLE    signs BUNDLE with $SIGNING_KEY / $SIGNING_CERT
#                             (secrets NOCAPOS_UPDATE_SIGNING_KEY and
#                             NOCAPOS_UPDATE_SIGNING_CERT, PEM)
# Without the secrets, nothing is signed: a warning says so, and devices
# check updates by the bundle hash from the release manifest instead.
set -euo pipefail
OS="$(cd "$(dirname "$0")/.." && pwd)"

warn() { echo "::warning::$*"; echo "WARNING: $*" >&2; }

case "${1:-}" in
  root)
    keys="$OS/recipes/nocapos-os/keys"
    rm -rf "$keys"
    if [ -z "${ROOT_CERT:-}" ]; then
      warn "NOCAPOS_UPDATE_ROOT_CERT is not set: the image accepts updates checked by their hash only (not signed)."
      exit 0
    fi
    mkdir -p "$keys"
    printf '%s\n' "$ROOT_CERT" >"$keys/update-root.crt"
    openssl x509 -in "$keys/update-root.crt" -noout -subject -enddate
    ;;
  sign)
    bundle="${2:?bundle}"
    if [ -z "${SIGNING_KEY:-}" ] || [ -z "${SIGNING_CERT:-}" ]; then
      warn "NOCAPOS_UPDATE_SIGNING_KEY / NOCAPOS_UPDATE_SIGNING_CERT are not set: $(basename "$bundle") is not signed."
      exit 0
    fi
    tmp="$OS/build/.sign"
    rm -rf "$tmp"; mkdir -p "$tmp"; chmod 700 "$tmp"
    trap 'rm -rf "$tmp"' EXIT
    printf '%s\n' "$SIGNING_KEY" >"$tmp/signer.key"
    printf '%s\n' "$SIGNING_CERT" >"$tmp/signer.crt"
    rel="${bundle#"$OS"/}"
    (cd "$OS" && ./run-bakery bundler signatures sign "$rel" build/.sign/signer.crt build/.sign/signer.key "$rel.new")
    mv -f "$bundle.new" "$bundle"
    (cd "$OS" && ./run-bakery bundler signatures list "$rel")
    touch "$bundle.signed" # tells the release manifest it's signed
    ;;
  *)
    sed -n '2,12p' "$0"
    exit 2
    ;;
esac
