package com.nocap.link.launcher

import android.Manifest
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Slider
import androidx.compose.material3.SliderDefaults
import androidx.compose.material3.Switch
import androidx.compose.material3.SwitchDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.nocap.link.BuildConfig
import com.nocap.link.DrawerStyle
import com.nocap.link.HeartbeatWorker
import com.nocap.link.IconTheme
import com.nocap.link.Layer
import com.nocap.link.MainActivity
import com.nocap.link.Notices
import com.nocap.link.PhoneApps
import com.nocap.link.Prefs
import com.nocap.link.Screen
import com.nocap.link.ServerState
import com.nocap.link.SwipeDown
import com.nocap.link.Wallpapers
import com.nocap.link.system.DotsService
import com.nocap.link.system.Phone
import com.nocap.link.t
import com.nocap.link.ui.Label
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.Wallpaper
import com.nocap.link.ui.glass

/** Settings for the launcher, laid out like NoCapOS's own Settings app. */
@Composable
fun SettingsScreen(a: MainActivity) {
    var tick by remember { mutableIntStateOf(0) } // re-check system grants on resume-ish
    LaunchedEffect(a.layer) { tick++ }
    var forget by remember { mutableStateOf(false) }
    var phoneWall by remember { mutableStateOf(false) }
    var lockSetup by remember { mutableStateOf(false) }
    val askNotif = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { ok ->
        if (ok) { Prefs.setServerNotices(true); HeartbeatWorker.now(a) } else a.toast = t("Notifications are off for NoCap OS, so no tea.", "Notifications are blocked for NoCap OS.")
    }
    val linked = a.server !is ServerState.NotLinked

    Column(
        Modifier.fillMaxSize().background(Color(0xF507080B)).clickable(remember { MutableInteractionSource() }, null) {}
            .statusBarsPadding().navigationBarsPadding(),
    ) {
        Row(Modifier.fillMaxWidth().padding(start = 18.dp, end = 8.dp, top = 10.dp, bottom = 6.dp), verticalAlignment = Alignment.CenterVertically) {
            Text(t("SETTINGS", "SETTINGS"), style = MaterialTheme.typography.headlineSmall, modifier = Modifier.weight(1f))
            IconButton(onClick = { a.layer = Layer.NONE }) { Icon(Icons.Filled.Close, t("Close", "Close"), tint = NoCap.Text) }
        }
        Column(Modifier.verticalScroll(rememberScrollState()).padding(horizontal = 14.dp, vertical = 6.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
            Group(t("The Lingo", "Language"), t("How NoCap OS words things", "Wording style")) {
                Choice(listOf(t("Gen Z", "Gen Z") to true, t("Standard", "Standard") to false), Prefs.genz) { Prefs.setGenz(it) }
                Hint(if (Prefs.genz) t("Playful names, with a plain description where it matters.", "") else "Plain, professional wording.")
            }

            Group(t("Wall Aesthetic", "Wallpaper"), t("The NoCapOS wallpapers, or your own pic", "Built-in wallpapers or your own picture")) {
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Wallpapers.builtIn.forEach { w ->
                        Thumb(Prefs.wallpaper == w.id, Modifier.weight(1f), { Prefs.setWallpaper(w.id) }) {
                            Image(painterResource(w.thumb), w.genz, Modifier.fillMaxSize(), contentScale = ContentScale.Crop)
                        }
                    }
                    Thumb(Prefs.wallpaper == Wallpapers.GRID, Modifier.weight(1f), { Prefs.setWallpaper(Wallpapers.GRID) }) { Wallpaper {} }
                    Thumb(Prefs.wallpaper == Wallpapers.CUSTOM, Modifier.weight(1f), { a.pickWallpaper() }) {
                        Box(Modifier.fillMaxSize().background(Color(0x22FFFFFF)), contentAlignment = Alignment.Center) { Text("+", style = MaterialTheme.typography.headlineSmall) }
                    }
                }
                Hint(t("Tide · Nebula · Midnight · Classic grid · From your gallery", "Tide, Nebula, Midnight, Classic grid, or a picture from your gallery"))
                SliderRow(t("Dim", "Dim"), Prefs.dim.toFloat(), 0f..60f, "${Prefs.dim}%") { Prefs.setDim(it.toInt()) }
                Action(t("Set as my phone's wallpaper too", "Also set as phone wallpaper")) { phoneWall = true }
                if (linked) Action(t("Sync wallpaper from my NoCap server", "Sync wallpaper from my NoCap server")) { a.syncFromServer() }
            }

            Group(t("The Hub", "Home screen"), t("Grid, icons and the Roster", "Grid, icons and app drawer")) {
                Label(t("Roster style", "App drawer style"))
                Choice(
                    listOf(
                        t("Launchpad", "Paged") to DrawerStyle.PAGED, t("Scroll", "Grid") to DrawerStyle.GRID,
                        "A–Z" to DrawerStyle.LIST, t("Vibes", "Categories") to DrawerStyle.CATEGORIES,
                    ),
                    Prefs.drawer,
                ) { Prefs.setDrawer(it) }
                Spacer(Modifier.height(10.dp))
                Label(t("Icon theme", "Icon theme"))
                Choice(listOf(t("Original", "Original") to IconTheme.ORIGINAL, t("NoCap Red", "NoCap tinted") to IconTheme.TINTED, t("Mono", "Monochrome") to IconTheme.MONO), Prefs.iconTheme) { Prefs.setIconTheme(it) }
                Spacer(Modifier.height(10.dp))
                SliderRow(t("Icon size", "Icon size"), Prefs.iconSize.toFloat(), 40f..68f, "${Prefs.iconSize}") { Prefs.setIconSize(it.toInt()) }
                Label(t("Columns", "Columns"))
                Choice(listOf("3" to 3, "4" to 4, "5" to 5, "6" to 6), Prefs.columns) { Prefs.setColumns(it) }
                Spacer(Modifier.height(8.dp))
                Label(t("Rows", "Rows"))
                Choice(listOf("5" to 5, "6" to 6, "7" to 7, "8" to 8), Prefs.rows) { Prefs.setRows(it) }
                Toggle(t("Show app names", "Show labels"), null, Prefs.labels) { Prefs.setLabels(it) }
                Action(t("Add widgets", "Add widgets")) { a.layer = Layer.WIDGETS }
            }

            Group(t("Gestures", "Gestures"), t("Swipes and taps on the home screen", "Home screen gestures")) {
                Hint(t("Swipe up opens the Roster.", "Swipe up opens the app drawer."))
                Label(t("Swipe down", "Swipe down"))
                Choice(listOf(t("The Tea", "Notifications") to SwipeDown.NOTIFICATIONS, t("Search", "Search") to SwipeDown.SEARCH, t("Nothing", "Nothing") to SwipeDown.NOTHING), Prefs.swipeDown) { Prefs.setSwipeDown(it) }
                val canLock = remember(tick) { Phone.canLock(a) }
                Toggle(
                    t("Double-tap to lock", "Double-tap to lock"),
                    if (Prefs.doubleTapLock && !canLock) t("Needs the lock helper switched on — tap to set it up.", "Needs the lock helper enabled. Tap to set it up.") else t("Off by default. Uses an accessibility helper that only locks the screen.", "Uses an accessibility service that only locks the screen."),
                    Prefs.doubleTapLock,
                ) { on ->
                    Prefs.setDoubleTapLock(on)
                    if (on && !Phone.canLock(a)) lockSetup = true
                    if (!on) Phone.turnOffLockHelpers(a)
                }
            }

            Group(t("Notification dots", "Notification dots"), t("A red dot on apps with notifications", "Show a dot on apps that have notifications")) {
                val allowed = remember(tick) { DotsService.allowed(a) }
                Toggle(t("Show dots", "Show dots"), if (allowed) t("Notification access: on", "Notification access is on") else t("Needs notification access — only counts, never reads or sends.", "Requires notification access. NoCap OS only counts notifications."), Prefs.dots && allowed) { on ->
                    Prefs.setDots(on)
                    if (on && !allowed) DotsService.openSettings(a)
                }
            }

            Group(t("Hidden apps", "Hidden apps"), t("Hidden from the Roster and search", "Hidden from the app drawer and search")) {
                val hidden = a.phoneApps.filter { it.key in Prefs.hidden }
                if (hidden.isEmpty()) Hint(t("Nothing hidden. Long-press an app → Hide.", "No hidden apps. Long-press an app and choose Hide."))
                hidden.forEach { app ->
                    Row(Modifier.fillMaxWidth().padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                        Image(app.icon, null, Modifier.size(30.dp))
                        Text(app.label, Modifier.weight(1f).padding(start = 10.dp), style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        Text(t("Unhide", "Unhide"), Modifier.clip(RoundedCornerShape(8.dp)).clickable { Prefs.hide(app.key, false) }.padding(8.dp), style = MaterialTheme.typography.labelLarge, color = NoCap.Red)
                    }
                }
            }

            Group(t("Home app", "Default home app"), null) {
                val isHome = remember(tick) { PhoneApps.isDefaultHome(a) }
                Hint(if (isHome) t("NoCap OS is your home screen. W.", "NoCap OS is your default home app.") else t("NoCap OS isn't your home screen yet.", "NoCap OS isn't the default home app."))
                Action(if (isHome) t("Change home app", "Change default home app") else t("Make NoCap OS my home screen", "Set as default home app")) { PhoneApps.askToBeHome(a) }
            }

            Group(t("Save Point", "Backup & restore"), t("Your pages, folders, dock and settings", "Home screen layout and settings")) {
                if (linked) {
                    Action(t("Back up to my NoCap server", "Back up to my NoCap server")) { a.backupToServer() }
                    Action(t("Restore from my NoCap server", "Restore from my NoCap server")) { a.restoreFromServer() }
                }
                Action(t("Save to a file", "Export to a file")) { a.exportToFile() }
                Action(t("Load from a file", "Import from a file")) { a.importFromFile() }
                Hint(t("Android widgets stay put — they belong to this phone.", "Android widgets aren't included; they belong to this phone."))
            }

            Group(t("NoCap Link", "NoCap Link"), null) {
                if (linked) {
                    Toggle(
                        t("Server tea on this phone", "Server notifications"),
                        t("Your server's alerts as phone notifications (admins). Checked about every 15 min.", "Show your server's notifications on this phone (administrators). Checked about every 15 minutes."),
                        Prefs.serverNotices,
                    ) { on ->
                        if (on && Build.VERSION.SDK_INT >= 33 && !Notices.allowed(a)) askNotif.launch(Manifest.permission.POST_NOTIFICATIONS)
                        else { Prefs.setServerNotices(on); if (on) HeartbeatWorker.now(a) }
                    }
                    Action(t("Open NoCap Link", "Open NoCap Link")) { a.layer = Layer.HUB }
                    Action(t("Unlink this phone", "Unlink this phone"), danger = true) { forget = true }
                } else {
                    Action(t("Link with NoCap Link", "Link with NoCap Link")) { a.layer = Layer.NONE; a.screen = Screen.LinkForm() }
                }
            }

            Group(t("About", "About"), null) {
                Action(t("Permissions", "Permissions")) { a.layer = Layer.NONE; a.screen = Screen.Intro }
                Hint("NoCap OS ${BuildConfig.VERSION_NAME} · Orbitron & Rajdhani fonts under the SIL Open Font License")
            }
            Spacer(Modifier.height(20.dp))
        }
    }

    if (phoneWall) {
        AlertDialog(
            onDismissRequest = { phoneWall = false },
            containerColor = NoCap.GlassStrong,
            title = { Text(t("Where should it go?", "Set wallpaper on"), style = MaterialTheme.typography.titleMedium) },
            text = { Text(t("Puts the current NoCap wallpaper on your phone itself.", "Applies the current wallpaper to your phone."), style = MaterialTheme.typography.bodyMedium) },
            confirmButton = {
                Row {
                    TextButton(onClick = { phoneWall = false; a.setPhoneWallpaper(true, false) }) { Text(t("Home", "Home screen"), color = NoCap.Red) }
                    TextButton(onClick = { phoneWall = false; a.setPhoneWallpaper(false, true) }) { Text(t("Lock", "Lock screen"), color = NoCap.Red) }
                    TextButton(onClick = { phoneWall = false; a.setPhoneWallpaper(true, true) }) { Text(t("Both", "Both"), color = NoCap.Red) }
                }
            },
        )
    }
    if (lockSetup) {
        AlertDialog(
            onDismissRequest = { lockSetup = false },
            containerColor = NoCap.GlassStrong,
            title = { Text(t("Switch on the lock helper", "Enable the lock helper"), style = MaterialTheme.typography.titleMedium) },
            text = {
                Text(
                    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) t("In Accessibility, find NoCap OS lock and turn it on. It can only lock your screen — nothing else.", "In Accessibility settings, turn on NoCap OS lock. It only locks the screen.")
                    else t("Allow NoCap OS as a device admin. It can only lock the screen.", "Allow NoCap OS as a device admin. It can only lock the screen."),
                    style = MaterialTheme.typography.bodyMedium,
                )
            },
            confirmButton = { TextButton(onClick = { lockSetup = false; runCatching { a.startActivity(Phone.lockSetupIntent(a)) } }) { Text(t("Take me there", "Open settings"), color = NoCap.Red) } },
            dismissButton = { TextButton(onClick = { lockSetup = false; Prefs.setDoubleTapLock(false) }) { Text(t("Nah", "Cancel"), color = NoCap.Text) } },
        )
    }
    if (forget) {
        AlertDialog(
            onDismissRequest = { forget = false },
            containerColor = NoCap.GlassStrong,
            title = { Text(t("Unlink this phone?", "Unlink this phone?"), style = MaterialTheme.typography.titleMedium) },
            text = { Text(t("This phone drops its key, signs out of your NoCap desktop, and your server forgets it.", "This phone deletes its key, signs out of the NoCap desktop and is removed from your server's devices."), style = MaterialTheme.typography.bodyMedium) },
            confirmButton = { TextButton(onClick = { forget = false; a.layer = Layer.NONE; a.forget(alsoServer = true) }) { Text(t("Unlink", "Unlink"), color = NoCap.Red) } },
            dismissButton = { TextButton(onClick = { forget = false }) { Text(t("Nah", "Cancel"), color = NoCap.Text) } },
        )
    }
}

