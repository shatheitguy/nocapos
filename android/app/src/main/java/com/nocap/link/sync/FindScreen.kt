package com.nocap.link.sync

import android.content.Intent
import android.provider.Settings
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.spring
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ArrowBack
import com.nocap.link.ui.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.nocap.link.MainActivity
import com.nocap.link.Screen
import com.nocap.link.data.Link
import com.nocap.link.data.Vault
import com.nocap.link.data.t
import com.nocap.link.net.ApiError
import com.nocap.link.net.NoCapApi
import com.nocap.link.net.Pins
import com.nocap.link.net.Probe
import com.nocap.link.net.ServerTooOld
import com.nocap.link.settings.Permissions
import com.nocap.link.ui.Chip
import com.nocap.link.ui.GhostButton
import com.nocap.link.ui.GlassCard
import com.nocap.link.ui.Label
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.RedButton
import com.nocap.link.ui.Wallpaper
import com.nocap.link.ui.reducedMotion
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** The radar keeps spinning at least this long, so a quick answer still reads as a search. */
private const val MIN_SCAN_MS = 2400L

/** A link request this phone is waiting on. */
private class Waiting(val probe: Probe, val asked: LinkAsked, val name: String)

/**
 * Find NoCap servers: on this Wi-Fi (mDNS) and nearby over Bluetooth, with
 * the NoCap radar while it looks. Tapping a server that's set up offers
 * "Ask to link this phone" (no code: someone on NoCap OS accepts it, after
 * checking the same four digits) or the code form; a brand-new one can be
 * set up from here; the one this phone is linked to shows as linked.
 * Bluetooth needs "Nearby devices" (location on Android 11 and older),
 * asked for only when the person taps Allow, and only used while searching.
 */
