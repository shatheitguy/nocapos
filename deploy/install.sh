#!/usr/bin/env bash
# NoCapOS installer for Linux servers: Debian, Ubuntu, Raspberry Pi OS,
# Fedora, Arch, ... (amd64, arm64, armv7). NoCapOS runs on top of your
# existing Linux and manages it: users, network, storage,
# power, a host terminal and one-click Docker apps.
#
#   curl -fsSL <release-url>/install.sh | sudo bash      download + install
#   sudo bash install.sh                  native install (recommended): runs as root, full control of this server
#   sudo bash install.sh --docker         run NoCapOS itself in Docker (no host control: users, network, power stay off)
#   sudo bash install.sh --uninstall      remove NoCapOS (your files are never deleted; same as uninstall.sh)
#
# Options:
#   --storage DIR       NoCap Drive folder for your files (default /srv/nocapos)
#   --port N            HTTPS port for native installs (default 443)
#   --no-docker         don't install Docker (the App Store stays off until Docker is present)
#   --install-docker    install Docker from get.docker.com without asking
#   --minimal           skip the optional host packages (virtual machines, Samba, remote desktop
#                       engine, Brave, Avahi, SMART, ZFS, FFmpeg, restic/rclone); NoCapOS offers
#                       to install each one later, from its app or from Packages
#   --no-branding       leave the console login banner (/etc/issue) and MOTD as they are
#   --mdns-name NAME    local name to announce with Avahi (default nocap -> http://nocap.local).
#                       If Avahi already has a host-name set (by you), that one is kept.
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
MINIMAL=0
BRANDING=1
MDNS_NAME=nocap
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
    --minimal) MINIMAL=1 ;;
    --no-branding) BRANDING=0 ;;
    --mdns-name) MDNS_NAME="${2:?--mdns-name needs a name}"; shift ;;
    --yes | -y) ASSUME_YES=1 ;;
    --uninstall) UNINSTALL=1 ;;
    --purge) PURGE=1 ;;
    -h | --help) sed -n '2,27p' "${BASH_SOURCE[0]:-$0}" 2>/dev/null || echo "see https://github.com/shatheitguy/nocapos"; exit 0 ;;
    *) die "unknown option: $1 (see --help)" ;;
  esac
  shift
done

