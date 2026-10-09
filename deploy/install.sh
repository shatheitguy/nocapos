#!/usr/bin/env bash
# NoCapOS installer for Linux servers: Debian, Ubuntu, Raspberry Pi OS,
# Fedora, Arch, ... (amd64, arm64, armv7). Like umbrelOS or OMV, NoCapOS runs
# on top of your existing Linux and manages it: users, network, storage,
# power, a root terminal and one-click Docker apps.
#
#   curl -fsSL <release-url>/install.sh | sudo bash      download + install
#   sudo bash install.sh                  native install (recommended): runs as root, full control of this server
#   sudo bash install.sh --docker         run NoCapOS itself in Docker (no host control: users, network, power stay off)
#   sudo bash install.sh --uninstall      remove NoCapOS (your files are never deleted)
#
# Options:
#   --storage DIR       NoCap Drive folder for your files (default /srv/nocapos)
#   --port N            HTTPS port for native installs (default 443)
#   --no-docker         don't install Docker (the App Store stays off until Docker is present)
#   --install-docker    install Docker from get.docker.com without asking
#   --yes               answer yes to prompts
#   --purge             with --uninstall: also delete accounts, settings and TLS keys
set -euo pipefail

NAME=NoCapOS
RELEASE_URL="${NOCAP_RELEASE_URL:-https://github.com/shatheitguy/nocapos/releases/latest/download}"
BUNDLE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || pwd)"
BIN=/usr/local/bin/nocapos
UNIT=/etc/systemd/system/nocapos.service
ETC=/etc/nocapos
DATA=/var/lib/nocapos
PREFIX=/opt/alfaos # Docker mode keeps its original Compose project (and data volume) name
MODE=native
STORAGE=/srv/nocapos
STORAGE_SET=0
PORT=443
INSTALL_DOCKER=ask
ASSUME_YES=0
UNINSTALL=0
PURGE=0
ARGS=("$@")

info() { printf '\033[1;31m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mwarning:\033[0m %s\n' "$*" >&2; }
die() { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }
has() { command -v "$1" >/dev/null 2>&1; }
confirm() {
  [ "$ASSUME_YES" -eq 1 ] && return 0
  [ -r /dev/tty ] || return 1 # piped from curl: ask on the terminal
  local a
  read -r -p "$1 [Y/n] " a </dev/tty
  [[ -z "$a" || "$a" =~ ^[Yy] ]]
}

while [ $# -gt 0 ]; do
  case "$1" in
    --native) MODE=native ;;
    --docker) MODE=docker ;;
    --storage) STORAGE="${2:?--storage needs a directory}"; STORAGE_SET=1; shift ;;
    --port) PORT="${2:?--port needs a number}"; shift ;;
    --no-docker) INSTALL_DOCKER=no ;;
    --install-docker) INSTALL_DOCKER=yes ;;
    --yes | -y) ASSUME_YES=1 ;;
    --uninstall) UNINSTALL=1 ;;
    --purge) PURGE=1 ;;
    -h | --help) sed -n '2,20p' "${BASH_SOURCE[0]:-$0}" 2>/dev/null || echo "see https://github.com/shatheitguy/nocapos"; exit 0 ;;
    *) die "unknown option: $1 (see --help)" ;;
  esac
  shift
done

