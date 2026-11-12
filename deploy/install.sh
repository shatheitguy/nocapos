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
#                       engine, Avahi, SMART, ZFS, FFmpeg, restic/rclone); NoCapOS offers
#                       to install each one later, from its app
#   --desktop           NoCap Desktop: a native desktop on this machine's own monitor (NoCapOS
#                       sign-in screen, dock, Files, Settings, Software, terminal, text editor,
#                       a web browser and the App Store's Linux apps; sign in with this
#                       machine's Linux accounts). Always on by default, with or without a
#                       monitor plugged in; this flag is kept for older instructions.
#   --no-desktop        don't set up NoCap Desktop (and turn it off if it was on)
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
DESKTOP=auto
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
    --desktop | --desktop=full) DESKTOP=full ;;
    --desktop=kiosk | --desktop=web) DESKTOP=full; warn "--desktop=kiosk is gone: the screen always gets the native NoCap Desktop" ;;
    --no-desktop | --desktop=off) DESKTOP=no ;;
    --mdns-name) MDNS_NAME="${2:?--mdns-name needs a name}"; shift ;;
    --yes | -y) ASSUME_YES=1 ;;
    --uninstall) UNINSTALL=1 ;;
    --purge) PURGE=1 ;;
    -h | --help) sed -n '2,30p' "${BASH_SOURCE[0]:-$0}" 2>/dev/null || echo "see https://github.com/shatheitguy/nocapos"; exit 0 ;;
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
discovery|wsdd|wsdd|wsdd|wsdd
remote-desktop|guacd libguac-client-rdp0 libguac-client-vnc0|guacd libguac-client-rdp libguac-client-vnc|-|-
mdns|avahi-daemon libnss-mdns|avahi nss-mdns|avahi nss-mdns|avahi nss-mdns
smart|smartmontools|smartmontools|smartmontools|smartmontools
zfs|zfsutils-linux|-|-|-
media|ffmpeg|ffmpeg-free|ffmpeg|ffmpeg
backup|restic rclone|restic rclone|restic rclone|restic rclone
flatpak|flatpak|flatpak|flatpak|flatpak
linux-desktop|xvfb x11vnc novnc websockify openbox lxpanel pcmanfm lxterminal dbus-x11 dbus-user-session wayvnc|xorg-x11-server-Xvfb x11vnc novnc python3-websockify openbox lxpanel pcmanfm lxterminal dbus-x11 wayvnc|xorg-server-xvfb x11vnc novnc websockify openbox lxpanel pcmanfm lxterminal wayvnc|xorg-x11-server-Xvfb x11vnc novnc python3-websockify openbox lxpanel pcmanfm lxterminal dbus-1-x11 wayvnc
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

# ---------------------------------------------------------------- ZFS on Debian
# Ubuntu's kernels carry ZFS; Debian and Raspberry Pi OS build it for the
# running kernel with DKMS, from Debian's "contrib" component. Same steps as
# backend/internal/syspkg/zfs.go (the Storage app's Install button).
# Anything going wrong here is a warning: the install always carries on.

