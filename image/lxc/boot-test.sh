#!/usr/bin/env bash
# Boots a built container template with systemd-nspawn (a system container
# like the hypervisor's: its own systemd as PID 1) and checks that it comes
# up as NoCapOS: the first-boot setup finishes, NoCapOS answers on its HTTPS
# port, and it knows it runs in a system container.
#
#   sudo image/lxc/boot-test.sh nocapos-<version>-lxc-<arch>.tar.zst [LOGDIR]
#
# Needs root, systemd-nspawn (systemd-container), zstd and curl, and nothing
# else listening on port 443: the container shares the host's network, so
# the first boot can reach Debian and the test can reach NoCapOS. Docker
# can't start inside nspawn without extra privileges; that's expected here.
set -euo pipefail

TPL="${1:?template .tar.zst}"
LOGS="${2:-$PWD/boot-test-logs}"
MACHINE=nocap-boot-test
[ "$(id -u)" -eq 0 ] || { echo "run as root" >&2; exit 1; }
mkdir -p "$LOGS"
DIR="$(mktemp -d /var/tmp/nocap-ct.XXXXXX)"
NSPAWN=""
cleanup() {
  machinectl poweroff "$MACHINE" >/dev/null 2>&1 || true
  for _ in $(seq 1 20); do
    [ -n "$NSPAWN" ] && kill -0 "$NSPAWN" 2>/dev/null || break
    sleep 1
  done
  [ -z "$NSPAWN" ] || kill "$NSPAWN" 2>/dev/null || true
  machinectl terminate "$MACHINE" >/dev/null 2>&1 || true
  rm -rf "$DIR"
}
trap cleanup EXIT

echo "==> Unpacking $(basename "$TPL")"
zstd -dc "$TPL" | tar --numeric-owner --xattrs --xattrs-include='*' -xpf - -C "$DIR"
[ -x "$DIR/usr/local/bin/nocapos" ] || { echo "no NoCapOS in the template" >&2; exit 1; }
[ ! -s "$DIR/etc/machine-id" ] || { echo "the template carries a machine id" >&2; exit 1; }
grep -q '^NAME="NoCapOS"' "$DIR/usr/lib/os-release" || { echo "os-release isn't NoCapOS's" >&2; exit 1; }
grep -q '^VARIANT_ID=lxc' "$DIR/usr/lib/os-release" || { echo "os-release has no container variant" >&2; exit 1; }

echo "==> Booting it (systemd-nspawn)"
systemd-nspawn --quiet --boot --directory="$DIR" --machine="$MACHINE" --console=passive \
  >"$LOGS/console.log" 2>&1 &
NSPAWN=$!

in_ct() { # runs a command inside the container
  local leader
  leader="$(machinectl show "$MACHINE" -p Leader --value 2>/dev/null || true)"
  [ -n "$leader" ] || return 1
  nsenter --target "$leader" --mount --uts --ipc --pid -- "$@"
}

ok=0
for _ in $(seq 1 180); do
  if ! kill -0 "$NSPAWN" 2>/dev/null; then
    echo "the container stopped" >&2
    cat "$LOGS/console.log" >&2 || true
    exit 1
  fi
  if [ -f "$DIR/var/lib/nocapos/.image-firstboot-done" ] && curl -fsk --max-time 5 https://127.0.0.1/healthz >"$LOGS/healthz.json" 2>/dev/null; then
    ok=1
    break
  fi
  sleep 5
done

in_ct journalctl --no-pager -b >"$LOGS/journal.log" 2>&1 || true
in_ct journalctl --no-pager -b -u nocapos -o cat >"$LOGS/nocapos.log" 2>&1 || true
in_ct journalctl --no-pager -b -u nocapos-firstboot -o cat >"$LOGS/firstboot.log" 2>&1 || true
in_ct systemctl --no-pager --failed >"$LOGS/failed-units.txt" 2>&1 || true

if [ "$ok" -ne 1 ]; then
  echo "NoCapOS didn't come up within 15 minutes" >&2
  tail -n 60 "$LOGS/firstboot.log" "$LOGS/nocapos.log" >&2 || true
  exit 1
fi
echo "    healthz: $(cat "$LOGS/healthz.json")"
grep -q '"alfad ready"' "$LOGS/nocapos.log" || { echo "no \"alfad ready\" in NoCapOS's log" >&2; exit 1; }
if ! grep -q '"running in a system container".*"container":"systemd-nspawn"' "$LOGS/nocapos.log"; then
  echo "NoCapOS didn't notice it runs in a system container" >&2
  exit 1
fi
grep -q 'first-boot setup: done' "$LOGS/firstboot.log" || { echo "the first-boot setup didn't finish" >&2; exit 1; }
# A container never sets up NoCap Desktop.
[ ! -f "$DIR/etc/nocapos/desktop.conf" ] || { echo "NoCap Desktop was set up in a container" >&2; exit 1; }
echo "    failed units (Docker can't run inside nspawn):"
sed 's/^/      /' "$LOGS/failed-units.txt"
echo "==> The template boots into NoCapOS"
