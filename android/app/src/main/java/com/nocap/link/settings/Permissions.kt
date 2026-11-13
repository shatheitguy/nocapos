package com.nocap.link.settings

import android.app.NotificationManager
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.Manifest
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.Icons
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.core.app.ActivityCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import com.nocap.link.data.PhoneApps
import com.nocap.link.data.t
import com.nocap.link.Layer
import com.nocap.link.MainActivity
import com.nocap.link.system.DotsService
import com.nocap.link.system.Phone
import com.nocap.link.ui.Chip
import com.nocap.link.ui.GhostButton
import com.nocap.link.ui.GlassCard
import com.nocap.link.ui.Label
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.RedButton

/**
 * Every sensitive thing NoCap OS can use: what it's for, whether it's on,
 * and one button to switch it on. All of them are optional; the launcher
 * works without any of them.
 */
object Permissions {
    enum class Kind { CORE, OPTIONAL }

    class Perm(
        val id: String,
        val kind: Kind,
        val title: () -> String,
        val why: () -> String,
        /** null = doesn't apply on this Android version. */
        val granted: (Context) -> Boolean?,
        val grant: (MainActivity) -> Unit,
        /** Android 13+ blocks it for apps installed from a file ("restricted settings"). */
        val restricted: Boolean = false,
    )

    private fun runtime(ctx: Context, p: String) = ContextCompat.checkSelfPermission(ctx, p) == PackageManager.PERMISSION_GRANTED