# awk helpers: is this source Debian's own archive (a mirror, security, or
# Raspbian), not Raspberry Pi's or a third party's repository?
CONTRIB_FUNCS='
function debian(u,   l) {
  l = tolower(u)
  if (l ~ /^cdrom:/ || l ~ /raspberrypi\.com/ || l ~ /raspberrypi\.org\/debian/) return 0
  return (l ~ /debian/ || l ~ /raspbian/)
}'
# One-line sources (sources.list, *.list): add contrib after "main".
CONTRIB_LIST='
{
  t = $0; sub(/^[ \t]+/, "", t)
  if (t !~ /^deb(-src)?[ \t]/) { print; next }
  b = t; sub(/[ \t]*#.*$/, "", b)
  n = split(b, f, /[ \t]+/)
  k = 2
  if (f[k] ~ /^\[/) { while (k <= n && f[k] !~ /\]$/) k++; k++ }
  if (k + 2 > n || !debian(f[k])) { print; next }
  hm = 0; hc = 0
  for (i = k + 2; i <= n; i++) { if (f[i] == "main") hm = 1; if (f[i] == "contrib") hc = 1 }
  line = $0
  if (hm && !hc && match(line, /[ \t]main([ \t#\r]|$)/)) {
    line = substr(line, 1, RSTART + 4) " contrib" substr(line, RSTART + 5); changed = 1
  }
  print line
}
END { exit changed ? 0 : 3 }'
# deb822 sources (*.sources): add contrib to the Components of enabled
# Debian stanzas.
CONTRIB_DEB822='
function val(s) { sub(/\r$/, "", s); sub(/^[^:]*:[ \t]*/, "", s); return s }
function flush(   i, en, deb, ci, n, k, u, c, hm, hc, line, cr, low) {
  en = 1; deb = 0; ci = 0
  for (i = 1; i <= cnt; i++) {
    low = tolower(buf[i])
    if (low ~ /^enabled:/ && tolower(val(buf[i])) == "no") en = 0
    if (low ~ /^uris:/) { n = split(val(buf[i]), u, /[ \t]+/); for (k = 1; k <= n; k++) if (debian(u[k])) deb = 1 }
    if (low ~ /^components:/) ci = i
  }
  if (en && deb && ci) {
    n = split(val(buf[ci]), c, /[ \t]+/); hm = 0; hc = 0
    for (k = 1; k <= n; k++) { if (c[k] == "main") hm = 1; if (c[k] == "contrib") hc = 1 }
    if (hm && !hc) {
      line = buf[ci]; cr = (line ~ /\r$/); sub(/[ \t\r]+$/, "", line)
      buf[ci] = line " contrib" (cr ? "\r" : ""); changed = 1
    }
  }
  for (i = 1; i <= cnt; i++) print buf[i]
  cnt = 0
}
/^[ \t\r]*$/ { flush(); print; next }
{ buf[++cnt] = $0 }
END { flush(); exit changed ? 0 : 3 }'

# Turns on contrib where it's missing. The original of each changed file is
# kept beside it as <file>.nocapos.bak (apt quietly ignores *.bak).
enable_contrib() {
  local f tmp prog
  for f in /etc/apt/sources.list /etc/apt/sources.list.d/*.list /etc/apt/sources.list.d/*.sources; do
    [ -f "$f" ] || continue
    case "$f" in *.sources) prog="$CONTRIB_DEB822" ;; *) prog="$CONTRIB_LIST" ;; esac
    tmp="$(mktemp)"
    if awk "$CONTRIB_FUNCS $prog" "$f" >"$tmp" 2>/dev/null; then
      [ -e "$f.nocapos.bak" ] || cp -p "$f" "$f.nocapos.bak"
      cat "$tmp" >"$f" # keeps the file's owner and mode
      printf '    zfs: turned on contrib in %s\n' "$f"
    fi
    rm -f "$tmp"
  done
}

# Prints the kernel headers package for the running kernel: the exact
# version first, then Raspberry Pi's flavour packages, then Debian's
# per-architecture one.
zfs_headers() {
  local rel cands c pi=0
  rel="$(uname -r)"
  grep -qs 'Raspberry Pi' /proc/device-tree/model && pi=1
  cands="linux-headers-$rel"
  if [ "$pi" -eq 1 ] || [[ "$rel" == *rpi* || "$rel" == *+rpt* || "$rel" == *-v8+ || "$rel" == *-v7l+ ]]; then
    case "$rel" in
      *2712*) cands="$cands linux-headers-rpi-2712 linux-headers-rpi-v8" ;;
      *v7l*) cands="$cands linux-headers-rpi-v7l linux-headers-rpi-v7" ;;
      *v7*) cands="$cands linux-headers-rpi-v7" ;;
      *) cands="$cands linux-headers-rpi-v8 linux-headers-rpi-2712" ;;
    esac
    cands="$cands raspberrypi-kernel-headers"
  else
    case "$ARCH" in
      amd64) cands="$cands linux-headers-amd64" ;;
      arm64) cands="$cands linux-headers-arm64" ;;
      armv7) cands="$cands linux-headers-armmp" ;;
    esac
  fi
  for c in $cands; do
    if pkg_available apt "$c"; then echo "$c"; return 0; fi
  done
  return 1
}

install_zfs_debian() {
  if has zfs && [ -e /sys/module/zfs ]; then printf '    zfs: already installed\n'; return 0; fi
  printf '    zfs: building OpenZFS for kernel %s with DKMS (a few minutes; longer on small boards)\n' "$(uname -r)"
  enable_contrib
  pkg_refresh apt || warn "zfs: couldn't refresh the package lists; trying with what's cached"
  local hdr
  if hdr="$(zfs_headers)"; then
    printf '    zfs: %s\n' "$hdr"
    pkg_install apt "$hdr" || warn "zfs: couldn't install $hdr; trying the build anyway"
  else
    warn "zfs: no kernel headers for kernel $(uname -r) in your apt sources; trying the build anyway"
  fi
  if ! pkg_available apt zfs-dkms; then
    warn "zfs: zfs-dkms isn't in your apt sources (contrib missing?); skipped. Install it later from the Storage app"
    return 0
  fi
  printf '    zfs: zfs-dkms zfsutils-linux\n'
  if ! pkg_install apt zfs-dkms zfsutils-linux; then
    warn "zfs: the ZFS build failed, usually no kernel headers for $(uname -r); update, reboot, then hit Install in the Storage app"
    return 0
  fi
  modprobe zfs 2>/dev/null || warn "zfs: installed, but the kernel module didn't load; reboot, then open the Storage app"
}

install_host_packages() {
  local mgr col line feature list p avail skipped
  mgr="$(pkg_manager)"
  case "$mgr" in
    apt) col=2 ;; dnf) col=3 ;; pacman) col=4 ;; zypper) col=5 ;;
    *) warn "No supported package manager: skipping the host packages (NoCapOS offers them later)."; return 0 ;;
  esac
  info "Installing host packages for virtual machines, sharing, remote desktop, mDNS, disks, media and Linux apps (skip with --minimal)"
  pkg_refresh "$mgr" || warn "Couldn't refresh the package lists; trying with what's cached."
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    feature="${line%%|*}"
    list="$(cut -d'|' -f"$col" <<<"$line")"
    [ "$list" = "-" ] && { warn "$feature: no packages for this distribution; skipped"; continue; }
    if [ "$feature" = zfs ] && [ "$mgr" = apt ] && ! is_ubuntu; then
      install_zfs_debian # Debian, Raspberry Pi OS: built with DKMS; never fails the install
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
  setup_flathub
  setup_apps_account
}

# Streamed Linux apps run as the person's own Linux account; people without
# one (or signed in as root) use this unprivileged, login-less account, with
# a home per person under /var/lib/nocap-apps/homes. Never fatal: NoCapOS
# makes it on the first launch too.
APPS_USER=nocap-apps
setup_apps_account() {
  if ! id "$APPS_USER" >/dev/null 2>&1; then
    useradd --system --user-group --create-home --home-dir /var/lib/nocap-apps \
      --shell /usr/sbin/nologin --comment "NoCapOS apps" "$APPS_USER" >/dev/null 2>&1 ||
      { warn "Couldn't create the $APPS_USER account; NoCapOS makes it on the first Linux app launch."; return 0; }
  fi
  mkdir -p /var/lib/nocap-apps/homes 2>/dev/null || true
  chown "$APPS_USER:$APPS_USER" /var/lib/nocap-apps /var/lib/nocap-apps/homes 2>/dev/null || true
  chmod 0755 /var/lib/nocap-apps 2>/dev/null || true
  chmod 0711 /var/lib/nocap-apps/homes 2>/dev/null || true
}

# Linux apps: the App Store installs desktop apps from Flathub, system-wide.
# Never fatal: NoCapOS adds the remote again on the first app install.
FLATHUB_REPO=https://dl.flathub.org/repo/flathub.flatpakrepo
setup_flathub() {
  has flatpak || { warn "flatpak isn't installed: Linux apps in the App Store stay off until it is"; return 0; }
  flatpak remote-add --if-not-exists --system flathub "$FLATHUB_REPO" >/dev/null 2>&1 ||
    warn "Couldn't add Flathub right now; NoCapOS adds it on the first Linux app install."
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

# ---------------------------------------------------------------- NoCap Desktop
# NoCap Desktop is a native desktop on this machine's own monitor: NoCapOS's
# GDM sign-in with the machine's Linux accounts, then the NoCapOS session
# (GNOME Shell with NoCapOS's look and native apps). There is no web UI on
# the screen. Turned on and off from Settings → General → NoCap Desktop.
# The helpers ship in the bundle's desktop/ folder.

display_connected() { grep -qsx connected /sys/class/drm/*/status; }

# Someone logged in on tty1 right now (maybe running this installer there):
# starting the desktop would take that console away from them.
tty1_in_use() { has who && who 2>/dev/null | awk '$2 == "tty1" {found=1} END {exit !found}'; }

DESKTOP_FULL_OK=0
if [ -f "$BUNDLE_DIR/desktop/lib.sh" ] && [ -f "$BUNDLE_DIR/desktop/setup.sh" ]; then
  # shellcheck source=/dev/null
  . "$BUNDLE_DIR/desktop/lib.sh"
  # shellcheck source=/dev/null
  . "$BUNDLE_DIR/desktop/setup.sh"
  DESKTOP_FULL_OK=1
fi

# NoCap Desktop is on or off; the last choice is kept in $ETC/desktop-mode
# and updates keep it. Updates also move machines off what earlier versions
# put on the screen (NoCapOS full screen in a browser, NoCap Hub) and off a
# distribution's own desktop (Raspberry Pi OS's, say) onto NoCap Desktop.
setup_desktop() {
  local want="" chosen
  [ "$DESKTOP_FULL_OK" -eq 1 ] || { warn "this bundle has no NoCap Desktop files; skipped"; return 0; }
  desk_remove_kiosk
  case "$DESKTOP" in
    full) want=full ;;
    no)
      if [ -f "$ETC/desktop.conf" ]; then apply_desktop_mode off; fi
      echo user-off >"$ETC/desktop-mode"
      return 0
      ;;
    auto)
      # NoCap Desktop is part of NoCapOS: every install and update sets it up,
      # monitor or not. Only an explicit "off" (Settings or --no-desktop) is kept.
      chosen="$(cat "$ETC/desktop-mode" 2>/dev/null || true)"
      case "$chosen" in
        user-off) return 0 ;;
        *) want=full ;;
      esac
      ;;
  esac
  if install_full_desktop; then
    apply_desktop_mode full
    echo full >"$ETC/desktop-mode"
  fi
  return 0
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

