# NoCap OS (phone app)

The phone side of **NoCap Link**: a full home screen for Android that looks
and feels like the NoCapOS desktop, and links to your NoCapOS server.

## Home screen

- **Pages** of apps, folders and widgets. Long-press and drag to move things,
  drop an app on another to make a folder, drag to the edge for the next page,
  drag to the dock to pin, drag to *Yeet / Remove* at the top to take it off.
- **Dock** in the NoCapOS style: The Roster (app drawer), your apps (up to
  five) and NoCap Link, with a status dot for your server.
- **Android widgets** (long-press an empty spot → Widgets): add any app's
  widget, configure it, resize it, move it, remove it.
- **NoCap widgets** (glass, resizable, each with its own edit sheet —
  long-press → Customize): NoCap status (server *and* this phone: animated
  rings, network sparklines, updates waiting), server status, phone status,
  clock (NoCap glow, big digits, analog, stacked, minimal; next alarm),
  weather (Open-Meteo; your city, or rough location only if you allow it;
  tap for hourly + 7 days), calendar agenda (opt-in), now playing with
  controls (needs notification access), quick toggles (flashlight, Wi-Fi,
  Bluetooth, Do not disturb, internet, location, volume, display), notes and
  to-do lists, photo frame, app shortcuts grid, search bar (voice + AI),
  NoCap Link actions, *Ask NoCap AI* bar with suggestions, recent server
  files, and countdown / timer. The picker shows live previews and every
  size each widget comes in (1×1 up to 4×4).
- **Widgets for any home screen** (also in other launchers): NoCap clock,
  weather, NoCap status, NoCap Link actions, search, and Ask NoCap AI.
