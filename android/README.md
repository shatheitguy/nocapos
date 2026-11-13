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
  weather (Open-Meteo, off until you turn it on; your city, or rough location only if you allow it;
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
    presets or any color, or synced from your NoCap desktop), font, Gen Z,
    Standard or your server's wording, NoCap lock screen, NoCap phone theme.
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
    up/down and the Home button (while home) can each open the drawer, the
    NoCap control center, search, NoCap AI, Android's notification shade,
    quick settings, lock the screen
    (opt-in helper), any app, your NoCap desktop, NoCap Link, settings,
    widgets or the first page.
  - *System*: status bar clear / tinted / hidden, navigation bar clear /
    tinted, rotation, haptics.
  - *Save Point*: back up and restore every setting with the layout; reset
    to defaults.
- **Wallpapers**: the three NoCapOS wallpapers (Tide, Nebula, Midnight), a
  classic grid, or a picture from your gallery; dim it, set it as the phone's
  own wallpaper too, or sync the one you picked on your NoCap desktop.
- **Language**: Gen Z (default), Standard, or *Same as my server* (follows the
  Language setting saved on your NoCap desktop, read from the device API's
  saved look; it keeps the last answer for offline use, and is Gen Z until
  there is one). All text switches instantly.
- **NoCap control center**: swipe down on the home screen (the new default;
  *Android's shade* is still a Gestures option) for a glass, NoCap-red panel:
  Wi-Fi, Bluetooth, flashlight, Do not disturb, rotation lock, a brightness
  slider, music controls, NoCap Link tiles (send clipboard, Find my phone,
  server status) and your notifications grouped by app, with reply and
  dismiss. Wi-Fi and Bluetooth open Android's own panel (apps can't flip them
  since Android 10); rotation lock and brightness need *Modify system
  settings*. Optional (Settings → Gestures → Control center): pull down on an
  invisible strip just under the status bar to open it in any app — needs
  *Display over other apps*, and only sees touches on that strip.
- **NoCap lock screen** (opt-in, Settings → Look): when the screen turns on
  and the phone is locked, a NoCap screen with the clock, logo, server status,
  music controls and notifications (app names and counts unless you allow the
  text) shows *over* Android's lock screen. Swipe up asks Android to dismiss
  the keyguard, so your PIN, pattern or fingerprint is still required; the
  system lock screen is never disabled or bypassed. Needs *Display over other
  apps* (Android only lets apps open a screen from the background with it).
- **NoCap phone theme** (Settings → Look → NoCap phone): sets NoCap
  wallpapers (the NoCapOS wallpaper you use, washed in NoCap red; the lock one
  carries the NoCap mark) on the phone's home and lock screens, or just the
  lock one. Red is the dominant color, so on Android 12+ the phone's Material
  You colors follow it and go NoCap red. Without root, apps can't restyle
  Android's own system screens; the setting says so.
- **Backup & restore** of pages, folders, dock and settings — to your NoCap
  server (if linked) or a file.

## NoCap Link

- **Link with NoCap Link** — scan the QR from *Settings → Linked Devices → Link a
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
- **Share Wi-Fi** (administrators) — pick a Wi-Fi network saved on the
  server, confirm with your account password, and the phone shows its join
  QR code and password (drawn on the phone; enterprise networks can't be
  shared this way).
- **Unlink** — removes the phone on the server too.
- **Linked Devices** — the heartbeat tells only your own server the phone's
  battery, app version, model, Android version and which of notifications /
  clipboard / files are on, so *Settings → Linked Devices* can show them.
  An administrator can cap how many devices each person links (5 by
  default); at the cap, linking answers `device_limit` until one is unlinked.
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
  own AI conversations (no provider settings), the server's saved Wi-Fi
  names and, after the administrator's account password, a network's
  password for Share Wi-Fi (administrators; audited), and unlinking itself.
- "Open my NoCap desktop" calls `POST /api/v1/device/session`, which returns
  a one-time, 60-second ticket; the web desktop trades it for a normal
  session at `POST /api/v1/auth/link`.

## First run: privacy, then the setup guide