@Composable
fun FindScreen(a: MainActivity, onBack: () -> Unit, onLinked: () -> Unit = onBack) {
    val scope = rememberCoroutineScope()
    var found by remember { mutableStateOf<List<FoundServer>>(emptyList()) }
    var lanBusy by remember { mutableStateOf(false) }
    var bleBusy by remember { mutableStateOf(false) }
    var rounds by remember { mutableIntStateOf(0) }
    var bluetoothOnly by remember { mutableStateOf<FoundServer?>(null) }
    var picked by remember { mutableStateOf<FoundServer?>(null) }
    var trust by remember { mutableStateOf<Pair<FoundServer, Probe>?>(null) }
    var waiting by remember { mutableStateOf<Waiting?>(null) }
    var busy by remember { mutableStateOf(false) }
    var problem by remember { mutableStateOf<String?>(null) }
    val bt = remember(a.resumeTick, rounds) { Finder.bt(a) }
    val current = remember(rounds, waiting) { runCatching { Vault.get(a).link }.getOrNull() }
    val linkedId = current?.nocapId

    fun scan() {
        if (lanBusy || bleBusy) return
        lanBusy = true
        val withBle = Finder.bt(a) == Finder.Bt.READY
        bleBusy = withBle
        scope.launch {
            val started = System.currentTimeMillis()
            val ble = async(Dispatchers.IO) { if (withBle) runCatching { Finder.bluetooth(a) }.getOrDefault(emptyList()) else emptyList() }
            val lan = withContext(Dispatchers.IO) { runCatching { Finder.lan(a) }.getOrDefault(emptyList()) }
            found = Finder.merge(lan, emptyList())
            val left = MIN_SCAN_MS - (System.currentTimeMillis() - started)
            if (left > 0 && !withBle) delay(left)
            lanBusy = false
            found = Finder.merge(lan, ble.await())
            bleBusy = false
            rounds++
        }
    }

    LaunchedEffect(Unit) { scan() }

    fun open(s: FoundServer) {
        val origin = s.origin
        when {
            origin == null -> bluetoothOnly = s
            s.isNew -> a.screen = Screen.NewServer(origin, s.nocapId, s.name)
            else -> picked = s
        }
    }

    // Check the server's certificate before asking (it gets pinned).
    fun check(s: FoundServer) {
        val origin = s.origin ?: return
        busy = true
        scope.launch {
            try {
                val p = withContext(Dispatchers.IO) { NoCapApi.probe(origin) }
                if (s.nocapId.isNotBlank() && !p.info.nocapId.equals(s.nocapId, ignoreCase = true)) {
                    problem = t(
                        "That address answered as ${p.info.nocapId}, not ${s.nocapId}. Not asking: that's sus.",
                        "The server at that address is ${p.info.nocapId}, not ${s.nocapId}. The request wasn't sent.",
                    )
                } else if (p.pin == null) {
                    problem = t("Asking needs the server on https. Use a code instead.", "Asking to link requires HTTPS. Use a pairing code instead.")
                } else {
                    trust = s to p
                }
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                problem = MainActivity.friendly(e)
            } finally {
                busy = false
            }
        }
    }

    fun ask(s: FoundServer, p: Probe) {
        busy = true
        scope.launch {
            try {
                val name = deviceName()
                val asked = withContext(Dispatchers.IO) { NoCapApi(p.server, p.pin).askToLink(name) }
                waiting = Waiting(p, asked, s.name.ifBlank { p.info.name })
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (_: ServerTooOld) {
                problem = t("That server needs a NoCapOS update for this. Use a code for now.", "That server needs a newer NoCapOS to accept link requests. Use a pairing code instead.")
            } catch (e: ApiError) {
                problem = e.message?.ifBlank { null } ?: MainActivity.friendly(e)
            } catch (e: Exception) {
                problem = MainActivity.friendly(e)
            } finally {
                busy = false
            }
        }
    }

    // While a request waits: check on it every couple of seconds.
    waiting?.let { w ->
        LaunchedEffect(w) {
            val api = NoCapApi(w.probe.server, w.probe.pin)
            val until = System.currentTimeMillis() + LinkAsk.TTL_MS
            while (System.currentTimeMillis() < until) {
                delay(LinkAsk.POLL_MS)
                val st = try {
                    withContext(Dispatchers.IO) { api.askState(w.asked) }
                } catch (e: kotlinx.coroutines.CancellationException) {
                    throw e
                } catch (e: ApiError) {
                    if (e.status == 404) null else continue
                } catch (_: Exception) {
                    continue // a blip on the network: try again
                }
                if (st == null) break
                when {
                    st.accepted -> {
                        val c = st.claimed!!
                        withContext(Dispatchers.IO) {
                            Vault.get(a).link = Link(w.probe.server, c.token, c.deviceId, c.nocapId.ifBlank { w.probe.info.nocapId }, c.serverName.ifBlank { w.probe.info.name }, c.username, w.probe.pin)
                        }
                        waiting = null
                        onLinked()
                        return@LaunchedEffect
                    }
                    st.declined -> {
                        waiting = null
                        problem = t("${w.name} passed on linking this phone.", "${w.name} declined the link request.")
                        return@LaunchedEffect
                    }
                }
            }
            waiting = null
            problem = t("Nobody answered in time. Ask again, or use a code.", "The request expired before anyone answered. Ask again, or use a pairing code.")
        }
    }

    val scanning = lanBusy || bleBusy
    Wallpaper {
        Column(
            Modifier.fillMaxSize().statusBarsPadding().navigationBarsPadding().verticalScroll(rememberScrollState()).padding(20.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Column(Modifier.widthIn(max = 560.dp).fillMaxWidth()) {
                IconButton(onClick = onBack) { Icon(Icons.Filled.ArrowBack, contentDescription = t("Back", "Back"), tint = NoCap.Text) }
                Label("NoCap Link", color = NoCap.Red)
                Text(t("FIND NOCAP SERVERS", "FIND NOCAP SERVERS"), style = MaterialTheme.typography.headlineSmall)
                Spacer(Modifier.height(8.dp))
                Text(
                    t(
                        "Servers on your Wi-Fi show up here, plus ones close by over Bluetooth. Tap one to ask to link, no code needed. Fresh ones can be set up right from your phone.",
                        "NoCap servers on this network appear here, along with servers nearby over Bluetooth. Select one to ask to link without a code. New servers can be set up from this phone.",
                    ),
                    style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted,
                )
                Spacer(Modifier.height(18.dp))

                Column(Modifier.fillMaxWidth(), horizontalAlignment = Alignment.CenterHorizontally) {
                    val status = if (scanning) t("Scanning… (${found.size} found)", "Searching… (${found.size} found)") else t("${found.size} found", "${found.size} found")
                    NoCapRadar(scanning, found.map { RadarBlip(it.nocapId.ifBlank { "ble:" + it.name }, LinkAsk.status(it, linkedId) == LinkAsk.Status.LINKED) }, status)
                    Spacer(Modifier.height(12.dp))
                    Text(status, style = MaterialTheme.typography.titleMedium)
                    if (scanning) {
                        Text(
                            if (lanBusy) t("Checking your Wi-Fi…", "Searching the network…") else t("Listening for Bluetooth…", "Scanning for Bluetooth…"),
                            style = MaterialTheme.typography.bodySmall, color = NoCap.Muted,
                        )
                    }
                }
                Spacer(Modifier.height(16.dp))

                if (found.isEmpty() && !scanning) {
                    GlassCard(Modifier.fillMaxWidth()) {
                        Text(
                            t(
                                "Nothing yet. Make sure your phone's on the same Wi-Fi as the server and it's switched on, then scan again.",
                                "No servers found. Check that this phone is on the same network as the server and that the server is on, then scan again.",
                            ),
                            style = MaterialTheme.typography.bodyMedium,
                        )
                    }
                }
                Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    found.forEachIndexed { i, s ->
                        PopIn(s.nocapId.ifBlank { "ble:" + s.name }, i) { ServerCard(s, LinkAsk.status(s, linkedId)) { open(s) } }
                    }
                }

                Spacer(Modifier.height(16.dp))
                BluetoothCard(a, bt) { scan() }

                Spacer(Modifier.height(18.dp))
                RedButton(t("Scan again", "Search again"), { scan() }, Modifier.fillMaxWidth(), enabled = !scanning)
                Spacer(Modifier.height(8.dp))
                GhostButton(t("Type the address instead", "Enter the address instead"), { a.screen = Screen.LinkForm() }, Modifier.fillMaxWidth())
            }
        }
    }

    picked?.let { s ->
        val st = LinkAsk.status(s, linkedId)
        AlertDialog(
            onDismissRequest = { picked = null },
            containerColor = NoCap.GlassStrong,
            title = { Text(s.name, style = MaterialTheme.typography.titleMedium) },
            text = {
                Column {
                    Text(listOfNotNull(s.origin?.substringAfter("://"), s.version.takeIf { it.isNotBlank() }?.let { "v$it" }).joinToString(" · "), style = MaterialTheme.typography.bodyMedium)
                    if (s.nocapId.isNotBlank()) Text(s.nocapId, style = MaterialTheme.typography.bodySmall, color = NoCap.Muted)
                    Spacer(Modifier.height(10.dp))
                    Text(
                        when {
                            st == LinkAsk.Status.LINKED -> t("This phone's already linked to it. You're good.", "This phone is already linked to this server.")
                            current != null -> t(
                                "Heads up: this phone's linked to ${current.serverName.ifBlank { "another server" }}. Linking here swaps it over.",
                                "This phone is linked to ${current.serverName.ifBlank { "another server" }}. Linking to this server replaces that link.",
                            )
                            else -> t(
                                "Ask to link and someone on NoCap OS says yes, no code to type. Or grab a 6-digit code from Settings → Your Squad.",
                                "Send a link request for an administrator to accept on NoCap OS, or use a 6-digit code from Settings → Linked Devices.",
                            )
                        },
                        style = MaterialTheme.typography.bodyMedium,
                    )
                }
            },
            confirmButton = {
                if (LinkAsk.canAsk(s, linkedId)) {
                    TextButton(onClick = { picked = null; check(s) }, enabled = !busy) { Text(t("Ask to link this phone", "Ask to link this phone"), color = NoCap.Red) }
                } else {
                    TextButton(onClick = { picked = null }) { Text(t("OK", "OK"), color = NoCap.Red) }
                }
            },
            dismissButton = {
                if (st != LinkAsk.Status.LINKED) {
                    TextButton(onClick = { picked = null; a.screen = Screen.LinkForm(server = s.origin.orEmpty(), id = s.nocapId) }) { Text(t("Use a code", "Use a code"), color = NoCap.Text) }
                }
            },
        )
    }

    trust?.let { (s, p) ->
        AlertDialog(
            onDismissRequest = { trust = null },
            containerColor = NoCap.GlassStrong,
            title = { Text(t("Trust ${p.info.name.ifBlank { s.name }}?", "Trust ${p.info.name.ifBlank { s.name }}?"), style = MaterialTheme.typography.titleMedium) },
            text = {
                Column {
                    Text("NoCap ID: ${p.info.nocapId}", style = MaterialTheme.typography.bodyMedium)
                    Spacer(Modifier.height(8.dp))
                    Text(t("Certificate (SHA-256). This phone will only ever trust this one:", "Certificate (SHA-256). This phone will only trust this certificate:"), style = MaterialTheme.typography.bodyMedium)
                    Spacer(Modifier.height(4.dp))
                    Text(Pins.pretty(p.pin.orEmpty()), style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace), color = NoCap.Text)
                }
            },
            confirmButton = { TextButton(onClick = { trust = null; ask(s, p) }) { Text(t("Trust + ask", "Trust and ask"), color = NoCap.Red) } },
            dismissButton = { TextButton(onClick = { trust = null }) { Text(t("Nope", "Cancel"), color = NoCap.Text) } },
        )
    }

    waiting?.let { w ->
        AlertDialog(
            onDismissRequest = {},
            containerColor = NoCap.GlassStrong,
            title = { Text(t("Waiting on ${w.name}", "Waiting for ${w.name}"), style = MaterialTheme.typography.titleMedium) },
            text = {
                Column(horizontalAlignment = Alignment.CenterHorizontally, modifier = Modifier.fillMaxWidth()) {
                    NoCapRadar(true, emptyList(), t("Waiting for an answer", "Waiting for an answer"), size = 120.dp)
                    Spacer(Modifier.height(12.dp))
                    Text(
                        t(
                            "Someone on NoCap OS needs to accept it in Settings → Your Squad. Make sure they see these digits:",
                            "An administrator needs to accept the request in Settings → Linked Devices. Check that NoCap OS shows these digits:",
                        ),
                        style = MaterialTheme.typography.bodyMedium,
                    )
                    Spacer(Modifier.height(10.dp))
                    Text(w.asked.check, color = NoCap.Red, fontFamily = FontFamily.Monospace, fontWeight = FontWeight.Bold, fontSize = 30.sp, letterSpacing = 6.sp)
                }
            },
            confirmButton = {
                TextButton(onClick = {
                    val gone = w
                    waiting = null
                    scope.launch(Dispatchers.IO) { runCatching { NoCapApi(gone.probe.server, gone.probe.pin).askCancel(gone.asked) } }
                }) { Text(t("Cancel", "Cancel"), color = NoCap.Text) }
            },
        )
    }

    problem?.let { msg ->
        AlertDialog(
            onDismissRequest = { problem = null },
            containerColor = NoCap.GlassStrong,
            title = { Text(t("Didn't link", "Not linked"), style = MaterialTheme.typography.titleMedium) },
            text = { Text(msg, style = MaterialTheme.typography.bodyMedium) },
            confirmButton = { TextButton(onClick = { problem = null }) { Text(t("OK", "OK"), color = NoCap.Red) } },
        )
    }

    if (busy) {
        AlertDialog(
            onDismissRequest = {},
            containerColor = NoCap.GlassStrong,
            title = null,
            text = {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    CircularProgressIndicator(Modifier.size(22.dp), color = NoCap.Red, strokeWidth = 2.dp)
                    Text(t("Hitting up the server…", "Contacting the server…"), Modifier.padding(start = 12.dp), style = MaterialTheme.typography.bodyMedium)
                }
            },
            confirmButton = {},
        )
    }

    bluetoothOnly?.let { s ->
        AlertDialog(
            onDismissRequest = { bluetoothOnly = null },
            containerColor = NoCap.GlassStrong,
            title = { Text(t("${s.name} is close, but not on your Wi-Fi", "${s.name} is nearby but not on this network"), style = MaterialTheme.typography.titleMedium) },
            text = {
                Text(
                    t(
                        "Bluetooth only tells us it's here. Setting it up and linking happen over your network, so plug it into your router (or put this phone on the same network as it), then scan again.",
                        "Bluetooth is only used to find servers. Setup and linking use the network, so connect the server to your router (or this phone to the server's network) and scan again.",
                    ),
                    style = MaterialTheme.typography.bodyMedium,
                )
            },
            confirmButton = { TextButton(onClick = { bluetoothOnly = null; scan() }) { Text(t("Scan again", "Scan again"), color = NoCap.Red) } },
            dismissButton = { TextButton(onClick = { bluetoothOnly = null }) { Text(t("OK", "OK"), color = NoCap.Text) } },
        )
    }
}