- **App drawer in six styles** (Settings → The Roster, with live sketches):
  NoCap Launchpad (paged grid), scrolling grid, A–Z list with a fast-scroll
  letter rail, categories (smart folders, plus your server's apps), a
  classic drawer (search, suggested apps, A–Z with a letter rail, and
  All / Recently installed / Hidden tabs), and *all apps on home pages*
  (new apps land on the last page; a page at the end (The Stash / App groups) sorts
  them into smart folders with search; swipe down searches). Sort A–Z, by
  most used or by newest; columns, background, blur and a recent-apps row.
- **Search** across phone apps, NoCap OS settings, Android settings, your
  server's apps and your server's files, with *Ask NoCap AI* for anything.
- **Long-press an app**: its shortcuts (while NoCap OS is your home app), add
  to home, pin to dock, app info, hide, uninstall.
- **Notification dots** (opt-in: needs notification access; only counts).
- **Settings you can search**, grouped like NoCapOS's own Settings:
  - *Look*: dark / light / follow the phone, accent color (NoCap red,
    presets or any color, or synced from your NoCap desktop), font, Gen Z or
    Standard wording.
  - *Home*: grid 3–7 columns × 4–9 rows, page transitions (slide, fade,
    zoom, cube, stack, carousel), folder style (grid or stack), search bar,
    page dots, lock the layout.
  - *Icons*: NoCap tiles (the default — every icon on a dark glass tile like
    the web desktop's, optional monochrome glyphs), original, themed
    (Android 13 monochrome icons in your accent), tinted or mono; shapes
    (original, circle, squircle, rounded square); size; label font, size,
    color and shadow; per-app custom name and icon (long-press → Edit icon
    and label); hide apps.
  - *Dock*: glass bar, floating or none; 3–7 apps; drawer and NoCap Link
    buttons; labels.
  - *Wallpaper*: dim, blur, parallax.
  - *Gestures*: swipe up/down, double-tap, pinch in/out, two-finger swipe
    up/down and the Home button (while home) can each open the drawer,
    search, NoCap AI, notifications, quick settings, lock the screen
    (opt-in helper), any app, your NoCap desktop, NoCap Link, settings,
    widgets or the first page.
  - *System*: status bar clear / tinted / hidden, navigation bar clear /
    tinted, rotation, haptics.
  - *Save Point*: back up and restore every setting with the layout; reset
    to defaults.
- **Wallpapers**: the three NoCapOS wallpapers (Tide, Nebula, Midnight), a
  classic grid, or a picture from your gallery; dim it, set it as the phone's
  own wallpaper too, or sync the one you picked on your NoCap desktop.
- **Language**: Gen Z (default) or Standard, like NoCapOS's own setting.
- **Backup & restore** of pages, folders, dock and settings — to your NoCap
  server (if linked) or a file.

## NoCap Link

- **Link with NoCap Link** — scan the QR from *Settings → Devices → Link a
  device*, tap a `nocap://pair` link, or type the address + 6-digit code.
- **Open my NoCap desktop** — your server's web desktop, full screen, signed
  in with a one-time ticket.
- **Files** — browse your locations and open a file on the phone.
- **Send to NoCap** — from any app's share sheet (or the hub): files, photos,
  links and text land in *NoCap Link* in your home folder.
- **NoCap AI** — chats with the server's AI Assistant as you (your own
  conversations, memory and rules), with streaming replies and voice input.
  Also a home-screen widget for any launcher.
- **Server notifications** on the phone (opt-in): live while NoCap OS runs
  and the screen is on (one long-poll, `GET /api/v1/device/events`), and
  otherwise checked with the heartbeat about every 15 minutes.
- **Phone notifications on NoCap OS** (opt-in, per app): they show in the
  web desktop's notification center as "From <phone>"; dismissing on either
  side dismisses on the other where Android allows. Needs notification
  access; only goes to your own server.
- **Shared clipboard** (opt-in): copy on NoCap OS → it's on the phone's
  clipboard. Phone → NoCap OS is sent when NoCap OS comes to the front, or
  from the *Send clipboard to NoCap* quick-settings tile — Android 10+ only
  lets the app in front read the clipboard, so there's no silent background
  copying.
- **Unlink** — removes the phone on the server too.
- **Older servers** — anything a server doesn't support yet says *Your NoCap
  server needs an update (Settings → Glow Up)* with the version it's running,
  instead of an error.

## Security

- The device token, the server address and the pinned certificate live in
  `EncryptedSharedPreferences` (key in the Android Keystore); backups are off.
  Launcher settings and the layout are plain app storage (nothing secret).
- HTTPS uses **trust on first use**: when you link, the app shows the
  server's certificate fingerprint (SHA-256) and pins it. After that, the
  API client and the desktop web view only accept that exact certificate.
  If it ever changes, the app stops and asks you to check it.
- Plain `http://` addresses work too (for a server on your own Wi-Fi), with a
  warning that the connection isn't encrypted.
- Device tokens only work on `/api/v1/device/*`: the server's summary, your
  saved look, your files (with your normal permissions), Send to NoCap (your
  home folder only), the layout backup, notifications (administrators), your
  own AI conversations (no provider settings), and unlinking itself.
- "Open my NoCap desktop" calls `POST /api/v1/device/session`, which returns
  a one-time, 60-second ticket; the web desktop trades it for a normal
  session at `POST /api/v1/auth/link`.

## Permissions

Asked only when you use the feature: notifications, camera (QR scan only),
approximate location (weather widget, only if you choose "My location"),
calendar (calendar widget). Opt-in in Android's settings: notification
access (dots, now playing, forwarding notifications), accessibility or
device admin (lock gesture only), Do not disturb access (quick toggles).
Normal permissions: set wallpaper, expand the notification shade, ask to
uninstall an app, Wi-Fi/Bluetooth state (toggles), set a timer in the clock
app. Weather requests go to Open-Meteo with only the coordinates.

## Build

Needs JDK 17 and the Android SDK (platform 34, build-tools 34.0.0).

```sh
cd android
./gradlew assembleDebug        # app/build/outputs/apk/debug/app-debug.apk
```

Kotlin + Jetpack Compose, minSdk 26, target 34, application ID
`com.nocap.link`.

## Releases and signing

`.github/workflows/android.yml` builds the APK on pull requests that touch
`android/**` (download it from the run's artifacts) and attaches
`nocap-os.apk` to each NoCapOS release, which is what
`https://github.com/shatheitguy/nocapos/releases/latest/download/nocap-os.apk`
serves.

For now the APK is **debug-signed** (the Android SDK's debug key, made fresh
on each CI runner). There are no signing secrets in the repo. Because the
key can differ between builds, installing a newer APK over an older one may
fail with "app not installed" — uninstall the old one first (you'll need to
link again). A proper release key can be added later as repository secrets.

## Credits

Orbitron and Rajdhani (the NoCapOS desktop's fonts), converted to TTF from
the `@fontsource` packages, under the SIL Open Font License 1.1 — see
`app/src/main/assets/OFL-*.txt`. The Tide, Nebula and Midnight wallpapers are
the NoCapOS desktop's own (`frontend/public/wallpapers`). Weather data by
Open-Meteo (CC BY 4.0).
