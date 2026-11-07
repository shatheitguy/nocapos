package com.nocap.link.ui

import android.content.Intent
import android.provider.Settings
import android.text.format.DateFormat
import androidx.activity.compose.BackHandler
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutVertically
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.gestures.detectVerticalDragGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.nocap.link.BuildConfig
import com.nocap.link.MainActivity
import com.nocap.link.PhoneApp
import com.nocap.link.PhoneApps
import com.nocap.link.R
import com.nocap.link.Screen
import com.nocap.link.ServerState
import com.nocap.link.Vault
import com.nocap.link.net.Pins
import com.nocap.link.net.ServerApp
import kotlinx.coroutines.delay
import java.util.Date

@Composable
fun HomeScreen(a: MainActivity) {
    var launchpad by remember { mutableStateOf(false) }
    var settings by remember { mutableStateOf(false) }
    var query by remember { mutableStateOf("") }
    var menuFor by remember { mutableStateOf<PhoneApp?>(null) }

    BackHandler(launchpad || settings || query.isNotEmpty()) {
        when {
            settings -> settings = false
            launchpad -> launchpad = false
            else -> query = ""
        }
    }
    LaunchedEffect(a.toast) {
        if (a.toast != null) { delay(2800); a.toast = null }
    }

    Wallpaper {
        Column(
            Modifier.fillMaxSize().statusBarsPadding().navigationBarsPadding().padding(horizontal = 18.dp)
                .pointerInput(Unit) {
                    var drag = 0f
                    detectVerticalDragGestures(onDragStart = { drag = 0f }, onDragEnd = { if (drag < -120f) launchpad = true }) { _, dy -> drag += dy }
                },
        ) {
            TopBar(onSettings = { settings = true })
            Clock()
            Spacer(Modifier.height(14.dp))
            SearchField(query, { query = it }, "Search apps")
            Spacer(Modifier.height(14.dp))
            Box(Modifier.weight(1f).fillMaxWidth()) {
                if (query.isNotBlank()) {
                    SearchResults(a, query, onLongPress = { menuFor = it })
                } else {
                    Column(Modifier.verticalScroll(rememberScrollState())) { LinkPanel(a) }
                }
            }
            Dock(a, onLaunchpad = { launchpad = true })
            Spacer(Modifier.height(10.dp))
        }

        AnimatedVisibility(launchpad, enter = fadeIn() + slideInVertically { it / 6 }, exit = fadeOut() + slideOutVertically { it / 6 }) {
            Launchpad(a, onClose = { launchpad = false }, onLongPress = { menuFor = it })
        }
        AnimatedVisibility(settings, enter = fadeIn(), exit = fadeOut()) {
            SettingsSheet(a, onClose = { settings = false })
        }
        a.toast?.let { Toast(it, Modifier.align(Alignment.BottomCenter).navigationBarsPadding().padding(bottom = 110.dp)) }
    }

    menuFor?.let { app ->
        val pinned = app.key in a.dock
        AlertDialog(
            onDismissRequest = { menuFor = null },
            containerColor = NoCap.GlassStrong,
            title = { Text(app.label, style = MaterialTheme.typography.titleMedium) },
            text = { Text("Pin it to your dock or peek at its settings.", style = MaterialTheme.typography.bodyMedium) },
            confirmButton = { TextButton(onClick = { a.toggleDock(app); menuFor = null }) { Text(if (pinned) "Unpin from dock" else "Pin to dock", color = NoCap.Red) } },
            dismissButton = { TextButton(onClick = { PhoneApps.info(a, app); menuFor = null }) { Text("App info", color = NoCap.Text) } },
        )
    }
}

@Composable
private fun TopBar(onSettings: () -> Unit) {
    Row(Modifier.fillMaxWidth().padding(top = 8.dp), verticalAlignment = Alignment.CenterVertically) {
        Image(painterResource(R.drawable.nocap_mark), contentDescription = null, modifier = Modifier.size(26.dp))
        Text("NOCAP OS", Modifier.padding(start = 10.dp), style = MaterialTheme.typography.labelMedium.copy(fontFamily = Orbitron, fontSize = 12.sp, letterSpacing = 3.sp))
        Spacer(Modifier.weight(1f))
        IconButton(onClick = onSettings) { Icon(Icons.Filled.Settings, contentDescription = "Settings", tint = NoCap.Muted) }
    }
}

