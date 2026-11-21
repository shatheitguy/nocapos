#!/usr/bin/env bash
# Headless smoke test of NoCap Desktop's shell: labwc with a headless output
# and software rendering, NoCap Shell started in it as an ordinary account
# (never root), the NoCap OS pages loaded from the shell bundle. Passes when:
#   - every surface has loaded and talked to the host ("nocap-shell: all ready");
#   - an app keeping the screen awake over org.freedesktop.ScreenSaver holds
#     the idle lock (swayidle) back, and its inhibit goes when it releases
#     it or leaves the bus;
#   - the web process, killed, comes back (the surfaces reload);
#   - nothing crashed.
# Takes a screenshot of the result.
#
#   shell/test/smoke.sh BIN_DIR UI_DIR OUT_DIR     (as root, in debian:trixie)
set -euo pipefail
BIN="$(cd "${1:?bin dir}" && pwd)"
UI="$(cd "${2:?ui dir}" && pwd)"
OUT="${3:?out dir}"
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"

export DEBIAN_FRONTEND=noninteractive
need="labwc grim swayidle python3-gi gir1.2-glib-2.0 dbus dbus-user-session libgtk-3-0t64 libgtk-layer-shell0 \
  libwebkit2gtk-4.1-0 libjson-glib-1.0-0 libpolkit-agent-1-0 libpam0g libxkbcommon0 fonts-dejavu-core \
  adwaita-icon-theme procps"
missing=""
for p in $need; do dpkg -s "$p" >/dev/null 2>&1 || missing="$missing $p"; done
if [ -n "$missing" ]; then
  apt-get update -q >/dev/null
  # shellcheck disable=SC2086 # a list of packages
  apt-get install -y -q --no-install-recommends $missing >/dev/null
fi

id smoke >/dev/null 2>&1 || useradd -m -s /bin/bash smoke
uid="$(id -u smoke)"
rt="/run/user/$uid"
mkdir -p "$rt" && chown smoke: "$rt" && chmod 700 "$rt"
install -d -o smoke "$OUT/run"
cp "$BIN/nocap-shell" "$BIN/nocap-lock" "$OUT/run/" && chmod 755 "$OUT/run/"*

# An app that keeps the screen awake: Inhibit, hold, then UnInhibit (or
# just leave the bus with "vanish").
cat >"$OUT/run/inhibit.py" <<'PY'
import sys, time
from gi.repository import Gio, GLib
bus = Gio.bus_get_sync(Gio.BusType.SESSION, None)
def call(method, args, reply):
    return bus.call_sync("org.freedesktop.ScreenSaver", "/org/freedesktop/ScreenSaver", "org.freedesktop.ScreenSaver",
                         method, args, GLib.VariantType(reply) if reply else None, Gio.DBusCallFlags.NONE, 5000, None)
cookie = call("Inhibit", GLib.Variant("(ss)", ("smoke", "playing a video")), "(u)").unpack()[0]
print("cookie", cookie, flush=True)
print("active", call("GetActive", None, "(b)").unpack()[0], flush=True)
time.sleep(float(sys.argv[1]))
if len(sys.argv) < 3 or sys.argv[2] != "vanish":
    call("UnInhibit", GLib.Variant("(u)", (cookie,)), None)
    print("released", flush=True)
PY

cat >"$OUT/run/inside.sh" <<INSIDE
#!/bin/sh
LOG="$OUT/shell.log"
NOCAP_SHELL_DEBUG=1 NOCAP_SHELL_TEST=1 NOCAP_SHELL_DIR="$UI" NOCAP_SHELL_SOCKET=/nonexistent/shell.sock \
  "$OUT/run/nocap-shell" >"\$LOG" 2>&1 &
pid=\$!
for i in \$(seq 1 90); do
  grep -q "all ready" "\$LOG" 2>/dev/null && break
  kill -0 \$pid 2>/dev/null || break
  sleep 1
done
sleep 3
grim "$OUT/screen.png" 2>>"\$LOG" || true

