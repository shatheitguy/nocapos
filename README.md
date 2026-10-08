# Alfa OS

A Docker-native Agent OS with a desktop-in-the-browser UX (Synology DSM feel, ZimaOS lightness).
The host stays minimal (Linux + Docker); everything else runs in containers orchestrated by `alfad`.

**Status:** backend daemon core ✅ · Web OS desktop shell ✅ · App Store, storage/NAS and agent framework next.

The desktop (React + TypeScript + Vite, in `frontend/`) is a ChromeOS/ZimaOS-style environment:
draggable/resizable windows with snap-to-edge and maximize, a shelf with launcher, pinned apps and
running indicators, a searchable launcher (`Ctrl+Space`), quick settings (theme, accent, lock, sign out),
desktop widgets, toasts, lock screen, light/dark themes and wallpapers, and per-user window layouts
that survive reloads. Apps: Resource Monitor, Containers (live stats, logs, start/stop/restart),
App Center (catalog preview), Settings, and **Files** (below). On phones, windows open full screen.

### Files

A NAS-style file manager: storage locations with free space, breadcrumbs, back/forward/up, grid view
with image thumbnails or sortable list view, multi-select (Ctrl/Shift), right-click menus, keyboard
shortcuts (Enter, F2, Del, Shift+Del, Backspace, Ctrl+A/C/X/V), drag-and-drop upload with progress,
cut/copy/paste, rename, new folder, and a per-location **Recycle Bin**. Double-click opens the viewer:
images (fit / 100%), video and audio (with seeking), PDF (browser tab), and a text/code editor with
Ctrl+S save and conflict detection.

Storage locations come from `ALFA_FILE_ROOTS="Name=path,Other=path"`. Default: **Alfa Drive**
(`<data>/files`) plus **Home** on Windows/macOS. In Docker, bind-mount host folders into alfad and list
them, e.g. `- /srv/media:/storage/media` with `ALFA_FILE_ROOTS=Media=/storage/media`.
Upload limit per request: `ALFA_MAX_UPLOAD_MB` (default 16 GiB).

```
Browser ──HTTPS──► Traefik :443 ─┬─ /api, /ws ──► alfad (Go, :8080) ──► /var/run/docker.sock
                     │           └─ (next) /    ──► alfa-ui (SPA)          ▲ sole socket holder
                     └── tcp:2375 ──► socket-proxy (GET-only, internal net) ┘ read-only view
```

## Layout

```
backend/                  alfad — Go 1.25, CGO-free, single static binary
  cmd/alfad               entrypoint: serve | healthcheck | version
  internal/config         env configuration
  internal/store          SQLite (pure Go) + embedded migrations: users, refresh tokens, audit log
  internal/auth           argon2id, JWT access tokens, rotating refresh tokens, rate limit, WS tickets
  internal/docker         minimal Engine API client (containers, images, stats, logs, events)
  internal/hardware       procfs/sysfs telemetry + GPU/NPU/TPU discovery for every device class
  internal/ws             multiplexed WebSocket hub (ref-counted topic feeds, backpressure)
  internal/api            REST handlers, middleware, topic resolver
  Dockerfile              buildx multi-arch → distroless, nonroot
deploy/
  docker-compose.yml      traefik + socket-proxy + alfad
  docker-compose.nvidia.yml  overlay for NVIDIA Container Toolkit hosts
```

## Install on Linux (recommended target)

Alfa OS targets Linux hosts: any PC, mini-PC, server or ARM board (Raspberry Pi 4/5, Rockchip,
Jetson) running Debian, Ubuntu, Raspberry Pi OS, Fedora or Arch. Build the release bundles on your dev
machine with `.\package.ps1` (→ `dist/alfaos-<version>-linux-{amd64,arm64,armv7}.tar.gz` +
`SHA256SUMS`), copy the one for your device, then:

```bash
tar -xzf alfaos-0.1.0-linux-arm64.tar.gz && cd alfaos-0.1.0-linux-arm64
sudo bash install.sh              # Docker mode: Traefik + HTTPS on 80/443
```

`sudo bash install.sh --native` installs a hardened systemd service instead (built-in self-signed
HTTPS on 443 via `ALFA_TLS=auto`). Options: `--storage /path` (default `/srv/alfaos`),
`--install-docker`, `--yes`. Remove with `--uninstall` (add `--purge` to delete accounts/settings;
storage is never deleted). The installer prints the URL and the first-run setup token.