@Composable
private fun Clock() {
    val ctx = LocalContext.current
    var now by remember { mutableStateOf(Date()) }
    LaunchedEffect(Unit) {
        while (true) { now = Date(); delay(1000L - System.currentTimeMillis() % 1000L) }
    }
    val time = DateFormat.format(if (DateFormat.is24HourFormat(ctx)) "HH:mm" else "h:mm", now).toString()
    val date = DateFormat.format("EEEE, d MMMM", now).toString()
    Column(Modifier.fillMaxWidth().padding(top = 18.dp)) {
        Text(time, style = MaterialTheme.typography.displayLarge)
        Label(date, Modifier.padding(start = 4.dp), color = NoCap.Red)
    }
}

@Composable
fun SearchField(value: String, onChange: (String) -> Unit, hint: String, modifier: Modifier = Modifier) {
    Row(
        modifier.fillMaxWidth().height(48.dp).glass(RoundedCornerShape(24.dp)).padding(horizontal = 16.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(Icons.Filled.Search, contentDescription = null, tint = NoCap.Muted, modifier = Modifier.size(20.dp))
        Box(Modifier.weight(1f).padding(horizontal = 10.dp)) {
            if (value.isEmpty()) Text(hint, style = MaterialTheme.typography.bodyLarge, color = NoCap.Muted)
            BasicTextField(
                value, onChange, singleLine = true, modifier = Modifier.fillMaxWidth(),
                textStyle = MaterialTheme.typography.bodyLarge, cursorBrush = SolidColor(NoCap.Red),
            )
        }
        if (value.isNotEmpty()) {
            Icon(Icons.Filled.Close, contentDescription = "Clear", tint = NoCap.Muted, modifier = Modifier.size(20.dp).clickable { onChange("") })
        }
    }
}

// ---------- NoCap Link panel ----------

@Composable
private fun LinkPanel(a: MainActivity) {
    val link = remember(a.server) { Vault.get(a).link }
    GlassCard(Modifier.fillMaxWidth()) {
        Column {
            when (val s = a.server) {
                ServerState.NotLinked -> {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Image(painterResource(R.drawable.nocap_mark), null, Modifier.size(40.dp))
                        Column(Modifier.padding(start = 12.dp)) {
                            Text("NOCAP LINK", style = MaterialTheme.typography.headlineSmall)
                            Text("Not linked yet", style = MaterialTheme.typography.bodySmall)
                        }
                    }
                    Spacer(Modifier.height(12.dp))
                    Text("Link this phone to your NoCapOS and your whole desktop + server apps are one tap away. No cap.", style = MaterialTheme.typography.bodyMedium)
                    Spacer(Modifier.height(16.dp))
                    RedButton("Link with NoCap Link", { a.screen = Screen.LinkForm() }, Modifier.fillMaxWidth())
                }
                ServerState.Unlinked -> {
                    Header(link?.serverName ?: "NoCapOS", "Unlinked", NoCap.Bad)
                    Spacer(Modifier.height(10.dp))
                    Text("Your server doesn't know this phone anymore (it got unlinked in Settings → Devices). Link it again to keep vibing.", style = MaterialTheme.typography.bodyMedium)
                    Spacer(Modifier.height(16.dp))
                    RedButton("Link again", { a.forget(); a.screen = Screen.LinkForm(server = link?.server ?: "") }, Modifier.fillMaxWidth())
                }
                is ServerState.CertChanged -> CertChangedPanel(a, s.seen, link?.serverName ?: "NoCapOS")
                else -> {
                    val (label, color) = when (s) {
                        is ServerState.Online -> "Online" to NoCap.Ok
                        is ServerState.Offline -> "Offline" to NoCap.Warn
                        else -> "Checking…" to NoCap.Muted
                    }
                    val name = (s as? ServerState.Online)?.me?.server?.name ?: link?.serverName ?: "NoCapOS"
                    Header(name, label, color)
                    val version = (s as? ServerState.Online)?.me?.server?.version
                    Text(
                        listOfNotNull(link?.nocapId, link?.username?.let { "@$it" }, version?.let { "v$it" }).joinToString("  ·  "),
                        Modifier.padding(top = 4.dp), style = MaterialTheme.typography.bodySmall,
                    )
                    if (s is ServerState.Offline) {
                        Text(s.why, Modifier.padding(top = 8.dp), style = MaterialTheme.typography.bodyMedium, color = NoCap.Warn)
                    }
                    Spacer(Modifier.height(16.dp))
                    RedButton("Open my NoCap desktop", { a.openDesktop() }, Modifier.fillMaxWidth()) {
                        Image(painterResource(R.drawable.nocap_mark), null, Modifier.size(20.dp))
                    }
                    val apps = a.serverApps.filter { it.id != "nocapos" }
                    if (apps.isNotEmpty()) {
                        Spacer(Modifier.height(18.dp))
                        Label("Server apps")
                        Spacer(Modifier.height(10.dp))
                        LazyRow(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                            items(apps, key = { it.id }) { app -> ServerAppTile(app, a.serverIcons[app.id]) { a.openServerApp(app) } }
                        }
                    } else if (s is ServerState.Online) {
                        Spacer(Modifier.height(12.dp))
                        Text("No server apps with a web page yet — grab some from the App Store on your NoCap desktop.", style = MaterialTheme.typography.bodySmall)
                    }
                }
            }
        }
    }
}