[ "$(id -u)" -eq 0 ] || die "run as root: sudo bash install.sh (or: curl -fsSL ... | sudo bash)"
case "$STORAGE" in /*) ;; *) die "--storage must be an absolute path" ;; esac
case "$PORT" in '' | *[!0-9]*) die "--port must be a number" ;; esac

case "$(uname -m)" in
  x86_64 | amd64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  armv7l | armv7* | armhf) ARCH=armv7 ;;
  *) die "unsupported CPU architecture: $(uname -m)" ;;
esac

lan_ip() {
  local ip=""
  if has ip; then ip="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i=1;i<NF;i++) if ($i=="src") {print $(i+1); exit}}')"; fi
  [ -n "$ip" ] || ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
  echo "${ip:-<this-device-ip>}"
}
port_busy() { has ss && ss -H -ltn "sport = :$1" 2>/dev/null | grep -q .; }

# ---------------------------------------------------------------- one-line install
# Run without a bundle next to it (curl | bash): download the right one, verify
# its checksum and run its installer with the same options.
if [ "$UNINSTALL" -eq 0 ] && [ ! -f "$BUNDLE_DIR/alfad" ]; then
  has tar || die "tar is required"
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  file="nocapos-linux-$ARCH.tar.gz"
  info "Downloading $NAME for $ARCH"
  fetch() { if has curl; then curl -fsSL "$1" -o "$2"; elif has wget; then wget -qO "$2" "$1"; else die "curl or wget is required"; fi; }
  fetch "$RELEASE_URL/$file" "$tmp/$file" || die "download failed: $RELEASE_URL/$file"
  if fetch "$RELEASE_URL/SHA256SUMS" "$tmp/SHA256SUMS" 2>/dev/null; then
    want="$(tr -d '\r' <"$tmp/SHA256SUMS" | awk -v f="$file" '$2 == f || $2 == "*" f {print $1}')"
    got="$(sha256sum "$tmp/$file" | awk '{print $1}')"
    [ -n "$want" ] && [ "$want" = "$got" ] || die "checksum mismatch for $file — not installing"
    info "Checksum verified"
  else
    die "could not download SHA256SUMS to verify the bundle"
  fi
  tar -xzf "$tmp/$file" -C "$tmp"
  dir="$(find "$tmp" -mindepth 1 -maxdepth 1 -type d | head -1)"
  [ -f "$dir/install.sh" ] || die "the downloaded bundle has no installer"
  bash "$dir/install.sh" "${ARGS[@]}"
  exit $?
fi

# ---------------------------------------------------------------- uninstall
if [ "$UNINSTALL" -eq 1 ]; then
  for unit in nocapos alfad; do
    if [ -f "/etc/systemd/system/$unit.service" ]; then
      info "Removing the $unit service"
      systemctl disable --now "$unit" 2>/dev/null || true
      rm -rf "/etc/systemd/system/$unit.service" "/etc/systemd/system/$unit.service.d"
    fi
  done
  systemctl daemon-reload 2>/dev/null || true
  rm -f "$BIN" /usr/local/bin/alfad
  if [ -f "$PREFIX/docker-compose.yml" ] && has docker; then
    info "Stopping the Docker stack"
    (cd "$PREFIX" && if [ "$PURGE" -eq 1 ]; then docker compose down -v; else docker compose down; fi) || warn "docker compose down failed"
    rm -rf "$PREFIX"
  fi
  if [ "$PURGE" -eq 1 ]; then
    rm -rf "$ETC" "$DATA" /etc/alfaos /var/lib/alfaos
    id alfa >/dev/null 2>&1 && userdel alfa 2>/dev/null || true
  fi
  info "$NAME removed."
  [ "$PURGE" -eq 1 ] || echo "   Accounts and settings were kept in $DATA (use --purge to delete them)."
  echo "   Apps you installed from the App Store keep running in Docker until you remove them."
  echo "   Your files were NOT touched."
  exit 0
fi

# ---------------------------------------------------------------- checks
BUNDLE_ARCH="$(cat "$BUNDLE_DIR/ARCH" 2>/dev/null || echo unknown)"
VERSION="$(cat "$BUNDLE_DIR/VERSION" 2>/dev/null || echo dev)"
[ "$BUNDLE_ARCH" = "$ARCH" ] || die "this bundle is for '$BUNDLE_ARCH' but this device is '$ARCH' — download nocapos-linux-$ARCH.tar.gz"
has systemctl || die "systemd is required"

info "Installing $NAME $VERSION ($ARCH, $MODE mode)"

prepare_storage() { # $1 = owner
  if [ -d "$STORAGE" ] && [ -n "$(ls -A "$STORAGE" 2>/dev/null)" ]; then
    warn "$STORAGE already has content; it is kept as it is."
  fi
  mkdir -p "$STORAGE"
  chown "$1" "$STORAGE"
  chmod 750 "$STORAGE"
}

since_last_start() { awk '/"msg":"starting alfad"/ {buf=""} {buf = buf $0 "\n"} END {printf "%s", buf}'; }
setup_token() { since_last_start | grep -o '"setup_token":"[^"]*"' | tail -1 | cut -d'"' -f4 || true; }

ensure_docker() { # $1 = required (1) or optional (0)
  if has docker && docker info >/dev/null 2>&1; then return 0; fi
  local want=0
  case "$INSTALL_DOCKER" in
    yes) want=1 ;;
    no) want=0 ;;
    ask) confirm "Docker is not installed. Install it now (needed for the App Store)?" && want=1 ;;
  esac
  if [ "$want" -eq 1 ]; then
    has curl || die "curl is required to install Docker"
    info "Installing Docker"
    curl -fsSL https://get.docker.com -o /tmp/get-docker.sh
    sh /tmp/get-docker.sh
    rm -f /tmp/get-docker.sh
    systemctl enable --now docker
    return 0
  fi
  [ "$1" -eq 1 ] && die "Docker is required for --docker mode. Re-run with --install-docker, or use the native install."
  warn "Skipping Docker: $NAME runs, the App Store stays off until Docker is installed."
}

# restic powers Backups; best effort — the Backups app offers to install it later too.
ensure_restic() {
  has restic && return 0
  info "Installing restic (for Backups)"
  if has apt-get; then
    DEBIAN_FRONTEND=noninteractive apt-get install -y restic >/dev/null 2>&1 ||
      { apt-get update >/dev/null 2>&1 && DEBIAN_FRONTEND=noninteractive apt-get install -y restic >/dev/null 2>&1; } || true
  elif has dnf; then dnf install -y restic >/dev/null 2>&1 || true
  elif has pacman; then pacman -Sy --noconfirm restic >/dev/null 2>&1 || true
  elif has zypper; then zypper --non-interactive install restic >/dev/null 2>&1 || true
  elif has apk; then apk add restic >/dev/null 2>&1 || true
  fi
  has restic || warn "Couldn't install restic; Backups will offer to install it later."
}

finish() { # $1 = URL, $2 = token
  local ip; ip="$(lan_ip)"
  echo
  info "$NAME is running."
  echo "   Open:  ${1//HOST/$ip}"
  echo "   Your browser warns about the self-signed certificate once; that's expected on a local network."
  if [ -n "$2" ]; then echo "   First-run setup token:  $2"; else echo "   Sign in with your existing account."; fi
  echo "   Files: NoCap Drive is $STORAGE"
  if has ufw && ufw status 2>/dev/null | grep -q "Status: active"; then
    echo "   ufw is active — allow access with:  sudo ufw allow $PORT/tcp"
  fi
}

# ---------------------------------------------------------------- docker mode
install_docker_mode() {
  ensure_docker 1
  docker compose version >/dev/null 2>&1 || die "Docker Compose v2 is missing (Debian/Ubuntu: apt install docker-compose-plugin)"
  local running_here=0
  [ -f "$PREFIX/docker-compose.yml" ] && (cd "$PREFIX" && docker compose ps -q traefik 2>/dev/null | grep -q .) && running_here=1
  if [ "$running_here" -eq 0 ]; then
    for p in 80 443; do port_busy "$p" && die "port $p is already in use (stop that program, or use the native install with --port)"; done
  fi
  info "Copying files to $PREFIX"
  mkdir -p "$PREFIX/rootfs/data"
  install -m 0755 "$BUNDLE_DIR/alfad" "$PREFIX/alfad"
  install -m 0644 "$BUNDLE_DIR/docker-compose.yml" "$BUNDLE_DIR/docker-compose.nvidia.yml" "$BUNDLE_DIR/Dockerfile.bundle" "$PREFIX/"
  local sock=/var/run/docker.sock gid compose_files=docker-compose.yml
  [ -S "$sock" ] || die "Docker socket not found at $sock"
  gid="$(stat -c %g "$sock")"
  if has nvidia-smi && docker info 2>/dev/null | grep -qi 'nvidia'; then
    info "NVIDIA runtime found: enabling GPU metrics"
    compose_files="docker-compose.yml:docker-compose.nvidia.yml"
  fi
  local env="$PREFIX/.env"
  touch "$env"; chmod 600 "$env"
  set_env() { if grep -q "^$1=" "$env"; then sed -i "s|^$1=.*|$1=$2|" "$env"; else echo "$1=$2" >>"$env"; fi; }
  set_env COMPOSE_FILE "$compose_files"
  set_env DOCKER_GID "$gid"
  set_env ALFA_VERSION "$VERSION"
  set_env ALFA_BUILD_CONTEXT "."
  set_env ALFA_DOCKERFILE "Dockerfile.bundle"
  set_env ALFA_STORAGE_DIR "$STORAGE"
  prepare_storage 65532:65532
  info "Starting containers (first start pulls Traefik and builds the image)"
  (cd "$PREFIX" && docker compose up -d --build --remove-orphans)
  info "Waiting for $NAME"
  local token="" ready=0 logs
  for _ in $(seq 1 60); do
    logs="$(cd "$PREFIX" && docker compose logs --no-color --no-log-prefix alfad 2>/dev/null || true)"
    if since_last_start <<<"$logs" | grep '"alfad ready"' >/dev/null; then ready=1; token="$(setup_token <<<"$logs")"; break; fi
    sleep 1
  done
  [ "$ready" -eq 1 ] || die "$NAME did not start; check: cd $PREFIX && docker compose logs alfad"
  finish "https://HOST/" "$token"
  echo "   Docker mode: NoCapOS manages apps, but not this server's users, network or power."
  echo "   Manage:  cd $PREFIX && docker compose ps | logs -f alfad | restart"
}

# ---------------------------------------------------------------- native mode
# Moves an older "Alfa OS" native install (alfad.service, /var/lib/alfaos) over.
migrate_alfaos() {
  [ -f /etc/systemd/system/alfad.service ] || return 0
  info "Upgrading the previous Alfa OS install to $NAME"
  systemctl disable --now alfad 2>/dev/null || true
  if [ -f /etc/alfaos/alfad.env ] && [ "$STORAGE_SET" -eq 0 ]; then
    local old
    old="$(sed -n 's/^ALFA_FILE_ROOTS=//p' /etc/alfaos/alfad.env | tr -d '"' | cut -d, -f1 | cut -d= -f2-)"
    [ -n "$old" ] && STORAGE="$old"
  fi
  if [ -d /var/lib/alfaos ] && [ ! -e "$DATA" ]; then mv /var/lib/alfaos "$DATA"; fi
  rm -rf /etc/systemd/system/alfad.service /etc/systemd/system/alfad.service.d /usr/local/bin/alfad
  systemctl daemon-reload
}

install_native_mode() {
  migrate_alfaos
  local fresh=1
  [ -f "$UNIT" ] && fresh=0
  if [ "$fresh" -eq 1 ] && port_busy "$PORT"; then die "port $PORT is already in use (choose another with --port N)"; fi

  ensure_docker 0
  ensure_restic

  info "Installing $BIN"
  systemctl stop nocapos 2>/dev/null || true
  install -m 0755 "$BUNDLE_DIR/alfad" "$BIN"

  mkdir -p "$ETC" "$DATA"
  chmod 700 "$DATA"
  chown -R root:root "$DATA"
  if [ ! -f "$ETC/nocapos.env" ]; then
    cat >"$ETC/nocapos.env" <<EOF
# NoCapOS settings — see README for all ALFA_* options. Restart after edits:
#   systemctl restart nocapos
ALFA_LISTEN_ADDR=:$PORT
ALFA_TLS=auto
ALFA_DATA_DIR=$DATA
ALFA_FILE_ROOTS="NoCap Drive=$STORAGE"
# Whole filesystem in Files as "System" (core OS folders are protected).
ALFA_SYSTEM_ROOT=true
EOF
  fi
  chmod 600 "$ETC/nocapos.env"
  prepare_storage root:root

  install -m 0644 "$BUNDLE_DIR/nocapos.service" "$UNIT"
  systemctl daemon-reload
  systemctl enable nocapos >/dev/null
  systemctl restart nocapos

  info "Waiting for $NAME"
  local token="" ready=0 logs
  for _ in $(seq 1 30); do
    logs="$(journalctl -u nocapos --no-pager -o cat -n 400 2>/dev/null || true)"
    if since_last_start <<<"$logs" | grep '"alfad ready"' >/dev/null; then ready=1; token="$(setup_token <<<"$logs")"; break; fi
    systemctl is-failed --quiet nocapos && break
    sleep 1
  done
  [ "$ready" -eq 1 ] || die "$NAME did not start; check: journalctl -u nocapos -e"
  local url="https://HOST/"
  [ "$PORT" = 443 ] || url="https://HOST:$PORT/"
  finish "$url" "$token"
  echo "   $NAME runs as root and can manage this server: Linux users (sign in with them), network, power,"
  echo "   storage and a root terminal — all admin-only and recorded in the audit log."
  echo "   Turn on two-factor sign-in under Settings → Security."
  echo "   Manage:  systemctl status|restart nocapos · journalctl -u nocapos -f · settings in $ETC/nocapos.env"
}

if [ "$MODE" = docker ]; then install_docker_mode; else install_native_mode; fi