[ "$(id -u)" -eq 0 ] || die "run as root: sudo bash install.sh (or: curl -fsSL ... | sudo bash)"
case "$STORAGE" in /*) ;; *) die "--storage must be an absolute path" ;; esac
case "$PORT" in '' | *[!0-9]*) die "--port must be a number" ;; esac
[[ "$MDNS_NAME" =~ ^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$ ]] || die "--mdns-name: use letters, numbers and dashes"

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
# The bundle's uninstall.sh does the full clean-up (also Samba and mounts).
if [ "$UNINSTALL" -eq 1 ] && [ -f "$BUNDLE_DIR/uninstall.sh" ]; then
  un=()
  [ "$PURGE" -eq 1 ] && un+=(--purge)
  [ "$ASSUME_YES" -eq 1 ] && un+=(--yes)
  exec bash "$BUNDLE_DIR/uninstall.sh" "${un[@]}"
fi
if [ "$UNINSTALL" -eq 1 ]; then
  for unit in nocapos alfad; do
    if [ -f "/etc/systemd/system/$unit.service" ]; then
      info "Removing the $unit service"
      systemctl disable --now "$unit" 2>/dev/null || true
      rm -rf "/etc/systemd/system/$unit.service" "/etc/systemd/system/$unit.service.d"
    fi
  done
  systemctl daemon-reload 2>/dev/null || true
  rm -f "$BIN" "$BIN.prev" /usr/local/bin/alfad /usr/local/bin/cap
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

# ---------------------------------------------------------------- host packages
# What NoCapOS's features need from the host, per package manager. One line per
# feature: feature|apt|dnf|pacman|zypper ("-" = this distribution doesn't have
# it). Keep in sync with backend/internal/syspkg/features.go (a test checks).
# Packages a distribution doesn't carry are skipped with a warning; none of
# them is required, so the install never fails over one. --minimal skips all.
PKG_TABLE='
virtualization|qemu-system-x86 qemu-utils libvirt-daemon-system libvirt-clients ovmf swtpm swtpm-tools|qemu-kvm qemu-img libvirt edk2-ovmf swtpm swtpm-tools|qemu-base libvirt edk2-ovmf swtpm dnsmasq|qemu-kvm qemu-tools libvirt qemu-ovmf-x86_64 swtpm
sharing|samba smbclient cifs-utils nfs-common|samba samba-client cifs-utils nfs-utils|samba smbclient cifs-utils nfs-utils|samba samba-client cifs-utils nfs-client
remote-desktop|guacd libguac-client-rdp0 libguac-client-vnc0|guacd libguac-client-rdp libguac-client-vnc|-|-
mdns|avahi-daemon libnss-mdns|avahi nss-mdns|avahi nss-mdns|avahi nss-mdns
smart|smartmontools|smartmontools|smartmontools|smartmontools
zfs|zfsutils-linux|-|-|-
media|ffmpeg|ffmpeg-free|ffmpeg|ffmpeg
backup|restic rclone|restic rclone|restic rclone|restic rclone
'

pkg_manager() {
  if has apt-get; then echo apt
  elif has dnf; then echo dnf
  elif has pacman; then echo pacman
  elif has zypper; then echo zypper
  else echo none; fi
}

pkg_refresh() { # $1 = manager
  case "$1" in
    apt) apt-get update -q >/dev/null 2>&1 ;;
    dnf) dnf -q makecache >/dev/null 2>&1 ;;
    pacman) pacman -Sy >/dev/null 2>&1 ;;
    zypper) zypper --non-interactive -q refresh >/dev/null 2>&1 ;;
  esac
}

pkg_available() { # $1 = manager, $2 = package
  case "$1" in
    apt) apt-cache show "$2" >/dev/null 2>&1 ;;
    dnf) dnf -q info "$2" >/dev/null 2>&1 ;;
    pacman) pacman -Si "$2" >/dev/null 2>&1 ;;
    zypper) zypper --non-interactive -q info "$2" 2>/dev/null | grep -q '^Name' ;;
    *) return 1 ;;
  esac
}

pkg_install() { # $1 = manager, rest = packages
  local mgr="$1"; shift
  case "$mgr" in
    apt) DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=a apt-get install -y -q \
      -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold "$@" >/dev/null 2>&1 ;;
    dnf) dnf install -y -q "$@" >/dev/null 2>&1 ;;
    pacman) pacman -S --noconfirm --needed "$@" >/dev/null 2>&1 ;;
    zypper) zypper --non-interactive -q install "$@" >/dev/null 2>&1 ;;
    *) return 1 ;;
  esac
}

is_ubuntu() { grep -qE '^ID="?ubuntu"?$' /etc/os-release 2>/dev/null; }

install_host_packages() {
  local mgr col line feature list p avail skipped
  mgr="$(pkg_manager)"
  case "$mgr" in
    apt) col=2 ;; dnf) col=3 ;; pacman) col=4 ;; zypper) col=5 ;;
    *) warn "No supported package manager: skipping the host packages (NoCapOS offers them later)."; return 0 ;;
  esac
  info "Installing host packages for virtual machines, sharing, remote desktop, mDNS, disks and media (skip with --minimal)"
  pkg_refresh "$mgr" || warn "Couldn't refresh the package lists; trying with what's cached."
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    feature="${line%%|*}"
    list="$(cut -d'|' -f"$col" <<<"$line")"
    [ "$list" = "-" ] && { warn "$feature: no packages for this distribution; skipped"; continue; }
    if [ "$feature" = zfs ] && ! is_ubuntu; then
      warn "zfs: NoCapOS installs ZFS on Ubuntu; on this system follow OpenZFS's instructions for your distribution"
      continue
    fi
    avail=() skipped=()
    for p in $list; do
      if pkg_available "$mgr" "$p"; then avail+=("$p"); else skipped+=("$p"); fi
    done
    [ "${#skipped[@]}" -eq 0 ] || warn "$feature: not available here, skipped: ${skipped[*]}"
    [ "${#avail[@]}" -gt 0 ] || continue
    printf '    %s: %s\n' "$feature" "${avail[*]}"
    if ! pkg_install "$mgr" "${avail[@]}"; then
      # One broken package shouldn't take the rest of the feature down.
      for p in "${avail[@]}"; do pkg_install "$mgr" "$p" || warn "$feature: couldn't install $p"; done
    fi
  done <<<"$PKG_TABLE"
  enable_host_services
}

enable_host_services() {
  if has virsh; then
    systemctl enable --now libvirtd >/dev/null 2>&1 ||
      systemctl enable --now virtqemud.socket virtnetworkd.socket virtstoraged.socket >/dev/null 2>&1 ||
      warn "libvirt is installed but its service didn't start"
    virsh net-autostart default >/dev/null 2>&1 || true
    virsh net-start default >/dev/null 2>&1 || true
  fi
  if has avahi-daemon || [ -x /usr/sbin/avahi-daemon ]; then
    systemctl enable --now avahi-daemon >/dev/null 2>&1 || warn "avahi-daemon didn't start"
  fi
}

# Brave comes from Brave's official repository, installed by the same script
# the Brave app runs on first launch (alfad carries it: `nocapos install-brave`).
install_brave() {
  case "$(pkg_manager)" in apt | dnf) ;; *) warn "Brave: NoCapOS installs it on apt and dnf systems; skipped"; return 0 ;; esac
  info "Installing Brave and its stream tools (from Brave's repository)"
  "$BIN" install-brave >/dev/null 2>&1 || warn "Brave didn't install now; the Brave app installs it on first launch."
}

# ---------------------------------------------------------------- console
# The text console shows a NoCapOS banner with the addresses to open. Every
# file changed here is backed up to $BRAND_DIR and put back by uninstall.sh.
BRAND_DIR="$DATA/console-backup"

nocap_logo() {
  cat <<'LOGO'
  _   _        ____             ___  ____
 | \ | | ___  / ___|__ _ _ __  / _ \/ ___|
 |  \| |/ _ \| |   / _` | '_ \| | | \___ \
 | |\  | (_) | |__| (_| | |_) | |_| |___) |
 |_| \_|\___/ \____\__,_| .__/ \___/|____/
                        |_|
LOGO
}

# Network cards worth listing (not Docker, VM or VPN bridges).
lan_ifaces() {
  has ip || return 0
  ip -o -4 addr show scope global 2>/dev/null | awk '{print $2}' | sort -u |
    grep -Ev '^(lo|docker|br-|veth|virbr|cni|podman|flannel|cali|vxlan|tun|tap|wg|tailscale|zt)' || true
}

backup_once() { # $1 = file, $2 = backup name; keeps the very first original
  if [ -e "$BRAND_DIR/$2" ] || [ -e "$BRAND_DIR/$2.absent" ]; then return 0; fi
  if [ -e "$1" ]; then cp -p "$1" "$BRAND_DIR/$2"; else touch "$BRAND_DIR/$2.absent"; fi
}

brand_console() {
  mkdir -p "$BRAND_DIR"
  chmod 700 "$BRAND_DIR"
  local suffix="" ifaces i
  [ "$PORT" = 443 ] || suffix=":$PORT"
  ifaces="$(lan_ifaces)"

  # /etc/issue: agetty fills in \4{eth0} with the card's current address, so
  # the banner stays right when DHCP hands out a new one. Backslashes in the
  # logo are doubled because agetty reads them as escapes.
  backup_once /etc/issue issue
  {
    nocap_logo | sed 's/\\/\\\\/g'
    echo
    echo "  Powered By Sha The IT Guy"
    echo
    echo "  Open NoCapOS in a browser on your network:"
    echo "    http://$MDNS_NAME.local"
    if [ -n "$ifaces" ]; then
      for i in $ifaces; do echo "    https://\\4{$i}$suffix"; done
    else
      echo "    https://\\4$suffix"
    fi
    echo
    echo "  \\S{PRETTY_NAME}  \\n  \\l"
    echo
  } >/etc/issue

  # MOTD after SSH/console login. Debian/Ubuntu build it from the scripts in
  # /etc/update-motd.d: switch the stock ones off (chmod -x, listed so
  # uninstall.sh switches them back on) and add ours, which reads the
  # addresses at login time. Elsewhere /etc/motd is a static file.
  backup_once /etc/motd motd
  if [ -d /etc/update-motd.d ]; then
    local f
    for f in /etc/update-motd.d/*; do
      [ -f "$f" ] && [ -x "$f" ] && [ "$(basename "$f")" != 05-nocapos ] || continue
      chmod -x "$f"
      basename "$f" >>"$BRAND_DIR/motd-disabled.list"
    done
    {
      echo '#!/bin/sh'
      echo '# NoCapOS login banner (removed by uninstall.sh)'
      echo "cat <<'LOGO'"
      nocap_logo
      echo 'LOGO'
      echo 'echo'
      echo 'echo "  Powered By Sha The IT Guy"'
      echo 'echo'
      echo "echo \"  http://$MDNS_NAME.local\""
      echo "for ip in \$(hostname -I 2>/dev/null); do case \"\$ip\" in *:*|172.1[7-9].*|172.2[0-9].*|172.3[01].*) continue ;; esac; echo \"  https://\$ip$suffix\"; done"
      echo 'echo'
    } >/etc/update-motd.d/05-nocapos
    chmod 755 /etc/update-motd.d/05-nocapos
    : >/etc/motd
  else
    {
      nocap_logo
      echo
      echo "  Powered By Sha The IT Guy"
      echo
      echo "  http://$MDNS_NAME.local"
      for i in $(hostname -I 2>/dev/null); do case "$i" in *:*) ;; *) echo "  https://$i$suffix" ;; esac; done
      echo
    } >/etc/motd
  fi
}

# mDNS: announce this server as <name>.local (default nocap.local) and
# publish NoCapOS as a web service. The system hostname is left alone; if
# Avahi already has a host-name of its own (set by you), it is kept.
setup_mdns() {
  if [ ! -d /etc/avahi ]; then
    warn "Avahi isn't installed, so $MDNS_NAME.local won't resolve (install avahi-daemon, then re-run the installer)"
    return 0
  fi
  mkdir -p "$BRAND_DIR" /etc/avahi/services
  local conf=/etc/avahi/avahi-daemon.conf current=""
  [ -f "$conf" ] && current="$(sed -n 's/^[[:space:]]*host-name[[:space:]]*=[[:space:]]*//p' "$conf" | head -1 | tr -d '[:space:]')"
  if [ -z "$current" ]; then
    backup_once "$conf" avahi-daemon.conf
    if [ -f "$conf" ] && grep -q '^\[server\]' "$conf"; then
      sed -i "/^\[server\]/a host-name=$MDNS_NAME" "$conf"
    else
      printf '[server]\nhost-name=%s\n' "$MDNS_NAME" >>"$conf"
    fi
  elif [ "$current" != "$MDNS_NAME" ]; then
    info "Avahi already announces this server as $current.local; keeping that name"
    MDNS_NAME="$current"
  fi
  cat >/etc/avahi/services/nocapos.service <<EOF
