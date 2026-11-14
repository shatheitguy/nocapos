# NoCapOS OS images

NoCapOS normally installs on top of a Linux you already have
(`deploy/install.sh`). This folder builds NoCapOS as an operating system of its
own: an image you write to a USB stick or SD card, boot, and get NoCapOS.

| Image | Hardware | Built with |
| --- | --- | --- |
| `nocapos-<version>-amd64.iso` | 64-bit PCs, mini PCs, servers, VMs (BIOS and UEFI) | Debian 13 "trixie" + live-build; graphical installer (Calamares) + text-mode debian-installer |
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
- ISO boot menus (BIOS and UEFI, NoCapOS art and colours,
  `amd64/config/hooks/normal/9000-nocapos-boot-menu.hook.binary`):
  "Try or Install NoCapOS", "Install NoCapOS (text mode, for servers)",
  "Try or Install NoCapOS (safe graphics)", "Memory test", and on UEFI
  "UEFI firmware settings". ISO volume label `NoCapOS <version>`.
- Installers: the graphical NoCapOS installer (below) and debian-installer
  with the NoCapOS title and preseed; both name the machine `nocap`.
- Console: NoCapOS banner on the login screen with the addresses to open, and
  the first-run setup token until the first admin account exists (only needed
  when setting up from outside the local network).
- Artwork is drawn from `frontend/public/brand` by `common/make-art.py`.

## The PC install experience

1. **Boot the ISO** → NoCapOS boot menu → "Try or Install NoCapOS" (the
   default). The NoCapOS splash plays, then the live session signs itself
   in to **NoCap Desktop**: NoCapOS wallpaper, dark theme, red accent, dock
   of native apps. NoCapOS itself runs too (`https://localhost`).
2. **"Install NoCapOS"** is first in the dock and on the desktop, and opens
   by itself on the first sign-in. It's Debian's own graphical installer
   (Calamares, from Debian's archive) with NoCapOS's settings and look
   (`amd64/calamares/`: near-black window, NoCap red sidebar and buttons,
   NoCapOS logo, title "NoCapOS <version>"). The steps follow Debian's
   order, each with a sensible default so Next is usually enough:
   - **Welcome**: language, and a check for disk space and memory
   - **Location**: region and time zone
   - **Keyboard**: layout
   - **Disk**: *Erase disk* (default), *Install alongside* (when another
     system is there) or *Manual partitioning*; an *Encrypt system* box
     (LUKS); ext4, a swap file
   - **Users**: your name, user name and password. This account is in the
     `sudo` group, so it's also the **NoCapOS administrator** (NoCapOS signs
     in with the machine's Linux accounts); no root password
   - **Summary** → **Install** (a slideshow shows what NoCapOS does) →
     **Finished**: "Restart now" is ticked
3. **The installed system** boots NoCapOS GRUB → Plymouth splash → the
   NoCapOS sign-in screen → NoCap Desktop, with NoCapOS running and every
   feature's packages already in place. Open `http://nocap.local` (or the
   address on the console) from any device and sign in with the same
   account.

**Text mode, for servers.** "Install NoCapOS (text mode, for servers)" in
the boot menu starts debian-installer with the NoCapOS title and preseed
(`amd64/config/includes.installer/preseed.cfg`). It asks the same questions
in a text interface and copies the same live system, so the result is the
same NoCapOS (switch the desktop off afterwards in Settings if the machine
has no screen).

**After either installer** `post-install.sh` runs inside the new system:
Debian's standard package sources, NoCapOS's services and first-boot setup
on, live-only packages (live-boot, live-config, the graphical installer,
desktop icons) and their settings removed, initramfs and boot menu rebuilt.

**What's live-only.** The installer, the "Install NoCapOS" launcher, the
automatic sign-in and the desktop icons exist only in the live session:
they're added at boot by `/usr/lib/live/config/2000-nocapos` (in memory) or
come from `nocapos-live.list.chroot_live` packages, which both installers
remove.

**NoCapOS layer.** The build downloads the release bundle
(`nocapos-linux-<arch>.tar.gz`) for the chosen version from this repository's
GitHub release and checks it against the release's `SHA256SUMS`; any mismatch
stops the build. `common/layer.sh` runs inside the image and installs it in
the same places `install.sh` uses (`/usr/local/bin/nocapos`,
`/etc/nocapos/nocapos.env`, `/var/lib/nocapos`, `/srv/nocapos`,
`nocapos.service`, enabled). The bundle stays in `/usr/lib/nocapos/bundle`.

