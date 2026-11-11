package com.nocap.link.launcher

import android.content.Intent
import android.provider.Settings
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.boundsInRoot
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.nocap.link.Layer
import com.nocap.link.MainActivity
import com.nocap.link.Prefs
import com.nocap.link.ServerState
import com.nocap.link.net.FileEntry
import com.nocap.link.net.ServerTooOld
import com.nocap.link.t
import com.nocap.link.ui.Label
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.SearchField
import com.nocap.link.ui.glass
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext

/** A setting you can jump to from search: the launcher's own, or Android's. */
private class SettingHit(val title: String, val words: String, val go: (MainActivity) -> Unit)

private val openMine: (MainActivity) -> Unit = { it.layer = Layer.SETTINGS }

private fun settingHits(): List<SettingHit> {
    fun sys(action: String): (MainActivity) -> Unit = { a -> runCatching { a.startActivity(Intent(action).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) } }
    val mine = openMine
    return listOf(
        SettingHit(t("Wall Aesthetic", "Wallpaper"), "wallpaper background aesthetic picture theme", mine),
        SettingHit(t("The Lingo", "Language"), "language lingo gen z standard words", mine),
        SettingHit(t("Roster style", "App drawer style"), "drawer launchpad roster grid list categories", mine),
        SettingHit(t("Icons & grid", "Icons and grid"), "icon size grid columns rows labels theme monochrome tinted shape", mine),
        SettingHit(t("Gestures", "Gestures"), "gesture swipe double tap lock pinch", mine),
        SettingHit(t("Theme & accent", "Theme and accent color"), "theme dark light accent color font", mine),
        SettingHit(t("Dock", "Dock"), "dock floating glass", mine),
        SettingHit(t("NoCap Link sync", "NoCap Link sync"), "sync clipboard notifications forward", mine),
        SettingHit(t("Status bar & rotation", "Status bar and rotation"), "status navigation bar rotation haptics", mine),
        SettingHit(t("Notification dots", "Notification dots"), "dots badges notifications", mine),
        SettingHit(t("Hidden apps", "Hidden apps"), "hide hidden unhide", mine),
        SettingHit(t("Backup & restore", "Backup and restore"), "backup restore layout export import", mine),
        SettingHit(t("Make NoCap OS home", "Default home app"), "default home launcher", mine),
        SettingHit(t("Widgets", "Add widgets"), "widget widgets") { it.layer = Layer.WIDGETS },
        SettingHit(t("NoCap Link", "NoCap Link"), "link server nocap desktop unlink pair") { it.layer = Layer.HUB },
        SettingHit("Wi-Fi", "wifi wi-fi wireless internet network", sys(Settings.ACTION_WIFI_SETTINGS)),
        SettingHit("Bluetooth", "bluetooth headphones pair", sys(Settings.ACTION_BLUETOOTH_SETTINGS)),
        SettingHit(t("Display", "Display"), "display brightness screen dark mode", sys(Settings.ACTION_DISPLAY_SETTINGS)),
        SettingHit(t("Sound", "Sound"), "sound volume ringtone vibrate", sys(Settings.ACTION_SOUND_SETTINGS)),
        SettingHit(t("Battery", "Battery"), "battery power saver", sys(Intent.ACTION_POWER_USAGE_SUMMARY)),
        SettingHit(t("Apps", "Apps"), "apps applications manage", sys(Settings.ACTION_APPLICATION_SETTINGS)),
        SettingHit(t("Phone storage", "Storage"), "storage space", sys(Settings.ACTION_INTERNAL_STORAGE_SETTINGS)),
        SettingHit(t("Location", "Location"), "location gps", sys(Settings.ACTION_LOCATION_SOURCE_SETTINGS)),
        SettingHit(t("Phone settings", "Android settings"), "settings system phone android", sys(Settings.ACTION_SETTINGS)),
    )
}

