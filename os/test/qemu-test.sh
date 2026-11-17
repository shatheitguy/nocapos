#!/usr/bin/env bash
# qemu-test.sh: boots the PC test image in QEMU (UEFI, headless) and checks
#   1. first boot: Rugix makes system B and the data partition;
#   2. NoCapOS answers on :443 (HTTPS) and :80 (redirect to HTTPS);
#   3. settings survive a restart (etc-sync) and so does data;
#   4. an A -> B update installs, boots once on trial, passes the health
#      check and becomes the default;
#   5. an update whose NoCapOS never starts is rolled back by itself, and
#      the result says so.
# Needs: qemu-system-x86_64, OVMF, python3, ssh, curl. Expects in os/build:
#   pc-test.img  pc-test-next.rugixb  pc-test-broken.rugixb  and the hashes
#   pc-test-next.hash  pc-test-broken.hash (rugix-bundler hash)
# and the key test/key (made by scripts/build.sh test-key).
set -euo pipefail

OS="$(cd "$(dirname "$0")/.." && pwd)"
BUILD="$OS/build"
WORK="${WORK:-$BUILD/qemu}"
KEY="$OS/test/key"
SSH_PORT=2222 HTTPS_PORT=8443 HTTP_PORT=8080 BUNDLE_PORT=8000
DISK_SIZE="${DISK_SIZE:-24G}"
log() { printf '\n==> %s\n' "$*"; }
fail() { printf '\nFAIL: %s\n' "$*" >&2; tail -n 80 "$WORK/serial.log" >&2 || true; exit 1; }

rm -rf "$WORK"; mkdir -p "$WORK"
cp "$BUILD/pc-test.img" "$WORK/disk.img"
truncate -s "$DISK_SIZE" "$WORK/disk.img"

code="" vars=""
for c in /usr/share/OVMF/OVMF_CODE_4M.fd /usr/share/OVMF/OVMF_CODE.fd; do [ -f "$c" ] && { code="$c"; break; }; done
for v in /usr/share/OVMF/OVMF_VARS_4M.fd /usr/share/OVMF/OVMF_VARS.fd; do [ -f "$v" ] && { vars="$v"; break; }; done
[ -n "$code" ] && [ -n "$vars" ] || fail "OVMF firmware not found (apt install ovmf)"
cp "$vars" "$WORK/vars.fd"

accel=()
[ -w /dev/kvm ] && accel=(-accel kvm -cpu host)

QEMU_PID=""
start_vm() {
  qemu-system-x86_64 "${accel[@]}" -machine q35 -m 3072 -smp 2 \
    -drive "if=pflash,format=raw,readonly=on,file=$code" \
    -drive "if=pflash,format=raw,file=$WORK/vars.fd" \
    -drive "file=$WORK/disk.img,format=raw,if=virtio" \
    -device virtio-net-pci,netdev=n0 \
    -netdev "user,id=n0,hostfwd=tcp:127.0.0.1:$SSH_PORT-:22,hostfwd=tcp:127.0.0.1:$HTTPS_PORT-:443,hostfwd=tcp:127.0.0.1:$HTTP_PORT-:80" \
    -device virtio-rng-pci -device i6300esb \
    -display none -serial "file:$WORK/serial.log" -monitor none \
    -no-reboot -daemonize -pidfile "$WORK/qemu.pid"
  QEMU_PID="$(cat "$WORK/qemu.pid")"
}

# -no-reboot: a reboot inside the VM ends QEMU, and the test starts it again
# (the disk and the firmware variables carry over, like a real restart).
wait_vm_exit() {
  local t=0
  while kill -0 "$QEMU_PID" 2>/dev/null; do
    sleep 2; t=$((t + 2))
    [ "$t" -lt "${1:-300}" ] || fail "the VM didn't restart within ${1:-300}s"
  done
}
stop_vm() { [ -n "$QEMU_PID" ] && kill "$QEMU_PID" 2>/dev/null || true; QEMU_PID=""; }
trap 'stop_vm; [ -n "${HTTP_PID:-}" ] && kill "$HTTP_PID" 2>/dev/null || true' EXIT

vssh() {
  ssh -i "$KEY" -p "$SSH_PORT" -o BatchMode=yes -o ConnectTimeout=5 \
    -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR root@127.0.0.1 "$@"
}
wait_ssh() {
  local t=0
  until vssh true 2>/dev/null; do
    sleep 3; t=$((t + 3))
    kill -0 "$QEMU_PID" 2>/dev/null || return 1
    [ "$t" -lt "${1:-600}" ] || fail "no SSH after ${1:-600}s"
  done
}
reboot_vm() { vssh 'systemctl reboot' || true; wait_vm_exit 180; start_vm; }
group() { vssh "rugix-ctrl system info --json" | tr -d ' \n' | sed -n "s/.*\"$1\":\"\([^\"]*\)\".*/\1/p"; }
marker() { vssh 'cat /usr/lib/nocapos/test-marker'; }
wait_boot_check() {
  # nocapos-boot-check.service is part of startup: wait until startup is done.
  vssh 'timeout 600 systemctl is-system-running --wait' >/dev/null 2>&1 || true
}

python3 -m http.server "$BUNDLE_PORT" --bind 127.0.0.1 --directory "$BUILD" >"$WORK/http.log" 2>&1 &
HTTP_PID=$!

