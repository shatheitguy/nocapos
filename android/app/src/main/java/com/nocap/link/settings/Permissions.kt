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
import androidx.compose.runtime.remember
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
            { t("Lock the screen with a gesture, like double-tap. It can only lock: it reads nothing on screen.", "Locks the screen from a home-screen gesture. It only locks; it doesn't read the screen.") },
            { Phone.canLock(it) },
            { a -> open(a, Phone.lockSetupIntent(a)) },
        ),
    )

    fun missingCore(ctx: Context): Int = all.count { it.kind == Kind.CORE && runCatching { it.granted(ctx) }.getOrNull() == false }
}

/** One permission: what it's for, its status, and a button to switch it on. */
@Composable
fun PermissionCard(a: MainActivity, p: Permissions.Perm, tick: Int) {
    val on = remember(tick, p.id) { runCatching { p.granted(a) }.getOrNull() }
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
            if (on == false) {
                Spacer(Modifier.height(10.dp))
                Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                    RedButton(t("Allow", "Allow"), { p.grant(a) })
                }
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
            }
        }
    }
}
