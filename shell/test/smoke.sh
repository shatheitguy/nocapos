#!/usr/bin/env bash
# Headless smoke test of NoCap Desktop's shell: labwc with a headless output
# and software rendering, NoCap Shell started in it as an ordinary account
# (never root), the NoCap OS pages loaded from the shell bundle. Passes when
# every surface has loaded and talked to the host ("nocap-shell: all ready")
# and nothing crashed. Takes a screenshot of the result.
#
#   shell/test/smoke.sh BIN_DIR UI_DIR OUT_DIR     (as root, in debian:trixie)
set -euo pipefail
BIN="$(cd "${1:?bin dir}" && pwd)"
UI="$(cd "${2:?ui dir}" && pwd)"
OUT="${3:?out dir}"
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"

export DEBIAN_FRONTEND=noninteractive
if ! command -v labwc >/dev/null 2>&1; then
  apt-get update -q >/dev/null
  apt-get install -y -q --no-install-recommends labwc grim dbus dbus-user-session libgtk-3-0t64 libgtk-layer-shell0 \
    libwebkit2gtk-4.1-0 libjson-glib-1.0-0 libpolkit-agent-1-0 libpam0g libxkbcommon0 fonts-dejavu-core \
    adwaita-icon-theme procps >/dev/null
fi
command -v grim >/dev/null 2>&1 || { apt-get update -q >/dev/null; apt-get install -y -q --no-install-recommends grim >/dev/null; }

id smoke >/dev/null 2>&1 || useradd -m -s /bin/bash smoke
uid="$(id -u smoke)"
rt="/run/user/$uid"
mkdir -p "$rt" && chown smoke: "$rt" && chmod 700 "$rt"
install -d -o smoke "$OUT/run"
cp "$BIN/nocap-shell" "$BIN/nocap-lock" "$OUT/run/" && chmod 755 "$OUT/run/"*

cat >"$OUT/run/inside.sh" <<INSIDE
#!/bin/sh
NOCAP_SHELL_DEBUG=1 NOCAP_SHELL_TEST=1 NOCAP_SHELL_DIR="$UI" NOCAP_SHELL_SOCKET=/nonexistent/shell.sock \
  "$OUT/run/nocap-shell" >"$OUT/shell.log" 2>&1 &
pid=\$!
for i in \$(seq 1 90); do
  grep -q "all ready" "$OUT/shell.log" 2>/dev/null && break
  kill -0 \$pid 2>/dev/null || break
  sleep 1
done
sleep 3
grim "$OUT/screen.png" 2>>"$OUT/shell.log" || true
kill -0 \$pid 2>/dev/null && echo "nocap-shell: still running" >>"$OUT/shell.log"
kill \$pid 2>/dev/null
labwc --exit
INSIDE
chmod 755 "$OUT/run/inside.sh"
chown -R smoke "$OUT"

runuser -u smoke -- env -i HOME="/home/smoke" PATH=/usr/bin:/bin XDG_RUNTIME_DIR="$rt" \
  WLR_BACKENDS=headless WLR_RENDERER=pixman WLR_LIBINPUT_NO_DEVICES=1 WLR_HEADLESS_OUTPUTS=1 \
  WEBKIT_DISABLE_DMABUF_RENDERER=1 LIBGL_ALWAYS_SOFTWARE=1 \
  timeout 150 dbus-run-session -- labwc -C /etc/xdg/labwc -s "$OUT/run/inside.sh" >"$OUT/labwc.log" 2>&1 || true

echo "---- nocap-shell"
cat "$OUT/shell.log" || true
grep -q "nocap-shell: all ready" "$OUT/shell.log" || { echo "smoke: the shell's surfaces didn't all load"; tail -30 "$OUT/labwc.log"; exit 1; }
grep -q "still running" "$OUT/shell.log" || { echo "smoke: nocap-shell ended on its own"; exit 1; }
if grep -qiE "segfault|core dumped|refused call" "$OUT/shell.log"; then echo "smoke: errors above"; exit 1; fi
echo "smoke: ok"
