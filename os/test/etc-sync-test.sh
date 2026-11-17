#!/usr/bin/env bash
# etc-sync-test.sh: etc-sync keeps people's accounts and settings across a
# system update while the image's own system accounts win.
set -euo pipefail
SYNC="$(cd "$(dirname "$0")/.." && pwd)/packaging/nocapos-image/root/usr/lib/nocapos/etc-sync"
T="$(mktemp -d)"
trap 'rm -rf "$T"' EXIT
export NOCAP_ETC_ROOT="$T/etc" NOCAP_ETC_STORE="$T/store"
fail() { echo "FAIL: $*" >&2; exit 1; }

# The machine as it ran: one person (sha), in sudo and docker; a hostname;
# a saved Wi-Fi network; a time zone link.
mkdir -p "$T/etc/NetworkManager/system-connections" "$T/etc/default"
cat >"$T/etc/passwd" <<'EOF'
root:x:0:0:root:/root:/bin/bash
messagebus:x:100:101::/nonexistent:/usr/sbin/nologin
sha:x:1000:1000:Sha:/home/sha:/bin/bash
EOF
cat >"$T/etc/group" <<'EOF'
root:x:0:
sudo:x:27:sha
docker:x:990:sha
messagebus:x:101:
sha:x:1000:
EOF
cat >"$T/etc/shadow" <<'EOF'
root:*:20000:0:99999:7:::
messagebus:!:20000::::::
sha:$y$j9T$saved:20000:0:99999:7:::
EOF
cat >"$T/etc/gshadow" <<'EOF'
root:*::
sudo:*::sha
docker:!::sha
messagebus:!::
sha:!::
EOF
echo my-server >"$T/etc/hostname"
echo '[connection]' >"$T/etc/NetworkManager/system-connections/home.nmconnection"
ln -s /usr/share/zoneinfo/Asia/Dubai "$T/etc/localtime"
bash "$SYNC" save

# A system update: fresh /etc from the new image, which adds a system
# account (polkitd) and a group (render), and has no people.
rm -rf "$T/etc"; mkdir -p "$T/etc"
cat >"$T/etc/passwd" <<'EOF'
root:x:0:0:root:/root:/bin/bash
messagebus:x:100:101::/nonexistent:/usr/sbin/nologin
polkitd:x:991:991::/nonexistent:/usr/sbin/nologin
EOF
cat >"$T/etc/group" <<'EOF'
root:x:0:
sudo:x:27:
docker:x:990:
messagebus:x:101:
polkitd:x:991:
render:x:992:
EOF
cat >"$T/etc/shadow" <<'EOF'
root:*:20100:0:99999:7:::
messagebus:!:20100::::::
polkitd:!:20100::::::
EOF
cat >"$T/etc/gshadow" <<'EOF'
root:*::
sudo:*::
docker:!::
messagebus:!::
polkitd:!::
render:!::
EOF
echo nocap >"$T/etc/hostname"
ln -s /usr/share/zoneinfo/Etc/UTC "$T/etc/localtime"
bash "$SYNC" restore

grep -q '^sha:x:1000:1000:' "$T/etc/passwd" || fail "person lost"
grep -q '^polkitd:x:991:' "$T/etc/passwd" || fail "image's new system account lost"
[ "$(grep -c '^root:' "$T/etc/passwd")" = 1 ] || fail "root duplicated"
grep -q '^sudo:x:27:sha$' "$T/etc/group" || fail "sudo membership lost: $(grep ^sudo: "$T/etc/group")"
grep -q '^docker:x:990:sha$' "$T/etc/group" || fail "docker membership lost"
grep -q '^sha:x:1000:$' "$T/etc/group" || fail "person's group lost"
grep -q '^render:x:992:$' "$T/etc/group" || fail "image's new group lost"
grep -q '^sha:\$y\$j9T\$saved:' "$T/etc/shadow" || fail "password lost"
grep -q '^root:\*:20100:' "$T/etc/shadow" || fail "image's root entry not used"
grep -q '^sudo:\*::sha$' "$T/etc/gshadow" || fail "gshadow members not merged: $(grep ^sudo: "$T/etc/gshadow")"
grep -q '^sha:!::$' "$T/etc/gshadow" || fail "person's gshadow lost"
[ "$(cat "$T/etc/hostname")" = my-server ] || fail "hostname lost"
[ -f "$T/etc/NetworkManager/system-connections/home.nmconnection" ] || fail "Wi-Fi lost"
[ "$(readlink "$T/etc/localtime")" = /usr/share/zoneinfo/Asia/Dubai ] || fail "time zone lost"

# A file removed on purpose stays removed.
rm "$T/etc/hostname"
bash "$SYNC" save
echo nocap >"$T/etc/hostname"
bash "$SYNC" restore
[ ! -e "$T/etc/hostname" ] || fail "removed file came back"

echo "etc-sync OK"