# ---------------------------------------------------------------- boot splash
# The "nocapos" Plymouth theme: the logo on near-black with loading dots and
# a progress bar while the machine boots. The previous theme and every
# kernel command line token added here are recorded in $SPLASH_DIR, so
# uninstall.sh puts them back.
SPLASH_DIR="$DATA/bootsplash-backup"
SPLASH_THEME=/usr/share/plymouth/themes/nocapos

# >>> cmdline helpers (pure; tested by backend/internal/deploytest)
# cmdline_with TOKEN...: reads a kernel command line (Raspberry Pi
# cmdline.txt) on stdin and prints it as one line with each token added
# once; what was there stays as it was.
cmdline_with() (
  set -f
  local out="" w tok
  for w in $(tr '\r\n' '  '); do out="$out $w"; done
  for tok in "$@"; do
    case " $out " in *" $tok "*) ;; *) out="$out $tok" ;; esac
  done
  printf '%s\n' "${out# }"
)

# cmdline_without TOKEN...: the opposite, for uninstalling.
cmdline_without() (
  set -f
  local out="" w tok keep
  for w in $(tr '\r\n' '  '); do
    keep=1
    for tok in "$@"; do [ "$w" = "$tok" ] && keep=0; done
    [ "$keep" -eq 0 ] || out="$out $w"
  done
  printf '%s\n' "${out# }"
)

