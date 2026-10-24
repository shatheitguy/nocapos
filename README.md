# NoCapOS

[![Website](https://img.shields.io/badge/website-nocapos-ff2d3d)](https://shatheitguy.github.io/nocapos/)
[![Latest release](https://img.shields.io/github/v/release/shatheitguy/nocapos?color=ff2d3d)](../../releases/latest)
[![CI](https://github.com/shatheitguy/nocapos/actions/workflows/ci.yml/badge.svg)](https://github.com/shatheitguy/nocapos/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

**Freedom to do more.** NoCapOS turns any Linux server into a personal cloud with a full windowed
desktop in your browser. It installs on top of your existing Linux (Debian, Ubuntu, Raspberry Pi OS,
Fedora, Arch), manages the machine as root, and runs apps in Docker:
files, photos, backups, RAID storage, virtual machines, network sharing and one-click apps, all in a frosted-glass desktop.

🌐 **Website:** https://shatheitguy.github.io/nocapos/

![The NoCapOS home screen](docs/screenshots/desktop.jpg)

## Install

On Debian, Ubuntu, Raspberry Pi OS, Fedora or Arch (amd64, arm64, armv7):

```bash
curl -fsSL https://github.com/shatheitguy/nocapos/releases/latest/download/install.sh | sudo bash
```

The installer picks the build for your CPU, verifies its SHA-256 checksum, installs Docker if it is
missing (for the App Store), sets NoCapOS up as the `nocapos` systemd service and prints the address and
a one-time setup token. Open the address, enter the token and create your administrator.

| Option | |
|---|---|
| `--storage DIR` | folder for NoCap Drive (default `/srv/nocapos`) |
| `--port N` | HTTPS port (default 443) |
| `--no-docker` / `--install-docker` | skip / don't ask about installing Docker |
| `--docker` | run NoCapOS itself in Docker (apps only — no control of the server) |
| `--uninstall [--purge]` | remove NoCapOS; your files are never deleted |

Settings live in `/etc/nocapos/nocapos.env`; manage the service with `systemctl status|restart nocapos`.

**Uninstall** (your files are never deleted):

```
curl -fsSL https://github.com/shatheitguy/nocapos/releases/latest/download/uninstall.sh | sudo bash
```

Add `-s -- --purge` to also delete accounts, settings and keys, and `--remove-apps` to remove App Store
apps with their data. It also removes NoCapOS's Samba shares, network-drive mounts and the Samba
accounts it made (with `--purge`, also the login-less file-sharing accounts it created; real Linux
users are never deleted). Piping from curl runs the download unchecked: to verify it first, download
`nocapos-linux-<arch>.tar.gz` and `SHA256SUMS`, run `sha256sum -c SHA256SUMS --ignore-missing`,
extract it and run `sudo bash install.sh --uninstall`.
Already downloaded a bundle? `tar -xzf nocapos-linux-arm64.tar.gz && sudo bash nocapos-*/install.sh`.

## What you get

- **A desktop in the browser** — a frosted *Glass* look with 10 wallpapers and a Transparency slider, glossy
  app icons (Objects, Glass tiles or Shapes), a menu bar that sits on the wallpaper with your profile photo
  and account menu, a home screen with your installed apps, dock, Launchpad, windows, widgets and a lock
  screen. Classic and *Cyber-Deck* themes too.
- **Everyone gets their own space** — each person has a private Home (their own Linux home on a Linux
  install), sees only the shared folders and apps you allow, and keeps their own look. People can be
  *Desktop* users or *File sharing only*.
- **App Store** — one-click self-hosted apps (Jellyfin, Nextcloud, Open WebUI + Ollama, Vaultwarden,
  Syncthing, Gitea, code-server, n8n, …) with their own icons, updates ("Update all") and release notes.
  Click and hold an installed app to wiggle and remove it.
- **Files** — Home, Recents, Favorites, shared folders, network devices and external drives; quick look,
  compress and extract (zip, tar), Trash, and a *System* view where core OS folders are protected.
- **Photos** — a timeline by years, months and days, albums, favorites, a viewer with camera details and
  Recently Deleted.
- **Backups & Rewind** — encrypted, scheduled backups (restic) to a drive, SFTP or S3/B2, and bring back
  any file from any day.
- **Storage** — disk health (SMART), ZFS pools (mirror, RAIDZ1/2/3), datasets, snapshots and scrubs, or
  use a single disk as storage. Erasing always needs a typed confirmation.
- **Shared folders & network drives** — create shared folders in Files and give each person Read/Write, Read
  only or No access; they connect over SMB or WebDAV with their own user name and password. Connect SMB/NFS
  shares from other devices too.
- **People and their spaces** — everyone gets a private home (their own Linux home on a native install),
  shared folders with Read & write / Read only / No access per person, and a choice of which apps each
  person sees. People can be desktop users or file-sharing-only (SMB/WebDAV with their own password).
  Everything is checked on the server, and each person's desktop look stays their own.
- **Cloud Imports** — bring files in from Google Drive, Dropbox, OneDrive and more (rclone).
- **Virtual Desk** — run Windows, Linux and Android virtual machines (KVM): a four-step New VM wizard,
  live usage, snapshots, and the machine's screen in its own window.
- **Docker** — edit any container (ports, folders, macvlan/ipvlan networks with fixed IPs, environment,
  limits), create networks and run Compose stacks.
- **Your server, managed** — sign in with the machine's Linux accounts, users and roles, Wi-Fi and network
  (DHCP/static with automatic rollback), software update check, restart and shut down.
- **Notifications** — updates, app updates, app errors, storage and backups, as macOS-style banners with
  the menu-bar desktop or Windows-style toasts with the taskbar.
- **Terminal, Monitor, Remote Desktop, Brave, Scripts and an AI Assistant** (local Ollama or cloud).
- **Security** — argon2id passwords, two-factor sign-in, rotating sessions, an audit log of every admin
  action and a strict Content-Security-Policy.

## Screenshots

| | |
|---|---|
| ![Launchpad](docs/screenshots/launchpad.jpg) **Launchpad** — glossy icons for NoCapOS apps, real icons for installed apps | ![Files](docs/screenshots/files.jpg) **Files** — favorites, recents, network devices and quick look |
| ![Photos](docs/screenshots/photos.jpg) **Photos** — timeline, On this day, albums and favorites | ![App Store](docs/screenshots/app-store.jpg) **App Store** — featured apps, categories and updates |
| ![Settings](docs/screenshots/settings.jpg) **Settings** — Network, General, Personalization, Security and more | ![Storage](docs/screenshots/storage.jpg) **Storage** — disk health and ZFS pools (sample data) |
| ![Virtual Desk](docs/screenshots/virtual-desk.jpg) **Virtual Desk** — virtual machines with live usage (sample data) | ![New VM](docs/screenshots/virtual-desk-new.jpg) **New VM** — pick the system, ISO, resources and network |
| ![File Sharing](docs/screenshots/file-sharing.jpg) **File Sharing** — shared folders and who may use them, in Files | ![Lock screen](docs/screenshots/login.jpg) **Lock screen** — your photo over the wallpaper |
| ![Cyber-Deck](docs/screenshots/cyber-deck.jpg) **Cyber-Deck** — an optional sci-fi theme | |

## Updating

Run the installer again — it upgrades in place and keeps your accounts, settings and files. If the new
version doesn't start, the installer puts the previous version and its database back automatically. Every push
to `main` is built by GitHub Actions and published under [Releases](../../releases), so `releases/latest`
always has the newest build.

## Build from source

Requirements: Go 1.25+, Node 20+.

```powershell
.\build.ps1                                   # UI + backend\bin\alfad.exe for this machine
.\package.ps1 -Version 0.4.0                  # Linux bundles in dist\ (amd64, arm64, armv7) + SHA256SUMS
```

```bash
cd frontend && npm install && npm run build   # builds the UI into backend/web/dist
cd ../backend && CGO_ENABLED=0 go build -o bin/alfad ./cmd/alfad
bash scripts/package.sh 0.4.0                 # (from the repo root) Linux bundles in dist/
```

Run it locally with `backend/bin/alfad` and open `http://127.0.0.1:8080`; the first start prints the
setup token. For UI work, `npm run dev` in `frontend/` proxies API calls to a running alfad.

## Layout

```
backend/            alfad — Go daemon, single static binary with the UI embedded
  internal/api        REST + WebSocket API
  internal/appstore   App Store catalog and installer
  internal/accounts   NoCapOS or Linux accounts
  internal/files      storage locations, recycle bin, archives, protection of system folders
  internal/photos     photo library, albums, Recently Deleted
  internal/backup     restic backups and Rewind
  internal/storage    disks, SMART and ZFS pools
  internal/netdrive   SMB/NFS mounts and SMB/WebDAV sharing
  internal/spaces     homes, shared-folder permissions, per-person app access
  internal/cloudimport rclone cloud imports
  internal/stacks     Docker Compose stacks
  internal/hostctl    network, Wi-Fi, power
  internal/docker     minimal Docker Engine API client
  internal/store      SQLite with migrations
frontend/           React + TypeScript + Vite desktop
deploy/             installer, systemd unit, Docker Compose files
```

## Security notes

- Native installs run as root on purpose, so NoCapOS can manage the machine. Everything that changes
  the system is admin-only and written to the audit log; turn on two-factor sign-in.
- Files refuses to delete, move or rename NoCapOS's own folders and core OS folders.
- App Store apps run as containers with their own volumes; uninstalling keeps data unless you choose
  to delete it.
- File links are short-lived tickets; files that could run script (HTML, SVG) always download instead
  of rendering.
- Signing in with two-factor authentication on takes the password, then a code from the
  authenticator app; each code works once. After 10 wrong passwords or codes in a row an account
  waits 15 minutes, whichever address the attempts come from.
- There is no password reset from the sign-in screen. An administrator resets passwords in
  Settings → Users & Groups. If you are the only administrator and are locked out, run on the
  server itself:

  ```
  sudo nocapos reset-password <user>
  ```

  It prints a temporary password, turns two-factor authentication off for that account and ends its
  sessions. Restart the service (`systemctl restart nocapos`) to lift a lockout right away.
- Everyone is signed out when the machine restarts.
- The Terminal's host shell runs as your own Linux account (use `sudo` for administration);
  administrators can open a root shell from the Terminal after re-entering their password.
- Choosing who can use an App Store app controls who sees it in NoCapOS, not network access to the
  app's own port.

## About the developer

<img src="https://avatars.githubusercontent.com/u/61654902?v=4&s=120" width="72" align="left" alt="Sharqan Ahamed" />

**Sharqan Ahamed, Sha The IT Guy**: Senior IT Infrastructure Engineer and Cloud & Cybersecurity Strategist, Dubai, UAE.
NoCapOS is the home server I wanted: every tool I reach for, on hardware I own, behind one clean desktop.

[shatheitguy.in](https://shatheitguy.in) · [GitHub](https://github.com/shatheitguy) · [LinkedIn](https://ae.linkedin.com/in/sharqan-ahamed-8555b8169) · [YouTube](https://www.youtube.com/@shatheitguy) · [X](https://x.com/Sha_The_IT_Guy)

Also by me: [ALFA Launcher](https://github.com/shatheitguy/alfa-launcher), a sci-fi Android home screen, and [IT-Vault](https://github.com/shatheitguy/it-vault), a self-hosted IT asset register and helpdesk.

## License

NoCapOS is open source under the [MIT License](LICENSE). © 2026 Sharqan Ahamed (Sha The IT Guy).