<?xml version="1.0" standalone='no'?>
<!DOCTYPE service-group SYSTEM "avahi-service.dtd">
<!-- NoCapOS web desktop (removed by uninstall.sh) -->
<service-group>
  <name replace-wildcards="yes">NoCapOS on %h</name>
  <service>
    <type>_https._tcp</type>
    <port>$PORT</port>
  </service>
  <service>
    <type>_http._tcp</type>
    <port>80</port>
  </service>
</service-group>
EOF
  systemctl enable avahi-daemon >/dev/null 2>&1 || true
  systemctl restart avahi-daemon >/dev/null 2>&1 || warn "avahi-daemon didn't restart; $MDNS_NAME.local may not resolve yet"
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
    echo "   ufw is active — allow access with:  sudo ufw allow $PORT/tcp && sudo ufw allow 80/tcp && sudo ufw allow 5353/udp"
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

# Updates keep the previous binary and a copy of the database, so an update
# that doesn't start can be put back the way it was.
PREV_BIN="$BIN.prev"
DB_BACKUP="$DATA/pre-update"

backup_for_rollback() {
  [ -f "$BIN" ] || return 1
  cp -p "$BIN" "$PREV_BIN" || return 1
  rm -rf "$DB_BACKUP"
  if [ -f "$DATA/alfa.db" ]; then
    mkdir -p "$DB_BACKUP"
    chmod 700 "$DB_BACKUP"
    local f
    for f in alfa.db alfa.db-wal alfa.db-shm; do
      if [ -f "$DATA/$f" ]; then cp -p "$DATA/$f" "$DB_BACKUP/$f"; fi
    done
  fi
  return 0
}

