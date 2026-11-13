# NoCapOS OS images

NoCapOS normally installs on top of a Linux you already have
(`deploy/install.sh`). This folder builds NoCapOS as an operating system of its
own: an image you write to a USB stick or SD card, boot, and get NoCapOS.

| Image | Hardware | Built with |
| --- | --- | --- |
| `nocapos-<version>-amd64.iso` | 64-bit PCs, mini PCs, servers, VMs (BIOS and UEFI) | Debian 13 "trixie" + live-build + debian-installer |
| `nocapos-<version>-rpi-arm64.img.xz` | Raspberry Pi 3, 4, 5, 400, 500, Zero 2 W, CM4/CM5 | Raspberry Pi OS Lite arm64 (trixie) + pi-gen |

`.github/workflows/image.yml` builds both on GitHub Actions and attaches them
to the release.

## Architecture

```
┌──────────────────────────────────────────────────────────────┐
│ NoCapOS identity: name, logo, boot menu, boot splash,        │
│ installer banner, console banner, hostname "nocap"           │
├──────────────────────────────────────────────────────────────┤
│ NoCapOS layer: /usr/local/bin/nocapos (alfad), cap,          │
│ nocapos.service, first-boot setup, bundled release           │
├──────────────────────────────────────────────────────────────┤
│ Debian 13 base (PC)  ·  Raspberry Pi OS Lite arm64 (Pi)      │
│ kernel, firmware, systemd, apt, Debian security archive      │
└──────────────────────────────────────────────────────────────┘
```

**Debian base.** The system underneath is plain Debian stable (on the Pi,
Raspberry Pi OS, which is Debian plus the Raspberry Pi archive for kernel and
firmware). Nothing in Debian is rebuilt or forked, so its security team keeps
patching it and `apt` keeps working as on any Debian machine.