## Run as a normal app (no Docker needed)

```powershell
.\build.ps1                      # builds frontend → embeds it → backend\bin\alfad.exe
.\backend\bin\alfad.exe          # then open http://127.0.0.1:8080
```

Frontend development with hot reload: run `alfad.exe`, then `cd frontend; npm run dev` and open
http://localhost:5173 (API and WebSocket calls are proxied to alfad). The built UI in `backend/web/dist`
is committed, so Go/Docker builds don't need Node.

First run prints a `setup_token` in the console; enter it on the setup screen to create the admin.
Data lives in `%APPDATA%\AlfaOS`. Docker is optional — container features light up automatically when
Docker Desktop is running. Cross-compile for other devices with `.\build.ps1 -Target linux/arm64`
(or `linux/amd64`, `linux/arm/v7`).

## Quick start (Linux host, Docker)

```bash
cd deploy
cp .env.example .env
sed -i "s/^DOCKER_GID=.*/DOCKER_GID=$(stat -c %g /var/run/docker.sock)/" .env
docker compose up -d --build
docker compose logs alfad | grep setup_token      # one-time admin setup token
```

Create the administrator (the UI will do this; until then use curl — `-k` because Traefik serves a self-signed cert):

```bash
curl -k -X POST https://<host>/api/v1/auth/setup -H 'Content-Type: application/json' \
  -d '{"setup_token":"<token>","username":"admin","password":"<10+ chars>"}'
```

Development without Docker (Linux): `make build && ALFA_DATA_DIR=./data ALFA_COOKIE_SECURE=false ./backend/bin/alfad`.

## Design decisions

| Concern | Choice | Why |
|---|---|---|
| Daemon | Go, hand-rolled Engine API client | Tiny static binary for ARM boards; no Moby SDK churn; exact control of which endpoints exist |
| Transport | REST for commands + one multiplexed WebSocket | Browsers can't speak native gRPC; SSE is one-way (terminal PTY needs duplex) |
| Docker access | Sibling containers, **no DinD** | alfad is the only socket holder; Traefik gets a GET-only socket-proxy on an internal network |
| Auth | argon2id + 15 min JWT + rotating httpOnly refresh cookie | Refresh reuse → whole session revoked; logout/disable take effect immediately (session checked per request) |
| First-run | Setup token printed to the log | Stops anyone on the LAN from claiming the box before the owner |
| Storage | SQLite (modernc, pure Go, WAL) | CGO-free cross-compile to amd64/arm64/armv7 |
| Telemetry | Direct `/proc` + `/sys` parsing | Same code on x86, Pi, Rockchip, Jetson; no dependency |

## REST API (`/api/v1`)

| Method & path | Auth | Notes |
|---|---|---|
| `GET /auth/status` | — | `{setup_required}` |
| `POST /auth/setup` | setup token | creates first admin, returns session |
| `POST /auth/login` | — | `{username,password}` → `{access_token,expires_at,user}` + refresh cookie; 5 tries then 1/12 s per IP |
| `POST /auth/refresh` | cookie + `X-Alfa-Request: 1` | rotates refresh token |
| `POST /auth/logout` | cookie + `X-Alfa-Request: 1` | revokes the session |
| `GET /auth/me` | bearer | |
| `POST /ws/ticket` | bearer | single-use 30 s ticket for `/ws?ticket=` |
| `GET /system/info` | bearer | host, Docker engine, accelerators (+ passthrough spec) |
| `GET /system/metrics` | bearer | latest snapshot |
| `GET /containers?all=false` | admin | |
| `GET /containers/{id}` | admin | env vars are never returned |
| `POST /containers/{id}/{start\|stop\|restart}` | admin | `alfa.system=true` containers can't be stopped; audited |
| `GET /images` | admin | |
| `GET /files/roots` · `GET /files/list?root=&path=` | admin | locations with capacity; folder listing |
| `GET/PUT /files/text` | admin | read (≤ 2 MB UTF-8) / atomic save with `mod_time` conflict check |
| `POST /files/{mkdir,rename,delete,transfer}` | admin | delete → Recycle Bin unless `permanent`; transfer = copy/move |
| `POST /files/upload?root=&path=` | admin | streaming multipart, rename on conflict |
| `POST /files/ticket` → `GET /files/raw?root=&path=&t=` | admin / ticket | 10-min link scoped to one file or folder, HTTP Range support |

