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
| `nocap-shell` | NoCap Desktop's own programs: packaged when `NOCAP_SHELL_DIST` points at NoCap Desktop's build output (hook in `build-debs.sh`) |

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

## Existing installs: a later one-time move

Script installs (Debian or Raspberry Pi OS plus `deploy/install.sh`)
owner's Pi) stay as they are: Glow Up updates them with the install script,
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
