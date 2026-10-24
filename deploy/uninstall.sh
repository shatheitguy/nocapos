#!/usr/bin/env bash
# NoCapOS uninstaller for Linux servers. Removes NoCapOS itself and what it
# set up on this machine; your files (NoCap Drive, shared folders, homes)
# are never deleted.
#
#   curl -fsSL <release-url>/uninstall.sh | sudo bash          remove NoCapOS, keep accounts and settings
#   sudo bash uninstall.sh --purge                            also delete accounts, settings and TLS keys
#   sudo bash uninstall.sh --purge --remove-apps --yes        ... and App Store apps (their data volumes too)
#
# Piping from curl runs whatever the download contains, with no checksum.
# To check it first, use the release bundle: download
# nocapos-linux-<arch>.tar.gz and SHA256SUMS, run sha256sum -c SHA256SUMS
# --ignore-missing, extract it and run: sudo bash install.sh --uninstall
#
# Options:
#   --purge         delete NoCapOS's accounts, settings, database and TLS keys (/etc/nocapos, /var/lib/nocapos)
#   --remove-apps   also remove App Store apps (their containers, networks and volumes)
#   --yes           don't ask for confirmation
set -euo pipefail

NAME=NoCapOS
BIN=/usr/local/bin/nocapos
ETC=/etc/nocapos
DATA=/var/lib/nocapos
PREFIX=/opt/alfaos # Docker mode's Compose project
MOUNTS=/mnt/nocapos
SAMBA_DIR=/etc/samba
SHARE_USER=nocapos
PURGE=0
REMOVE_APPS=0
ASSUME_YES=0

info() { printf '\033[1;31m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mwarning:\033[0m %s\n' "$*" >&2; }
die() { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }
has() { command -v "$1" >/dev/null 2>&1; }

for a in "$@"; do
  case "$a" in
    --purge) PURGE=1 ;;
    --remove-apps) REMOVE_APPS=1 ;;
    --yes|-y) ASSUME_YES=1 ;;
    --uninstall) ;; # accepted when called from install.sh --uninstall
    -h|--help) sed -n '2,18p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) die "unknown option: $a (see --help)" ;;
  esac
done

[ "$(id -u)" -eq 0 ] || die "run as root (sudo bash uninstall.sh)"

# ---------------------------------------------------------------- confirm
echo "This removes $NAME from this server:"
echo "  - the nocapos service and program"
echo "  - its Samba sharing settings and network-drive mounts"
[ "$PURGE" -eq 1 ] && echo "  - its accounts, settings, database and TLS keys ($ETC, $DATA)"
[ "$REMOVE_APPS" -eq 1 ] && echo "  - App Store apps, including their data volumes"
echo "Your files (NoCap Drive, shared folders, people's homes) are NOT deleted."
if [ "$ASSUME_YES" -eq 0 ]; then
  if [ -r /dev/tty ]; then
    printf 'Continue? [y/N] '
    read -r answer </dev/tty || answer=n
    case "$answer" in y|Y|yes|YES) ;; *) echo "Nothing was changed."; exit 1 ;; esac
  else
    die "no terminal to confirm on: re-run with --yes"
  fi
fi

# ---------------------------------------------------------------- service
for unit in nocapos alfad; do
  if [ -f "/etc/systemd/system/$unit.service" ]; then
    info "Removing the $unit service"
    systemctl disable --now "$unit" 2>/dev/null || true
    rm -rf "/etc/systemd/system/$unit.service" "/etc/systemd/system/$unit.service.d"
  fi
done
systemctl daemon-reload 2>/dev/null || true
rm -f "$BIN" "$BIN.prev" /usr/local/bin/alfad

# Docker mode: NoCapOS itself ran as a Compose stack.
if [ -f "$PREFIX/docker-compose.yml" ] && has docker; then
  info "Stopping the Docker stack"
  (cd "$PREFIX" && if [ "$PURGE" -eq 1 ]; then docker compose down -v; else docker compose down; fi) || warn "docker compose down failed"
  rm -rf "$PREFIX"
fi

