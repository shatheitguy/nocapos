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
- **NoCap widgets**: clock, server status (CPU, RAM, storage, online), quick
  actions, recent server files, and *Ask NoCap AI*.
- **App drawer in four styles** (Settings → The Hub): NoCap Launchpad (paged
  grid), scrolling grid, A–Z list with a fast-scroll letter rail, and
  categories (smart folders, plus your server's apps).
- **Search** across phone apps, NoCap OS settings, Android settings, your
  server's apps and your server's files, with *Ask NoCap AI* for anything.
- **Long-press an app**: its shortcuts (while NoCap OS is your home app), add
  to home, pin to dock, app info, hide, uninstall.
- **Notification dots** (opt-in: needs notification access; only counts).
- **Hide apps**, icon size, grid size, labels, and icon themes (original,
  NoCap tinted, monochrome).
- **Gestures**: swipe up = app drawer, swipe down = notifications (or
  search), double-tap = lock (opt-in, via an accessibility helper on Android
  9+ or a lock-only device admin on older phones).
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
- **Server notifications** on the phone (opt-in, checked with the heartbeat
  about every 15 minutes; your server shows them to administrators).
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

Asked only when you use the feature: notifications, camera (QR scan only).
Opt-in in Android's settings: notification access (dots), accessibility or
device admin (double-tap to lock only). Normal permissions: set wallpaper,
expand the notification shade, ask to uninstall an app.

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
the NoCapOS desktop's own (`frontend/public/wallpapers`).
