#!/bin/bash
# The NoCapOS layer: NoCapOS's own packages (built by
# packaging/build-debs.sh into debs/), the image's identity, the boot
# branding and the read-only system setup.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive NOCAP_IMAGE_BUILD=1

TARGET="${RECIPE_PARAM_TARGET}"
BOOT_DIR="${RUGIX_LAYER_DIR}/roots/boot"

# ---- NoCapOS packages ------------------------------------------------------
shopt -s nullglob
debs=("${RECIPE_DIR}"/debs/*.deb)
[ "${#debs[@]}" -gt 0 ] || { echo "no NoCapOS packages in ${RECIPE_DIR}/debs (run scripts/build.sh)" >&2; exit 1; }
mkdir -p /tmp/nocapos-debs
cp "${debs[@]}" /tmp/nocapos-debs/
apt-get install -y --no-install-recommends /tmp/nocapos-debs/*.deb
rm -rf /tmp/nocapos-debs
VERSION="$(dpkg-query -W -f='${Version}' nocapos-core)"

# ---- identity --------------------------------------------------------------
echo nocap >/etc/hostname
cat >/etc/hosts <<'EOF'
127.0.0.1       localhost
127.0.1.1       nocap
::1             localhost ip6-localhost ip6-loopback
ff02::1         ip6-allnodes
ff02::2         ip6-allrouters
EOF
cp /usr/share/nocapos/motd /etc/motd
snapshot="$(sed -n 's|.*/archive/debian/\([0-9TZ]*\)/ trixie .*|\1|p' /usr/lib/nocapos/build-sources.list | head -1)"
cat >/usr/lib/nocapos/image-os <<EOF
# NoCapOS image install. alfad reads this to update by whole system images.
TARGET=$TARGET
VERSION=$VERSION
CHANNEL=${RECIPE_PARAM_CHANNEL}
DEBIAN_SNAPSHOT=$snapshot
BUILT=$(date -u -d "@${SOURCE_DATE_EPOCH:-$(date +%s)}" +%Y-%m-%dT%H:%M:%SZ)
EOF
printf 'IMAGE_ID=nocapos-%s\nIMAGE_VERSION=%s\n' "$TARGET" "$VERSION" >>/usr/lib/os-release

# ---- update signing ---------------------------------------------------------
# With a root certificate in keys/, the system only installs update bundles
# signed for it (the release workflow adds it; see README.md, Signing).
if [ -f "${RECIPE_DIR}/keys/update-root.crt" ]; then
  install -D -m 644 "${RECIPE_DIR}/keys/update-root.crt" /etc/rugix/update-root.crt
  printf '[signatures]\nroots = ["/etc/rugix/update-root.crt"]\n' >/etc/rugix/ctrl.toml
  echo "Update signing: bundles must be signed for keys/update-root.crt"
else
  echo "WARNING: no update root certificate in keys/: updates are checked by their hash only" >&2
fi

# ---- services ---------------------------------------------------------------
systemctl enable nocapos.service nocapos-boot-check.service nocapos-etc-save.service \
  nocapos-etc-changed.path NetworkManager.service avahi-daemon.service \
  systemd-timesyncd.service docker.service
# NoCap Desktop's sign-in screen starts once NoCap Shell is in the image
# (the nocap-shell package); until then the screen shows the console banner.
if ! dpkg-query -W -f='${Status}' nocap-shell 2>/dev/null | grep -q 'ok installed'; then
  systemctl disable greetd.service 2>/dev/null || true
fi
# systemd-networkd and NetworkManager must not both manage the network.
systemctl disable systemd-networkd.service 2>/dev/null || true

# /etc/systemd/system is this machine's own (it is saved across updates by
# etc-sync), so the image's enabled services move to /usr/lib, where an
# update can change them.
flatten_units() {
  local etc=/etc/systemd/system lib=/usr/lib/systemd/system p rel dst
  [ -d "$etc" ] || return 0
  while IFS= read -r -d '' p; do
    rel="${p#"$etc"/}"
    dst="$lib/$rel"
    mkdir -p "$(dirname "$dst")"
    if [ -e "$dst" ] || [ -L "$dst" ]; then rm -f "$p"; else mv "$p" "$dst"; fi
  done < <(find "$etc" -mindepth 1 \( -type l -o -type f \) -print0)
  find "$etc" -mindepth 1 -depth -type d -empty -delete
}
flatten_units

# ---- boot branding ----------------------------------------------------------
# Quiet boot with the NoCap splash, no kernel text; NoCap console colours.
VT_RED="11,232,48,255,10,191,100,199,58,255,50,255,64,218,112,242"
VT_GRN="11,35,209,214,132,90,210,199,58,69,215,214,156,143,215,242"
VT_BLU="16,43,88,10,255,242,255,204,60,58,75,10,255,255,255,247"
ARGS="quiet splash loglevel=3 rd.udev.log_level=3 systemd.show_status=auto vt.global_cursor_default=0 rugix.init.quiet panic=10 plymouth.ignore-serial-consoles vt.default_red=$VT_RED vt.default_grn=$VT_GRN vt.default_blu=$VT_BLU"

plymouth-set-default-theme nocapos
cat >/etc/initramfs-tools/conf.d/nocapos <<'EOF'
# NoCapOS: one boot image for every PC, with the GPU modules the splash needs.
MODULES=most
EOF
update-initramfs -u -k all

mkdir -p "$BOOT_DIR"
case "$TARGET" in
  pc)
    cp -L /vmlinuz "$BOOT_DIR/vmlinuz"
    cp -L /initrd.img "$BOOT_DIR/initrd.img"
    cfg="$BOOT_DIR/grub.cfg"
    [ -f "$cfg" ] || { echo "missing $cfg (from core/debian-grub-setup)" >&2; exit 1; }
    if ! grep -q ' splash' "$cfg"; then
      sed -i "s|^linux /vmlinuz \${rugpi_bootargs}\$|linux /vmlinuz \${rugpi_bootargs} $ARGS|" "$cfg"
    fi
    grep -q ' splash' "$cfg" || { echo "couldn't add the boot arguments to $cfg" >&2; exit 1; }
    ;;
  pi)
    fw=/boot/firmware
    if ! grep -q ' splash' "$fw/cmdline.txt"; then
      sed -i "1s|\$| $ARGS|" "$fw/cmdline.txt"
    fi
    # No rainbow square from the firmware: the NoCap splash is the first picture.
    grep -q '^disable_splash=1' "$fw/config.txt" || printf '\n# NoCapOS\ndisable_splash=1\n' >>"$fw/config.txt"
    cp -rp "$fw"/. "$BOOT_DIR/"
    ;;
  *) echo "unknown target $TARGET" >&2; exit 1 ;;
esac

# ---- record what's inside -----------------------------------------------------
mkdir -p /usr/share/nocapos
dpkg-query -W -f='${Package}\t${Version}\t${source:Package}\t${source:Version}\t${Architecture}\n' |
  sort >/usr/share/nocapos/packages.tsv
echo "NoCapOS $VERSION ($TARGET): $(wc -l </usr/share/nocapos/packages.tsv) packages"
