#!/usr/bin/env bash
# installer-test.sh: the USB stick's setup, end to end, in QEMU (UEFI).
#   1. the stick (made from the test image) boots into the full-screen
#      setup; screenshots of the first steps go to build/screenshots/;
#   2. the setup lists three drives: the stick is hidden, the 8 GB one is
#      too small;
#   3. installing onto the 40 GB drive (with "Lock my drive") finishes;
#   4. the installed drive boots on its own: NoCapOS answers, the account
#      made in the setup signs in, the usage mode is applied, and "Desktop
#      only" really keeps other devices out.
# Needs what qemu-test.sh needs, plus mtools and python3.
set -euo pipefail

OS="$(cd "$(dirname "$0")/.." && pwd)"
BUILD="$OS/build"
WORK="${WORK:-$BUILD/qemu-installer}"
SHOTS="$BUILD/screenshots"
KEY="$OS/test/key"
SSH_PORT=2223 HTTPS_PORT=8444
log() { printf '\n==> %s\n' "$*"; }
fail() { printf '\nFAIL: %s\n' "$*" >&2; tail -n 80 "$WORK/serial.log" >&2 || true; exit 1; }

rm -rf "$WORK"; mkdir -p "$WORK" "$SHOTS"
bash "$OS/scripts/make-usb.sh" "$BUILD/pc-test.img" "$WORK/usb.img"
qemu-img create -q -f qcow2 "$WORK/target.qcow2" 40G
qemu-img create -q -f qcow2 "$WORK/other.qcow2" 64G
qemu-img create -q -f qcow2 "$WORK/small.qcow2" 8G

code="" vars=""
for c in /usr/share/OVMF/OVMF_CODE_4M.fd /usr/share/OVMF/OVMF_CODE.fd; do [ -f "$c" ] && { code="$c"; break; }; done
for v in /usr/share/OVMF/OVMF_VARS_4M.fd /usr/share/OVMF/OVMF_VARS.fd; do [ -f "$v" ] && { vars="$v"; break; }; done
cp "$vars" "$WORK/vars-usb.fd"; cp "$vars" "$WORK/vars-installed.fd"
accel=()
[ -w /dev/kvm ] && accel=(-accel kvm -cpu host)

QEMU_PID=""
qemu() {
  local vars="$1"; shift
  qemu-system-x86_64 "${accel[@]}" -machine q35 -m 4096 -smp 2 \
    -drive "if=pflash,format=raw,readonly=on,file=$code" -drive "if=pflash,format=raw,file=$vars" \
    "$@" \
    -device virtio-net-pci,netdev=n0 \
    -netdev "user,id=n0,hostfwd=tcp:127.0.0.1:$SSH_PORT-:22,hostfwd=tcp:127.0.0.1:$HTTPS_PORT-:443" \
    -device virtio-rng-pci -vga virtio -display none \
    -qmp "unix:$WORK/qmp.sock,server,nowait" \
    -serial "file:$WORK/serial.log" -no-reboot -daemonize -pidfile "$WORK/qemu.pid"
  QEMU_PID="$(cat "$WORK/qemu.pid")"
}
stop_vm() { [ -n "$QEMU_PID" ] && kill "$QEMU_PID" 2>/dev/null || true; QEMU_PID=""; sleep 1; }
trap stop_vm EXIT
wait_exit() { local t=0; while kill -0 "$QEMU_PID" 2>/dev/null; do sleep 2; t=$((t + 2)); [ "$t" -lt "${1:-300}" ] || fail "the VM didn't stop"; done; }
vssh() { ssh -i "$KEY" -p "$SSH_PORT" -o BatchMode=yes -o ConnectTimeout=5 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR root@127.0.0.1 "$@"; }
wait_ssh() { local t=0; until vssh true 2>/dev/null; do sleep 3; t=$((t + 3)); [ "$t" -lt "${1:-600}" ] || fail "no SSH"; done; }

