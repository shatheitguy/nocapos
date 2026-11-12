package com.nocap.link.sync

import android.Manifest
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.Image
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.Icons
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.nocap.link.data.Prefs
import com.nocap.link.data.t
import com.nocap.link.drawer.FileRow
import com.nocap.link.HeartbeatWorker
import com.nocap.link.home.WindowFrame
import com.nocap.link.Layer
import com.nocap.link.MainActivity
import com.nocap.link.net.FileEntry
import com.nocap.link.net.Pins
import com.nocap.link.net.Root
import com.nocap.link.net.ServerApp
import com.nocap.link.R
import com.nocap.link.Screen
import com.nocap.link.ServerState
import com.nocap.link.settings.Toggle
import com.nocap.link.ui.Chip
import com.nocap.link.ui.GhostButton
import com.nocap.link.ui.glass
import com.nocap.link.ui.Label
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.RedButton
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** NoCap Link: your server, its desktop, files, send, AI, notifications and unlink. */
@Composable
fun Hub(a: MainActivity) {
    var unlink by remember { mutableStateOf(false) }
    var sendText by remember { mutableStateOf(false) }
    val askNotif = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { ok ->
        if (ok) { Prefs.setServerNotices(true); HeartbeatWorker.now(a) }
    }
    Column(
        Modifier.fillMaxSize().background(NoCap.Sheet).clickable(remember { MutableInteractionSource() }, null) {}
            .statusBarsPadding().navigationBarsPadding(),
    ) {
        Row(Modifier.fillMaxWidth().padding(start = 18.dp, end = 8.dp, top = 10.dp, bottom = 6.dp), verticalAlignment = Alignment.CenterVertically) {
            Image(painterResource(R.drawable.nocap_mark), null, Modifier.size(26.dp))
            Text("NOCAP LINK", Modifier.weight(1f).padding(start = 10.dp), style = MaterialTheme.typography.headlineSmall)
            IconButton(onClick = { a.layer = Layer.NONE }) { Icon(Icons.Filled.Close, t("Close", "Close"), tint = NoCap.Text) }
        }
        Column(Modifier.verticalScroll(rememberScrollState()).padding(horizontal = 14.dp, vertical = 6.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
            ServerCard(a)
            val online = a.server is ServerState.Online
            if (online) {
                Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                    Big(t("Ask NoCap AI", "NoCap AI"), "AI", Modifier.weight(1f)) { a.openAi() }
                    Big(t("Send a file", "Send a file"), "↑", Modifier.weight(1f)) { a.pickToSend() }
                    Big(t("Send a note", "Send text"), "✎", Modifier.weight(1f)) { sendText = true }
                }
                val apps = a.serverApps.filter { it.id != "nocapos" }
                if (apps.isNotEmpty()) {
                    Column(Modifier.fillMaxWidth().glass(RoundedCornerShape(18.dp)).padding(14.dp)) {
                        Label(t("Server apps", "Server apps"))
                        Spacer(Modifier.height(10.dp))
                        LazyRow(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                            items(apps, key = { it.id }) { app -> ServerAppTile(a, app) }
                        }
                    }
                }
                FilesBrowser(a)
                WifiShareCard(a)
                Column(Modifier.fillMaxWidth().glass(RoundedCornerShape(18.dp)).padding(14.dp)) {
                    Toggle(
                        t("Server tea on this phone", "Server notifications"),
                        t("Get your server's alerts here (admins). Checked about every 15 min.", "Show your server's notifications here (administrators). Checked about every 15 minutes."),
                        Prefs.serverNotices,
                    ) { on ->
                        if (on && Build.VERSION.SDK_INT >= 33 && !Notices.allowed(a)) askNotif.launch(Manifest.permission.POST_NOTIFICATIONS)
                        else { Prefs.setServerNotices(on); if (on) HeartbeatWorker.now(a) }
                    }
                    Text(
                        t("Unlink this phone", "Unlink this phone"), Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).clickable { unlink = true }.padding(vertical = 10.dp),
                        style = MaterialTheme.typography.titleSmall, color = NoCap.Red,
                    )
                }
            }
            Spacer(Modifier.height(20.dp))
        }
    }
    if (unlink) {
        AlertDialog(
            onDismissRequest = { unlink = false },
            containerColor = NoCap.GlassStrong,
            title = { Text(t("Unlink this phone?", "Unlink this phone?"), style = MaterialTheme.typography.titleMedium) },
            text = { Text(t("This phone drops its key, signs out of your NoCap desktop, and your server forgets it.", "This phone deletes its key, signs out of the NoCap desktop and is removed from your server's devices."), style = MaterialTheme.typography.bodyMedium) },
            confirmButton = { TextButton(onClick = { unlink = false; a.forget(alsoServer = true) }) { Text(t("Unlink", "Unlink"), color = NoCap.Red) } },
            dismissButton = { TextButton(onClick = { unlink = false }) { Text(t("Nah", "Cancel"), color = NoCap.Text) } },
        )
    }
    if (sendText) SendTextDialog(a) { sendText = false }
}