**Everything preinstalled.** Nothing is downloaded on the first boot or the
first use of a feature: the build puts every host package NoCapOS uses into
the image (`host-packages.sh`), all from Debian's own archive:
- the installer's package table (`PKG_TABLE` in `deploy/install.sh`, read
  from the bundle so the two never drift): virtual machines (QEMU/KVM,
  libvirt, OVMF/UEFI firmware, swtpm; `qemu-system-arm` and
  `qemu-efi-aarch64` on the Pi), sharing (Samba, SMB/NFS clients, wsdd),
  the remote desktop engine (guacd), mDNS, smartmontools, FFmpeg, restic,
  rclone, Flatpak and the Linux-apps streaming pieces
- Docker (Debian's `docker.io`, plus `docker-compose`), `ufw` (Shield),
  `mergerfs` and `snapraid`

A package the archive doesn't carry is skipped with a note in the build log
("not in this image's archive"). Debian 13 has no `guacd`/`libguac-client-*`
or `wsdd`, so those stay out of the images (NoCapOS's Packages page shows
them as unavailable, the same as on any Debian 13 install).
- ZFS: `zfsutils-linux` + `zfs-dkms` from Debian's contrib, with the kernel
  headers of every kernel in the image (`linux-headers-amd64`; on the Pi the
  Raspberry Pi kernel flavours' `linux-headers-rpi-*`). DKMS builds the
  module at image build time (the build fails if it doesn't) and again
  after every kernel update. ZFS's licence note is accepted
  non-interactively.
- the full NoCap Desktop (`desktop-packages.sh`, the installer's own list
  from `deploy/desktop/lib.sh`), a web browser and `gnome-remote-desktop`.

**NoCap Desktop.** It gets NoCapOS's look and is switched on at build time
(`desktop-brand.sh`, the same steps as the installer's
`install_full_desktop`): sign-in screen theme, wallpaper, dark theme and
red accent, NoCapOS session, dock, GDM at boot. So the live session and the
first start of an installed system go straight to the NoCapOS sign-in
screen. Turn it off in Settings, or set `NOCAP_FIRSTBOOT_ARGS="--no-desktop"`
for a headless server.

**First boot.** `nocapos-firstboot.service` runs the bundled `install.sh --yes
--minimal --no-docker --desktop` once (the packages are already there, so
it downloads nothing): services, mDNS (`nocap.local`), the console banner,
boot splash, NoCap Desktop's memory tuning, Flathub when online. Images
built without the packages fall back to installing them here. Settings in
`/etc/nocapos/image.conf` (`NOCAP_DOCKER`, `NOCAP_FIRSTBOOT_ARGS`). It doesn't
run in the ISO's live session. Progress: `journalctl -u nocapos-firstboot -f`.
Every machine gets the same
full desktop; ones with less than 2 GB of memory run it with compressed
swap in memory, no file indexing or calendar store, and no animations.

**Users.** No default account exists in either image.
- PC: the installer (graphical or text) asks for your name, user name and
  password; there is no root password and that user gets `sudo`, which makes
  it a NoCapOS administrator. The same Linux account signs in to the desktop
  and to NoCapOS in the browser. (The ISO's live session has a temporary
  `nocap` user that signs in by itself; it isn't installed.)
- Raspberry Pi: the first boot asks for a user name and password (or takes
  those set in Raspberry Pi Imager's OS customisation).

Then open `https://<address>/` (shown on the console) or `http://nocap.local`
and sign in with that account.

### Debian stays Debian (update model)

NoCapOS is a layer on plain Debian, never a fork of it:
- **Kernel and firmware are Debian's**: the stock `linux-image-amd64`
  (Raspberry Pi: Raspberry Pi OS's kernel packages) and Debian's firmware
  packages (non-free-firmware on, as Debian 13 ships it). Nothing is rebuilt,
  pinned or held.
- **Package sources are Debian's own**: an installed PC has exactly
  `/etc/apt/sources.list.d/debian.sources` with `trixie`, `trixie-updates`
  (deb.debian.org) and `trixie-security` (security.debian.org); components
  `main contrib non-free-firmware` (contrib for ZFS). No other repository,
  no `/etc/apt/preferences` pinning, no `apt-mark hold`.
- **Security updates install themselves** (`unattended-upgrades`, daily, on
  by default).
- **Branding survives Debian updates**: every NoCapOS file is one no Debian
  package owns (GRUB settings in `/etc/default/grub.d/nocapos.cfg` and the
  theme in `/usr/share/grub/themes/nocapos`, the Plymouth theme in its own
  folder chosen with `plymouth-set-default-theme`, desktop defaults in
  GSettings/dconf override files), and `/etc/os-release` is a
  `dpkg-divert` plus an apt hook that rebuilds it after base-files updates.
  Debian-owned files are never edited.
- **ZFS follows the kernel**: DKMS rebuilds the module for each new Debian
  kernel (the headers metapackage pulls in the matching headers).

**Checked on every build** (`check-image.sh`, run inside the finished image;
its output is in `check-amd64.log` and the Pi build log): `apt-get update &&
apt-get -s full-upgrade` resolves with nothing held, kept back or removed;
no pinning or holds; only the distribution's package sources; automatic
security updates on; the NoCapOS branding files are owned by no package and
os-release is diverted; `zfs version`, `ufw --version`, `virsh --version`,
`docker --version` (and more) work; `dkms status` shows ZFS built for every
kernel in the image; GDM and the NoCapOS session are on. The PC build also
fails when the ISO reaches GitHub's 2 GiB release asset limit.

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
    layer.sh           runs inside the image: installs NoCapOS, identity, splash, packages
    rootfs/usr/lib/nocapos/image/
      host-packages.sh   every host package + Docker, ufw, ZFS (DKMS) in the image
      desktop-packages.sh, desktop-brand.sh   NoCap Desktop, branded and on
      post-install.sh    end of both PC installers (sources, services, clean-up)
      check-image.sh     update / ownership / feature checks (CI)
    make-art.py        boot menu / installer / GRUB artwork from the brand files
    (GRUB theme: deploy/desktop/grub-theme.txt, shared with install.sh)
    rootfs/            files copied as-is (units, apt settings, scripts)
  amd64/
    build.sh           lb config + lb build, then check-image.sh in the result
    calamares/         graphical installer: settings, modules, NoCapOS branding + slideshow
    config/            live-build tree: package lists, hooks, boot menus,
                       live-session launcher (includes.chroot_after_packages),
                       text installer preseed
  rpi/
    build.sh           clones pi-gen (arm64 branch), adds the stage, builds in Docker
    config             pi-gen settings
    stage-nocapos/     pi-gen stage: packages + NoCapOS layer
```

## Building locally (Linux)

Both builds take a NoCapOS version that has a published release (the bundle
comes from it). To build from an unreleased tree, run `scripts/package.sh
<version>` first and point `NOCAP_DIST` at its `dist/` folder.

PC ISO (needs root and about 25 GB free; on Debian 13 directly, or in a
privileged container anywhere):

```bash
# On Debian 13:
sudo image/amd64/build.sh 0.8.0 out
# Anywhere with Docker:
docker run --rm --privileged -v "$PWD:/src" -w /src debian:trixie \
  bash -c 'apt-get update && apt-get install -y live-build debootstrap curl cpio python3-pil python3-yaml fonts-dejavu-core && image/amd64/build.sh 0.8.0 /src/out'
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

Pick "Try or Install NoCapOS": NoCap Desktop starts live (nothing is
written to the disk) and the installer opens; or "Install NoCapOS (text
mode, for servers)". After installing, remove `-cdrom ... -boot d` and boot
from the disk. Give the VM at least 4 GB of memory for the live desktop.

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

### Installing on a mirrored system drive (ZFS mirror)

To keep the server running (and starting) when a system disk dies, install
onto two disks at once. Start "Try or Install NoCapOS", close the installer,
connect to the network, open a terminal and run:

```bash
ls -l /dev/disk/by-id/          # find the two disks
sudo /usr/lib/nocapos/image/install-zfs-mirror.sh /dev/disk/by-id/ata-DISK1 /dev/disk/by-id/ata-DISK2
```

Both disks get the same layout (BIOS boot, EFI, a 1 GiB boot pool member and
the root pool member), the system from the live image, and their own
bootloader (UEFI entries "nocapos" and "nocapos-mirror" plus fallback copies,
or GRUB on both disks for BIOS). It asks you to type `ERASE` first and never
touches the USB stick it runs from. Supported on 64-bit PCs (UEFI or BIOS)
and 64-bit ARM with UEFI; the Raspberry Pi's firmware starts from one card or
drive only, so it can't use a mirrored system drive.

An existing ZFS-on-root install can add the second disk later from
Storage → Boot drive → "Mirror my boot drive"; a system installed on ext4
can't be converted safely in place, and the app says so.

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