# grub_default_with TOKEN...: reads /etc/default/grub on stdin and prints it
# with the tokens added to GRUB_CMDLINE_LINUX_DEFAULT (adding that line if
# it's missing). Everything else is left exactly as it was.
grub_default_with() {
  awk -v toks="$*" '
    function addtoks(v,   n, t, i, have) {
      n = split(toks, t, " ")
      for (i = 1; i <= n; i++) {
        have = (" " v " ") ~ (" " t[i] " ")
        if (!have) v = (v == "" ? t[i] : v " " t[i])
      }
      return v
    }
    /^GRUB_CMDLINE_LINUX_DEFAULT=/ && !done {
      v = substr($0, length("GRUB_CMDLINE_LINUX_DEFAULT=") + 1)
      q = substr(v, 1, 1)
      if (q == "\"" || q == "\047") { v = substr(v, 2); sub(q "[[:space:]]*$", "", v) }
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", v)
      print "GRUB_CMDLINE_LINUX_DEFAULT=\"" addtoks(v) "\""
      done = 1
      next
    }
    { print }
    END { if (!done) print "GRUB_CMDLINE_LINUX_DEFAULT=\"" addtoks("") "\"" }
  '
}

# grub_default_without TOKEN...: removes the tokens from GRUB_CMDLINE_LINUX_DEFAULT.
grub_default_without() {
  awk -v toks="$*" '
    /^GRUB_CMDLINE_LINUX_DEFAULT=/ {
      v = substr($0, length("GRUB_CMDLINE_LINUX_DEFAULT=") + 1)
      q = substr(v, 1, 1)
      if (q == "\"" || q == "\047") { v = substr(v, 2); sub(q "[[:space:]]*$", "", v) }
      n = split(v, w, " "); m = split(toks, t, " "); out = ""
      for (i = 1; i <= n; i++) {
        drop = 0
        for (j = 1; j <= m; j++) if (w[i] == t[j]) drop = 1
        if (!drop && w[i] != "") out = (out == "" ? w[i] : out " " w[i])
      }
      print "GRUB_CMDLINE_LINUX_DEFAULT=\"" out "\""
      next
    }
    { print }
  '
}