# Idle lock vs. keeping the screen awake: the idle "lock" here only leaves a mark.
IDLE="$OUT/idle-fired"
rm -f "\$IDLE"
python3 "$OUT/run/inhibit.py" 9 >"$OUT/inhibit.log" 2>&1 &
ipid=\$!
# The idle watch starts once the screen is kept awake.
for i in \$(seq 1 30); do
  grep -q "wayland idle inhibitor on" "\$LOG" 2>/dev/null && break
  sleep 0.2
done
swayidle -w timeout 3 "touch \$IDLE" >"$OUT/swayidle.log" 2>&1 &
spid=\$!
sleep 6
if [ -e "\$IDLE" ]; then echo "smoke: idle fired while inhibited" >>"$OUT/result.log"; else echo "smoke: idle held back" >>"$OUT/result.log"; fi
wait \$ipid
sleep 6
if [ -e "\$IDLE" ]; then echo "smoke: idle fired after release" >>"$OUT/result.log"; else echo "smoke: idle never fired after release" >>"$OUT/result.log"; fi
kill \$spid 2>/dev/null
# A client that leaves the bus without releasing: its inhibit goes too.
python3 "$OUT/run/inhibit.py" 1 vanish >>"$OUT/inhibit.log" 2>&1
sleep 2

# The web process dies: the surfaces come back.
pkill -KILL -u "\$(id -u)" -f WebKitWebProcess && echo "smoke: killed the web process" >>"$OUT/result.log"
for i in \$(seq 1 40); do
  grep -q "reloaded desktop" "\$LOG" 2>/dev/null && break
  sleep 1
done
sleep 2
kill -0 \$pid 2>/dev/null && echo "nocap-shell: still running" >>"\$LOG"
kill \$pid 2>/dev/null
labwc --exit
INSIDE
chmod 755 "$OUT/run/inside.sh"
chown -R smoke "$OUT"

runuser -u smoke -- env -i HOME="/home/smoke" PATH=/usr/bin:/bin XDG_RUNTIME_DIR="$rt" \
  WLR_BACKENDS=headless WLR_RENDERER=pixman WLR_LIBINPUT_NO_DEVICES=1 WLR_HEADLESS_OUTPUTS=1 \
  WEBKIT_DISABLE_DMABUF_RENDERER=1 LIBGL_ALWAYS_SOFTWARE=1 \
  timeout 240 dbus-run-session -- labwc -C /etc/xdg/labwc -s "$OUT/run/inside.sh" >"$OUT/labwc.log" 2>&1 || true

echo "---- nocap-shell"
cat "$OUT/shell.log" || true
echo "---- checks"
cat "$OUT/result.log" "$OUT/inhibit.log" 2>/dev/null || true
fail=0
grep -q "nocap-shell: all ready" "$OUT/shell.log" || { echo "smoke: the shell's surfaces didn't all load"; tail -30 "$OUT/labwc.log"; exit 1; }
grep -q "still running" "$OUT/shell.log" || { echo "smoke: nocap-shell ended on its own"; fail=1; }
grep -q "owns org.freedesktop.ScreenSaver" "$OUT/shell.log" || { echo "smoke: the shell doesn't answer org.freedesktop.ScreenSaver"; fail=1; }
grep -q "^cookie " "$OUT/inhibit.log" || { echo "smoke: Inhibit got no cookie"; fail=1; }
grep -q "wayland idle inhibitor on" "$OUT/shell.log" || { echo "smoke: no Wayland idle inhibitor while inhibited"; fail=1; }
grep -q "idle held back" "$OUT/result.log" || { echo "smoke: the idle lock fired while an app kept the screen awake"; fail=1; }
grep -q "idle fired after release" "$OUT/result.log" || { echo "smoke: the idle lock never fired after the release"; fail=1; }
[ "$(grep -c "idle inhibits 0" "$OUT/shell.log")" -ge 2 ] || { echo "smoke: an inhibit wasn't released (UnInhibit, or the client leaving)"; fail=1; }
grep -q "reloaded desktop" "$OUT/shell.log" || { echo "smoke: the desktop didn't come back after the web process died"; fail=1; }
if grep -qiE "segfault|core dumped|refused call" "$OUT/shell.log"; then echo "smoke: errors above"; fail=1; fi
[ "$fail" -eq 0 ] || exit 1
echo "smoke: ok"