1. **Welcome.**
2. **Terms & privacy**, once: a short card ("NoCap OS doesn't collect a single
   piece of your data…"), no checkboxes. The glowing red *Join the NoCap Fam*
   (Gen Z) / *Join the NoCap Community* (Standard) button records that you
   agreed and moves on. Reopen it from Settings → About → Privacy.
3. **Link your NoCap server** (or later).
4. **The setup guide**: one screen per permission, each saying what it's for,
   the exact steps, and a button that opens the right Android page. Its status
   turns green once Android reports it's on (re-checked every time you come
   back), with progress like "6 of 11 done". Every step can be skipped; *Skip*
   at the top ends setup. It picks up where you left off and reopens from
   Settings → Privacy & permissions (or About) → *Setup guide*.

## Permissions

The guide, in order (steps that don't apply to the phone are left out):

| Step | What it's for |
| --- | --- |
| Default home app | NoCap OS as your home screen |
| Notifications | Unlinked / download / opt-in server notifications |
| Allow restricted settings (Android 13+) | Android blocks accessibility, notification access and device admin for apps installed from a file until you tap App info → ⋮ → *Allow restricted settings*. That item only appears after you've tried once, so the step is "try, then App info". Status is read from Android where it allows it |
| Accessibility (lock helper) | Lock-screen gesture; it can only lock |
| Notification access | Dots, now playing, forwarding chosen apps to your server |
| Device admin (Android 9+) | Fallback lock; its only policy is "lock the screen" |
| No battery limits | Keeps NoCap Link, sign-in alerts and clipboard sync alive |
| Install unknown apps | In-app updates (only NoCap OS signed with the same key) |
| Camera | Scanning the pairing QR code |
| Calls & messages | Not requested today. If a feature later adds phone or SMS permissions to the manifest, the guide shows this step automatically and asks one at a time |
| Weather | Turns weather on and asks for *approximate* location |
| Calendar | The calendar widget |
| Screen mirroring | Nothing to switch on: Android asks every time mirroring starts |

Also opt-in in Android's settings: Do not disturb access (quick toggles),
display over other apps (NoCap lock screen, control-center edge swipe) and
modify system settings (control center rotation lock and brightness).
Notification access also feeds the control center and lock screen.
Normal permissions: set wallpaper, expand the notification shade, ask to
uninstall an app, Wi-Fi/Bluetooth state (toggles), set a timer in the clock
app. Settings → Privacy & permissions → *Manage permissions* shows them all,
with the same restricted-settings help.

## Privacy

NoCap OS collects nothing: no tracking, analytics, ads, advertising ID or
third-party crash reporting. It connects only to:

| Host | Why | When |
| --- | --- | --- |
| Your NoCap server | Everything NoCap Link does, backups, reports you choose to send | Once linked |
| `api.github.com`, `github.com`, `*.githubusercontent.com` | Update checks and downloads; *Report to NoCap developers* opens github.com in your browser | When you check (or turn on automatic checks, off by default), or tap report |
| `api.open-meteo.com`, `geocoding-api.open-meteo.com` | Weather: a city you search for, or your location rounded to about 1 km | Only if weather is on (it's **off** until you turn it on) |

The phone's location is never named through a geocoding service. The list
lives in `settings/Privacy.kt` (Settings → About → Privacy shows it, with the
weather switch).

## Build

Needs JDK 17 and the Android SDK (platform 34, build-tools 34.0.0).

```sh
cd android
./gradlew assembleDebug                 # app/build/outputs/apk/debug/app-debug.apk
./gradlew testDebugUnitTest lintDebug   # unit tests + lint (lint-baseline.xml lists older warnings)
./gradlew assembleRelease bundleRelease # release APK + Play bundle (.aab)
```

Kotlin + Jetpack Compose, minSdk 26, target 34, application ID
`com.nocap.link`.

### Code layout

`app/src/main/java/com/nocap/link/`:

| Package | What's in it |
| --- | --- |
| (root) | `MainActivity` (screens and layers), `NoCapApp`, `HeartbeatWorker` |
| `data` | Settings (`Opt`/`Prefs`), the encrypted `Vault`, phone apps, usage, custom icons |
| `home` | Home screen, pages and layout file, gestures, menus, wallpaper |
| `drawer` | App drawer styles, search, icon styles |
| `widget` | NoCap widgets, hosting Android widgets, weather, now playing, and the widgets for other home screens |
| `settings` | Settings, Permissions page, first-run setup and the setup guide, restricted-settings help, privacy page, crash and bug report dialogs |
| `sync` | NoCap Link: pairing, QR scan, hub, notification and clipboard sync, sign-in alerts, notifications |
| `net` | The NoCapOS device API client and certificate pinning |
| `ai`, `desktop`, `share` | NoCap AI, the web desktop, "Send to NoCap" |
| `system` | Notification listener, lock helper, crash log (and report scrubbing) |
| `update` | In-app updates from GitHub releases |
| `lock` | NoCap lock screen (`LockActivity`, screen-on watch) and the NoCap wallpapers for the phone |
| `control` | NoCap control center, its toggles, notifications (reply / dismiss), the edge strip and `ControlActivity` |
| `ui` | Theme (NoCap colors, fonts, glass) and shared controls |

Manifest components (services, receivers, the widget providers, activities)
keep their class names across releases: renaming one would switch off
accessibility, notification access, device admin or the quick-settings tile
for people who turned them on, and remove placed widgets.

### Crash reports

If the app crashes, freezes (ANR, Android 11+) or a background job keeps
failing, the report is kept on the phone. On the next start NoCap OS asks
once, showing exactly what the report contains: the error and stack trace,
app and Android version, phone model. Addresses, emails, IPs, host names,
file paths, tokens and other long secrets are blanked out first. Then:

- **Send to my server**: saved in NoCap Link in your home folder.
- **Report to NoCap developers**: opens a prefilled
  `https://github.com/shatheitguy/nocapos/issues/new?title=…&body=…` in your
  browser (cut to fit URL limits); you review and submit it there yourself.
- **Don't send**.

Nothing is ever sent automatically. Settings → About → *Report a bug* does the
same for anything you describe (with the last crash attached if there is one).

### Updates

Settings → About → Updates checks the latest NoCapOS release on GitHub,
downloads `nocap-os.apk`, checks its checksum (from `nocap-os.json`, when
published), package name, version and signing key, and installs it through
Android's package installer. It also says when your server is behind and
opens Glow Up on it. Automatic checks (when Settings opens and once a day)
are off until you turn them on. After the first in-app update Android
usually lifts the restricted-settings block for sideloaded installs.

## Releases and signing

`.github/workflows/android.yml` runs the unit tests and lint, then builds the
release APK and the Play bundle on pull requests that touch `android/**`
(download them from the run's artifacts), and attaches `nocap-os.apk` and
`nocap-os.aab` to each NoCapOS release, which is what
`https://github.com/shatheitguy/nocapos/releases/latest/download/nocap-os.apk`
serves.

Release signing reads four repository secrets:

| Secret | Value |
| --- | --- |
| `NOCAP_KEYSTORE_B64` | The upload/release keystore (`.jks`), base64-encoded (`base64 -w0 nocap.jks`) |
| `NOCAP_KEYSTORE_PASSWORD` | The keystore password |
| `NOCAP_KEY_ALIAS` | The key alias inside it |
| `NOCAP_KEY_PASSWORD` | The key password |

Without them the build falls back to the Android SDK's debug key (made fresh
on each CI runner), so installing a newer APK over an older one may fail with
"app not installed": uninstall the old one first (you'll need to link again).
Once a release key is in place, keep it safe: every later update must be
signed with the same key. The first update from a debug-signed build to the
release-signed one also needs the old app removed once.
## Credits

Orbitron and Rajdhani (the NoCapOS desktop's fonts), converted to TTF from
the `@fontsource` packages, under the SIL Open Font License 1.1 — see
`app/src/main/assets/OFL-*.txt`. The Tide, Nebula and Midnight wallpapers are
the NoCapOS desktop's own (`frontend/public/wallpapers`). Weather data by
Open-Meteo (CC BY 4.0).
