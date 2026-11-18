#!/bin/bash
# CI only. test/qemu-test.sh makes a fresh key pair for every run and puts
# the public key in files/test-key.pub (git-ignored). Released images never
# contain this layer.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive

key="${RECIPE_DIR}/files/test-key.pub"
[ -f "$key" ] || { echo "no test key: run test/qemu-test.sh" >&2; exit 1; }

cp /usr/lib/nocapos/build-sources.list /etc/apt/sources.list
apt-get update -y
apt-get install -y --no-install-recommends openssh-server
rm -f /etc/apt/sources.list
apt-get clean -y
rm -rf /var/lib/apt/lists/*

install -d -m 700 /root/.ssh
install -m 600 "$key" /root/.ssh/authorized_keys
mkdir -p /etc/ssh/sshd_config.d
printf 'PermitRootLogin prohibit-password\nPasswordAuthentication no\n' >/etc/ssh/sshd_config.d/nocapos-test.conf
# /root and the host keys live on the root overlay, fresh at every boot: a
# new host key per boot is fine for the test (it doesn't check them).
systemctl enable ssh.service
for p in /etc/systemd/system/*.wants/ssh*.service /etc/systemd/system/sshd.service; do
  [ -e "$p" ] || [ -L "$p" ] || continue
  rel="${p#/etc/systemd/system/}"
  mkdir -p "/usr/lib/systemd/system/$(dirname "$rel")"
  mv "$p" "/usr/lib/systemd/system/$rel"
done
echo test >/usr/lib/nocapos/test-marker
