# NoCap Shell: NoCap OS on the machine's own screen

NoCap Desktop is NoCap OS itself, running natively. `labwc` is the
compositor (windows, decorations for other apps, keybindings). NoCap Shell,
the native host `nocap-shell` (C, `shell/host`), shows the **same NoCap OS
build as the browser** (`frontend/`, entry `shell.html` →
`src/native/main.tsx`, `npm run build:shell`, installed to
`/usr/share/nocapos/shell`) in WebKitGTK views. The same React components
run there (menu bar, dock, launcher, panels, notifications, widgets,
windows), so the screen and the browser can't drift apart
(`frontend/e2e/parity.spec.ts` checks this pixel by pixel).

Programs: `/usr/libexec/nocapos/{nocap-shell,nocap-lock,nocap-session,nocap-greeter-session,nocap-lock-start,nocap-open}`.
Session services: `/etc/systemd/user/{nocap-session.target,nocap-shell.service,nocap-idle.service}`.
labwc settings: `/usr/share/nocapos/labwc` (sessions), `/usr/share/nocapos/greeter/labwc` (sign-in screen).

## Surfaces

| surface | kind | what |
|---|---|---|
| `desktop` | layer-shell, bottom, full screen | wallpaper, widgets, sticky notes, desktop icons, the desktop menu |
| `chrome` | layer-shell, top, full screen, see-through | menu bar, dock, launcher, search, Control Center, notifications, dialogs |
| `polkit` | layer-shell, overlay, on demand | the admin password prompt |
| `greeter` | layer-shell, top (`nocap-shell --greeter`) | the sign-in screen, in greetd as its own account |
| `app` | a normal window (`nocap-shell --app ID [--props JSON] [--single]`) | one NoCap OS app, with the browser's own title bar (client-side) |

The chrome surface takes clicks only where the page draws something: the
page reports those rectangles (`surface.input`), and the room the menu bar
and dock keep free for maximized windows (`surface.zones`, invisible
spacer surfaces with exclusive zones). A full-screen part (the launcher,
search, a dialog) makes the whole surface take input and the keyboard.

With the menu bar layout, the menu bar slides away while the screen's
active window (`windows.changed`, any app) is maximized, the same as in the
browser (`useMenubarAuto`): the page sends `surface.zones` with `top: 0`, so
labwc gives the maximized window the full height (it re-applies maximized
geometry when the usable area changes), and the input region keeps only a
3 px strip along the top. When the pointer reaches it, or while the bar is
hovered, focused or has a menu open, the bar's whole box takes input again
and the bar peeks back over the window, without changing the zone. The zone
comes back when the window is restored, minimized or closed, or a window
that isn't maximized becomes active (while no window is active, e.g. the
bar's own menu has the keyboard, the last active one still counts).

Manual check (the headless smoke run has no session, so no menu bar):
title buttons on the left, open any app and maximize it. The bar slides up
and the window's top edge reaches y = 0; touch the top edge and the bar
peeks back over the window (the window doesn't move); open a bar menu and
it stays; restore, minimize or close the window and the bar is back with
the window below it.

URL: `nocap-shell://shell/shell.html?surface=<name>` (`&app=&props=` for apps).

## Nothing remote, ever

- Pages come from the `nocap-shell://` scheme (local files under the bundle
  folder only, with a strict CSP: own scripts only, no inline code).
- The page's requests to `/api/…`, `/dav/…` and `/healthz` go through the
  host to alfad's loopback-only listener (`DesktopAddr`, normally
  `http://127.0.0.1:8780`). Live connections (WebSockets) go straight to
  that listener, with a ticket from an authenticated request.
- Every other network address goes to a dead proxy; the CSP says the same.
- Navigation away from the bundle, new windows, permissions, WebKit's own
  context menu, page zoom and back/forward gestures are all refused.

## The session

alfad serves `/run/nocapos/shell.sock` (HTTP on a Unix socket). The kernel
says who calls (SO_PEERCRED): the Linux account, and the program, which
must be `/usr/libexec/nocapos/nocap-shell`; root never gets a session.

- `POST /v1/shell/session` `{refresh?}` → `{user, realName, admin, apiOrigin,
  access_token, token_type, expires_at, refresh_token, account}`.
  The host keeps the refresh token; pages get the rest (`session.get`) and
  ask for the next one before it ends (`session.refresh`).

## Bridge

Page → host: `window.webkit.messageHandlers.nocap.postMessage(JSON string)`
with `{"id": <int>, "method": "<name>", "params": {...}}`.