# QMP: screenshots and key presses.
qmp() {
  python3 - "$WORK/qmp.sock" "$@" <<'PY'
import json, socket, sys
s = socket.socket(socket.AF_UNIX); s.connect(sys.argv[1]); f = s.makefile('rw')
f.readline()
def cmd(c, **a):
    f.write(json.dumps({"execute": c, "arguments": a} if a else {"execute": c}) + "\n"); f.flush()
    while True:
        r = json.loads(f.readline())
        if "return" in r or "error" in r: return r
cmd("qmp_capabilities")
op = sys.argv[2]
if op == "shot":
    print(cmd("screendump", filename=sys.argv[3]))
elif op == "key":
    for k in sys.argv[3:]:
        cmd("send-key", keys=[{"type": "qcode", "data": k}])
PY
}
# PPM (QEMU's screendump) to PNG, with Python's standard library only.
png() {
  python3 - "$1" "$2" <<'PY'
import sys, zlib, struct
d = open(sys.argv[1], 'rb').read()
parts = d.split(b'\n', 3)
w, h = map(int, parts[1].split()); px = parts[3]
raw = b''.join(b'\x00' + px[y * w * 3:(y + 1) * w * 3] for y in range(h))
def chunk(t, b): return struct.pack('>I', len(b)) + t + b + struct.pack('>I', zlib.crc32(t + b) & 0xffffffff)
open(sys.argv[2], 'wb').write(b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB', w, h, 8, 2, 0, 0, 0)) + chunk(b'IDAT', zlib.compress(raw, 9)) + chunk(b'IEND', b''))
PY
}
shot() { qmp shot "$WORK/s.ppm" >/dev/null; png "$WORK/s.ppm" "$SHOTS/$1.png"; echo "screenshot: $1.png"; }

# ---- 1. the stick boots into the setup -----------------------------------------------
log "Booting the USB stick"
qemu "$WORK/vars-usb.fd" \
  -drive "file=$WORK/usb.img,format=raw,if=none,id=usb" -device virtio-blk-pci,drive=usb,bootindex=0 \
  -drive "file=$WORK/target.qcow2,format=qcow2,if=virtio" \
  -drive "file=$WORK/other.qcow2,format=qcow2,if=virtio" \
  -drive "file=$WORK/small.qcow2,format=qcow2,if=virtio"
wait_ssh 900
vssh 'systemctl is-active nocapos-installer.service nocapos-installer-screen.service' || {
  vssh 'journalctl -b -u nocapos-installer -u nocapos-installer-screen --no-pager | tail -40'; fail "the setup didn't start"; }
vssh '! systemctl is-active --quiet nocapos.service' || fail "NoCapOS itself runs on the stick"
t=0
until vssh 'curl -fsS http://127.0.0.1:8790/api/installer/info' >"$WORK/info.json" 2>/dev/null; do
  sleep 2; t=$((t + 2)); [ "$t" -lt 120 ] || fail "the setup's backend doesn't answer"
done
cat "$WORK/info.json"; echo

# ---- 2. the disk list -----------------------------------------------------------------
python3 - "$WORK/info.json" <<'PY' || fail "unexpected disk list"
import json, sys
d = {x["path"]: x for x in json.load(open(sys.argv[1]))["disks"]}
assert d["/dev/vda"]["boot"], "the stick must be the boot disk"
assert not d["/dev/vdb"]["too_small"] and not d["/dev/vdb"]["boot"], d["/dev/vdb"]
assert d["/dev/vdd"]["too_small"], "the 8 GB drive must be too small"
print("disk list OK")
PY

# Screens: wait for the kiosk to draw, then the first two steps.
sleep 25
shot 01-language
qmp key ret >/dev/null; sleep 2
shot 02-disks
qmp key tab tab spc >/dev/null; sleep 2
shot 03-disk-selected

# ---- 3. install -------------------------------------------------------------------------
log "Installing onto the 40 GB drive (locked with a passphrase)"
cat >"$WORK/req.json" <<'EOF'
{"disk":"/dev/vdb","encrypt":true,"use_tpm":false,"passphrase":"nocap-test-passphrase",
 "account":{"full_name":"Test Person","username":"tester","password":"Correct-Horse-9"},
 "hostname":"nocap-installed","timezone":"Asia/Dubai","keyboard":"us","language":"en","usage":"web"}
EOF
vssh 'curl -fsS -X POST -H "Content-Type: application/json" --data-binary @- http://127.0.0.1:8790/api/installer/install' <"$WORK/req.json" || fail "the install didn't start"
t=0
while :; do
  p="$(vssh 'curl -fsS http://127.0.0.1:8790/api/installer/progress')"
  echo "$p"
  echo "$p" | grep -q '"done":true' && break
  echo "$p" | grep -q '"error"' && fail "install failed: $p"
  sleep 10; t=$((t + 10)); [ "$t" -lt 1800 ] || fail "the install took too long"
done
shot 04-done
vssh 'systemctl poweroff' || true
wait_exit 120

# ---- 4. the installed drive on its own --------------------------------------------------
log "Booting the installed drive"
# The data partition is locked: type the passphrase at the prompt.
qemu "$WORK/vars-installed.fd" -drive "file=$WORK/target.qcow2,format=qcow2,if=virtio"
t=0
until grep -q 'unlock NoCapOS\|passphrase' "$WORK/serial.log" 2>/dev/null; do
  sleep 2; t=$((t + 2))
  if vssh true 2>/dev/null; then break; fi
  [ "$t" -lt 300 ] || break
done
for k in n o c a p minus t e s t minus p a s s p h r a s e ret; do qmp key "$k" >/dev/null; done
wait_ssh 900
vssh 'lsblk -o NAME,SIZE,TYPE,FSTYPE,MOUNTPOINTS; cat /etc/hostname; findmnt /run/rugix/mounts/data'
vssh 'findmnt -no SOURCE /run/rugix/mounts/data' | grep -q mapper || fail "the data partition isn't the locked one"
t=0
until curl -fsk --max-time 5 "https://127.0.0.1:$HTTPS_PORT/healthz" | grep -q '"status":"ok"'; do
  sleep 3; t=$((t + 3)); [ "$t" -lt 300 ] || fail "NoCapOS doesn't answer on the installed drive"
done
[ "$(vssh hostname)" = nocap-installed ] || fail "hostname from the setup wasn't applied"
vssh '[ ! -e /run/rugix/mounts/data/nocapos-seed.json ]' || fail "the seed (with the password) is still there"
tok="$(curl -fsk -X POST -H 'Content-Type: application/json' -d '{"username":"tester","password":"Correct-Horse-9"}' \
  "https://127.0.0.1:$HTTPS_PORT/api/v1/auth/login" | python3 -c 'import sys,json; print(json.load(sys.stdin)["access_token"])')" ||
  fail "the account from the setup can't sign in"
usage="$(curl -fsk -H "Authorization: Bearer $tok" "https://127.0.0.1:$HTTPS_PORT/api/v1/system/host-access" | python3 -c 'import sys,json; print(json.load(sys.stdin)["usage"])')"
[ "$usage" = web ] || fail "usage mode is $usage, not web"
echo "account signs in; usage: $usage"

log "\"Desktop only\" keeps other devices out"
vssh "curl -fsS -X PUT -H 'Content-Type: application/json' -H 'Authorization: Bearer $tok' -d '{\"mode\":\"desktop\"}' http://127.0.0.1:8780/api/v1/system/usage" >/dev/null ||
  fail "couldn't switch to Desktop only"
if curl -sk --max-time 5 "https://127.0.0.1:$HTTPS_PORT/healthz" | grep -q status; then fail "another device still reaches NoCapOS"; fi
vssh 'curl -fsS http://127.0.0.1:8780/healthz' | grep -q '"status":"ok"' || fail "NoCapOS stopped answering this machine"
echo "network connections are closed; this machine still reaches NoCapOS"

log "Installer checks passed"
