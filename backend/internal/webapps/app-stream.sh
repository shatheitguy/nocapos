#!/usr/bin/env bash
# NoCapOS — a native app (or a whole desktop) on its own virtual display,
# streamed to the web desktop the same way Brave is (Xvfb + x11vnc +
# noVNC/websockify, loopback only).
#
#   app-stream.sh app <app argv…>   one app, maximized, with a window manager
#   app-stream.sh desktop           a full desktop: panel, app menu, files, terminal
#   app-stream.sh screen            the server's own screen (the NoCap Desktop session)
#
# alfad writes this file to /run/nocapos and runs it in a transient systemd
# unit of its own, as the person's account (never root), with NOCAP_DISPLAY,
# NOCAP_VNC_PORT, NOCAP_WEB_PORT, HOME and XDG_RUNTIME_DIR set. A root step
# before it has made /tmp/.X11-unix (root, 1777) and the runtime folder.
# The app's argv comes as arguments (never through a shell string). When the
# app (or the desktop) exits, the whole stream ends.
set -euo pipefail

MODE="${1:-}"
[ "$#" -gt 0 ] && shift
VNC_PORT="${NOCAP_VNC_PORT:?}"
WEB_PORT="${NOCAP_WEB_PORT:?}"
GEOM="${NOCAP_GEOMETRY:-1600x900}"
REPAIR='open Packages and repair "Full Linux desktop and app streams"'

novnc_dir() {
  local d
  for d in /usr/share/novnc /usr/share/webapps/novnc /usr/share/noVNC; do
    if [ -f "$d/vnc.html" ]; then echo "$d"; return 0; fi
  done
  return 1
}

need() {
  local tool
  for tool in "$@"; do
    command -v "$tool" >/dev/null || { echo "$tool is missing: $REPAIR." >&2; exit 5; }
  done
}

# Whatever happens, take the display, VNC and bridge down with us.
cleanup() {
  kill $(jobs -p) 2>/dev/null || true
  if [ -n "${wmdir:-}" ]; then rm -rf "$wmdir"; fi
}
trap cleanup EXIT

bridge() { # the noVNC web page + WebSocket bridge, loopback only
  websockify --web "$web" "127.0.0.1:${WEB_PORT}" "127.0.0.1:${VNC_PORT}" &
}

# ---------------------------------------------------------------- screen mode
if [ "$MODE" = screen ]; then
  need websockify
  web="$(novnc_dir)" || { echo "noVNC is missing: $REPAIR." >&2; exit 5; }
  if [ -n "${WAYLAND_DISPLAY:-}" ] && command -v wayvnc >/dev/null; then
    wayvnc 127.0.0.1 "$VNC_PORT" &
  elif [ -n "${DISPLAY:-}" ] && command -v x11vnc >/dev/null; then
    x11vnc -display "$DISPLAY" -auth guess -rfbport "$VNC_PORT" -localhost -forever -shared -nopw -quiet -o /dev/null &
  else
    echo "Showing the server's screen needs wayvnc (Wayland) or x11vnc (X11): $REPAIR." >&2
    exit 5
  fi
  vnc=$!
  bridge
  wait "$vnc"
  exit 0
fi