Host → page: `window.__nocap.reply(id, ok, data)` and
`window.__nocap.event(name, payload)`.

Messages over 64 KiB, unknown methods, methods not allowed for the surface,
unknown parameters, wrong types or out-of-range values are refused with
`ok=false` and nothing happens (`host/bridge.c`, tests in `test/`).

### Methods (surfaces in brackets; "os" = desktop, chrome, app)

- `shell.info` [all] → `{surface, user, realName, voice, theme, version}` (+ `app`, `props` for apps)
- `session.get`, `session.refresh` [os] → the web session (above)
- `surface.input` `{rects: [x,y,w,h,…] (≤256 rectangles), full?}` [chrome]
- `surface.zones` `{top?, bottom?, left?, right?: 0..600}` [chrome]
- `surface.keyboard` `{mode: none|on-demand|exclusive}` [chrome, desktop]
- `ui.command` `{name: launcher|spotlight|center|notifications}` [os] → `ui.command` event to the chrome
- `nocap.open` `{app: [a-z0-9]{1,32}, props?: JSON text ≤ 8 KiB, single?}` [os] → an app window
- `apps.list`, `apps.launch` `{id: *.desktop}` [chrome, desktop] (installed Linux apps)
- `windows.list`, `windows.focus|minimize|close|maximize` `{id}` [chrome] (the screen's windows)
- `window.action` `{action: minimize|maximize|restore|close|move|resize-<edge>|fullscreen|unfullscreen}`, `window.title` `{title}` [app]
- `audio.get/set`, `brightness.get/set`, `network.get`, `power.action` [chrome] (`power.action` reboot/poweroff on the greeter)
- `notify.list|dismiss|invoke|clear|read` [chrome] (other apps' desktop notifications)
- `polkit.respond|cancel` [polkit]; `greeter.users|login|respond|cancel` [greeter]

### Events

`windows.changed`, `ui.command` `{name, action}`, `osd.show`, `audio.changed`,
`brightness.changed`, `network.changed`, `notify.*`, `polkit.request`,
`polkit.done`, `apps.changed`, `settings.changed`, `app.props` (app),
`window.state` `{maximized, fullscreen, focused, tiled}` (app).

## Control

`nocap-shell --toggle|--show|--hide launcher|spotlight|center|notifications`
and `--osd volume-up|…` talk to the running host over the session bus
(`io.nocapos.Shell`, `Control(s action, s arg)`), so labwc's keybindings can
drive NoCap OS's panels.

## Keeping the screen awake

NoCap Shell owns `org.freedesktop.ScreenSaver` on the session bus
(`/org/freedesktop/ScreenSaver` and `/ScreenSaver`): `Inhibit(s app, s
reason) → u cookie`, `UnInhibit(u)`, `GetActive() → b` (the lock screen is
up), `Lock()`, plus no-op `SimulateUserActivity` and `GetActiveTime`.
Cookies belong to the bus client that asked (only it can release them) and
all of its cookies go when it leaves the bus; at most 16 per client, 64 in
all. While any is held, the shell keeps one Wayland idle inhibitor
(`zwp_idle_inhibit_manager_v1`) on a 1×1 invisible background surface;
labwc then holds back ext-idle-notify, so swayidle's idle lock
(`nocap-idle.service`) waits. The lock before sleep isn't affected. The
desktop portal's Inhibit goes to the gtk backend (`nocap-portals.conf`),
which calls `org.freedesktop.ScreenSaver` (idle only: it refuses requests
that also ask to block suspend or logout). Apps using Wayland idle inhibit
directly (players in full screen) need nothing from the shell.

## Staying up

- A web view whose process crashed, or whose page didn't load, reloads:
  after 1 s for the first three times in two minutes, then 5 s, then every
  30 s, never giving up. Each web process cleans up early above a third of
  the memory (512 MB to 3 GB).
- `nocap-shell.service` restarts on failure with backoff (2 s up to 30 s)
  and no start limit; its web processes are first in line if memory runs
  out (`OOMScoreAdjust=200`).
- `nocap-lock-start` returns once the screens are locked (at most 5 s) and
  starts `nocap-lock` again if it ends without an unlock; labwc keeps the
  screens covered meanwhile (an abandoned lock stays locked), so the lock
  never fails open.
- A screen that goes away and comes back gets the surfaces and the
  menu bar/dock zones back.

## Never as root

`nocap-shell`, `nocap-lock` and the session scripts refuse to run as root;
the sign-in screen runs as greetd's own account and only lets the people's
own accounts sign in.