Errors: `{"error":{"code":"...","message":"..."}}`.

## WebSocket protocol (`/ws?ticket=…`)

```jsonc
→ {"id":"1","op":"subscribe","topic":"system.metrics"}
← {"type":"ack","id":"1","topic":"system.metrics"}
← {"type":"event","topic":"system.metrics","data":{...}}
← {"type":"end","topic":"container.logs/web","error":"No such container: web"}
```

| Topic | Role | Payload |
|---|---|---|
| `system.metrics` | any | CPU (total/per-core/load), memory, disks, NICs with rates, temps, accelerators — every 2 s while watched |
| `docker.events` | admin | container/image/network/volume events (`exec_*` dropped: they contain command lines) |
| `container.stats/<id>` | admin | CPU %, memory, net/block I/O with rates, ~1/s |
| `container.logs/<id>` | admin | batches of `{stream,time,text}`; last 200 lines then follow |

Feeds start with the first subscriber and stop with the last. Clients that fall 256 messages behind are disconnected rather than slowing everyone else down.

## Hardware support

| Class | Detection | Live metrics | Container passthrough |
|---|---|---|---|
| NVIDIA dGPU | PCI | util, VRAM, temp, power, clock (via `nvidia-smi` with the nvidia overlay) | `DeviceRequests` driver `nvidia` |
| AMD dGPU/APU | PCI | util, VRAM, temp, power, clock (amdgpu sysfs) | `/dev/kfd` + `/dev/dri/*`, groups video/render |
| Intel iGPU/Arc | PCI | clock, temp (Arc) — util needs perf counters | `/dev/dri/*` |
| Raspberry Pi (V3D), Mali (Panfrost/Panthor/Lima), Adreno, Vivante, PowerVR | DRM platform devices | clock/load where devfreq exposes it | `/dev/dri/*` |
| Mali BSP kernels (Rockchip) | devfreq | load, clock | `/dev/mali0` |
| NVIDIA Jetson | devfreq | load, clock | runtime `nvidia` |
| NPUs: Intel NPU, AMD XDNA, Rockchip, Hailo-8/8L | accel / devfreq / chardev | — | device node |
| Google Coral (PCIe/M.2/USB) | apex class / USB IDs | — | `/dev/apex_*` or `/dev/bus/usb` |

Images: `linux/amd64`, `linux/arm64`, `linux/arm/v7`. Docker Desktop on Windows/macOS works for development (metrics describe the Docker VM).

## Security notes

- The socket mount is root-equivalent. Only alfad gets it; it runs as uid 65532 with a read-only rootfs, no capabilities and `no-new-privileges`.
- Refresh tokens are stored as SHA-256 hashes; the cookie is `HttpOnly; Secure; SameSite=Strict; Path=/api/v1/auth`.
- `X-Forwarded-For` is only honored from trusted proxy ranges, and only the entry Traefik appended is used.
- The request log omits query strings. Traefik's access log does record `/ws?ticket=…`, but tickets are single-use and expire in 30 s.
- Pin `TRAEFIK_IMAGE` / `SOCKET_PROXY_IMAGE` by digest for production.
- File access goes through Go's `os.Root`: `..`, absolute paths and symlinks pointing outside a storage
  location are refused by the OS-level lookup, not just string checks.
- Raw file responses never execute in the Alfa OS origin: only images, audio, video, PDF and plain text
  render inline; HTML/SVG/XML and everything else download as `application/octet-stream`, with a
  `sandbox` CSP and `nosniff`. File links are 10-minute tickets scoped to a single file or folder.

## Roadmap

1. **Web OS shell** — React + TS + Vite: window manager, desktop/taskbar, login/setup, Resource Monitor, container manager.
2. Terminal (exec + PTY over the WS hub), TOTP 2FA, local CA for `alfa.local` TLS, mDNS.
3. **App Store** — compose-based manifests, Traefik label routing, ForwardAuth SSO for apps.
4. **Storage/NAS** — File Station, volumes, SMB/NFS/WebDAV shares, snapshots.
5. **Agent framework** — sandboxed agent containers (optional gVisor), scoped tool API/MCP, accelerator scheduling using the passthrough specs above.
