#!/usr/bin/env bash
# Alfa OS installer for Linux: Debian, Ubuntu, Raspberry Pi OS, Fedora, Arch, ...
#
#   sudo bash install.sh                  Docker mode: Traefik + HTTPS on ports 80/443 (recommended)
#   sudo bash install.sh --native         systemd service with built-in HTTPS on port 443
#   sudo bash install.sh --uninstall      remove Alfa OS (NAS storage is never deleted)
#
# Options:
#   --storage DIR       where Files keeps your data (default /srv/alfaos)
#   --install-docker    install Docker from get.docker.com without asking
#   --yes               answer yes to prompts
#   --purge             with --uninstall: also delete accounts, settings and TLS keys
set -euo pipefail

BUNDLE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PREFIX=/opt/alfaos
MODE=docker
STORAGE=/srv/alfaos
INSTALL_DOCKER=0
ASSUME_YES=0
UNINSTALL=0
PURGE=0

info() { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mwarning:\033[0m %s\n' "$*" >&2; }
die() { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }
has() { command -v "$1" >/dev/null 2>&1; }
confirm() {
  [ "$ASSUME_YES" -eq 1 ] && return 0
  [ -t 0 ] || return 1
  local a
  read -r -p "$1 [y/N] " a
  [[ "$a" =~ ^[Yy] ]]
}

while [ $# -gt 0 ]; do
  case "$1" in
    --native) MODE=native ;;
    --docker) MODE=docker ;;
    --storage) STORAGE="${2:?--storage needs a directory}"; shift ;;
    --install-docker) INSTALL_DOCKER=1 ;;
    --yes | -y) ASSUME_YES=1 ;;
    --uninstall) UNINSTALL=1 ;;
    --purge) PURGE=1 ;;
    -h | --help) sed -n '2,15p' "$0"; exit 0 ;;
    *) die "unknown option: $1 (see --help)" ;;
  esac
  shift
done

