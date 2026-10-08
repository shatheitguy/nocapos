#!/usr/bin/env bash
# NoCapOS — native Brave on a virtual display, streamed to the web desktop.
#
#   brave-run.sh install   install Brave (official repo) + Xvfb, x11vnc, noVNC
#   brave-run.sh run       start the display, Brave, VNC and the noVNC bridge
#
# alfad writes this file to its data dir and runs it; edit freely for odd distros.
set -euo pipefail

MODE="${1:-run}"
DISPLAY_NUM="${BRAVE_DISPLAY:-:99}"
WEB_PORT="${BRAVE_WEB_PORT:-6080}"
VNC_PORT="${BRAVE_VNC_PORT:-5999}"
PROFILE="${BRAVE_PROFILE:-$HOME/.nocap-brave}"
GEOM="${BRAVE_GEOMETRY:-1600x900}"

as_root() {
  if [ "$(id -u)" = 0 ]; then "$@"
  elif command -v sudo >/dev/null 2>&1 && sudo -n true 2>/dev/null; then sudo -n "$@"
  else
    echo "Installing Brave needs root: run alfad as root or allow passwordless sudo." >&2
    exit 3
  fi
}

brave_bin() { command -v brave-browser || command -v brave-browser-stable || command -v brave; }

novnc_dir() {
  for d in /usr/share/novnc /usr/share/webapps/novnc /usr/share/noVNC; do
    if [ -f "$d/vnc.html" ]; then echo "$d"; return 0; fi
  done
  return 1
}

have_all() {
  brave_bin >/dev/null && command -v Xvfb >/dev/null && command -v x11vnc >/dev/null &&
    command -v websockify >/dev/null && novnc_dir >/dev/null
}

install() {
  if have_all; then echo "Brave and stream tools already installed."; return 0; fi

  if command -v apt-get >/dev/null 2>&1; then
    export DEBIAN_FRONTEND=noninteractive
    as_root apt-get update -q
    as_root apt-get install -y -q curl ca-certificates xvfb x11vnc novnc websockify fluxbox fonts-dejavu-core
    if ! brave_bin >/dev/null; then
      as_root curl -fsSLo /usr/share/keyrings/brave-browser-archive-keyring.gpg \
        https://brave-browser-apt-release.s3.brave.com/brave-browser-archive-keyring.gpg
      echo "deb [signed-by=/usr/share/keyrings/brave-browser-archive-keyring.gpg] https://brave-browser-apt-release.s3.brave.com/ stable main" |
        as_root tee /etc/apt/sources.list.d/brave-browser-release.list >/dev/null
      as_root apt-get update -q
      as_root apt-get install -y -q brave-browser
    fi
  elif command -v dnf >/dev/null 2>&1; then
    as_root dnf install -y -q xorg-x11-server-Xvfb x11vnc novnc python3-websockify fluxbox dnf-plugins-core
    if ! brave_bin >/dev/null; then
      as_root dnf config-manager addrepo --from-repofile=https://brave-browser-rpm-release.s3.brave.com/brave-browser.repo ||
        as_root dnf config-manager --add-repo https://brave-browser-rpm-release.s3.brave.com/brave-browser.repo
      as_root rpm --import https://brave-browser-rpm-release.s3.brave.com/brave-core.asc
      as_root dnf install -y -q brave-browser
    fi
  else
    echo "Unsupported distro: NoCapOS can install Brave on apt (Debian/Ubuntu) or dnf (Fedora) systems." >&2
    exit 4
  fi
  have_all || { echo "Install finished but some tools are still missing." >&2; exit 5; }
}

run() {
  local brave web n
  brave="$(brave_bin)"
  web="$(novnc_dir)"
  n="${DISPLAY_NUM#:}"
  mkdir -p "$PROFILE"

  # Clear a stale lock left by a crashed display.
  if [ -f "/tmp/.X${n}-lock" ] && ! pgrep -f "Xvfb ${DISPLAY_NUM}" >/dev/null; then
    rm -f "/tmp/.X${n}-lock" "/tmp/.X11-unix/X${n}"
  fi

  Xvfb "$DISPLAY_NUM" -screen 0 "${GEOM}x24" -nolisten tcp &
  for _ in $(seq 1 50); do [ -S "/tmp/.X11-unix/X${n}" ] && break; sleep 0.1; done
  export DISPLAY="$DISPLAY_NUM"

  if command -v fluxbox >/dev/null; then fluxbox >/dev/null 2>&1 & fi

  local sandbox=()
  [ "$(id -u)" = 0 ] && sandbox=(--no-sandbox)
  # Relaunch Brave if its window is closed, so the stream never goes blank.
  (
    while true; do
      "$brave" "${sandbox[@]}" --user-data-dir="$PROFILE" --no-first-run --no-default-browser-check \
        --start-maximized --disable-dev-shm-usage >/dev/null 2>&1 || true
      sleep 1
    done
  ) &

  x11vnc -display "$DISPLAY_NUM" -rfbport "$VNC_PORT" -localhost -forever -shared -nopw -quiet -bg -o /dev/null

  # Foreground: the noVNC web client + WebSocket bridge, loopback only.
  exec websockify --web "$web" "127.0.0.1:${WEB_PORT}" "127.0.0.1:${VNC_PORT}"
}

case "$MODE" in
  install) install ;;
  run) run ;;
  *) echo "usage: $0 install|run" >&2; exit 2 ;;
esac
