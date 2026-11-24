# NoCapOS OS images

NoCapOS normally installs on top of a Linux you already have
(`deploy/install.sh`). This folder builds NoCapOS as an operating system of its
own: an image you write to a USB stick or SD card, boot, and get NoCapOS.

| Image | Hardware | Built with |
| --- | --- | --- |
| `nocapos-<version>-amd64.iso` | 64-bit PCs, mini PCs, servers, VMs (BIOS and UEFI) | Debian 13 "trixie" + live-build; the full-screen NoCapOS setup (Calamares) |
| `nocapos-<version>-rpi-arm64.img.xz` | Raspberry Pi 3, 4, 5, 400, 500, Zero 2 W, CM4/CM5 | Raspberry Pi OS Lite arm64 (trixie) + pi-gen |
| `nocapos-<version>-lxc-amd64.tar.zst`, `-lxc-arm64.tar.zst` | A system container on Proxmox VE (see [Container template](#container-template-proxmox-ve)) | Debian 13 "trixie" + mmdebstrap |

`.github/workflows/image.yml` builds the first two on GitHub Actions and
attaches them to the release; `.github/workflows/lxc.yml` builds the
container templates (by hand only).

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

- GRUB: a silent boot, the same as script installs (`deploy/desktop/nocap-boot`):
  the menu stays hidden unless Shift or Esc is held at power-on, NoCap's own
  background instead of any distribution wallpaper, GRUB's "Loading ..." lines
  invisible (`/etc/grub.d/06_nocapos`), a quiet kernel line, and the NoCapOS
  theme with the medium logo when the menu shows (`/etc/default/grub.d/nocapos.cfg`,
  `/usr/share/grub/themes/nocapos`). Settings → Show boot details brings the
  menu and the boot messages back.
- Boot splash: the Plymouth theme from `deploy/plymouth` (the same one
  `install.sh` sets up).
- ISO boot menus (BIOS and UEFI, NoCapOS art and colours,
  `amd64/config/hooks/normal/9000-nocapos-boot-menu.hook.binary`):
  "Install NoCapOS" (the default after 5 seconds: the full-screen setup,
  no desktop), "Try NoCapOS" and "Memory test". ISO volume label `NoCapOS <version>`.
- Installer: the full-screen NoCapOS setup (below); it names the machine `nocap`.
- Console: NoCapOS banner on the login screen with the addresses to open, and
  the first-run setup token until the first admin account exists (only needed
  when setting up from outside the local network).
- Artwork is drawn from `frontend/public/brand` by `common/make-art.py`.

## The PC install experience

1. **Boot the ISO** → NoCapOS boot menu → "Install NoCapOS" (the
   default, after 5 seconds). The NoCapOS splash plays, then the setup fills
   the whole screen straight away, before any desktop (greetd runs
   `live/nocapos-install-screen` as root: the installer alone in a kiosk
   compositor, drawn with the processor so it looks the same on every
   graphics card; closing it brings it back, and if it stops by itself the
   screen says so before it starts again). Its logs (`/var/log/nocapos`)
   are copied into the installed system at the end of the install, so
   they're still there after the restart. "Try NoCapOS" instead signs the
   live session in to **NoCap Desktop**: NoCapOS wallpaper, dark theme, red accent, dock
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

**Servers without a screen** use the same setup (switch the desktop off
afterwards in Settings).

**After the installer** `post-install.sh` runs inside the new system:
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
- the remote desktop engine as a container image instead: the
  `guacamole/guacd` image pinned (tag and digest) in
  `backend/internal/rdp/guacd.ref`, the same file alfad reads, so the two
  never drift. `common/save-guacd.sh` fetches it for the image's
  architecture (skopeo, or docker) and stores it as a docker-load archive,
  `/usr/lib/nocapos/images/guacd-<arch>.tar.zst` (zstd).
  On the first boot `nocapos-images.service` (after `docker.service`) loads
  it into Docker and deletes the archive, so remote desktop and screen
  viewing work offline. alfad waits for that load if it's still running
  and only downloads the image when it isn't in Docker (an archive that
  didn't load stays and is tried again on the next boot). Not run in the
  ISO's live session. Change the pin in `guacd.ref` only.
- ZFS: `zfsutils-linux` + `zfs-dkms` from Debian's contrib, with the kernel
  headers of every kernel in the image (`linux-headers-amd64`; on the Pi the
  Raspberry Pi kernel flavours' `linux-headers-rpi-*`). DKMS builds the
  module at image build time (the build fails if it doesn't) and again
  after every kernel update. ZFS's licence note is accepted
  non-interactively.
- the full NoCap Desktop (`desktop-packages.sh`, the installer's own list
  from `deploy/desktop/lib.sh`): labwc, greetd, wayvnc and the libraries
  NoCap Shell uses. No third-party default apps or browser.

**NoCap Desktop.** It is put in place and switched on at build time
(`desktop-brand.sh`, the same steps as the installer's
`install_full_desktop`): NoCap Shell's programs and pages, the session,
greetd with NoCap's sign-in screen at boot. The desktop on the screen is the
same NoCap OS as in the browser, running natively, with its apps in their
own windows. The live session signs its user in straight away with the
"Install NoCapOS" app; an installed system starts at the NoCapOS sign-in
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
kernel in the image; greetd with NoCap's sign-in screen, labwc and NoCap
Shell (its programs run, with every library) are there and on, and no
earlier desktop packages are; the guacd archive
passes `zstd -t`, lists a `manifest.json` tagged `guacamole/guacd:<tag>`
for the image's architecture and its load service is enabled (where a
Docker daemon runs, it is also `docker load`ed). The PC build also
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
      load-images.sh     first boot: docker load the bundled images, then delete them
    save-guacd.sh      the pinned guacd image as images/guacd-<arch>.tar.zst
    make-art.py        boot menu / installer / GRUB artwork from the brand files
    (GRUB theme: deploy/desktop/grub-theme.txt, shared with install.sh)
    rootfs/            files copied as-is (units, apt settings, scripts)
  amd64/
    build.sh           lb config + lb build, then check-image.sh in the result
    calamares/         graphical installer: settings, modules, NoCapOS branding + slideshow
    config/            live-build tree: package lists, hooks, boot menus,
                       live-session launcher and full-screen setup
                       (includes.chroot_after_packages)
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
  bash -c 'apt-get update && apt-get install -y live-build debootstrap curl cpio skopeo zstd python3-pil python3-yaml fonts-dejavu-core && image/amd64/build.sh 0.8.0 /src/out'
```

Raspberry Pi image (needs Docker, git, zstd and about 20 GB free; fastest on an
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

Pick "Try NoCapOS": NoCap Desktop starts live (nothing is
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
onto two disks at once. Start "Try NoCapOS", close the installer,
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

## Container template (Proxmox VE)

`nocapos-<version>-lxc-<arch>.tar.zst` is NoCapOS as a system container: a
whole Debian 13 system with its own systemd, NoCapOS in server mode (the web
OS, Docker for the App Store, Sharing with Samba, `nocap.local` with Avahi,
Shield with ufw, backups with restic and rclone) and no NoCap Desktop, since
a container has no screen. The host keeps the hardware, so NoCapOS explains
instead of offering what can't work there: Storage leaves pools, formatting,
disk health, disk sleep and the boot drive mirror to the host; Settings says
the network and Wi-Fi are set up on the host; NoCap Desktop and the boot
details switch are off; Virtual Desk works only when `/dev/kvm` is passed in.
NoCapOS finds out by itself (`backend/internal/virt`; `ALFA_CONTAINER=lxc`
previews it anywhere, `ALFA_CONTAINER=none` turns it off).

### Install it

1. Get the template onto the host. In the web interface: your storage (one
   with "Container template" content, e.g. `local`) → **CT Templates** →
   **Upload**, and pick the `.tar.zst`; or on the host's shell:

   ```bash
   cd /var/lib/vz/template/cache
   wget https://github.com/shatheitguy/nocapos/releases/download/v<version>/nocapos-<version>-lxc-amd64.tar.zst
   wget https://github.com/shatheitguy/nocapos/releases/download/v<version>/nocapos-<version>-lxc-amd64.tar.zst.sha256
   sha256sum -c nocapos-<version>-lxc-amd64.tar.zst.sha256
   ```

2. **Create CT** with these settings:

   | Setting | Value | Why |
   | --- | --- | --- |
   | Unprivileged container | yes (the default) | safer; everything in the template works unprivileged |
   | Features | `nesting=1,keyctl=1` | Docker inside the container (App Store) and systemd's sandboxing need both |
   | Template | `nocapos-<version>-lxc-amd64.tar.zst` | |
   | Disk | 16 GB or more | the system takes about 1.5 GB; apps and their data go here too unless you add a mount point |
   | CPU | 2 cores | |
   | Memory | 2048 MB (1024 MB works without apps), swap 512 MB | |
   | Network | a bridge (`vmbr0`), IPv4 DHCP or a static address | the host sets up the container's network |
   | Password / SSH key | anything | the template has no root password; use `pct enter` or the console |

   The same from the host's shell (pick your own ID, storage and size):

   ```bash
   pct create 120 local:vztmpl/nocapos-<version>-lxc-amd64.tar.zst      --hostname nocap --unprivileged 1 --features nesting=1,keyctl=1      --cores 2 --memory 2048 --swap 512 --rootfs local-lvm:16      --net0 name=eth0,bridge=vmbr0,ip=dhcp --onboot 1
   pct start 120
   ```

   The OS type is found from the template (Debian). Use a host version that
   knows Debian 13 containers.

3. **First start.** The first boot finishes the setup by itself in about a
   minute (`journalctl -u nocapos-firstboot -f` inside the container). Then
   open `https://<container address>/` (`pct exec 120 -- hostname -I`) or
   `http://nocap.local` from a device on your network and create the
   administrator. From outside the local network, setup also asks for the
   one-time token on the container's console (`pct console 120`) or in
   `journalctl -u nocapos`.

### Options for more

- **Your files on the host's disks:** add a mount point (Resources → Add →
  Mount Point, or `pct set 120 -mp0 /tank/media,mp=/srv/media`) and add the
  folder in Files. In an unprivileged container the files show up owned by
  `nobody` unless they belong to the host's mapped IDs (100000 and up).
- **Virtual machines (Virtual Desk):** pass `/dev/kvm` in
  (`pct set 120 -dev0 /dev/kvm`); Virtual Desk then offers to install
  what it needs.
- **Shield (ufw)** works in the container's own network; the host's firewall
  for the container (in the host's container settings) also applies.
- **Network drives:** an unprivileged container may not mount SMB or NFS
  shares. Mount them on the host and pass them in as a mount point.
- **Backups:** the host's container backups and snapshots cover the whole
  NoCapOS system; NoCapOS Backups covers your files and apps.
- **Updates** work as on any NoCapOS server: Settings → Glow Up (NoCapOS
  and Debian's packages); security updates install themselves.

### Build it

On GitHub: Actions → **Container template** → Run workflow, with the
version and, to put the templates on the release, "attach". Each run builds
the amd64 and arm64 templates on runners of that architecture, boots each one
with `systemd-nspawn` (the first-boot setup has to finish and NoCapOS has to
answer on its HTTPS port) and keeps them as workflow artifacts.

Locally (root on Debian 13, or in a privileged `debian:trixie` container, on
a machine of the target architecture):

```bash
apt-get install -y mmdebstrap zstd curl
image/lxc/build.sh 0.14.6 amd64 out          # the bundle from release v0.14.6
NOCAP_DIST=dist image/lxc/build.sh 0.14.6 amd64 out   # or from scripts/package.sh
sudo image/lxc/boot-test.sh out/nocapos-0.14.6-lxc-amd64.tar.zst
```

The root filesystem is Debian 13 from a fixed snapshot
(`os/config/snapshot.toml`, or `NOCAP_SNAPSHOT`), with timestamps from
`SOURCE_DATE_EPOCH`, so the same inputs give the same packages. Its packages
are `image/lxc/packages.list`, each with the feature that needs it (the same
rule as `os/packages`); the installed system then uses Debian's own mirrors.

```
image/lxc/
  build.sh        mmdebstrap + the NoCapOS layer, then the .tar.zst
  customize.sh    runs in the root filesystem: packages, NoCapOS, identity
  packages.list   every package, with its reason
  boot-test.sh    boots a template with systemd-nspawn and checks NoCapOS
  rootfs/         files copied as-is (first-boot settings)
```

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
