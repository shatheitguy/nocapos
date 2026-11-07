#!/usr/bin/env bash
# NoCapOS — one native app on its own virtual display, streamed to the web
# desktop the same way Brave is (Xvfb + x11vnc + noVNC/websockify, loopback).
#
#   app-stream.sh <app argv…>
#
# alfad runs this with NOCAP_DISPLAY, NOCAP_VNC_PORT, NOCAP_WEB_PORT and
# NOCAP_GEOMETRY set; the app's argv comes as arguments (never through a
# shell string). When the app exits the whole stream ends.
set -euo pipefail

DISPLAY_NUM="${NOCAP_DISPLAY:?}"
VNC_PORT="${NOCAP_VNC_PORT:?}"
WEB_PORT="${NOCAP_WEB_PORT:?}"
GEOM="${NOCAP_GEOMETRY:-1600x900}"
[ "$#" -gt 0 ] || { echo "usage: $0 <app argv…>" >&2; exit 2; }

novnc_dir() {
  for d in /usr/share/novnc /usr/share/webapps/novnc /usr/share/noVNC; do
    if [ -f "$d/vnc.html" ]; then echo "$d"; return 0; fi
  done
  return 1
}

for tool in Xvfb x11vnc websockify; do
  command -v "$tool" >/dev/null || { echo "$tool is missing: open Packages and repair \"Brave browser and its stream tools\"." >&2; exit 5; }
done
web="$(novnc_dir)" || { echo "noVNC is missing: open Packages and repair \"Brave browser and its stream tools\"." >&2; exit 5; }

n="${DISPLAY_NUM#:}"
# Clear a stale lock left by a crashed display.
if [ -f "/tmp/.X${n}-lock" ] && ! pgrep -f "Xvfb ${DISPLAY_NUM}" >/dev/null; then
  rm -f "/tmp/.X${n}-lock" "/tmp/.X11-unix/X${n}" 2>/dev/null || true
fi

# Whatever happens, take the display, VNC and bridge down with us.
trap 'kill $(jobs -p) 2>/dev/null || true; [ -z "${fbdir:-}" ] || rm -rf "$fbdir"' EXIT

Xvfb "$DISPLAY_NUM" -screen 0 "${GEOM}x24" -nolisten tcp &
for _ in $(seq 1 50); do [ -S "/tmp/.X11-unix/X${n}" ] && break; sleep 0.1; done
export DISPLAY="$DISPLAY_NUM"
unset WAYLAND_DISPLAY

# The window manager runs every window maximized with no title bar or
# borders of its own (and no toolbar): the NoCapOS window around the stream
# already has the title bar and the close / minimize / maximize buttons.
if command -v fluxbox >/dev/null; then
  fbdir="$(mktemp -d)"
  cat >"$fbdir/apps" <<'APPS'
[app] (name=.*)
  [Deco]      {NONE}
  [Maximized] {yes}
[end]
APPS
  cat >"$fbdir/init" <<INIT
session.appsFile: $fbdir/apps
session.screen0.toolbar.visible: false
session.screen0.slit.autoHide: true
session.screen0.defaultDeco: NONE
session.screen0.workspaces: 1
INIT
  : >"$fbdir/keys"
  printf 'session.keyFile: %s/keys\n' "$fbdir" >>"$fbdir/init"
  fluxbox -rc "$fbdir/init" >/dev/null 2>&1 &
fi
# GTK apps: no client-side title bars where the toolkit allows it.
export GTK_CSD=0

x11vnc -display "$DISPLAY_NUM" -rfbport "$VNC_PORT" -localhost -forever -shared -nopw -quiet -o /dev/null &
websockify --web "$web" "127.0.0.1:${WEB_PORT}" "127.0.0.1:${VNC_PORT}" &

# The app, with its own session bus when one can be made.
if command -v dbus-run-session >/dev/null; then
  dbus-run-session -- "$@" || true
else
  "$@" || true
fi