@Composable
private fun SendTextDialog(a: MainActivity, onDone: () -> Unit) {
    var text by remember { mutableStateOf("") }
    val scope = rememberCoroutineScope()
    AlertDialog(
        onDismissRequest = onDone,
        containerColor = NoCap.GlassStrong,
        title = { Text(t("Send a note or link", "Send text or a link"), style = MaterialTheme.typography.titleMedium) },
        text = {
            OutlinedTextField(
                text, { text = it }, Modifier.fillMaxWidth().height(140.dp), placeholder = { Text(t("Paste a link, jot a thought…", "Text or link")) },
                colors = OutlinedTextFieldDefaults.colors(focusedBorderColor = NoCap.Red, cursorColor = NoCap.Red),
            )
        },
        confirmButton = {
            TextButton(enabled = text.isNotBlank(), onClick = {
                val api = runCatching { a.api() }.getOrNull()
                onDone()
                if (api == null) { a.toast = t("Link your server first.", "Link your server first."); return@TextButton }
                scope.launch {
                    a.toast = try {
                        withContext(Dispatchers.IO) { api.sendText(text, null) }
                        t("Sent to NoCap Link in your home. W.", "Saved to NoCap Link in your home folder.")
                    } catch (e: kotlinx.coroutines.CancellationException) {
                        throw e
                    } catch (e: Exception) {
                        a.say(e)
                    }
                }
            }) { Text(t("Send it", "Send"), color = NoCap.Red) }
        },
        dismissButton = { TextButton(onClick = onDone) { Text(t("Nah", "Cancel"), color = NoCap.Text) } },
    )
}