    fun appSettings(ctx: Context) {
        open(ctx, Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.parse("package:${ctx.packageName}")))
    }

    fun open(ctx: Context, i: Intent): Boolean =
        runCatching { ctx.startActivity(i.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)); true }.getOrElse {
            runCatching { ctx.startActivity(Intent(Settings.ACTION_SETTINGS).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
            false
        }

    /**
     * Asks for a runtime permission. Once Android stops showing its prompt
     * (denied twice, or "don't ask again"), opens this app's settings page instead.
     */
    fun ask(a: MainActivity, perm: String, done: (Boolean) -> Unit = {}) {
        val sp = a.getSharedPreferences("nocap_launcher", Context.MODE_PRIVATE)
        val asked = sp.getBoolean("asked:$perm", false)
        if (asked && !runtime(a, perm) && !ActivityCompat.shouldShowRequestPermissionRationale(a, perm)) {
            appSettings(a)
            return
        }
        sp.edit().putBoolean("asked:$perm", true).apply()
        a.askPermission(perm, done)
    }

    fun notificationsOn(ctx: Context): Boolean =
        (Build.VERSION.SDK_INT < 33 || runtime(ctx, Manifest.permission.POST_NOTIFICATIONS)) &&
            NotificationManagerCompat.from(ctx).areNotificationsEnabled()

    val all: List<Perm> = listOf(
        Perm(
            "notifications", Kind.CORE,
            { t("Notifications", "Notifications") },
            { t("Sign-in alerts, \"phone unlinked\", downloads saved, and your server's notifications if you turn them on.", "Sign-in alerts, unlink notices, saved downloads and (optional) your server's notifications.") },
            { notificationsOn(it) },
            { a ->
                if (Build.VERSION.SDK_INT >= 33 && !runtime(a, Manifest.permission.POST_NOTIFICATIONS)) {
                    ask(a, Manifest.permission.POST_NOTIFICATIONS)
                } else {
                    open(a, Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS).putExtra(Settings.EXTRA_APP_PACKAGE, a.packageName))
                }
            },
        ),
        Perm(
            "camera", Kind.CORE,
            { t("Camera", "Camera") },
            { t("Only to scan the NoCap Link QR code. No photos, no recording, nothing uploaded.", "Only for scanning the NoCap Link pairing QR code. Nothing is recorded or uploaded.") },
            { runtime(it, Manifest.permission.CAMERA) },
            { a -> ask(a, Manifest.permission.CAMERA) },
        ),
        Perm(
            "home", Kind.CORE,
            { t("Default home app", "Default home app") },
            { t("Makes NoCap OS your home screen, and lets it show app shortcuts on long-press.", "Sets NoCap OS as the home screen; also needed for app shortcuts.") },
            { PhoneApps.isDefaultHome(it) },
            { a -> PhoneApps.askToBeHome(a) },
        ),
        Perm(
            "listener", Kind.OPTIONAL,
            { t("Notification access", "Notification access") },
            { t("Notification dots, the now-playing widget, and sending the apps you pick to NoCap OS. Only to your own server.", "Notification dots, now playing, and forwarding chosen apps' notifications to your own server.") },
            { DotsService.allowed(it) },
            { a -> DotsService.openSettings(a) },
            restricted = true,
        ),
        Perm(
            "location", Kind.OPTIONAL,
            { t("Location (approximate)", "Approximate location") },
            { t("Weather for where you are. Or skip it and pick a city instead.", "Local weather. You can pick a city instead.") },
            { runtime(it, Manifest.permission.ACCESS_COARSE_LOCATION) },
            { a -> ask(a, Manifest.permission.ACCESS_COARSE_LOCATION) },
        ),
        Perm(
            "calendar", Kind.OPTIONAL,
            { t("Calendar", "Calendar") },
            { t("Your next events in the calendar widget. Read-only, never sent anywhere.", "Upcoming events in the calendar widget. Read-only; never leaves the phone.") },
            { runtime(it, Manifest.permission.READ_CALENDAR) },
            { a -> ask(a, Manifest.permission.READ_CALENDAR) },
        ),
        Perm(
            "dnd", Kind.OPTIONAL,
            { t("Do not disturb", "Do Not Disturb access") },
            { t("Lets the quick toggles widget switch Do not disturb.", "Lets the quick toggles widget turn Do Not Disturb on and off.") },
            { it.getSystemService(NotificationManager::class.java)?.isNotificationPolicyAccessGranted == true },
            { a -> open(a, Intent(Settings.ACTION_NOTIFICATION_POLICY_ACCESS_SETTINGS)) },
        ),
        Perm(
            "lock", Kind.OPTIONAL,
            { if (Build.VERSION.SDK_INT >= 28) t("Lock helper (accessibility)", "Lock helper (accessibility service)") else t("Lock helper (device admin)", "Lock helper (device administrator)") },
            { t("Lock the screen with a gesture, like double-tap. While you mirror this phone to NoCap OS, it also does your taps and typing. Nothing else.", "Locks the screen from a home-screen gesture, and performs your taps and typing while you mirror this phone to NoCap OS. Nothing else.") },
            { Phone.canLock(it) },
            { a -> open(a, Phone.lockSetupIntent(a)) },
            restricted = true,
        ),
        Perm(
            "admin", Kind.OPTIONAL,
            { t("Device admin (lock backup)", "Device administrator (lock fallback)") },
            {
                t(
                    "A second way to lock the screen, for phones where the accessibility one keeps switching off. Only \"lock the screen\": no wiping, no passwords.",
                    "A fallback way to lock the screen if the accessibility service is turned off. Its only policy is \"lock the screen\".",
                )
            },
            // Below Android 9 the lock helper row above already is the device admin.
            { if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) Phone.adminActive(it) else null },
            { a -> open(a, Phone.adminIntent(a)) },
            restricted = true,
        ),
        Perm(
            "installs", Kind.OPTIONAL,
            { t("Install updates (unknown apps)", "Install unknown apps") },
            {
                t(
                    "Lets NoCap OS install its own updates from Settings → About → Updates. It only ever installs NoCap OS, signed with the same key.",
                    "Lets NoCap OS install its own updates (Settings → About → Updates). It only installs NoCap OS builds signed with the same key.",
                )
            },
            { it.packageManager.canRequestPackageInstalls() },
            { a -> open(a, com.nocap.link.update.Updater.unknownAppsIntent(a)) },
        ),
    )

    fun missingCore(ctx: Context): Int = all.count { it.kind == Kind.CORE && runCatching { it.granted(ctx) }.getOrNull() == false }
}

