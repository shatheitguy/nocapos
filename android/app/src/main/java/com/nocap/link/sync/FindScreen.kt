package com.nocap.link.sync

import android.content.Intent
import android.provider.Settings
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
import androidx.compose.material3.AlertDialog
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
import androidx.compose.ui.unit.dp
import com.nocap.link.MainActivity
import com.nocap.link.Screen
import com.nocap.link.data.t
import com.nocap.link.settings.Permissions
import com.nocap.link.ui.Chip
import com.nocap.link.ui.GhostButton
import com.nocap.link.ui.GlassCard
import com.nocap.link.ui.Label
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.RedButton
import com.nocap.link.ui.Wallpaper
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * Find NoCap servers: on this Wi-Fi (mDNS) and nearby over Bluetooth. A
 * server that's set up goes to the normal link form with its address
 * filled in (the 6-digit code is still needed); a brand-new one can be set
 * up from here.
 */
@Composable
fun FindScreen(a: MainActivity, onBack: () -> Unit) {
    val scope = rememberCoroutineScope()
    var found by remember { mutableStateOf<List<FoundServer>>(emptyList()) }
    var lanBusy by remember { mutableStateOf(false) }
    var bleBusy by remember { mutableStateOf(false) }
    var rounds by remember { mutableIntStateOf(0) }
    var bluetoothOnly by remember { mutableStateOf<FoundServer?>(null) }
    val bt = remember(a.resumeTick, rounds) { Finder.bt(a) }

    fun scan() {
        if (lanBusy || bleBusy) return
        lanBusy = true
        val withBle = Finder.bt(a) == Finder.Bt.READY
        bleBusy = withBle
        scope.launch {
            val ble = async(Dispatchers.IO) { if (withBle) runCatching { Finder.bluetooth(a) }.getOrDefault(emptyList()) else emptyList() }
            val lan = withContext(Dispatchers.IO) { runCatching { Finder.lan(a) }.getOrDefault(emptyList()) }
            found = Finder.merge(lan, emptyList())
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
            else -> a.screen = Screen.LinkForm(server = origin, id = s.nocapId)
        }
    }

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
                        "Servers on your Wi-Fi show up here, plus ones close by over Bluetooth. Fresh ones can be set up right from your phone.",
                        "NoCap servers on this network appear here, along with servers nearby over Bluetooth. New servers can be set up from this phone.",
                    ),
                    style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted,
                )
                Spacer(Modifier.height(16.dp))

                if (found.isEmpty() && !lanBusy && !bleBusy) {
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
                    found.forEach { s -> ServerCard(s) { open(s) } }
                }
                if (lanBusy || bleBusy) {
                    Spacer(Modifier.height(14.dp))
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        CircularProgressIndicator(Modifier.size(20.dp), color = NoCap.Red, strokeWidth = 2.dp)
                        Text(
                            if (lanBusy) t("Checking your Wi-Fi…", "Searching the network…") else t("Listening for Bluetooth…", "Scanning for Bluetooth…"),
                            Modifier.padding(start = 12.dp), style = MaterialTheme.typography.bodyMedium,
                        )
                    }
                }

                Spacer(Modifier.height(16.dp))
                BluetoothCard(a, bt) { scan() }

                Spacer(Modifier.height(18.dp))
                RedButton(t("Scan again", "Scan again"), { scan() }, Modifier.fillMaxWidth(), enabled = !lanBusy && !bleBusy)
                Spacer(Modifier.height(8.dp))
                GhostButton(t("Type the address instead", "Enter the address instead"), { a.screen = Screen.LinkForm() }, Modifier.fillMaxWidth())
            }
        }
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

@Composable
private fun ServerCard(s: FoundServer, onClick: () -> Unit) {
    GlassCard(Modifier.fillMaxWidth().clip(RoundedCornerShape(18.dp)).clickable(onClick = onClick)) {
        Column {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(s.name, Modifier.weight(1f), style = MaterialTheme.typography.titleMedium)
                when {
                    s.isNew -> Chip(t("Fresh: set it up", "New: set up"), NoCap.Red)
                    s.origin != null -> Chip(t("Ready to link", "Ready to link"), NoCap.Ok)
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