@Composable
private fun Big(label: String, glyph: String, modifier: Modifier, onClick: () -> Unit) {
    Column(
        modifier.glass(RoundedCornerShape(18.dp)).clickable(onClick = onClick).padding(vertical = 14.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(glyph, style = MaterialTheme.typography.headlineSmall, color = NoCap.Red)
        Spacer(Modifier.height(6.dp))
        Text(label, style = MaterialTheme.typography.labelLarge, textAlign = TextAlign.Center, maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}

@Composable
private fun ServerCard(a: MainActivity) {
    val link = a.link
    Column(Modifier.fillMaxWidth().glass(RoundedCornerShape(20.dp)).padding(18.dp)) {
        when (val s = a.server) {
            ServerState.NotLinked -> {
                Header(t("Not linked yet", "Not linked"), null, null)
                Spacer(Modifier.height(12.dp))
                Text(t("Link this phone to your NoCapOS and your whole desktop, files, AI and server apps are one tap away. No cap.", "Link this phone to your NoCapOS server to reach its desktop, files, AI Assistant and apps."), style = MaterialTheme.typography.bodyMedium)
                Spacer(Modifier.height(16.dp))
                RedButton(t("Link with NoCap Link", "Link with NoCap Link"), { a.layer = Layer.NONE; a.screen = Screen.LinkForm() }, Modifier.fillMaxWidth())
            }
            ServerState.Unlinked -> {
                Header(link?.serverName ?: "NoCapOS", t("Unlinked", "Unlinked"), NoCap.Bad)
                Spacer(Modifier.height(10.dp))
                Text(t("Your server doesn't know this phone anymore (it got unlinked in Settings → The Squad). Link it again to keep vibing.", "Your server no longer recognizes this phone (it was unlinked in Settings → Devices). Link it again."), style = MaterialTheme.typography.bodyMedium)
                Spacer(Modifier.height(16.dp))
                RedButton(t("Link again", "Link again"), { a.forget(); a.layer = Layer.NONE; a.screen = Screen.LinkForm(server = link?.server ?: "") }, Modifier.fillMaxWidth())
            }
            is ServerState.CertChanged -> CertChangedPanel(a, s.seen, link?.serverName ?: "NoCapOS")
            else -> {
                val (label, color) = when (s) {
                    is ServerState.Online -> t("Online", "Online") to NoCap.Ok
                    is ServerState.Offline -> t("Offline", "Offline") to NoCap.Warn
                    else -> t("Checking…", "Checking…") to NoCap.Muted
                }
                val name = (s as? ServerState.Online)?.me?.server?.name ?: link?.serverName ?: "NoCapOS"
                Header(name, label, color)
                val version = (s as? ServerState.Online)?.me?.server?.version
                Text(
                    listOfNotNull(link?.nocapId, link?.username?.let { "@$it" }, version?.let { "v$it" }).joinToString("  ·  "),
                    Modifier.padding(top = 4.dp), style = MaterialTheme.typography.bodySmall,
                )
                if (s is ServerState.Offline) Text(s.why, Modifier.padding(top = 8.dp), style = MaterialTheme.typography.bodyMedium, color = NoCap.Warn)
                if (a.outdated != null) {
                    Text(
                        MainActivity.needsUpdate(a.outdated) + " " + t("Widgets, files, Send to NoCap and AI light up after that.", "Widgets, files, Send to NoCap and AI need it."),
                        Modifier.padding(top = 8.dp), style = MaterialTheme.typography.bodyMedium, color = NoCap.Warn,
                    )
                }
                Spacer(Modifier.height(16.dp))
                RedButton(t("Open my NoCap desktop", "Open my NoCap desktop"), { a.openDesktop() }, Modifier.fillMaxWidth()) {
                    Image(painterResource(R.drawable.nocap_mark), null, Modifier.size(20.dp))
                }
            }
        }
    }
}

@Composable
private fun Header(name: String, status: String?, color: Color?) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Image(painterResource(R.drawable.nocap_mark), null, Modifier.size(40.dp))
        Column(Modifier.weight(1f).padding(start = 12.dp)) {
            Label("NoCap Link")
            Text(name, style = MaterialTheme.typography.headlineSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        if (status != null && color != null) Chip(status, color)
    }
}

@Composable
private fun CertChangedPanel(a: MainActivity, seen: String, name: String) {
    var confirm by remember { mutableStateOf(false) }
    Header(name, t("Cert changed", "Certificate changed"), NoCap.Warn)
    Spacer(Modifier.height(10.dp))
    Text(
        t(
            "Your server is showing a different security certificate than the one this phone trusted. That's fine if you just reinstalled NoCapOS or changed its certificate — sus if you didn't.",
            "Your server presented a different security certificate than the one this phone trusts. That's expected after reinstalling NoCapOS or changing its certificate; otherwise, don't trust it.",
        ),
        style = MaterialTheme.typography.bodyMedium,
    )
    Spacer(Modifier.height(16.dp))
    Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
        GhostButton(t("Forget link", "Forget link"), { a.forget() }, Modifier.weight(1f))
        RedButton(t("Check it", "Review"), { confirm = true }, Modifier.weight(1f))
    }
    if (confirm) {
        AlertDialog(
            onDismissRequest = { confirm = false },
            containerColor = NoCap.GlassStrong,
            title = { Text(t("Trust the new certificate?", "Trust the new certificate?"), style = MaterialTheme.typography.titleMedium) },
            text = {
                Column {
                    Text(t("Only say yes if you changed it yourself. New fingerprint (SHA-256):", "Only trust it if you changed it yourself. New fingerprint (SHA-256):"), style = MaterialTheme.typography.bodyMedium)
                    Spacer(Modifier.height(8.dp))
                    Text(Pins.pretty(seen), style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace), color = NoCap.Text)
                }
            },
            confirmButton = { TextButton(onClick = { confirm = false; a.trustNewCert(seen) }) { Text(t("Trust it", "Trust"), color = NoCap.Red) } },
            dismissButton = { TextButton(onClick = { confirm = false }) { Text(t("Nope", "Cancel"), color = NoCap.Text) } },
        )
    }
}

@Composable
private fun ServerAppTile(a: MainActivity, app: ServerApp) {
    Column(Modifier.width(68.dp).clickable { a.openServerApp(app) }, horizontalAlignment = Alignment.CenterHorizontally) {
        Box(Modifier.size(56.dp).glass(RoundedCornerShape(16.dp)), contentAlignment = Alignment.Center) {
            val icon = a.serverIcons[app.id]
            if (icon != null) Image(icon, null, Modifier.size(36.dp))
            else Text(app.name.take(1).uppercase(), style = MaterialTheme.typography.headlineSmall, color = NoCap.Red)
        }
        Text(app.name, Modifier.padding(top = 6.dp), style = MaterialTheme.typography.labelSmall, maxLines = 1, overflow = TextOverflow.Ellipsis, textAlign = TextAlign.Center)
    }
}

/** Files quick view: your locations, then folders; tap a file to open it on the phone. */
@Composable
private fun FilesBrowser(a: MainActivity) {
    var roots by remember { mutableStateOf<List<Root>?>(null) }
    var root by remember { mutableStateOf<Root?>(null) }
    var path by remember { mutableStateOf("/") }
    var entries by remember { mutableStateOf<List<FileEntry>>(emptyList()) }
    var error by remember { mutableStateOf<String?>(null) }
    var loading by remember { mutableStateOf(false) }
    LaunchedEffect(Unit) {
        try {
            val api = a.api()
            if (api == null) { error = t("Link your server to browse its files.", "Link your server to see its files."); return@LaunchedEffect }
            roots = withContext(Dispatchers.IO) { api.roots() }
        } catch (e: kotlinx.coroutines.CancellationException) {
            throw e
        } catch (e: Exception) {
            error = a.say(e)
        }
    }
    LaunchedEffect(root, path) {
        val r = root ?: return@LaunchedEffect
        loading = true
        try {
            val api = a.api() ?: throw java.io.IOException(t("Link your server to browse its files.", "Link your server to see its files."))
            entries = withContext(Dispatchers.IO) { api.list(r.id, path) }
            error = null
        } catch (e: kotlinx.coroutines.CancellationException) {
            throw e
        } catch (e: Exception) {
            error = a.say(e)
        } finally {
            loading = false
        }
    }
    WindowFrame(
        title = {
            Text(
                root?.let { r -> r.name + if (path != "/") path else "" } ?: t("Files", "Files"),
                style = MaterialTheme.typography.labelLarge, maxLines = 1, overflow = TextOverflow.Ellipsis,
            )
        },
        onClose = null,
    ) {
        Column(Modifier.padding(12.dp)) {
            error?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = NoCap.Warn) }
            val r = root
            if (r == null) {
                val rs = roots
                if (rs == null && error == null) Text(t("Loading your spots…", "Loading…"), style = MaterialTheme.typography.bodySmall)
                if (rs != null && rs.isEmpty()) Text(t("No file spots shared with you yet.", "No file locations available."), style = MaterialTheme.typography.bodySmall)
                rs?.forEach { x ->
                    FileRow(FileEntry(x.id, "/", x.name, true, 0, "")) { root = x; path = "/" }
                }
            } else {
                Text(
                    "‹ " + t("Back", "Back"), Modifier.clip(RoundedCornerShape(8.dp)).clickable {
                        if (path == "/" || path.isBlank()) root = null else path = path.trimEnd('/').substringBeforeLast('/').ifBlank { "/" }
                    }.padding(vertical = 6.dp, horizontal = 4.dp),
                    style = MaterialTheme.typography.labelLarge, color = NoCap.Red,
                )
                if (loading) Text(t("Loading…", "Loading…"), style = MaterialTheme.typography.bodySmall)
                if (!loading && entries.isEmpty() && error == null) Text(t("Empty folder. Clean.", "This folder is empty."), style = MaterialTheme.typography.bodySmall)
                entries.take(200).forEach { f ->
                    FileRow(f) { if (f.dir) path = f.path else a.openFile(f) }
                }
            }
        }
    }
}