/** One permission: what it's for, its status, and a button to switch it on. */
@Composable
fun PermissionCard(a: MainActivity, p: Permissions.Perm, tick: Int) {
    val on = remember(tick, p.id) { runCatching { p.granted(a) }.getOrNull() }
    val help = remember(tick, p.id, on) { if (p.restricted) runCatching { Restricted.help(a, on) }.getOrDefault(Restricted.Help.NONE) else Restricted.Help.NONE }
    // Restricted ones: try first (that's what makes Android's ⋮ menu item appear), then guide.
    val go = { if (p.restricted) Restricted.markTried(a); p.grant(a) }
    GlassCard(Modifier.fillMaxWidth()) {
        Column {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(p.title(), Modifier.weight(1f), style = MaterialTheme.typography.titleMedium)
                when (on) {
                    true -> Chip(t("On", "Allowed"), NoCap.Ok)
                    false -> Chip(if (p.kind == Permissions.Kind.CORE) t("Off", "Not allowed") else t("Optional", "Optional"), if (p.kind == Permissions.Kind.CORE) NoCap.Warn else NoCap.Muted)
                    null -> Chip(t("Not needed", "Not needed"), NoCap.Muted)
                }
            }
            Text(p.why(), Modifier.padding(top = 4.dp), style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted)
            if (on == false && help != Restricted.Help.UNBLOCK) {
                RestrictedHelp(a, help, go)
                Spacer(Modifier.height(10.dp))
                Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                    RedButton(t("Allow", "Allow"), go)
                }
            } else if (on == false) {
                RestrictedHelp(a, help, go)
            } else if (on == true) {
                Spacer(Modifier.height(6.dp))
                Text(
                    t("Change it in Android settings", "Manage in Android settings"),
                    Modifier.clickable { Permissions.appSettings(a) }.padding(vertical = 4.dp),
                    style = MaterialTheme.typography.labelLarge, color = NoCap.Red,
                )
            }
        }
    }
}

