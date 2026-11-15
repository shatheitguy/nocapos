# NoCap Shell: surfaces, bridge and local API

NoCap Desktop on the physical screen is `labwc` (the compositor: windows,
decorations, keybindings) plus NoCap Shell: our own surfaces drawn with
wlr-layer-shell. Each surface is a page of the shell bundle (built from
`frontend/`, `npm run build:shell`, installed to `/usr/share/nocapos/shell`)
shown in a WebKitGTK view by the native host `nocap-shell` (C, `shell/host`).

Nothing remote is ever loaded: pages come from the `nocap-shell://` scheme
(local files only, strict CSP), every network load is refused, and the page
reaches the system only through the bridge below. The host validates every
call against a whitelist (method allowed for that surface, every parameter
type-checked and bounded) before doing anything.

## Surfaces

| surface | layer | where | shown |
|---|---|---|---|
| `wallpaper` | background | full screen | always (drawn natively, no web view) |
| `bar` | top | top edge, 32 px, exclusive zone | always |
| `dock` | top | bottom edge, centred, 76 px, exclusive zone | always |
| `launcher` | overlay | full screen, keyboard exclusive | on demand (Super, dock button) |
| `center` | overlay | right edge under the bar, 380 px wide, keyboard on-demand | on demand (bar: clock/status) |
| `banners` | overlay | top right, 380 px wide, height follows content | while a notification banner shows |
| `osd` | overlay | bottom centre, 260 x 72 | volume/brightness changes, 1.5 s |
| `polkit` | overlay | full screen, keyboard exclusive | while an admin password is asked |
| `greeter` | top | full screen, keyboard exclusive | the sign-in screen (`nocap-shell --greeter`) |

URL: `nocap-shell://shell/shell.html?surface=<name>` (the page renders that
surface only). All views of one host share one web process.

## Bridge

Page → host: `window.webkit.messageHandlers.nocap.postMessage(JSON string)`
with `{"id": <int>, "method": "<name>", "params": {...}}`.

Host → page (via `evaluate_javascript`):
- reply: `window.__nocap.reply(id, ok, data)` (`data` is the result, or
  `{"error": "<message>"}` when `ok` is false);
- events: `window.__nocap.event(name, payload)`.

Messages over 64 KiB, unknown methods, methods not allowed for the surface,
unknown parameters, wrong types or out-of-range values are refused with
`ok=false` and nothing happens.

### Methods (allowed surfaces in brackets; `*` = every session surface, not the greeter)

- `shell.info` [all] → `{surface, user, realName, voice: "genz"|"standard", theme: "dark"|"light", version}`
- `surface.show` / `surface.hide` / `surface.toggle` `{name: "launcher"|"center"|"osd"}` [*]
- `surface.size` `{height: 1..2000}` [banners] (the banner surface's height follows its content)
- `apps.list` [launcher, dock] → `[{id, name, comment, icon, categories: [..], keywords: [..]}]`
  (`id` is the desktop file name, e.g. `org.gnome.Nautilus.desktop`; `icon` is a
  `nocap-shell://icon/<name>?size=<px>` URL or `""`)
- `apps.launch` `{id}` [launcher, dock, center] — only an id from `apps.list`
- `dock.get` [dock, launcher] → `{pinned: [id..]}` ; `dock.set` `{pinned: [id..] (max 24)}` [dock, launcher]
- `windows.list` [dock, bar] → `[{id, appId, title, active, minimized, maximized}]`
- `windows.focus` / `windows.minimize` / `windows.close` / `windows.maximize` `{id}` [dock, bar]
- `audio.get` [center, bar, osd] → `{available, volume: 0..100, muted}` ; `audio.set` `{volume?: 0..150, muted?: bool}` [center]
- `brightness.get` [center, osd] → `{available, percent}` ; `brightness.set` `{percent: 1..100}` [center]
- `network.get` [center, bar] → `{state: "online"|"connecting"|"offline", kind: "wifi"|"ethernet"|"other"|"none", name, strength: 0..100}`
- `power.action` `{action}` [center, bar: `lock|logout|suspend|reboot|poweroff`; greeter: `reboot|poweroff`]
- `notify.list` [center, banners, bar] → `[{id, source: "app"|"nocapos", app, icon, title, body, time, urgency: 0..2, actions: [{key, label}], read}]`
- `notify.dismiss` `{id}` ; `notify.invoke` `{id, key}` ; `notify.clear` ; `notify.read` [center, banners]
- `nocapos.open` `{app?: "settings"|"appstore"|"files"|"home"}` [center, bar, launcher, dock] — opens NoCapOS (local) in an app window
- `polkit.respond` `{cookie, password}` ; `polkit.cancel` `{cookie}` [polkit]
- `greeter.users` [greeter] → `[{name, realName}]`
- `greeter.login` `{username}` / `greeter.respond` `{response?: string}` / `greeter.cancel` [greeter]
  → `{state: "secret"|"visible"|"info"|"error"|"done"|"failed", message}`;
  on `done` the host starts the NoCap session and exits.

### Events

`windows.changed` (the list), `audio.changed`, `brightness.changed`,
`network.changed`, `notify.added` (item), `notify.closed` `{id}`,
`notify.unread` `{count}`, `surface.visible` `{name, visible}`,
`osd.show` `{kind: "volume"|"brightness", value, muted}`,
`polkit.request` `{cookie, message, icon, user}`, `polkit.done` `{cookie, ok}`,
`apps.changed`, `settings.changed` `{voice, theme}`.

## Control

`nocap-shell --toggle launcher|center`, `--show <surface>`,
`--osd volume-up|volume-down|volume-mute|brightness-up|brightness-down`
talk to the running host over the session bus (`io.nocapos.Shell`,
`/io/nocapos/Shell`, method `Control(s action, s arg)`), so labwc's
keybindings can drive the shell.

## alfad (local API)

The host talks to alfad over `/run/nocapos/shell.sock` (HTTP/1.1 on a Unix
socket; alfad identifies the Linux account with SO_PEERCRED, so nothing
another account runs can act as this one). Every request after the first
carries `X-NoCap-Session: <token>` from `POST /v1/shell/session` (a token per
desktop session, dropped when alfad restarts; the host asks again on 401).

- `POST /v1/shell/session` → `{token, user, realName, admin, voice, theme, webURL}`
- `GET /v1/shell/notifications?after=<id>&wait=<0..30>` → `{items: [...], unread, cursor}`
  (long poll; NoCapOS notifications this account may see; items use the
  notify.list shape with `source: "nocapos"`)
- `POST /v1/shell/notifications/read` `{ids: [..]}` / `{all: true}`

Surfaces never see the token or the socket.