# ---- 1. first boot -----------------------------------------------------------
log "First boot (Rugix makes system B and the data partition)"
start_vm
wait_ssh 900 || {
  # The first boot may restart once after partitioning.
  wait_vm_exit 60; start_vm; wait_ssh 600 || fail "no SSH after the first boot"
}
vssh 'cat /etc/os-release; lsblk -o NAME,SIZE,FSTYPE,LABEL,MOUNTPOINTS; rugix-ctrl system info'
[ "$(group activeGroup)" = a ] || fail "not booted from system A"
vssh 'findmnt /var/lib/nocapos' >/dev/null || fail "/var/lib/nocapos isn't on the data partition"
vssh 'grep -q "^ID=nocapos" /etc/os-release' || fail "os-release doesn't say NoCapOS"
vssh '[ -f /usr/lib/nocapos/image-os ]' || fail "image marker missing"
vssh '! command -v apt-get >/dev/null || [ ! -s /etc/apt/sources.list ]' || fail "apt has sources on the device"
vssh 'modinfo zfs >/dev/null' || fail "ZFS module missing"

# ---- 2. NoCapOS answers ------------------------------------------------------
log "NoCapOS answers on :443 and :80"
t=0
until curl -fsk --max-time 5 "https://127.0.0.1:$HTTPS_PORT/healthz" | grep -q '"status":"ok"'; do
  sleep 3; t=$((t + 3)); [ "$t" -lt 300 ] || fail "NoCapOS doesn't answer on :443"
done
curl -fsk "https://127.0.0.1:$HTTPS_PORT/" | grep -qi '<html' || fail "no web UI on :443"
code80="$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$HTTP_PORT/")"
case "$code80" in 301 | 302 | 307 | 308) ;; *) fail ":80 answered $code80, not a redirect" ;; esac
echo "NoCapOS is up: :443 OK, :80 redirects ($code80)"

# ---- 3. settings and data survive a restart ----------------------------------
log "Settings and data survive a restart"
vssh 'hostnamectl set-hostname nocap-test && useradd -m -u 1500 -s /bin/bash testperson && echo hello >/srv/nocapos-test.txt && echo scratch >/etc/nocap-scratch-test && systemctl start nocapos-etc-changed.service'
reboot_vm; wait_ssh 600 || fail "no SSH after restart"
[ "$(vssh hostname)" = nocap-test ] || {
  vssh 'journalctl -b -k --no-pager | grep -i -e etc-sync -e rugix | tail -40; ls -la /etc/rugix/hooks/boot/post-init/ /var/lib/nocapos-system/etc/ /var/lib/nocapos-system/etc/files/ 2>&1; cat /etc/hostname /var/lib/nocapos-system/etc/files/hostname 2>&1; findmnt /var/lib/nocapos-system'
  fail "hostname was lost"
}
vssh 'id testperson >/dev/null' || fail "the account was lost"
vssh 'test -d /home/testperson' || fail "the home folder was lost"
[ "$(vssh cat /srv/nocapos-test.txt)" = hello ] || fail "a file in /srv was lost"
vssh '[ ! -e /etc/nocap-scratch-test ]' || fail "an unlisted /etc file survived (the root must start fresh)"
echo "hostname, account, home and data kept; nothing else"

# ---- 4. A -> B update ----------------------------------------------------------
log "Update A -> B"
hash_next="$(cat "$BUILD/pc-test-next.hash")"
vssh "rugix-ctrl update install --reboot no --bundle-hash '$hash_next' http://10.0.2.2:$BUNDLE_PORT/pc-test-next.rugixb" | tail -n 3
vssh 'rugix-ctrl system reboot --spare' || true
wait_vm_exit 180; start_vm; wait_ssh 600 || fail "no SSH after the update"
[ "$(marker)" = next ] || fail "the new system didn't boot"
wait_boot_check
[ "$(group defaultGroup)" = b ] || { vssh 'journalctl -b -u nocapos-boot-check --no-pager | tail -20'; fail "the healthy update wasn't kept"; }
vssh 'cat /var/lib/nocapos/os-boot-check.json' | grep -q '"ok":true' || fail "no success record"
[ "$(vssh hostname)" = nocap-test ] || fail "settings were lost in the update"
reboot_vm; wait_ssh 600 || fail "no SSH after restart"
[ "$(group activeGroup)" = b ] && [ "$(marker)" = next ] || fail "the update didn't stay"
echo "updated to B, health check passed, kept as default"

# ---- 5. a broken update rolls back --------------------------------------------
log "Broken update rolls back by itself"
hash_broken="$(cat "$BUILD/pc-test-broken.hash")"
vssh "rugix-ctrl update install --reboot no --bundle-hash '$hash_broken' http://10.0.2.2:$BUNDLE_PORT/pc-test-broken.rugixb" | tail -n 3
vssh 'rugix-ctrl system reboot --spare' || true
wait_vm_exit 180; start_vm
# The trial system boots, its health check fails (NoCapOS never answers) and
# it restarts into B by itself; -no-reboot turns that restart into an exit.
wait_vm_exit 900
start_vm; wait_ssh 600 || fail "no SSH after the rollback"
[ "$(marker)" = next ] || fail "didn't go back to the previous system (marker $(marker))"
[ "$(group activeGroup)" = b ] && [ "$(group defaultGroup)" = b ] || fail "boot groups wrong after the rollback"
vssh 'cat /var/lib/nocapos/os-boot-check.json' | tee "$WORK/rollback.json" | grep -q '"ok":false' || fail "the failed check wasn't recorded"
grep -q nocapos_not_responding "$WORK/rollback.json" || fail "unexpected rollback reason"
echo "the broken update was rolled back and recorded"

log "All QEMU checks passed"
stop_vm