# tokens_missing LINE TOKEN...: prints the tokens LINE doesn't have yet.
tokens_missing() (
  set -f
  local line=" $1 " tok out=""
  shift
  for tok in "$@"; do case "$line" in *" $tok "*) ;; *) out="$out $tok" ;; esac; done
  printf '%s\n' "${out# }"
)
# <<< cmdline helpers

# pi_cmdline: the Raspberry Pi's kernel command line file, if this is one.
pi_cmdline() {
  local f
  for f in /boot/firmware/cmdline.txt /boot/cmdline.txt; do
    if [ -f "$f" ]; then echo "$f"; return 0; fi
  done
  return 1
}

setup_bootsplash() {
  [ -f "$BUNDLE_DIR/plymouth/nocapos.script" ] || return 0 # an older bundle
  if ! has plymouth-set-default-theme && [ "$MINIMAL" -eq 0 ]; then
    local mgr
    mgr="$(pkg_manager)"
    case "$mgr" in
      apt) pkg_available apt plymouth && pkg_install apt plymouth plymouth-themes || true ;;
      dnf) pkg_install dnf plymouth plymouth-scripts || true ;;
    esac
  fi
  if ! has plymouth-set-default-theme; then
    warn "Plymouth isn't available, so the NoCapOS boot splash was skipped"
    return 0
  fi
  mkdir -p "$SPLASH_DIR"
  chmod 700 "$SPLASH_DIR"
  local current changed=0
  current="$(plymouth-set-default-theme 2>/dev/null || true)"
  if [ ! -f "$SPLASH_DIR/theme" ] && [ "$current" != nocapos ]; then echo "${current:-text}" >"$SPLASH_DIR/theme"; fi

  if ! diff -rq "$BUNDLE_DIR/plymouth" "$SPLASH_THEME" >/dev/null 2>&1; then
    rm -rf "$SPLASH_THEME"
    mkdir -p "$SPLASH_THEME"
    cp -R "$BUNDLE_DIR/plymouth/." "$SPLASH_THEME/"
    chmod -R a+rX "$SPLASH_THEME"
    changed=1
  fi
  if [ "$current" != nocapos ] || [ "$changed" -eq 1 ]; then
    info "Setting up the NoCapOS boot splash"
    # -R rebuilds the initramfs so the splash shows from the very start.
    if ! plymouth-set-default-theme -R nocapos >/dev/null 2>&1; then
      plymouth-set-default-theme nocapos >/dev/null 2>&1 || { warn "couldn't set the boot splash theme"; return 0; }
      warn "boot splash set, but the initramfs couldn't be rebuilt; it shows a little later in the boot"
    fi
  fi

  # The kernel needs "quiet splash" for Plymouth to draw instead of boot
  # text; the rest of the tokens hide firmware logos and boot messages.
  local f add BOOT_TOKENS="${SPLASH_TOKENS:-quiet splash} ${EXTRA_BOOT_TOKENS:-}"
  BOOT_GRUB_CHANGED=0
  # Boot menus and firmware: GRUB theme, systemd-boot, U-Boot, Raspberry Pi.
  if [ "$DESKTOP_FULL_OK" -eq 1 ]; then brand_boot || warn "some boot menus couldn't be branded"; fi
  if f="$(pi_cmdline)"; then
    # A serial console (enabled on many Pi images) makes Plymouth fall back to text.
    add="$(tokens_missing "$(tr '\r\n' '  ' <"$f")" $BOOT_TOKENS plymouth.ignore-serial-consoles)"
    if [ -n "$add" ]; then
      [ -f "$SPLASH_DIR/cmdline.txt" ] || cp -p "$f" "$SPLASH_DIR/cmdline.txt"
      # shellcheck disable=SC2086 # add is a list of tokens
      cmdline_with $add <"$f" >"$f.nocap.tmp" && cat "$f.nocap.tmp" >"$f" && rm -f "$f.nocap.tmp"
      echo "$add" >>"$SPLASH_DIR/cmdline-added"
    fi
  elif [ -f /etc/default/grub ]; then
    local cur
    cur="$(sed -n 's/^GRUB_CMDLINE_LINUX_DEFAULT=//p' /etc/default/grub | head -1 | tr -d "\"'")"
    add="$(tokens_missing "$cur" $BOOT_TOKENS)"
    if [ -n "$add" ]; then
      [ -f "$SPLASH_DIR/grub" ] || cp -p /etc/default/grub "$SPLASH_DIR/grub"
      # shellcheck disable=SC2086
      grub_default_with $add </etc/default/grub >/etc/default/grub.nocap.tmp && cat /etc/default/grub.nocap.tmp >/etc/default/grub
      rm -f /etc/default/grub.nocap.tmp
      echo "$add" >>"$SPLASH_DIR/grub-added"
      BOOT_GRUB_CHANGED=1
    fi
  fi
  if [ "$BOOT_GRUB_CHANGED" -eq 1 ]; then
    if has update-grub; then update-grub >/dev/null 2>&1 || warn "update-grub failed; the splash shows after the next GRUB update"
    elif has grub2-mkconfig && [ -f /boot/grub2/grub.cfg ]; then grub2-mkconfig -o /boot/grub2/grub.cfg >/dev/null 2>&1 || true
    elif has grub-mkconfig && [ -f /boot/grub/grub.cfg ]; then grub-mkconfig -o /boot/grub/grub.cfg >/dev/null 2>&1 || true
    fi
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
  local logs deadline
  READY=0 TOKEN=""
  # Up to 2 minutes: a Raspberry Pi on an SD card can take a while to start,
  # and giving up too early would roll back an update that was fine.
  deadline=$((SECONDS + 120))
  while [ "$SECONDS" -lt "$deadline" ]; do
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