/** Settings → Privacy & permissions → Permissions. */
@Composable
fun PermissionsScreen(a: MainActivity) {
    val tick = a.resumeTick
    androidx.activity.compose.BackHandler { a.layer = Layer.SETTINGS }
    Column(
        Modifier.fillMaxSize().background(NoCap.Sheet).clickable(remember { MutableInteractionSource() }, null) {}
            .statusBarsPadding().navigationBarsPadding(),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Column(Modifier.widthIn(max = 680.dp).fillMaxWidth()) {
            Row(Modifier.fillMaxWidth().padding(start = 18.dp, end = 8.dp, top = 10.dp, bottom = 6.dp), verticalAlignment = Alignment.CenterVertically) {
                Text(t("PERMISSIONS", "PERMISSIONS"), style = MaterialTheme.typography.headlineSmall, modifier = Modifier.weight(1f))
                IconButton(onClick = { a.layer = Layer.SETTINGS }) { Icon(Icons.Filled.Close, t("Close", "Close"), tint = NoCap.Text) }
            }
            LazyColumn(contentPadding = PaddingValues(start = 14.dp, end = 14.dp, bottom = 28.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                item { Label(t("The basics", "Recommended")) }
                items(Permissions.all.filter { it.kind == Permissions.Kind.CORE }, key = { it.id }) { PermissionCard(a, it, tick) }
                item { Label(t("Extras, only if you use them", "Optional"), Modifier.padding(top = 8.dp)) }
                items(Permissions.all.filter { it.kind == Permissions.Kind.OPTIONAL }, key = { it.id }) { PermissionCard(a, it, tick) }
                item {
                    Text(
                        t(
                            "Also on from install, no prompt: internet (only your own server and the weather), setting the wallpaper, pulling down the notification shade, and asking Android to uninstall an app (Android still asks you).",
                            "Granted at install without a prompt: network access (your server and weather only), set wallpaper, expand the status bar, and request app uninstalls (Android confirms each one).",
                        ),
                        Modifier.padding(top = 8.dp), style = MaterialTheme.typography.bodySmall,
                    )
                }
                item { GhostButton(t("All app permissions in Android", "Open Android app settings"), { Permissions.appSettings(a) }, Modifier.fillMaxWidth()) }
                item { DeviceOwnerCard(a) }
            }
        }
    }
}

/**
 * Advanced: device owner. Full control of the phone needs a freshly
 * factory-reset phone and a one-time USB command. Explained only; the app
 * never does this by itself.
 */
@Composable
fun DeviceOwnerCard(a: MainActivity) {
    var open by androidx.compose.runtime.saveable.rememberSaveable { mutableStateOf(false) }
    var tick by remember { mutableStateOf(0) }
    val owner = remember(a.resumeTick, tick) { Phone.isDeviceOwner(a) }
    val cmd = "adb shell dpm set-device-owner ${a.packageName}/${Phone.ADMIN_RECEIVER}"
    GlassCard(Modifier.fillMaxWidth().padding(top = 8.dp)) {
        Column {
            Row(Modifier.fillMaxWidth().clickable { open = !open }, verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Label(t("Advanced", "Advanced"))
                    Text(t("Device owner (full control)", "Device owner mode"), style = MaterialTheme.typography.titleMedium)
                }
                if (owner) Chip(t("On", "Active"), NoCap.Ok) else Text(if (open) "−" else "+", style = MaterialTheme.typography.titleLarge, color = NoCap.Muted)
            }
            if (open || owner) {
                Text(
                    t(
                        "Not needed for anything in NoCap OS today. Device owner is the deepest level of control Android gives an app, and Android only allows it on a phone that was just factory reset, with no accounts added yet. It's set once over USB from a computer:",
                        "Not required by any current NoCap OS feature. Device owner is Android's highest level of app control. Android only allows it on a freshly factory-reset phone with no accounts added, and it is set once over USB from a computer:",
                    ),
                    Modifier.padding(top = 8.dp), style = MaterialTheme.typography.bodySmall,
                )
                androidx.compose.foundation.text.selection.SelectionContainer {
                    Text(
                        cmd,
                        Modifier.fillMaxWidth().padding(top = 8.dp).background(NoCap.Field, androidx.compose.foundation.shape.RoundedCornerShape(10.dp)).padding(10.dp),
                        style = MaterialTheme.typography.bodySmall.copy(fontFamily = androidx.compose.ui.text.font.FontFamily.Monospace),
                    )
                }
                Text(
                    t(
                        "1. Factory reset, skip adding accounts. 2. Turn on USB debugging. 3. Install NoCap OS. 4. Run the command above. The receiver is ${Phone.ADMIN_RECEIVER} (from the app's manifest). NoCap OS never runs this itself.",
                        "Steps: 1. Factory reset and skip account setup. 2. Enable USB debugging. 3. Install NoCap OS. 4. Run the command above. The receiver is ${Phone.ADMIN_RECEIVER}, as declared in the app's manifest. NoCap OS never does this automatically.",
                    ),
                    Modifier.padding(top = 8.dp), style = MaterialTheme.typography.bodySmall, color = NoCap.Muted,
                )
                Row(Modifier.padding(top = 10.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                    GhostButton(t("Copy command", "Copy command"), {
                        val cm = a.getSystemService(android.content.ClipboardManager::class.java)
                        cm?.setPrimaryClip(android.content.ClipData.newPlainText("adb", cmd))
                        a.toast = t("Copied.", "Copied to clipboard.")
                    }, Modifier.weight(1f))
                    if (owner) GhostButton(t("Release it", "Remove device owner"), { Phone.releaseDeviceOwner(a); tick++ }, Modifier.weight(1f))
                }
            }
        }
    }
}