@Composable
private fun Header(name: String, status: String, color: Color) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Image(painterResource(R.drawable.nocap_mark), null, Modifier.size(40.dp))
        Column(Modifier.weight(1f).padding(start = 12.dp)) {
            Label("NoCap Link")
            Text(name, style = MaterialTheme.typography.headlineSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        Chip(status, color)
    }
}

@Composable
private fun CertChangedPanel(a: MainActivity, seen: String, name: String) {
    var confirm by remember { mutableStateOf(false) }
    Header(name, "Cert changed", NoCap.Warn)
    Spacer(Modifier.height(10.dp))
    Text(
        "Your server is showing a different security certificate than the one this phone trusted. That's fine if you just reinstalled NoCapOS or changed its certificate — sus if you didn't.",
        style = MaterialTheme.typography.bodyMedium,
    )
    Spacer(Modifier.height(16.dp))
    Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
        GhostButton("Forget link", { a.forget() }, Modifier.weight(1f))
        RedButton("Check it", { confirm = true }, Modifier.weight(1f))
    }
    if (confirm) {
        AlertDialog(
            onDismissRequest = { confirm = false },
            containerColor = NoCap.GlassStrong,
            title = { Text("Trust the new certificate?", style = MaterialTheme.typography.titleMedium) },
            text = {
                Column {
                    Text("Only say yes if you changed it yourself. New fingerprint (SHA-256):", style = MaterialTheme.typography.bodyMedium)
                    Spacer(Modifier.height(8.dp))
                    Text(Pins.pretty(seen), style = MaterialTheme.typography.bodySmall.copy(fontFamily = androidx.compose.ui.text.font.FontFamily.Monospace), color = NoCap.Text)
                }
            },
            confirmButton = { TextButton(onClick = { confirm = false; a.trustNewCert(seen) }) { Text("Trust it", color = NoCap.Red) } },
            dismissButton = { TextButton(onClick = { confirm = false }) { Text("Nope", color = NoCap.Text) } },
        )
    }
}

@Composable
private fun ServerAppTile(app: ServerApp, icon: ImageBitmap?, onClick: () -> Unit) {
    Column(Modifier.width(68.dp).clickable(onClick = onClick), horizontalAlignment = Alignment.CenterHorizontally) {
        Box(Modifier.size(56.dp).glass(RoundedCornerShape(16.dp)), contentAlignment = Alignment.Center) {
            if (icon != null) Image(icon, null, Modifier.size(36.dp))
            else Text(app.name.take(1).uppercase(), style = MaterialTheme.typography.headlineSmall, color = NoCap.Red)
        }
        Text(app.name, Modifier.padding(top = 6.dp), style = MaterialTheme.typography.labelSmall, maxLines = 1, overflow = TextOverflow.Ellipsis, textAlign = TextAlign.Center)
    }
}