/** A found server's card pops in (fade, rise, a little bounce) the first time it shows. */
@Composable
private fun PopIn(key: String, index: Int, content: @Composable () -> Unit) {
    val still = reducedMotion()
    val p = remember(key) { Animatable(if (still) 1f else 0f) }
    LaunchedEffect(key) {
        if (!still) {
            delay(index * 70L)
            p.animateTo(1f, spring(dampingRatio = Spring.DampingRatioMediumBouncy, stiffness = Spring.StiffnessMediumLow))
        }
    }
    androidx.compose.foundation.layout.Box(
        Modifier.graphicsLayer {
            val v = p.value
            alpha = v.coerceIn(0f, 1f)
            translationY = (1f - v) * 24f
            scaleX = 0.94f + 0.06f * v
            scaleY = 0.94f + 0.06f * v
        },
    ) { content() }
}

@Composable
private fun ServerCard(s: FoundServer, st: LinkAsk.Status, onClick: () -> Unit) {
    GlassCard(Modifier.fillMaxWidth().clip(RoundedCornerShape(18.dp)).clickable(onClick = onClick)) {
        Column {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(s.name, Modifier.weight(1f), style = MaterialTheme.typography.titleMedium)
                when (st) {
                    LinkAsk.Status.LINKED -> Chip(t("Linked", "Linked"), NoCap.Ok)
                    LinkAsk.Status.NEW -> Chip(t("Fresh: set it up", "New: set up"), NoCap.Red)
                    LinkAsk.Status.READY -> Chip(t("Ask to link", "Ask to link"), NoCap.Red)
                    LinkAsk.Status.BLUETOOTH_ONLY -> {}
                }
            }
            val where = s.origin?.substringAfter("://") ?: t("Nearby over Bluetooth", "Nearby (Bluetooth)")
            Text(
                listOfNotNull(where, s.version.takeIf { it.isNotBlank() }?.let { "v$it" }).joinToString(" · "),
                style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted,
            )
            val via = listOfNotNull(
                if (s.lan) t("On your Wi-Fi", "On this network") else null,
                if (s.bluetooth) t("Bluetooth", "Bluetooth") else null,
            ).joinToString(" + ")
            Text(via, style = MaterialTheme.typography.bodySmall, color = NoCap.Muted)
            if (s.nocapId.isNotBlank()) Text(s.nocapId, style = MaterialTheme.typography.bodySmall, color = NoCap.Muted)
        }
    }
}

