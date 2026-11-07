# NoCap OS (phone app)

The phone side of **NoCap Link**: a home screen for Android that links to
your NoCapOS server.

- **Launcher** — clock, app search, dock and a full app grid (Launchpad),
  styled like the NoCapOS desktop. Pick it as your home app, or just open it.
- **Link with NoCap Link** — scan the QR from *Settings → Devices → Link a
  device*, tap a `nocap://pair` link, or type the address + 6-digit code.
- **Open my NoCap desktop** — your server's web desktop, full screen,
  signed in automatically with a one-time ticket.
- **Server status + apps** — see if the server's up and open its apps.
- **Heartbeat** — every 15 minutes the phone tells the server it's alive
  (battery + app version), shown in Settings → Devices.

## Security

- The device token, the server address and the pinned certificate live in
  `EncryptedSharedPreferences` (key in the Android Keystore); backups are off.
- HTTPS uses **trust on first use**: when you link, the app shows the
  server's certificate fingerprint (SHA-256) and pins it. After that, the
  API client and the desktop web view only accept that exact certificate.
  If it ever changes, the app stops and asks you to check it.
- Plain `http://` addresses work too (for a server on your own Wi-Fi), with a
  warning that the connection isn't encrypted.
- "Open my NoCap desktop" calls `POST /api/v1/device/session`, which returns
  a one-time, 60-second ticket; the web desktop trades it for a normal
  session at `POST /api/v1/auth/link`. Unlinked devices, disabled accounts
  and local-only mode all refuse it.

## Permissions

Only two, both explained in the app and asked when you tap *Allow*:
notifications (unlinked / download saved) and the camera (QR scan only).

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

## Fonts

Orbitron and Rajdhani (the NoCapOS desktop's fonts), converted to TTF from
the `@fontsource` packages, under the SIL Open Font License 1.1 — see
`app/src/main/assets/OFL-*.txt`.