wait_native() { # sets READY and TOKEN
  local logs
  READY=0 TOKEN=""
  for _ in $(seq 1 30); do
    logs="$(journalctl -u nocapos --no-pager -o cat -n 400 2>/dev/null || true)"
    if since_last_start <<<"$logs" | grep '"alfad ready"' >/dev/null; then READY=1; TOKEN="$(setup_token <<<"$logs")"; break; fi
    systemctl is-failed --quiet nocapos && break
    sleep 1
  done
}

rollback_native() {
  local prev
  prev="$("$PREV_BIN" version 2>/dev/null || echo "the previous version")"
  warn "$NAME $VERSION did not start; putting back $prev"
  systemctl stop nocapos 2>/dev/null || true
  install -m 0755 "$PREV_BIN" "$BIN"
  if [ -f "$DB_BACKUP/alfa.db" ]; then
    rm -f "$DATA/alfa.db-wal" "$DATA/alfa.db-shm"
    local f
    for f in alfa.db alfa.db-wal alfa.db-shm; do
      if [ -f "$DB_BACKUP/$f" ]; then cp -p "$DB_BACKUP/$f" "$DATA/$f"; fi
    done
  fi
  systemctl restart nocapos
  wait_native
  if [ "$READY" -eq 1 ]; then
    die "the update to $VERSION failed and was rolled back: $prev is running again with its database as it was. Details: journalctl -u nocapos -e"
  fi
  die "the update to $VERSION failed and $prev didn't start either. Check: journalctl -u nocapos -e (database copy: $DB_BACKUP)"
}

