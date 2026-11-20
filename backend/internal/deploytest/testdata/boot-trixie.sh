#!/usr/bin/env bash
# The silent boot against Debian 13's real GRUB scripts and Plymouth, in a
# throwaway container (CI runs it; root inside a container only):
#
#   docker run --rm -v "$PWD:/src:ro" -v "$PWD/boot-out:/out" debian:trixie \
#     bash /src/backend/internal/deploytest/testdata/boot-trixie.sh
#
# GRUB: nocap-boot with Debian's own update-grub and every /etc/grub.d
# script (desktop-base installed, as on the owner's PC, so its wallpaper
# would show): a hidden menu, NoCap's background instead of the
# distribution's, GRUB's "Loading ..." lines invisible, the quiet kernel
# line once, grub-script-check happy; then Show boot details and back.
# Plymouth: the NoCapOS splash drawn in a virtual X screen (Plymouth's own
# test renderer), screenshots of the boot, the disk password prompt and a
# restart into /out.
set -euo pipefail
SRC="${SRC:-/src}"
OUT="${OUT:-/out}"
[ "$(id -u)" -eq 0 ] && { [ -f /.dockerenv ] || [ -f /run/.containerenv ]; } || {
  echo "run this in a throwaway container only" >&2
  exit 2
}
export DEBIAN_FRONTEND=noninteractive
mkdir -p "$OUT"
FAILS=0
fail() { echo "FAIL: $*"; FAILS=$((FAILS + 1)); }
ok() { echo "ok: $*"; }

apt-get update -q >/dev/null
apt-get install -y -q --no-install-recommends grub-common grub2-common grub-pc-bin desktop-base \
  plymouth plymouth-themes plymouth-label plymouth-x11 xvfb x11-apps imagemagick fonts-dejavu-core >/dev/null

# ---------------------------------------------------------------- GRUB
# A machine to build a menu for: a kernel, its initramfs, and grub-probe
# answering for a plain ext4 root (a container has no real disk).
KVER=6.12.0-0-amd64
mkdir -p /boot/grub /etc/default/grub.d /usr/local/lib/nocapos/desktop /var/lib/nocapos/desktop-backup
printf 'kernel' >/boot/vmlinuz-$KVER
printf 'initrd' >/boot/initrd.img-$KVER
cat >/usr/local/sbin/grub-probe <<'EOF'
#!/bin/sh
for a in "$@"; do case "$a" in --target=*) t="${a#--target=}" ;; esac; done
prev=""; for a in "$@"; do [ "$prev" = -t ] && t="$a"; prev="$a"; done
case "$t" in
  device) echo /dev/sda2 ;;
  fs) echo ext2 ;;
  fs_uuid) echo 0d3c1a3e-1111-2222-3333-444455556666 ;;
  partuuid) echo 7a7a7a7a-02 ;;
  partmap) echo gpt ;;
  drive) echo "(hd0,gpt2)" ;;
  disk) echo /dev/sda ;;
  compatibility_hint) echo hd0,gpt2 ;;
  *) echo "" ;;
esac
EOF
chmod 755 /usr/local/sbin/grub-probe
# The owner's PC: Debian's defaults (installed from the 0.13 ISO, updated
# to 0.14, whose drop-in didn't hide the menu yet).
cat >/etc/default/grub <<'EOF'
GRUB_DEFAULT=0
GRUB_TIMEOUT=5
GRUB_DISTRIBUTOR=`( . /etc/os-release && echo ${NAME} )`
GRUB_CMDLINE_LINUX_DEFAULT="quiet splash loglevel=3 logo.nologo"
GRUB_CMDLINE_LINUX=""
EOF
printf '# NoCapOS boot menu (written by install.sh, removed by uninstall.sh).\nGRUB_DISTRIBUTOR="NoCapOS"\nGRUB_THEME="/usr/share/grub/themes/nocapos/theme.txt"\nGRUB_GFXMODE=auto\n' >/etc/default/grub.d/nocapos.cfg
update-grub >/dev/null 2>&1
grep -q 'desktop-base\|desktop-grub' /boot/grub/grub.cfg && echo "before: the distribution wallpaper is in grub.cfg (as on the owner's PC)"

# What brand_boot installs, then nocap-boot.
D=/usr/share/grub/themes/nocapos
mkdir -p "$D"
cp "$SRC/deploy/desktop/grub-theme.txt" "$D/theme.txt"
cp "$SRC/deploy/desktop/art/grub-background.png" "$D/background.png"
cp "$SRC/deploy/desktop/art/grub-boot.png" "$D/boot.png"
for f in nocap-boot lib.sh grub-nocapos; do tr -d '\r' <"$SRC/deploy/desktop/$f" >"/usr/local/lib/nocapos/desktop/$f"; done
chmod 755 /usr/local/lib/nocapos/desktop/nocap-boot
B=/usr/local/lib/nocapos/desktop/nocap-boot