/** What the Bluetooth half needs, with one button to fix it. */
@Composable
private fun BluetoothCard(a: MainActivity, bt: Finder.Bt, rescan: () -> Unit) {
    if (bt == Finder.Bt.READY) return
    GlassCard(Modifier.fillMaxWidth()) {
        Column {
            Label(t("Bluetooth", "Bluetooth"))
            Spacer(Modifier.height(4.dp))
            when (bt) {
                Finder.Bt.NO_HARDWARE -> Text(t("This phone can't scan for Bluetooth servers. Wi-Fi works fine.", "This phone doesn't support Bluetooth LE scanning. Network search still works."), style = MaterialTheme.typography.bodyMedium)
                Finder.Bt.NEED_PERMISSION -> {
                    Text(
                        if (android.os.Build.VERSION.SDK_INT >= android.os.Build.VERSION_CODES.S) {
                            t("To spot servers close by, allow \"Nearby devices\". NoCap OS only listens for NoCap servers, never your location.", "To find servers nearby, allow \"Nearby devices\". NoCap OS only looks for NoCap servers and never uses your location.")
                        } else {
                            t("Android 11 and older only let apps scan Bluetooth with location allowed. NoCap OS only listens for NoCap servers and never reads where you are.", "Android 11 and earlier require location permission for Bluetooth scanning. NoCap OS only looks for NoCap servers and never reads your location.")
                        },
                        style = MaterialTheme.typography.bodyMedium,
                    )
                    Spacer(Modifier.height(10.dp))
                    RedButton(t("Allow Bluetooth scan", "Allow Bluetooth scanning"), { Finder.askBle(a) { ok -> if (ok) rescan() } }, Modifier.fillMaxWidth())
                }
                Finder.Bt.OFF -> {
                    Text(t("Bluetooth's off. Turn it on to spot servers close by.", "Bluetooth is off. Turn it on to find servers nearby."), style = MaterialTheme.typography.bodyMedium)
                    Spacer(Modifier.height(10.dp))
                    GhostButton(t("Open Bluetooth settings", "Open Bluetooth settings"), { Permissions.open(a, Intent(Settings.ACTION_BLUETOOTH_SETTINGS)) }, Modifier.fillMaxWidth())
                }
                Finder.Bt.LOCATION_OFF -> {
                    Text(t("On this Android version, Bluetooth scans need location switched on (we still never read it).", "On this Android version, Bluetooth scanning requires location services to be on. NoCap OS doesn't read your location."), style = MaterialTheme.typography.bodyMedium)
                    Spacer(Modifier.height(10.dp))
                    GhostButton(t("Open location settings", "Open location settings"), { Permissions.open(a, Intent(Settings.ACTION_LOCATION_SOURCE_SETTINGS)) }, Modifier.fillMaxWidth())
                }
                Finder.Bt.READY -> {}
            }
        }
    }
}