/** Search across phone apps, NoCap settings, Android settings, server apps and server files. */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun SearchLayer(a: MainActivity, ui: HomeUi) {
    var q by remember { mutableStateOf(a.searchStart) }
    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) { a.searchStart = ""; runCatching { focus.requestFocus() } }
    val term = q.trim().lowercase()
    val apps = if (term.isEmpty()) emptyList() else a.phoneApps.filter { it.key !in Prefs.hidden && term in it.label.lowercase() }
    val server = if (term.isEmpty()) emptyList() else a.serverApps.filter { term in it.name.lowercase() || (it.id == "nocapos" && "desktop".contains(term)) }
    val settings = if (term.isEmpty()) emptyList() else settingHits().filter { term in it.title.lowercase() || it.words.split(' ').any { w -> w.startsWith(term) } }
    var files by remember { mutableStateOf<List<FileEntry>>(emptyList()) }
    var filesNote by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(term) {
        files = emptyList(); filesNote = null
        if (term.length < 2 || a.server !is ServerState.Online) return@LaunchedEffect
        delay(300)
        val api = a.api() ?: return@LaunchedEffect
        try {
            files = withContext(Dispatchers.IO) { api.search(term) }
        } catch (e: ServerTooOld) {
            filesNote = a.say(e)
        } catch (e: kotlinx.coroutines.CancellationException) {
            throw e
        } catch (_: Exception) {
        }
    }

    Column(
        Modifier.fillMaxSize().background(Color(0xF007080B)).clickable(remember { MutableInteractionSource() }, null) {}
            .statusBarsPadding().navigationBarsPadding().imePadding().padding(horizontal = 14.dp),
    ) {
        Row(Modifier.fillMaxWidth().padding(top = 10.dp, bottom = 10.dp), verticalAlignment = Alignment.CenterVertically) {
            SearchField(q, { q = it }, t("Apps, files, settings…", "Search apps, files and settings"), Modifier.weight(1f).focusRequester(focus))
            Text(t("Done", "Close"), Modifier.padding(start = 10.dp).clickable { a.layer = Layer.NONE }.padding(6.dp), style = MaterialTheme.typography.labelLarge, color = NoCap.Red)
        }
        LazyColumn(contentPadding = PaddingValues(bottom = 24.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            if (term.isEmpty()) {
                item {
                    Text(t("Type to search your phone, your NoCap server and settings. No cap.", "Search apps, settings, and the files and apps on your NoCap server."), style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted)
                }
            }
            if (apps.isNotEmpty()) item {
                Section(t("On this phone", "Apps")) {
                    LazyRow(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                        items(apps, key = { it.key }) { app ->
                            var r by remember { mutableStateOf(Rect.Zero) }
                            Box(
                                Modifier.width(76.dp).onGloballyPositioned { r = it.boundsInRoot() }.clip(RoundedCornerShape(14.dp))
                                    .combinedClickable(onClick = { a.launch(app); a.layer = Layer.NONE }, onLongClick = { ui.menu = Menu(app, null, r, From.SEARCH) }),
                                contentAlignment = Alignment.Center,
                            ) { AppTile(app, size = 50.dp) }
                        }
                    }
                }
            }
            if (term.isNotEmpty()) item {
                Row(
                    Modifier.fillMaxWidth().glass(RoundedCornerShape(16.dp), edge = NoCap.Red.copy(alpha = 0.45f)).clickable { a.openAi(q.trim()); a.layer = Layer.NONE }.padding(14.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Text("AI", style = MaterialTheme.typography.labelLarge.copy(fontFamily = com.nocap.link.ui.Orbitron), color = NoCap.Red)
                    Text(t("Ask NoCap AI: “${q.trim()}”", "Ask NoCap AI: “${q.trim()}”"), Modifier.padding(start = 12.dp), style = MaterialTheme.typography.titleSmall, maxLines = 2, overflow = TextOverflow.Ellipsis)
                }
            }
            if (settings.isNotEmpty()) item {
                Section(t("Settings", "Settings")) {
                    settings.take(6).forEach { s ->
                        Text(s.title, Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).clickable { a.layer = Layer.NONE; if (s.go === openMine) a.settingsQuery = s.words.substringBefore(' '); s.go(a) }.padding(vertical = 9.dp, horizontal = 4.dp), style = MaterialTheme.typography.titleSmall)
                    }
                }
            }
            if (server.isNotEmpty()) item {
                Section(t("On your NoCap server", "NoCap server apps")) {
                    server.forEach { s ->
                        Row(Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).clickable { a.openServerApp(s) }.padding(vertical = 7.dp), verticalAlignment = Alignment.CenterVertically) {
                            a.serverIcons[s.id]?.let { Image(it, null, Modifier.size(30.dp)) } ?: Spacer(Modifier.size(30.dp))
                            Text(s.name, Modifier.padding(start = 12.dp), style = MaterialTheme.typography.titleSmall)
                        }
                    }
                }
            }
            if (files.isNotEmpty() || filesNote != null) item {
                Section(t("Server files", "Files on your server")) {
                    filesNote?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = NoCap.Warn) }
                    files.forEach { f -> FileRow(f) { if (!f.dir) a.openFile(f) else { a.layer = Layer.HUB } } }
                }
            }
            if (term.isNotEmpty() && apps.isEmpty() && settings.isEmpty() && server.isEmpty() && files.isEmpty()) item {
                Text(t("Nothing called “$q”. Maybe it's not installed?", "No results for “$q”."), style = MaterialTheme.typography.bodyMedium)
            }
        }
    }
}

@Composable
private fun Section(title: String, content: @Composable () -> Unit) {
    Column(Modifier.fillMaxWidth().glass(RoundedCornerShape(18.dp)).padding(12.dp)) {
        Label(title)
        Spacer(Modifier.height(8.dp))
        content()
    }
}

@Composable
fun FileRow(f: FileEntry, onClick: () -> Unit) {
    Row(Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).clickable(onClick = onClick).padding(vertical = 8.dp, horizontal = 4.dp), verticalAlignment = Alignment.CenterVertically) {
        Box(Modifier.size(30.dp).clip(RoundedCornerShape(8.dp)).background(if (f.dir) NoCap.Red.copy(alpha = 0.2f) else Color(0x22FFFFFF)), contentAlignment = Alignment.Center) {
            Text(if (f.dir) "▤" else f.name.substringAfterLast('.', "").take(3).uppercase().ifBlank { "•" }, style = MaterialTheme.typography.labelSmall, color = if (f.dir) NoCap.Red else NoCap.Text)
        }
        Column(Modifier.weight(1f).padding(start = 10.dp)) {
            Text(f.name, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(f.path.substringBeforeLast('/').ifBlank { "/" } + if (!f.dir && f.size > 0) "  ·  ${human(f.size)}" else "", style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
    }
}

fun human(b: Long): String {
    if (b < 1024) return "$b B"
    val u = listOf("KB", "MB", "GB", "TB")
    var v = b / 1024.0
    var i = 0
    while (v >= 1024 && i < u.size - 1) { v /= 1024; i++ }
    return if (v >= 100) "%.0f %s".format(v, u[i]) else "%.1f %s".format(v, u[i])
}