# Started from inside NoCapOS itself (Update now in an older version, or its
# host terminal), this installer runs in nocapos.service's cgroup, and
# stopping the service below would kill it halfway through. So it moves to a
# transient unit of its own first and follows that one from here.
detach_from_service() {
  [ -z "${NOCAP_DETACHED:-}" ] || return 0
  grep -qs 'nocapos\.service' /proc/self/cgroup || return 0
  has systemd-run || return 0
  local work="$DATA/update-bundle" unit out log tailpid="" rc
  unit="nocapos-install-$(date +%s)"
  rm -rf "$work"
  mkdir -p "$work"
  chmod 700 "$work"
  cp -a "$BUNDLE_DIR/." "$work/" # the bundle may sit in the service's private /tmp
  out="$(readlink -f "/proc/$$/fd/1" 2>/dev/null || true)"
  if [ -n "$out" ] && [ -f "$out" ]; then log="$out"; else log="$work/install.log"; : >"$log"; fi
  info "Continuing in its own service ($unit), so restarting $NAME doesn't stop it"
  # An older NoCapOS's lock (a bare timestamp) is never removed by the updater
  # that wrote it, since that one stops with the service: clear it at the end.
  if ! systemd-run --unit="$unit" --collect --quiet --setenv=NOCAP_DETACHED=1 /bin/bash -c '
      bash "$1/install.sh" "${@:4}" >>"$2" 2>&1; rc=$?
      echo "$rc" >"$1/rc"
      if [ -f "$3/update.running" ] && ! grep -q "^{" "$3/update.running"; then rm -f "$3/update.running"; fi
      sleep 10; rm -rf "$1"; exit "$rc"' _ "$work" "$log" "$DATA" "${ARGS[@]}"; then
    warn "Couldn't start a separate service for the update; carrying on here"
    rm -rf "$work"
    return 0
  fi
  [ "$log" = "$out" ] || { tail -n +1 -f "$log" & tailpid=$!; }
  while [ ! -f "$work/rc" ] && systemctl is-active --quiet "$unit"; do sleep 1; done
  sleep 1
  [ -z "$tailpid" ] || kill "$tailpid" 2>/dev/null || true
  rc="$(cat "$work/rc" 2>/dev/null || echo 1)"
  exit "$rc"
}

# Run by hand (not by Update now), clear an update lock whose updater is gone:
# an older NoCapOS's bare-timestamp lock (its updater stopped with the
# service), or one naming a unit that no longer runs. Left behind, it makes
# Settings think an update is still going (and then that it failed).
clear_stale_update_lock() {
  local lock="$DATA/update.running" unit
  [ -f "$lock" ] || return 0
  [ -z "${NOCAP_UPDATER:-}" ] && [ -z "${NOCAP_DETACHED:-}" ] || return 0
  if ! grep -q '^{' "$lock"; then rm -f "$lock"; return 0; fi
  unit="$(sed -n 's/.*"unit":"\([A-Za-z0-9_.@-]*\)".*/\1/p' "$lock")"
  if [ -z "$unit" ] || ! systemctl is-active --quiet "$unit" 2>/dev/null; then rm -f "$lock"; fi
}

install_native_mode() {
  detach_from_service
  clear_stale_update_lock
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
  setup_desktop
  setup_mdns
  if [ "$BRANDING" -eq 1 ]; then
    brand_console
    setup_bootsplash || warn "the boot splash couldn't be set up; NoCapOS itself is fine"
  fi
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