install_native_mode() {
  migrate_alfaos
  local fresh=1
  [ -f "$UNIT" ] && fresh=0
  if [ "$fresh" -eq 1 ] && port_busy "$PORT"; then die "port $PORT is already in use (choose another with --port N)"; fi

  ensure_docker 0
  if [ "$MINIMAL" -eq 1 ]; then
    info "Minimal install: skipping the optional host packages (Packages in NoCapOS installs them later)"
  else
    install_host_packages
  fi

  info "Installing $BIN"
  systemctl stop nocapos 2>/dev/null || true
  local can_rollback=0
  if backup_for_rollback; then
    can_rollback=1
    info "Kept the previous version and a copy of the database for rollback"
  fi
  install -m 0755 "$BUNDLE_DIR/alfad" "$BIN"
  [ -f "$BUNDLE_DIR/cap" ] && install -m 0755 "$BUNDLE_DIR/cap" /usr/local/bin/cap

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
  wait_native
  if [ "$READY" -ne 1 ]; then
    [ "$can_rollback" -eq 1 ] && rollback_native
    die "$NAME did not start; check: journalctl -u nocapos -e"
  fi
  [ "$MINIMAL" -eq 1 ] || install_brave
  setup_mdns
  if [ "$BRANDING" -eq 1 ]; then brand_console; fi
  local url="https://HOST/"
  [ "$PORT" = 443 ] || url="https://HOST:$PORT/"
  finish "$url" "$TOKEN"
  if [ -d /etc/avahi ]; then echo "   Or:    http://$MDNS_NAME.local (redirects to HTTPS)"; fi
  echo "   $NAME runs as root and can manage this server: Linux users (sign in with them), network, power,"
  echo "   storage and a host terminal — all admin-only and recorded in the audit log."
  echo "   Turn on two-factor sign-in under Settings → Security."
  echo "   Manage:  systemctl status|restart nocapos · cap update · cap logs -f · settings in $ETC/nocapos.env"
}

if [ "$MODE" = docker ]; then install_docker_mode; else install_native_mode; fi