# ------------------------------------------------- inner session (on the bus)
# Runs inside dbus-run-session, so the window manager, panel and app share
# one session bus (portals, file pickers, notifications).
if [ "$MODE" = --session ]; then
  kind="${1:?}"
  shift
  wmdir="$(mktemp -d)"
  # Openbox with the system's own config (menus, key and mouse bindings),
  # plus NoCapOS's window rules.
  rules=''
  if [ "$kind" = app ]; then
    # The main window fills the stream with no title bar of its own (the
    # NoCapOS window around it has one); dialogs, menus and pickers keep
    # their frames so they can be moved and closed.
    rules='<application class="*" type="normal"><decor>no</decor><maximized>yes</maximized></application><application class="*" type="dialog"><decor>yes</decor><maximized>no</maximized><position force="yes"><x>center</x><y>center</y></position></application>'
  fi
  if command -v openbox >/dev/null; then
    base=''
    for f in "$HOME/.config/openbox/rc.xml" /etc/xdg/openbox/rc.xml /usr/share/openbox/rc.xml; do
      if [ -f "$f" ]; then base="$f"; break; fi
    done
    if [ -n "$base" ]; then
      # Applications rules go first, so they win over the user's own.
      sed "s#<applications>#<applications>${rules}#" "$base" >"$wmdir/rc.xml"
      grep -q '<applications>' "$wmdir/rc.xml" || sed -i "s#</openbox_config>#<applications>${rules}</applications></openbox_config>#" "$wmdir/rc.xml"
    else
      printf '<?xml version="1.0"?>\n<openbox_config xmlns="http://openbox.org/3.4/rc"><applications>%s</applications></openbox_config>\n' "$rules" >"$wmdir/rc.xml"
    fi
    openbox --config-file "$wmdir/rc.xml" >/dev/null 2>&1 &
    wm=$!
  elif command -v fluxbox >/dev/null; then
    if [ "$kind" = app ]; then
      printf '[app] (name=.*)\n  [Deco]      {NONE}\n  [Maximized] {yes}\n[end]\n' >"$wmdir/apps"
    else
      : >"$wmdir/apps"
    fi
    {
      echo "session.appsFile: $wmdir/apps"
      echo "session.screen0.workspaces: 1"
      if [ "$kind" = app ]; then echo "session.screen0.toolbar.visible: false"; fi
    } >"$wmdir/init"
    fluxbox -rc "$wmdir/init" >/dev/null 2>&1 &
    wm=$!
  else
    wm=''
    [ "$kind" = desktop ] && { echo "No window manager (openbox) is installed: $REPAIR." >&2; exit 5; }
  fi
  sleep 0.3

  if [ "$kind" = desktop ]; then
    cd "$HOME"
    # The panel's app menu reads the lxde-applications menu when it's there.
    if [ -f /etc/xdg/menus/lxde-applications.menu ]; then export XDG_MENU_PREFIX=lxde-; fi
    # Desktop icons and wallpaper, the file manager behind them.
    if command -v pcmanfm >/dev/null; then pcmanfm --desktop >/dev/null 2>&1 & fi
    # The panel: app menu (installed Linux apps included), windows, clock.
    if command -v lxpanel >/dev/null; then lxpanel >/dev/null 2>&1 &
    elif command -v tint2 >/dev/null; then tint2 >/dev/null 2>&1 &
    fi
    # The desktop lasts as long as its window manager.
    if [ -n "$wm" ]; then wait "$wm"; fi
    exit 0
  fi

  # One app: the stream ends when it does.
  cd "$HOME"
  "$@" || true
  exit 0
fi

# ---------------------------------------------------------- app and desktop
case "$MODE" in
  app) [ "$#" -gt 0 ] || { echo "usage: $0 app <app argv…>" >&2; exit 2; } ;;
  desktop) ;;
  *) echo "usage: $0 app <argv…> | desktop | screen" >&2; exit 2 ;;
esac
DISPLAY_NUM="${NOCAP_DISPLAY:?}"
need Xvfb x11vnc websockify
web="$(novnc_dir)" || { echo "noVNC is missing: $REPAIR." >&2; exit 5; }

n="${DISPLAY_NUM#:}"
if [ ! -d /tmp/.X11-unix ] || [ "$(stat -c %u /tmp/.X11-unix)" != 0 ]; then
  echo "/tmp/.X11-unix isn't ready (it must belong to root, mode 1777)." >&2
  exit 6
fi
Xvfb "$DISPLAY_NUM" -screen 0 "${GEOM}x24" -nolisten tcp >/dev/null 2>&1 &
for _ in $(seq 1 50); do [ -S "/tmp/.X11-unix/X${n}" ] && break; sleep 0.1; done
[ -S "/tmp/.X11-unix/X${n}" ] || { echo "The virtual display $DISPLAY_NUM didn't start." >&2; exit 6; }
export DISPLAY="$DISPLAY_NUM"
unset WAYLAND_DISPLAY
export XDG_SESSION_TYPE=x11
# GTK apps: no client-side title bars where the toolkit allows it.
if [ "$MODE" = app ]; then export GTK_CSD=0; fi

# x11vnc keeps the app's clipboard and the browser's (noVNC) in sync.
x11vnc -display "$DISPLAY_NUM" -rfbport "$VNC_PORT" -localhost -forever -shared -nopw -quiet -o /dev/null &
bridge

# The session gets its own D-Bus session bus when one can be made.
self="${BASH_SOURCE[0]}"
if command -v dbus-run-session >/dev/null; then
  dbus-run-session -- bash "$self" --session "$MODE" "$@" || true
elif command -v dbus-launch >/dev/null; then
  dbus-launch --exit-with-session bash "$self" --session "$MODE" "$@" || true
else
  bash "$self" --session "$MODE" "$@" || true
fi