if NOCAP_ARCH=x86_64 "$B" apply >"$OUT/nocap-boot.log" 2>&1; then ok "nocap-boot apply"; else fail "nocap-boot apply"; cat "$OUT/nocap-boot.log"; fi
cp /boot/grub/grub.cfg "$OUT/grub-silent.cfg"
C=/boot/grub/grub.cfg
grub-script-check "$C" && ok "grub-script-check" || fail "grub-script-check rejects grub.cfg"
grep -q 'set timeout_style=hidden' "$C" && grep -q 'set timeout=0' "$C" && ok "menu hidden" || fail "menu not hidden"
grep -q "set theme=.*/grub/themes/nocapos/theme.txt" "$C" && ok "NoCap theme" || fail "no NoCap theme"
grep -q 'background_image.*/grub/themes/nocapos/boot.png' "$C" && ok "NoCap background" || fail "no NoCap background"
if grep -q 'desktop-base\|desktop-grub\|debian-blueish' "$C"; then fail "the distribution wallpaper is still in grub.cfg"; else ok "no distribution wallpaper"; fi
awk '/### NoCapOS: a silent boot ###/ {n = NR} /### BEGIN \/etc\/grub.d\/10_linux ###/ {l = NR} END {exit !(n && l && n < l)}' "$C" &&
  grep -q 'set color_normal=black/black' "$C" && grep -q 'keystatus --shift' "$C" &&
  ok "GRUB's own lines invisible, Shift shows the menu (before the entries)" || fail "06_nocapos isn't in grub.cfg before 10_linux"
grep -q 'set gfxpayload=keep' "$C" && ok "gfxpayload=keep" || fail "no gfxpayload=keep"
line="$(grep -m1 "^[[:space:]]*linux[[:space:]]" "$C")"
for t in quiet splash loglevel=3 rd.systemd.show_status=false systemd.show_status=false rd.udev.log_level=3 vt.global_cursor_default=0; do
  n="$(printf '%s\n' "$line" | tr ' \t' '\n\n' | grep -cx -- "$t" || true)"
  [ "$n" -eq 1 ] || fail "kernel line has $t $n times: $line"
done
ok "kernel line: ${line#*ro }"
grep -q "menuentry 'NoCapOS GNU/Linux'" "$C" && ok "entry named NoCapOS" || fail "entry not named NoCapOS"

if "$B" details on >>"$OUT/nocap-boot.log" 2>&1; then ok "details on"; else fail "details on"; fi
cp "$C" "$OUT/grub-details.cfg"
grep -q 'set timeout_style=menu' "$C" && ! grep -q '### NoCapOS: a silent boot ###' "$C" && ok "details: menu shows" || fail "details: menu not shown"
grep -m1 "^[[:space:]]*linux[[:space:]]" "$C" | grep -qw -e quiet -e splash && fail "details: quiet tokens still there" || ok "details: boot messages shown"
"$B" details off >>"$OUT/nocap-boot.log" 2>&1 && grep -q 'set timeout_style=hidden' "$C" && ok "details off: silent again" || fail "details off"

# A broken 06_nocapos (say, a bad edit) must not break the boot menu.
cp "$C" /tmp/before.cfg
printf '#!/bin/sh\necho "if [ x = x ; then"\n' >/usr/local/lib/nocapos/desktop/grub-nocapos
if "$B" apply >>"$OUT/nocap-boot.log" 2>&1; then fail "a broken grub.cfg was accepted"; else ok "broken GRUB script refused"; fi
cmp -s "$C" /tmp/before.cfg && grub-script-check "$C" && ok "grub.cfg kept as it was" || fail "grub.cfg changed after a failed update"
grep -q 'if \[ x = x ; then' /etc/grub.d/06_nocapos && fail "the broken script was left in /etc/grub.d" || ok "previous 06_nocapos put back"

# ---------------------------------------------------------------- Plymouth
T=/usr/share/plymouth/themes/nocapos
mkdir -p "$T"
cp -R "$SRC/deploy/plymouth/." "$T/"
plymouth-set-default-theme nocapos
Xvfb :99 -screen 0 1920x1080x24 >/dev/null 2>&1 &
export DISPLAY=:99
sleep 2
shot() { xwd -root -silent | convert xwd:- "$OUT/$1.png"; }
splash() { # MODE: plymouthd with the X11 test renderer (it stands in for the screen)
  mkdir -p /run/plymouth
  plymouthd --mode="$1" --debug --debug-file="$OUT/plymouth-$1.log" \
    --kernel-command-line="quiet splash plymouth.ignore-serial-consoles plymouth.ignore-udev"
  sleep 1
  plymouth show-splash
  sleep 2
}
splash boot
shot splash-boot
sleep 0.6
shot splash-boot-2
plymouth ask-for-password --prompt "Please unlock disk nvme0n1p3_crypt" >/dev/null 2>&1 &
sleep 1.5
shot splash-password
plymouth quit
sleep 1
splash reboot
shot splash-restart
plymouth quit --retain-splash
sleep 1
for m in boot reboot; do
  if grep -qi 'script.*error\|parse error\|failed to parse' "$OUT/plymouth-$m.log"; then fail "Plymouth ($m) reports a script error"; grep -i 'error' "$OUT/plymouth-$m.log" | head; fi
  grep -q 'executing script file' "$OUT/plymouth-$m.log" && ok "Plymouth ($m) ran the NoCapOS script" || fail "Plymouth ($m) didn't run the script"
done
# The logo is there: red near the middle, near-black at the edges.
red="$(convert "$OUT/splash-boot.png" -crop 400x300+760+300 -format '%[fx:maxima.r]' info:)"
edge="$(convert "$OUT/splash-boot.png" -crop 200x200+0+0 -format '%[fx:maxima.r]' info:)"
awk -v r="$red" -v e="$edge" 'BEGIN { exit !(r > 0.6 && e < 0.05) }' && ok "splash shows the logo (red $red, edge $edge)" || fail "splash looks wrong (red $red, edge $edge)"
cmp -s "$OUT/splash-boot.png" "$OUT/splash-boot-2.png" && fail "splash doesn't animate" || ok "splash animates"

echo
if [ "$FAILS" -gt 0 ]; then echo "$FAILS check(s) failed"; exit 1; fi
echo "all boot checks passed"