# ---------------------------------------------------------------- network drives
# Shares from other devices that NoCapOS connected live under $MOUNTS.
if [ -d "$MOUNTS" ]; then
  info "Disconnecting network drives"
  for m in "$MOUNTS"/*; do
    [ -d "$m" ] || continue
    if mountpoint -q "$m" 2>/dev/null; then umount "$m" 2>/dev/null || umount -l "$m" 2>/dev/null || warn "could not unmount $m"; fi
    rmdir "$m" 2>/dev/null || true
  done
  rmdir "$MOUNTS" 2>/dev/null || true
fi

# ---------------------------------------------------------------- Samba
# NoCapOS only adds one include line to smb.conf and its own nocapos.conf.
if [ -f "$SAMBA_DIR/nocapos.conf" ] || grep -qs '^include = /etc/samba/nocapos.conf' "$SAMBA_DIR/smb.conf"; then
  info "Removing NoCapOS's Samba shares"
  rm -f "$SAMBA_DIR/nocapos.conf"
  if [ -f "$SAMBA_DIR/smb.conf" ]; then
    sed -i '\|^include = /etc/samba/nocapos.conf$|d' "$SAMBA_DIR/smb.conf"
  fi
  if has systemctl; then
    for svc in smbd smb samba; do systemctl reload "$svc" 2>/dev/null && break; done || true
  fi
fi
# The old shared "nocapos" sharing account (a login-less system user NoCapOS made).
if has pdbedit && pdbedit -L 2>/dev/null | grep -q "^$SHARE_USER:"; then
  pdbedit -x "$SHARE_USER" >/dev/null 2>&1 || true
fi
if [ "$PURGE" -eq 1 ] && id "$SHARE_USER" >/dev/null 2>&1; then
  shell="$(getent passwd "$SHARE_USER" | cut -d: -f7)"
  case "$shell" in */nologin|*/false) userdel "$SHARE_USER" 2>/dev/null || true ;; esac
fi

# Per-person Samba accounts and login-less file-sharing accounts: NoCapOS
# lists the ones it made in its data folder, and only those are touched.
# Real Linux users are never deleted; their Samba passwords made by NoCapOS
# are removed (the shares they opened are gone anyway).
data_dir="$DATA"
if [ -f "$ETC/nocapos.env" ]; then
  d="$(sed -n 's/^ALFA_DATA_DIR=//p' "$ETC/nocapos.env" | tail -n1 | tr -d '"'"'"'')"
  [ -n "$d" ] && data_dir="$d"
fi
uid_min="$(awk '$1=="UID_MIN"{print $2}' /etc/login.defs 2>/dev/null)"
uid_min="${uid_min:-1000}"
valid_name() { case "$1" in ''|-*|*[!a-zA-Z0-9._-]*) return 1 ;; esac; [ "$1" != root ]; }
if [ -f "$data_dir/sharing/samba-users" ] && has pdbedit; then
  info "Removing the Samba accounts NoCapOS made"
  while IFS= read -r u; do
    valid_name "$u" || continue
    pdbedit -L 2>/dev/null | grep -q "^$u:" && { pdbedit -x "$u" >/dev/null 2>&1 || warn "could not remove the Samba account $u"; }
  done <"$data_dir/sharing/samba-users"
fi
if [ "$PURGE" -eq 1 ] && [ -f "$data_dir/sharing/system-users" ]; then
  info "Deleting the login-less file-sharing accounts NoCapOS made"
  while IFS= read -r u; do
    valid_name "$u" || continue
    ent="$(getent passwd "$u" 2>/dev/null)" || continue
    uid="$(printf '%s' "$ent" | cut -d: -f3)"
    shell="$(printf '%s' "$ent" | cut -d: -f7)"
    # Only system accounts that still can't log in: never a real person.
    if [ "$uid" -gt 0 ] && [ "$uid" -lt "$uid_min" ]; then
      case "$shell" in */nologin|*/false) userdel "$u" 2>/dev/null || warn "could not delete $u" ;; esac
    fi
  done <"$data_dir/sharing/system-users"
fi

# ---------------------------------------------------------------- apps
if has docker; then
  apps="$(docker ps -a --filter label=nocapos.app --format '{{.Label "nocapos.app"}}' 2>/dev/null | sort -u | tr '\n' ' ')"
  if [ -n "${apps// /}" ]; then
    if [ "$REMOVE_APPS" -eq 1 ]; then
      info "Removing App Store apps: $apps"
      ids="$(docker ps -aq --filter label=nocapos.app)"
      [ -z "$ids" ] || docker rm -f -v $ids >/dev/null 2>&1 || warn "some app containers could not be removed"
      nets="$(docker network ls -q --filter label=nocapos.app)"
      [ -z "$nets" ] || docker network rm $nets >/dev/null 2>&1 || true
      vols="$(docker volume ls -q --filter label=nocapos.app)"
      [ -z "$vols" ] || docker volume rm $vols >/dev/null 2>&1 || true
    else
      echo "   App Store apps keep running in Docker: $apps(use --remove-apps to remove them)"
    fi
  fi
fi

# ---------------------------------------------------------------- data
if [ "$PURGE" -eq 1 ]; then
  info "Deleting accounts, settings and keys"
  rm -rf "$ETC" "$DATA" "$data_dir" /etc/alfaos /var/lib/alfaos
  id alfa >/dev/null 2>&1 && userdel alfa 2>/dev/null || true
fi

info "$NAME removed."
[ "$PURGE" -eq 1 ] || echo "   Accounts and settings were kept in $DATA (use --purge to delete them)."
echo "   Linux user accounts and their homes were left as they are."
echo "   Virtual machines (if any) are still defined in libvirt."
echo "   Your files were NOT touched."