[ "$(id -u)" -eq 0 ] || die "run as root: sudo bash $0"
case "$STORAGE" in /*) ;; *) die "--storage must be an absolute path" ;; esac

lan_ip() {
  local ip=""
  if has ip; then ip="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i=1;i<NF;i++) if ($i=="src") {print $(i+1); exit}}')"; fi
  [ -n "$ip" ] || ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
  echo "${ip:-<this-device-ip>}"
}

port_busy() {
  has ss && ss -H -ltn "sport = :$1" 2>/dev/null | grep -q .
}

# ---------------------------------------------------------------- uninstall
if [ "$UNINSTALL" -eq 1 ]; then
  if [ -f "$PREFIX/docker-compose.yml" ] && has docker; then
    info "Stopping the Docker stack"
    (cd "$PREFIX" && if [ "$PURGE" -eq 1 ]; then docker compose down -v; else docker compose down; fi) || warn "docker compose down failed"
    rm -rf "$PREFIX"
  fi
  if [ -f /etc/systemd/system/alfad.service ]; then
    info "Removing the alfad service"
    systemctl disable --now alfad 2>/dev/null || true
    rm -rf /etc/systemd/system/alfad.service /etc/systemd/system/alfad.service.d
    systemctl daemon-reload
    rm -f /usr/local/bin/alfad
    if [ "$PURGE" -eq 1 ]; then
      rm -rf /etc/alfaos /var/lib/alfaos
      id alfa >/dev/null 2>&1 && userdel alfa 2>/dev/null || true
    fi
  fi
  info "Alfa OS removed."
  [ "$PURGE" -eq 1 ] || echo "   Accounts and settings were kept (use --purge to delete them)."
  echo "   Your files were NOT touched. Storage folder: $STORAGE"
  exit 0
fi

# ---------------------------------------------------------------- checks
case "$(uname -m)" in
  x86_64 | amd64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  armv7l | armv7* | armhf) ARCH=armv7 ;;
  *) die "unsupported CPU architecture: $(uname -m)" ;;
esac
BUNDLE_ARCH="$(cat "$BUNDLE_DIR/ARCH" 2>/dev/null || echo unknown)"
VERSION="$(cat "$BUNDLE_DIR/VERSION" 2>/dev/null || echo dev)"
[ -f "$BUNDLE_DIR/alfad" ] || die "alfad binary not found next to install.sh (run this from an extracted release bundle)"
[ "$BUNDLE_ARCH" = "$ARCH" ] || die "this bundle is for '$BUNDLE_ARCH' but this device is '$ARCH' — download alfaos-$VERSION-linux-$ARCH.tar.gz"
has systemctl || die "systemd is required"

info "Installing Alfa OS $VERSION ($ARCH, $MODE mode)"

prepare_storage() { # $1 = owner (uid:gid or user:group)
  if [ -d "$STORAGE" ] && [ -n "$(ls -A "$STORAGE" 2>/dev/null)" ]; then
    # Never re-own existing data recursively; only the top folder.
    warn "$STORAGE already has content; Alfa OS gets access to the folder itself. Files owned by other users may be read-only in Files."
  fi
  mkdir -p "$STORAGE"
  chown "$1" "$STORAGE"
  chmod 750 "$STORAGE"
}

# Only look at output since the most recent start, so a token from an earlier
# run (setup already done) is never shown.
since_last_start() { awk '/"msg":"starting alfad"/ {buf=""} {buf = buf $0 "\n"} END {printf "%s", buf}'; }
setup_token() { since_last_start | grep -o '"setup_token":"[^"]*"' | tail -1 | cut -d'"' -f4 || true; }

finish() { # $1 = URL, $2 = token
  local ip; ip="$(lan_ip)"
  echo
  info "Alfa OS is running."
  echo "   Open:  ${1//HOST/$ip}"
  echo "   Your browser will warn about the self-signed certificate once; that's expected on a local network."
  if [ -n "$2" ]; then
    echo "   First-run setup token:  $2"
  else
    echo "   Sign in with your existing account."
  fi
  echo "   Files are stored in: $STORAGE"
  if has ufw && ufw status 2>/dev/null | grep -q "Status: active"; then
    echo "   ufw is active — allow access with:  sudo ufw allow 80,443/tcp"
  fi
}

# ---------------------------------------------------------------- docker mode
install_docker_mode() {
  if ! (has docker && docker info >/dev/null 2>&1); then
    if [ "$INSTALL_DOCKER" -eq 1 ] || confirm "Docker is not installed. Install it now from get.docker.com?"; then
      has curl || die "curl is required to install Docker"
      info "Installing Docker"
      curl -fsSL https://get.docker.com -o /tmp/get-docker.sh
      sh /tmp/get-docker.sh
      rm -f /tmp/get-docker.sh
      systemctl enable --now docker
    else
      die "Docker is required for Docker mode. Re-run with --install-docker, or use --native."
    fi
  fi
  docker compose version >/dev/null 2>&1 || die "Docker Compose v2 is missing (Debian/Ubuntu: apt install docker-compose-plugin)"

  local running_here=0
  [ -f "$PREFIX/docker-compose.yml" ] && (cd "$PREFIX" && docker compose ps -q traefik 2>/dev/null | grep -q .) && running_here=1
  if [ "$running_here" -eq 0 ]; then
    for p in 80 443; do port_busy "$p" && die "port $p is already in use by another program (stop it, or use --native on a free port)"; done
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

  # Settings live in .env; keep user edits from previous installs, refresh managed keys.
  local env="$PREFIX/.env"
  touch "$env"
  chmod 600 "$env"
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

  info "Waiting for alfad"
  local token="" ready=0
  for _ in $(seq 1 60); do
    logs="$(cd "$PREFIX" && docker compose logs --no-color --no-log-prefix alfad 2>/dev/null || true)"
    if since_last_start <<<"$logs" | grep '"alfad ready"' >/dev/null; then ready=1; token="$(setup_token <<<"$logs")"; break; fi
    sleep 1
  done
  [ "$ready" -eq 1 ] || die "alfad did not start; check: cd $PREFIX && docker compose logs alfad"
  finish "https://HOST/" "$token"
  echo "   Manage:  cd $PREFIX && docker compose ps | logs -f alfad | restart"
}

# ---------------------------------------------------------------- native mode
install_native_mode() {
  local fresh=1
  [ -f /etc/systemd/system/alfad.service ] && fresh=0
  if [ "$fresh" -eq 1 ]; then port_busy 443 && die "port 443 is already in use"; fi

  if ! id alfa >/dev/null 2>&1; then
    info "Creating system user 'alfa'"
    useradd --system --home-dir /var/lib/alfaos --no-create-home --shell "$(command -v nologin || echo /usr/sbin/nologin)" alfa
  fi

  info "Installing /usr/local/bin/alfad"
  systemctl stop alfad 2>/dev/null || true
  install -m 0755 "$BUNDLE_DIR/alfad" /usr/local/bin/alfad

  mkdir -p /etc/alfaos
  if [ ! -f /etc/alfaos/alfad.env ]; then
    cat >/etc/alfaos/alfad.env <<EOF
# Alfa OS daemon settings — see README for all ALFA_* options.
ALFA_LISTEN_ADDR=:443
ALFA_TLS=auto
ALFA_DATA_DIR=/var/lib/alfaos
ALFA_FILE_ROOTS=Alfa Drive=$STORAGE
EOF
  fi
  chown root:alfa /etc/alfaos/alfad.env
  chmod 640 /etc/alfaos/alfad.env

  prepare_storage alfa:alfa

  install -m 0644 "$BUNDLE_DIR/alfad.service" /etc/systemd/system/alfad.service
  mkdir -p /etc/systemd/system/alfad.service.d
  {
    echo "[Service]"
    echo "ReadWritePaths=$STORAGE"
    if getent group docker >/dev/null; then
      # Docker socket access is root-equivalent; alfad is the only process granted it.
      echo "SupplementaryGroups=docker"
    fi
  } >/etc/systemd/system/alfad.service.d/10-install.conf
  getent group docker >/dev/null || warn "Docker not found: Alfa OS runs, container features stay off until Docker is installed."

  systemctl daemon-reload
  systemctl enable alfad >/dev/null
  systemctl restart alfad

  info "Waiting for alfad"
  local token="" ready=0
  for _ in $(seq 1 30); do
    logs="$(journalctl -u alfad --no-pager -o cat -n 400 2>/dev/null || true)"
    if since_last_start <<<"$logs" | grep '"alfad ready"' >/dev/null; then ready=1; token="$(setup_token <<<"$logs")"; break; fi
    systemctl is-failed --quiet alfad && break
    sleep 1
  done
  [ "$ready" -eq 1 ] || die "alfad did not start; check: journalctl -u alfad -e"
  finish "https://HOST/" "$token"
  echo "   Manage:  systemctl status|restart alfad · journalctl -u alfad -f · settings in /etc/alfaos/alfad.env"
}

if [ "$MODE" = docker ]; then install_docker_mode; else install_native_mode; fi
