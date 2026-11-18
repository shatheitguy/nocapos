# NoCapOS, the operating system

NoCapOS is its own image-based Linux distribution. Debian 13 (trixie) is
only the package source: every release is one read-only system image, built
from a fixed snapshot of Debian, that replaces the running system whole. The
device never runs apt and never compiles anything.

| | |
|---|---|
| Targets | PC: x86_64 with UEFI. Raspberry Pi 4, 5, CM4, CM5 (arm64) |
| Base | Debian 13 from `snapshot.debian.org` (date in `config/snapshot.toml`), plus the security archive of the same date |
| Build tool | Rugix Bakery `v0.9.5` (pinned by image digest in `run-bakery`) |
| Updates | Rugix Ctrl `v1.4.0`: A/B system slots, trial boot, commit or automatic rollback |
| Packages | only those in `packages/*.list`, each with the NoCapOS feature that needs it |

Script installs on an existing Debian or Raspberry Pi OS (`deploy/install.sh`)
keep working and keep updating as before; see
[Existing installs](#existing-installs-a-later-one-time-move).

## Why Rugix

We compared three ways to build and update an image OS:

| | Rugix (Bakery + Ctrl) | mkosi + systemd-sysupdate | RAUC |
|---|---|---|---|
| Licence | MIT / Apache-2.0 | LGPL-2.1+ | LGPL-2.1 |
| Debian trixie | built-in `core/debian-trixie` layer | yes | yes (needs our own image build) |
| Raspberry Pi 5 / 4 A/B | built-in `rpi-tryboot` (firmware tryboot) | no Pi boot flow: we'd write it | U-Boot or custom bootloader backend |
| x86 UEFI A/B | built-in GRUB boot flow | systemd-boot boot counting | GRUB backend |
| Image build | Bakery: layered recipes, cached | mkosi | separate tool needed |
| Signed bundles, delta downloads | CMS signatures; block-indexed bundles fetched over HTTP ranges | sysupdate verifies hashes; no deltas | signatures; casync deltas |
| Data partition, first-boot growth, LUKS2/TPM2 | built in | systemd-repart | separate |
| Maintenance | releases every few weeks (Ctrl 1.4.0, Bakery 0.9.5: Nov 2026) | active | active |

Rugix covers both of our targets with one build and one updater, including
the Pi firmware's tryboot, which the others would need custom work for. It
is permissively licensed and actively maintained.

## How the OS is built

```
os/
  rugix-bakery.toml     systems: pc, pi (+ pc-test* for CI)
  run-bakery            the pinned Bakery container
  config/snapshot.toml  the Debian snapshot date of this release
  layers/               trixie -> pc-base / pi-base -> pc / pi
  recipes/              what each layer does
  packages/*.list       every package, with its reason
  packaging/            NoCapOS's own .deb packages
  scripts/              build helpers (build.sh, signing.sh, ...)
  test/                 QEMU boot + update + rollback test
```

Layers (each cached by Bakery; a NoCapOS change only rebuilds the top one):

1. **trixie**: `mmdebstrap` of Debian 13 from the snapshot date.
2. **pc-base / pi-base**:
   - `nocapos-sources`: the snapshot's `trixie`, `trixie-updates` and
     `trixie-security` sources; no recommends, no suggests; dpkg drops docs,
     man pages and translations (copyright files stay).
   - Rugix's `core/debian-grub-setup` (PC kernel + firmware) or
     `core/rpi-debian-setup` (Pi kernels for Pi 4 and Pi 5, boot firmware;
     these come from the Raspberry Pi archive, which Debian doesn't cover).
   - `nocapos-packages`: `packages/common.list`, `desktop.list` and
     `pc.list` / `pi.list`.
   - `nocapos-zfs`: builds the ZFS module for exactly the image's kernels,
     then removes dkms, the compiler and the kernel headers.
3. **pc / pi**:
   - `nocapos-os`: installs NoCapOS's packages (below), sets the identity
     (hostname `nocap`), boot branding, services, and moves the image's
     enabled services from `/etc` to `/usr/lib`.
   - `nocapos-slim`: no apt sources, caches, logs or build helpers.

NoCapOS's own components are Debian packages, built by
`packaging/build-debs.sh` (amd64, arm64; armhf for script installs):

| Package | Contents |
|---|---|
| `nocapos-core` | alfad (`/usr/bin/nocapos`), `cap`, `nocapos.service`, `/etc/nocapos/nocapos.env` |
| `nocapos-image` | image-only: data partition driver, what persists, `etc-sync`, the post-update health check |
| `nocapos-branding` | `os-release` (`ID=nocapos`, `ID_LIKE=debian`), `lsb-release`, console banner, boot splash |
| `nocapos-base` | metapackage: depends on exactly `packages/*.list` |
| `nocap-desktop` | NoCap Desktop: `nocap-shell` and `nocap-lock` (built from `shell/` in a Debian 13 container), NoCap Shell's pages (`npm run build:shell`) and `deploy/desktop`; its postinst runs the same setup steps as a script install (`packaging/nocap-desktop/setup-image.sh`) |

Build locally (Linux with Docker, privileged containers):

```
cd frontend && npm ci && npm run build && cd ..
bash os/scripts/build.sh debs amd64
bash os/scripts/build.sh image pc        # os/build/pc.img
bash os/scripts/build.sh bundle pc       # os/build/pc.rugixb + pc.hash
```

### Dropped targets

- **armv7 (32-bit Pi 2/3)**: dropped for the image OS. Rugix's Pi boot flow
  needs the tryboot firmware of the Pi 4 and newer, and Debian's 32-bit Pi
  kernels lack Pi 5 support. 32-bit boards keep the script install.
- **Legacy BIOS PCs**: not supported by the image (Rugix's PC boot flow is
  UEFI). Every PC from the last decade boots UEFI.

## Disk layout

PC (GPT):

| # | Size | What |
|---|---|---|
| 1 | 256 MiB | EFI + Rugix config (GRUB, boot selection) |
| 2 | 256 MiB | boot A (kernel, initrd) |
| 3 | 256 MiB | boot B |
| 4 | 6 GiB | system A (squashfs, read-only) |
| 5 | 6 GiB | system B |
| 6 | rest | data (ext4, or ext4 in LUKS2 with "Lock my drive") |

Raspberry Pi (MBR): config 256 MiB, boot A 128 MiB, boot B 128 MiB, then an
extended partition with system A, system B and data. A flashed image holds
partitions 1-4; on first boot Rugix makes system B and the data partition,
which grows to fill the disk.

### What persists

The root filesystem is read-only, with a scratch overlay that starts fresh at
every boot. Only this explicit list survives restarts and updates
(`packaging/nocapos-image/root/etc/rugix/state/nocapos.toml`, bind-mounted
from the data partition by Rugix before systemd starts):

`/home`, `/srv` (your files, shares, pool mount points), `/var/lib/nocapos`
(NoCapOS's database and settings), `/var/lib/docker`, `/var/lib/containerd`,
`/var/lib/libvirt` (VMs), `/var/lib/flatpak`, `/var/log`, and the state of
NetworkManager, Samba, ufw, systemd, NoCap Desktop and native apps.

Settings in `/etc` are copied, not bind-mounted (tools replace those files by
renaming, which a bind mount refuses): `etc-sync` saves them to
`/var/lib/nocapos-system/etc` when they change and puts them back before
systemd starts (a Rugix `boot/post-init` hook). The list: hostname, hosts,
time zone, keyboard, locale, fstab, environment, the ZFS pool cache, Samba,
greetd, NetworkManager connections, ufw rules, sudoers.d, `/etc/systemd/system`,
avahi services, libvirt VM definitions, WireGuard, NoCapOS's own settings,
and the accounts. Accounts (`passwd`, `group`, `shadow`, `gshadow`) are
merged: system accounts come from the new image, people (UID 1000-59999),
their passwords and group memberships from the saved copy.

ZFS pools on extra disks: the module is in the image, the pool cache is kept
by `etc-sync`, and `zfs-import-cache.service` imports them at boot.

## Updates

See also [Signing](#signing).

1. Each release publishes, per target, a system update bundle
   (`nocapos-<target>.rugixb`) and a manifest (version, bundle hash, size,
   minimum previous version, channel).
2. Glow Up on an image install checks the manifest, installs the bundle into
   the inactive slot (Rugix verifies the signature or the bundle hash; with
   a block index it only downloads the blocks that changed), then offers
   "Restart to finish".
3. The restart boots the new slot once (Pi: firmware tryboot; PC: GRUB).
4. `nocapos-boot-check.service` checks NoCapOS answers, its database is up
   and NoCap Desktop's sign-in screen started (when the machine shows one).
   All good: `rugix-ctrl system commit`. Otherwise it restarts, and the old
   slot, still the default, comes back; Glow Up says the update was undone.
   A hang is caught by the watchdog, a kernel panic by `panic=10`: both
   restart into the old slot too.

apt is not used on the device: system packages only change with a release.

Channels: **stable** sees full releases; **beta** also sees pre-releases
(Glow Up > Update settings). Images record the channel they were built for.

### Versions

| Version | What |
|---|---|
| `0.14.0` | a NoCapOS release (tag `v0.14.0`) |
| `0.14.0-s1`, `-s2`, ... | security refreshes of that release: the same NoCapOS, newer Debian fixes (tag `v0.14.0-s1`, never marked "latest", so script installs are unaffected) |
| `0.15.0-beta.1` | a pre-release (beta channel) |

Order: `0.14.0-beta.N` < `0.14.0` < `0.14.0-s1` < `0.14.0-s2` < `0.14.1`
(`backend/internal/osimage`).

### Security updates from Debian

`.github/workflows/os-security.yml` runs every day (and by hand):

1. It reads the package lists (`nocapos-<target>.packages.tsv`) of the
   newest stable release with system images and compares them with Debian
   13's security archive, `trixie-updates` and point releases
   (`scripts/security-check.py`).
2. When a package has a security fix, it rebuilds that same release (its
   tag) from today's Debian snapshot, runs the QEMU tests (boot, update,
   rollback, installer) and publishes `vX.Y.Z-sN` with the images, bundles
   and a manifest marked `security`, listing each fixed package and its CVE
   IDs from the Debian changelog.
3. Glow Up shows it as a security update with those fixes. It installs when
   the owner presses the button, or by itself at the chosen time when
   "Install security updates automatically" is on (off by default).

Bundles carry a block index: a device downloads only the blocks that changed
(HTTP range requests), so a security refresh downloads far less than a full
image.

### What persists through an update

See [What persists](#what-persists): the data partition is never touched by
an update; `/etc` settings come back through `etc-sync`.

## Cutting a release

1. Bump `VERSION` in a pull request and merge it. The Release workflow
   publishes the script-install files (tarballs, `install.sh`, APK) as
   before.
2. When it finishes, `os-release.yml` sees a release without system images,
   builds them from that tag (`os.yml` with `release: true`: both targets,
   all QEMU tests), signs the bundles, and attaches:
   - `nocapos-pc.img.xz`, `nocapos-pc-usb.img.xz` (USB stick with the setup),
     `nocapos-pi.img.xz`;
   - `nocapos-pc.rugixb`, `nocapos-pi.rugixb` (update bundles);
   - `nocapos-os.json` (the update manifest);
   - `nocapos-<target>.packages.tsv` (what's inside).
3. It refreshes the APT repository (release `apt`).

A pre-release (beta) gets the same treatment and shows up on the beta
channel. To redo a release's images: run "NoCapOS image release" by hand
with `force`.

## The installer

The PC USB stick (`nocapos-pc-usb.img.xz`, made by `scripts/make-usb.sh`) is
the PC image plus a flag on its startup partition: instead of NoCapOS it
starts `nocapos-installer.service` (`backend/cmd/nocap-installer`, on
`127.0.0.1:8790` only) and a kiosk session (`cage` + `nocap-kiosk`, a
WebKitGTK window) showing the NoCap web UI's `/installer` page. It asks:

1. Language & region (time zone and keyboard follow the region);
2. Where NoCapOS goes (disk cards with what's on each disk now, the new
   layout, "Lock my drive" with the security chip or a passphrase);
3. The owner's account and the computer name;
4. How NoCapOS is used: desktop + web access, desktop only, web only;
5. Review ("Erase and install" needs "I understand" ticked);
6. Installing, then "Restart now".

It writes the A/B layout itself (the stick's own startup, boot and system
partitions; an empty system B; a data partition filling the rest, with LUKS2
when locked) and leaves the answers on the new data partition. NoCapOS's
first start applies them (account, name, time zone, keyboard, usage) and
destroys that file.

The Raspberry Pi image needs no installer: flash it, and the first-run
setup in the browser makes the owner's account and asks the same "How will
you use NoCapOS?" question.

### How NoCapOS is used

| Mode | Screen | Other devices |
|---|---|---|
| Desktop + web access | NoCap Desktop | NoCapOS in the browser |
| Desktop only | NoCap Desktop | kept out: connections from the network are closed on arrival; NoCapOS answers this machine only |
| Web only | not started | NoCapOS in the browser |

Switch any time in Settings > This Screen & Access.

## NoCapOS APT repository

NoCapOS's own packages (`nocapos-core`, `nocapos-image`, `nocapos-branding`,
`nocapos-base`, `nocapos-installer`, later `nocap-shell`) are published as a
flat, signed APT repository: the assets of the `apt` release.

```
deb [signed-by=/usr/share/keyrings/nocapos.gpg] https://github.com/shatheitguy/nocapos/releases/download/apt ./
```

Image installs never use it (they update by whole images). Script installs
may later switch to it for `nocapos-core`; `deploy/install.sh` doesn't yet.

## Image size

The base PC system (squashfs) is about 2.5 GiB and the Pi system about
3 GiB. The largest parts are the GPU/Wi-Fi firmware, the VM stack (qemu,
libvirt, OVMF), Docker, ffmpeg and WebKitGTK. Follow-up: move the VM stack
(and maybe Remote Desktop and Samba) into optional signed system extensions
(systemd-sysext images built from the same snapshot), turned on from the UI
the first time a feature is used.

## Signing

Bundles are signed with X.509/CMS (Rugix). The owner adds three repository
secrets:

| Secret | What |
|---|---|
| `NOCAPOS_UPDATE_ROOT_CERT` | the root certificate (PEM). Built into images; they then only accept bundles signed under it |
| `NOCAPOS_UPDATE_SIGNING_CERT` | the signing certificate (PEM), issued by the root |
| `NOCAPOS_UPDATE_SIGNING_KEY` | the signing key (PEM) |

Without them CI builds unsigned bundles (with a warning) and devices verify
each bundle by the hash in the release manifest, fetched over HTTPS.

Making the keys (once, offline):

```
openssl ecparam -genkey -name prime256v1 -out root.key
openssl req -x509 -new -key root.key -subj "/CN=NoCapOS Update Root" -days 7300 -out root.crt
openssl ecparam -genkey -name prime256v1 -out signer.key
openssl req -new -key signer.key -subj "/CN=NoCapOS Update Signer" -out signer.csr
printf 'basicConstraints=CA:FALSE\nkeyUsage=digitalSignature\n' >signer.ext
openssl x509 -req -in signer.csr -CA root.crt -CAkey root.key -CAcreateserial -days 1825 -extfile signer.ext -out signer.crt
```

Keep `root.key` offline; it is only needed to issue a new signing
certificate.

The APT repository is signed with OpenPGP:

| Secret | What |
|---|---|
| `NOCAPOS_APT_SIGNING_KEY` | an armored private key without a passphrase (`gpg --quick-gen-key "NoCapOS APT" ed25519 sign never`, then `gpg --armor --export-secret-keys`) |

Without it the repository is published unsigned, with a warning.

## Existing installs: a later one-time move

Script installs (Debian or Raspberry Pi OS plus `deploy/install.sh`)
stay as they are: Glow Up updates them with the install script,
as today. A later, one-time move to the image OS, keeping data:

1. Glow Up offers "Move to NoCapOS (image)" only when the machine qualifies
   (Pi 4/5 or a UEFI PC, enough free space on another disk or a USB drive
   for a backup).
2. NoCapOS exports its data folder (`/var/lib/nocapos`), `/srv/nocapos`,
   Docker volumes and the accounts to a backup archive on another disk.
3. The image is written (PC: by the installer from USB; Pi: flashed to the
   card), and the first-run setup offers "Restore from a NoCapOS backup",
   which reads that archive into the data partition.
4. Accounts come back through `etc-sync`'s store; Docker apps are reinstalled
   from their saved stacks with their volumes.

Script installs may later switch their own packages to the NoCapOS apt
repository (the same `nocapos-core` package); `deploy/install.sh` is not
changed for now.