@Composable
fun Group(title: String, sub: String?, content: @Composable () -> Unit) {
    Column(Modifier.fillMaxWidth().glass(RoundedCornerShape(18.dp)).padding(14.dp)) {
        Text(title, style = MaterialTheme.typography.titleMedium)
        if (!sub.isNullOrBlank()) Text(sub, style = MaterialTheme.typography.bodySmall)
        Spacer(Modifier.height(10.dp))
        content()
    }
}

@Composable
fun Hint(text: String) {
    if (text.isNotBlank()) Text(text, Modifier.padding(vertical = 4.dp), style = MaterialTheme.typography.bodySmall)
}

@Composable
fun <T> Choice(options: List<Pair<String, T>>, selected: T, onPick: (T) -> Unit) {
    Row(Modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp)).background(Color(0x22FFFFFF)).padding(3.dp)) {
        options.forEach { (label, v) ->
            val on = v == selected
            Box(
                Modifier.weight(1f).clip(RoundedCornerShape(10.dp)).background(if (on) NoCap.Red else Color.Transparent).clickable { onPick(v) }.padding(vertical = 8.dp),
                contentAlignment = Alignment.Center,
            ) { Text(label, style = MaterialTheme.typography.labelLarge, color = if (on) Color.White else NoCap.Text, maxLines = 1) }
        }
    }
}

