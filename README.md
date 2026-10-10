# NoCapOS

[![Website](https://img.shields.io/badge/website-nocapos-ff2d3d)](https://shatheitguy.github.io/nocapos/)
[![Latest release](https://img.shields.io/github/v/release/shatheitguy/nocapos?color=ff2d3d)](../../releases/latest)
[![CI](https://github.com/shatheitguy/nocapos/actions/workflows/ci.yml/badge.svg)](https://github.com/shatheitguy/nocapos/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

**Freedom to do more.** NoCapOS turns any Linux server into a personal cloud with a desktop in your
browser — like umbrelOS or OpenMediaVault on top of Raspberry Pi OS, but with a full windowed desktop.
It installs on top of your existing Linux, manages the machine as root, and runs apps in Docker:
files, photos, backups, RAID storage, network sharing and one-click apps, all in a frosted-glass desktop.

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
Already downloaded a bundle? `tar -xzf nocapos-linux-arm64.tar.gz && sudo bash nocapos-*/install.sh`.

## What you get

- **A desktop in the browser** — a frosted *Glass* look with 10 wallpapers (the accent and the glass app
  icons follow the wallpaper), a home screen with your installed apps, dock, Launchpad, windows, widgets
  and a lock screen. Classic and *Cyber-Deck* themes too.
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
- **Network drives & sharing** — connect SMB/NFS shares and share folders over SMB and WebDAV, inside Files.
- **Cloud Imports** — bring files in from Google Drive, Dropbox, OneDrive and more (rclone).
- **Docker** — edit any container (ports, folders, macvlan/ipvlan networks with fixed IPs, environment,
  limits), create networks and run Compose stacks.
- **Your server, managed** — sign in with the machine's Linux accounts, users and roles, Wi-Fi and network
  (DHCP/static with automatic rollback), software update check, restart and shut down.
- **Terminal, Monitor, Remote Desktop, Brave, Scripts and an AI Assistant** (local Ollama or cloud).
- **Security** — argon2id passwords, two-factor sign-in, rotating sessions, an audit log of every admin
  action and a strict Content-Security-Policy.

## Screenshots

| | |
|---|---|
| ![Launchpad](docs/screenshots/launchpad.jpg) **Launchpad** — glass icons for NoCapOS apps, real icons for installed apps | ![Files](docs/screenshots/files.jpg) **Files** — favorites, recents, network devices and quick look |
| ![Photos](docs/screenshots/photos.jpg) **Photos** — timeline, On this day, albums and favorites | ![App Store](docs/screenshots/app-store.jpg) **App Store** — featured apps, categories and updates |
| ![Settings](docs/screenshots/settings.jpg) **Settings** — Network, General, Personalization, Security and more | ![Storage](docs/screenshots/storage.jpg) **Storage** — disk health and ZFS pools (sample data) |
| ![Lock screen](docs/screenshots/login.jpg) **Lock screen** — your photo over the wallpaper | ![Cyber-Deck](docs/screenshots/cyber-deck.jpg) **Cyber-Deck** — an optional sci-fi theme |

## Updating

Run the installer again — it upgrades in place and keeps your accounts, settings and files. Every push
to `main` is built by GitHub Actions and published under [Releases](../../releases), so `releases/latest`
always has the newest build.

## Build from source

Requirements: Go 1.25+, Node 20+.

```powershell
.\build.ps1                                   # UI + backend\bin\alfad.exe for this machine
.\package.ps1 -Version 0.3.0                  # Linux bundles in dist\ (amd64, arm64, armv7) + SHA256SUMS
```

```bash
cd frontend && npm install && npm run build   # builds the UI into backend/web/dist
cd ../backend && CGO_ENABLED=0 go build -o bin/alfad ./cmd/alfad
bash scripts/package.sh 0.3.0                 # (from the repo root) Linux bundles in dist/
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

## About the developer

<img src="https://avatars.githubusercontent.com/u/61654902?v=4&s=120" width="72" align="left" alt="Sharqan Ahamed" />

**Sharqan Ahamed, Sha The IT Guy**: Senior IT Infrastructure Engineer and Cloud & Cybersecurity Strategist, Dubai, UAE.
NoCapOS is the home server I wanted: every tool I reach for, on hardware I own, behind one clean desktop.

[shatheitguy.in](https://shatheitguy.in) · [GitHub](https://github.com/shatheitguy) · [LinkedIn](https://ae.linkedin.com/in/sharqan-ahamed-8555b8169) · [YouTube](https://www.youtube.com/@shatheitguy) · [X](https://x.com/Sha_The_IT_Guy)

Also by me: [ALFA Launcher](https://github.com/shatheitguy/alfa-launcher), a sci-fi Android home screen, and [IT-Vault](https://github.com/shatheitguy/it-vault), a self-hosted IT asset register and helpdesk.

## License

NoCapOS is open source under the [MIT License](LICENSE). © 2026 Sharqan Ahamed (Sha The IT Guy).