// ---------- apps ----------

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun AppTile(app: PhoneApp, onClick: () -> Unit, onLongPress: () -> Unit, iconSize: Dp = 52.dp, label: Boolean = true) {
    Column(
        Modifier.clip(RoundedCornerShape(14.dp)).combinedClickable(onClick = onClick, onLongClick = onLongPress).padding(vertical = 6.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Image(app.icon, contentDescription = app.label, modifier = Modifier.size(iconSize))
        if (label) {
            Text(
                app.label, Modifier.padding(top = 6.dp).widthIn(max = 76.dp), style = MaterialTheme.typography.labelSmall,
                maxLines = 1, overflow = TextOverflow.Ellipsis, textAlign = TextAlign.Center,
            )
        }
    }
}

@Composable
private fun SearchResults(a: MainActivity, query: String, onLongPress: (PhoneApp) -> Unit) {
    val q = query.trim().lowercase()
    val phone = a.phoneApps.filter { q in it.label.lowercase() }
    val server = a.serverApps.filter { q in it.name.lowercase() || (it.id == "nocapos" && "desktop".contains(q)) }
    Column(Modifier.fillMaxSize().glass().padding(14.dp)) {
        if (phone.isEmpty() && server.isEmpty()) {
            Text("Nothing called \"$query\". Maybe it's not installed?", style = MaterialTheme.typography.bodyMedium)
            return@Column
        }
        if (server.isNotEmpty()) {
            Label("On your server")
            Spacer(Modifier.height(8.dp))
            LazyRow(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                items(server, key = { it.id }) { app -> ServerAppTile(app, a.serverIcons[app.id]) { a.openServerApp(app) } }
            }
            Spacer(Modifier.height(14.dp))
        }
        if (phone.isNotEmpty()) {
            Label("On this phone")
            Spacer(Modifier.height(8.dp))
            LazyVerticalGrid(GridCells.Adaptive(78.dp)) {
                items(phone, key = { it.key }) { app -> AppTile(app, { PhoneApps.launch(a, app) }, { onLongPress(app) }) }
            }
        }
    }
}

@Composable
private fun Dock(a: MainActivity, onLaunchpad: () -> Unit) {
    val apps = a.dock.mapNotNull { k -> a.phoneApps.firstOrNull { it.key == k } }
    Row(
        Modifier.fillMaxWidth().glass(RoundedCornerShape(26.dp)).padding(horizontal = 12.dp, vertical = 10.dp),
        horizontalArrangement = Arrangement.SpaceEvenly, verticalAlignment = Alignment.CenterVertically,
    ) {
        apps.forEach { app -> AppTile(app, { PhoneApps.launch(a, app) }, { a.toggleDock(app) }, iconSize = 46.dp, label = false) }
        if (apps.isNotEmpty()) Box(Modifier.width(1.dp).height(36.dp).background(NoCap.GlassEdge))
        if (a.server !is ServerState.NotLinked) {
            Box(Modifier.size(50.dp).clip(RoundedCornerShape(14.dp)).clickable { a.openDesktop() }, contentAlignment = Alignment.Center) {
                Image(painterResource(R.drawable.nocap_mark), contentDescription = "Open my NoCap desktop", modifier = Modifier.size(40.dp))
            }
        }
        Box(Modifier.size(50.dp).clip(RoundedCornerShape(14.dp)).clickable(onClick = onLaunchpad), contentAlignment = Alignment.Center) {
            Canvas(Modifier.size(26.dp)) {
                val r = size.minDimension / 12f
                for (i in 0..2) for (j in 0..2) {
                    drawCircle(if (i == 1 && j == 1) NoCap.Red else NoCap.Text, r, Offset(size.width * (i * 2 + 1) / 6f, size.height * (j * 2 + 1) / 6f))
                }
            }
        }
    }
}

@Composable
private fun Launchpad(a: MainActivity, onClose: () -> Unit, onLongPress: (PhoneApp) -> Unit) {
    var q by remember { mutableStateOf("") }
    val shown = a.phoneApps.filter { q.isBlank() || q.trim().lowercase() in it.label.lowercase() }
    Column(Modifier.fillMaxSize().background(Color(0xF007080B)).statusBarsPadding().navigationBarsPadding().padding(horizontal = 18.dp)) {
        Row(Modifier.fillMaxWidth().padding(top = 14.dp, bottom = 12.dp), verticalAlignment = Alignment.CenterVertically) {
            Text("LAUNCHPAD", style = MaterialTheme.typography.headlineSmall, modifier = Modifier.weight(1f))
            IconButton(onClick = onClose) { Icon(Icons.Filled.Close, contentDescription = "Close", tint = NoCap.Text) }
        }
        SearchField(q, { q = it }, "Search ${a.phoneApps.size} apps")
        Spacer(Modifier.height(12.dp))
        LazyVerticalGrid(GridCells.Adaptive(80.dp), contentPadding = PaddingValues(bottom = 24.dp), modifier = Modifier.weight(1f)) {
            items(shown, key = { it.key }) { app -> AppTile(app, { PhoneApps.launch(a, app); onClose() }, { onLongPress(app) }) }
        }
        Text("Long-press an app to pin it to your dock.", Modifier.fillMaxWidth().padding(bottom = 12.dp), style = MaterialTheme.typography.bodySmall, textAlign = TextAlign.Center)
    }
}

@Composable
private fun SettingsSheet(a: MainActivity, onClose: () -> Unit) {
    var forget by remember { mutableStateOf(false) }
    val linked = a.server !is ServerState.NotLinked
    Box(Modifier.fillMaxSize().background(Color(0xB3000000)).clickable(onClick = onClose)) {
        Column(
            Modifier.align(Alignment.BottomCenter).fillMaxWidth().navigationBarsPadding().padding(12.dp)
                .glass(RoundedCornerShape(24.dp), strong = true).clickable(enabled = false) {}.padding(20.dp),
        ) {
            Text("SETTINGS", style = MaterialTheme.typography.headlineSmall)
            Spacer(Modifier.height(14.dp))
            SheetRow("Permissions", "What NoCap OS uses and why") { onClose(); a.screen = Screen.Intro }
            SheetRow("Make NoCap OS your home screen", "Pick it in your phone's default apps") {
                runCatching { a.startActivity(Intent(Settings.ACTION_HOME_SETTINGS)) }
                    .onFailure { a.startActivity(Intent(Settings.ACTION_SETTINGS)) }
            }
            if (linked) SheetRow("Forget NoCap Link on this phone", "Also unlink it in Settings → Devices on your server", danger = true) { forget = true }
            else SheetRow("Link with NoCap Link", "Pair this phone with your NoCapOS") { onClose(); a.screen = Screen.LinkForm() }
            Spacer(Modifier.height(10.dp))
            Text("NoCap OS ${BuildConfig.VERSION_NAME} · Orbitron & Rajdhani fonts under the SIL Open Font License", style = MaterialTheme.typography.bodySmall)
        }
    }
    if (forget) {
        AlertDialog(
            onDismissRequest = { forget = false },
            containerColor = NoCap.GlassStrong,
            title = { Text("Forget the link?", style = MaterialTheme.typography.titleMedium) },
            text = { Text("This phone drops its key and signs out of your NoCap desktop. Unlink it in Settings → Devices too, so the server forgets it.", style = MaterialTheme.typography.bodyMedium) },
            confirmButton = { TextButton(onClick = { forget = false; onClose(); a.forget() }) { Text("Forget it", color = NoCap.Red) } },
            dismissButton = { TextButton(onClick = { forget = false }) { Text("Nah", color = NoCap.Text) } },
        )
    }
}

@Composable
private fun SheetRow(title: String, sub: String, danger: Boolean = false, onClick: () -> Unit) {
    Column(Modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp)).clickable(onClick = onClick).padding(vertical = 12.dp, horizontal = 6.dp)) {
        Text(title, style = MaterialTheme.typography.titleSmall, color = if (danger) NoCap.Red else NoCap.Text)
        Text(sub, style = MaterialTheme.typography.bodySmall)
    }
}

@Composable
fun Toast(text: String, modifier: Modifier = Modifier) {
    Box(modifier.padding(horizontal = 24.dp).glass(RoundedCornerShape(50), strong = true, edge = NoCap.Red.copy(alpha = 0.5f)).padding(horizontal = 18.dp, vertical = 10.dp)) {
        Text(text, style = MaterialTheme.typography.titleSmall)
    }
}