**Identity.** `/etc/os-release` says `NAME="NoCapOS"` / `PRETTY_NAME="NoCapOS"`
with NoCapOS's home and bug URLs, but keeps `ID=debian`, `ID_LIKE=debian`,
`VERSION_ID` and `VERSION_CODENAME` from Debian, so Debian tooling (apt, Docker's
installer, DKMS, NoCapOS's own package manager detection) still recognises the
system. Debian's `base-files` owns that file and rewrites it on point
releases, so the image diverts Debian's copy to `/usr/lib/os-release.debian`
(`dpkg-divert`) and an apt hook (`/etc/apt/apt.conf.d/99nocapos-os-release`)
rebuilds NoCapOS's version after every dpkg run
(`/usr/lib/nocapos/image/os-release-sync`). The rest of the identity:

- GRUB: `GRUB_DISTRIBUTOR="NoCapOS"`, NoCapOS theme
  (`/usr/share/grub/themes/nocapos`), `quiet splash` (`/etc/default/grub.d/nocapos.cfg`).
- Boot splash: the Plymouth theme from `deploy/plymouth` (the same one
  `install.sh` sets up).
- ISO boot menus: "Try NoCapOS" / "Install NoCapOS", NoCapOS art for BIOS and UEFI
  (`amd64/config/hooks/normal/9000-nocapos-boot-menu.hook.binary`).
- Installer: NoCapOS banner in debian-installer, hostname `nocap`.
- Console: NoCapOS banner on the login screen with the addresses to open, and
  the first-run setup token until the first admin account exists (only needed
  when setting up from outside the local network).
- Artwork is drawn from `frontend/public/brand` by `common/make-art.py`.

**NoCapOS layer.** The build downloads the release bundle
(`nocapos-linux-<arch>.tar.gz`) for the chosen version from this repository's
GitHub release and checks it against the release's `SHA256SUMS`; any mismatch
stops the build. `common/layer.sh` runs inside the image and installs it in
the same places `install.sh` uses (`/usr/local/bin/nocapos`,
`/etc/nocapos/nocapos.env`, `/var/lib/nocapos`, `/srv/nocapos`,
`nocapos.service`, enabled). The bundle stays in `/usr/lib/nocapos/bundle`.

**First boot.** `nocapos-firstboot.service` runs the bundled `install.sh --yes`
once, which does everything that needs a running system and the network:
Docker (for the App Store), the host packages (virtual machines, sharing,
remote desktop, media, ZFS), NoCap Desktop, mDNS (`nocap.local`), the
console banner and boot splash. Without network it
does a minimal setup and tries again on the next boot. Settings in
`/etc/nocapos/image.conf` (`NOCAP_DOCKER`, `NOCAP_FIRSTBOOT_ARGS`). It doesn't
run in the ISO's live session. Progress: `journalctl -u nocapos-firstboot -f`.

**NoCap Desktop.** Both images come with the full desktop: its packages are
put into the image at build time (`desktop-packages.sh`, using the
installer's own list from `deploy/desktop/lib.sh`), and the first boot runs
the installer with `--desktop` (`NOCAP_FIRSTBOOT_ARGS`), which adds the
NoCapOS look (sign-in screen, wallpaper, dock of native apps) and switches
it on. Set `NOCAP_FIRSTBOOT_ARGS="--no-desktop"` for a headless server.
Every machine gets the same
full desktop; ones with less than 2 GB of memory run it with compressed
swap in memory, no file indexing or calendar store, and no animations.

**Users.** No default account exists in either image.
- PC: debian-installer asks for your name, user name and password; there is no
  root password and that user gets `sudo`. The same Linux account signs in to
  NoCapOS.
- Raspberry Pi: the first boot asks for a user name and password (or takes
  those set in Raspberry Pi Imager's OS customisation).

Then open `https://<address>/` (shown on the console) or `http://nocap.local`,
and create the NoCapOS admin. From outside the local network, setup also asks
for the setup token shown on the console.

### Update flow

| What | Where from | How |
| --- | --- | --- |
| Security updates (incl. kernel security fixes) | Debian security archive (`trixie-security`) | Installed automatically every day by `unattended-upgrades` (security only), and listed in Settings → Software Update |
| Kernel, point releases, other packages | Debian (`trixie`, `trixie-updates`); on the Pi also `archive.raspberrypi.com` (kernel, firmware, bootloader) | Settings → Software Update (apt), or `apt full-upgrade` |
| NoCapOS itself | NoCapOS releases on GitHub | Settings → Software Update / `cap update`, same as any NoCapOS install |

Automatic updates are configured in `/etc/apt/apt.conf.d/52nocapos-unattended-upgrades`
(which origins) and `21nocapos-auto-upgrades` (on/off). Delete the `#clear`
line and the list in the first to take every stable update automatically, or
set `APT::Periodic::Unattended-Upgrade "0"` in the second to turn them off.

## Layout

```
image/
  common/
    stage-overlay.sh   assembles the NoCapOS layer tree (downloads + verifies the bundle)
    layer.sh           runs inside the image: installs NoCapOS, identity, splash
    make-art.py        boot menu / installer / GRUB artwork from the brand files
    (GRUB theme: deploy/desktop/grub-theme.txt, shared with install.sh)
    rootfs/            files copied as-is (units, apt settings, scripts)
  amd64/
    build.sh           lb config + lb build
    config/            live-build tree: package list, hooks, installer preseed
  rpi/
    build.sh           clones pi-gen (arm64 branch), adds the stage, builds in Docker
    config             pi-gen settings
    stage-nocapos/     pi-gen stage: packages + NoCapOS layer
```

## Building locally (Linux)

Both builds take a NoCapOS version that has a published release (the bundle
comes from it). To build from an unreleased tree, run `scripts/package.sh
<version>` first and point `NOCAP_DIST` at its `dist/` folder.

PC ISO (needs root and about 15 GB free; on Debian 13 directly, or in a
privileged container anywhere):

```bash
# On Debian 13:
sudo image/amd64/build.sh 0.8.0 out
# Anywhere with Docker:
docker run --rm --privileged -v "$PWD:/src" -w /src debian:trixie \
  bash -c 'apt-get update && apt-get install -y live-build debootstrap curl cpio python3-pil fonts-dejavu-core && image/amd64/build.sh 0.8.0 /src/out'
```

Raspberry Pi image (needs Docker, git and about 20 GB free; fastest on an
arm64 machine, works on x86_64 through QEMU emulation):

```bash
image/rpi/build.sh 0.8.0 out
```

On GitHub: Actions → OS images → Run workflow (any branch), with the version
and, to put the images on the release, "attach".

## Trying the images

PC ISO in QEMU (UEFI; drop the two `-drive if=pflash` lines for BIOS):

```bash
qemu-img create -f qcow2 nocapos-disk.qcow2 32G
qemu-system-x86_64 -enable-kvm -m 4096 -smp 2 -cpu host \
  -drive if=pflash,format=raw,readonly=on,file=/usr/share/OVMF/OVMF_CODE_4M.fd \
  -drive if=pflash,format=raw,file=OVMF_VARS_4M.fd \
  -cdrom nocapos-0.8.0-amd64.iso -boot d \
  -drive file=nocapos-disk.qcow2,if=virtio \
  -nic user,model=virtio-net-pci,hostfwd=tcp::8443-:443
# copy /usr/share/OVMF/OVMF_VARS_4M.fd next to it first; then open https://localhost:8443
```

Pick "Try NoCapOS" to run it live (NoCapOS starts; nothing is written to the
disk) or "Install NoCapOS". After installing, remove `-cdrom ... -boot d` and
boot from the disk.

PC on real hardware: write the ISO to a USB stick (all data on it is erased):

```bash
sudo dd if=nocapos-0.8.0-amd64.iso of=/dev/sdX bs=4M status=progress conv=fsync
```

Raspberry Pi: write the image with Raspberry Pi Imager ("Use custom"), which
also lets you preset the user, Wi-Fi and SSH, or:

```bash
xz -dc nocapos-0.8.0-rpi-arm64.img.xz | sudo dd of=/dev/sdX bs=4M status=progress conv=fsync
```

Put the card in, connect Ethernet (or Wi-Fi set in Imager), power on. The
first boot asks for a user, then sets up NoCapOS (a few minutes; longer if
ZFS is built). Open `http://nocap.local` or the address on the screen.

## Roadmap

1. **Secure Boot.** The ISO and installed PCs boot through Debian's signed
   shim and GRUB already when the firmware allows it; the NoCapOS GRUB theme and
   menu text don't change signatures. Next: test on Secure Boot machines, and
   later sign NoCapOS's own boot pieces (own shim/MOK) if they ever diverge.
2. **NoCapOS as .deb packages in its own apt repository** (`nocapos`,
   `nocapos-desktop`, `nocapos-branding`), signed with a NoCapOS key. NoCapOS
   then updates through apt like the rest of the system, the image layer
   becomes `apt install nocapos`, and the os-release / GRUB / splash pieces live
   in a package instead of a build script.
3. **OTA updates.** Image-level updates (A/B root partitions or snapshot
   based) for appliances that must never half-update, with rollback on a failed
   boot.
4. **Image signing.** Sign the ISO and `.img.xz` and their checksums (minisign
   or GPG, plus GitHub artifact attestations) and verify them in the docs and
   in Raspberry Pi Imager's repository listing.
5. **More targets:** an arm64 UEFI ISO (generic ARM servers, VMs on Apple
   silicon), cloud/VM images (qcow2, OVA), and an unattended install option.