@Composable
fun Toggle(title: String, sub: String?, on: Boolean, onChange: (Boolean) -> Unit) {
    Row(Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).clickable { onChange(!on) }.padding(vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.titleSmall)
            if (!sub.isNullOrBlank()) Text(sub, style = MaterialTheme.typography.bodySmall)
        }
        Switch(on, onChange, colors = SwitchDefaults.colors(checkedTrackColor = NoCap.Red, checkedThumbColor = Color.White))
    }
}

@Composable
fun Action(title: String, danger: Boolean = false, onClick: () -> Unit) {
    Text(
        title, Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).clickable(onClick = onClick).padding(vertical = 10.dp),
        style = MaterialTheme.typography.titleSmall, color = if (danger) NoCap.Red else NoCap.Text,
    )
}

@Composable
private fun SliderRow(title: String, value: Float, range: ClosedFloatingPointRange<Float>, shown: String, onChange: (Float) -> Unit) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Text(title, Modifier.weight(0.3f), style = MaterialTheme.typography.titleSmall)
        Slider(value, onChange, Modifier.weight(0.55f), valueRange = range, colors = SliderDefaults.colors(thumbColor = NoCap.Red, activeTrackColor = NoCap.Red))
        Text(shown, Modifier.weight(0.15f).padding(start = 6.dp), style = MaterialTheme.typography.labelLarge, color = NoCap.Muted)
    }
}

@Composable
private fun Thumb(selected: Boolean, modifier: Modifier, onClick: () -> Unit, content: @Composable () -> Unit) {
    Box(
        modifier.aspectRatio(0.62f).clip(RoundedCornerShape(10.dp))
            .border(if (selected) 2.dp else 1.dp, if (selected) NoCap.Red else NoCap.GlassEdge, RoundedCornerShape(10.dp))
            .clickable(onClick = onClick),
    ) { content() }
}
