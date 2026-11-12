package com.nocap.link.sync

import android.os.Build
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.filled.ArrowBack
import androidx.compose.material.icons.Icons
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
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
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import com.nocap.link.data.Link
import com.nocap.link.data.Vault
import com.nocap.link.MainActivity
import com.nocap.link.net.ApiError
import com.nocap.link.net.NoCapApi
import com.nocap.link.net.Pins
import com.nocap.link.net.Probe
import com.nocap.link.R
import com.nocap.link.Screen
import com.nocap.link.ui.GlassCard
import com.nocap.link.ui.Label
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.RedButton
import com.nocap.link.ui.Wallpaper
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * Link with NoCap Link: server address + 6-digit code (typed, scanned or
 * from a nocap://pair link). The server's certificate is shown and
 * pinned before the code is sent.
 */
@Composable
fun LinkScreen(start: Screen.LinkForm, onScan: () -> Unit, onCancel: () -> Unit, onLinked: () -> Unit) {
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    var server by remember(start) { mutableStateOf(start.server) }
    var code by remember(start) { mutableStateOf(start.code) }
    var name by remember { mutableStateOf(deviceName()) }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    var probe by remember { mutableStateOf<Probe?>(null) }

    fun claim(p: Probe) {
        busy = true
        error = null
        scope.launch {
            try {
                val typedCode = code
                val phoneName = name.trim()
                withContext(Dispatchers.IO) {
                    val c = NoCapApi(p.server, p.pin).claim(typedCode, phoneName)
                    // The vault is encrypted storage backed by the keystore: keep it off the main thread.
                    Vault.get(ctx).link = Link(p.server, c.token, c.deviceId, c.nocapId.ifBlank { p.info.nocapId }, c.serverName.ifBlank { p.info.name }, c.username, p.pin)
                }
                onLinked()
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: ApiError) {
                error = e.message?.ifBlank { null } ?: MainActivity.friendly(e)
            } catch (e: Exception) {
                error = MainActivity.friendly(e)
            } finally {
                busy = false
            }
        }
    }

    fun go() {
        if (busy) return
        error = when {
            server.isBlank() -> "Type your NoCapOS address first."
            code.length != 6 -> "The code is 6 digits — it's in Settings → Devices → Link a device."
            name.isBlank() -> "Give this phone a name."
            else -> null
        }
        if (error != null) return
        busy = true
        scope.launch {
            try {
                val typed = server
                val p = withContext(Dispatchers.IO) { NoCapApi.probe(typed) }
                if (start.id.isNotBlank() && start.server == typed && !p.info.nocapId.equals(start.id, ignoreCase = true)) {
                    error = "That server's NoCap ID is ${p.info.nocapId}, not ${start.id} from the QR. Not linking — that's sus."
                } else {
                    probe = p // confirm the certificate first
                }
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                error = MainActivity.friendly(e)
            } finally {
                busy = false
            }
        }
    }

    LaunchedEffect(start) { if (start.auto) go() }

    Wallpaper {
        Column(
            Modifier.fillMaxSize().statusBarsPadding().navigationBarsPadding().imePadding().verticalScroll(rememberScrollState()).padding(20.dp),
        ) {
            IconButton(onClick = onCancel) { Icon(Icons.Filled.ArrowBack, contentDescription = "Back", tint = NoCap.Text) }
            Spacer(Modifier.height(8.dp))
            Row(verticalAlignment = Alignment.CenterVertically) {
                Image(painterResource(R.drawable.nocap_mark), null, Modifier.size(48.dp))
                Column(Modifier.padding(start = 14.dp)) {
                    Label("NoCap Link", color = NoCap.Red)
                    Text("LINK THIS PHONE", style = MaterialTheme.typography.headlineSmall)
                }
            }
            Spacer(Modifier.height(10.dp))
            Text(
                "On your NoCap desktop, open Settings → Devices → Link a device. Scan the QR, or type the address and the 6-digit code.",
                style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted,
            )
            Spacer(Modifier.height(18.dp))
            RedButton("Scan the QR code", onScan, Modifier.fillMaxWidth(), enabled = !busy)
            Spacer(Modifier.height(18.dp))
            GlassCard(Modifier.fillMaxWidth()) {
                Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    Field("Server address", server, { server = it.trim() }, "192.168.1.20:8443", KeyboardType.Uri)
                    Field("6-digit code", code, { v -> code = v.filter(Char::isDigit).take(6) }, "123456", KeyboardType.NumberPassword)
                    Field("This phone's name", name, { name = it.take(64) }, "My phone", KeyboardType.Text)
                }
            }
            error?.let {
                Spacer(Modifier.height(12.dp))
                Text(it, style = MaterialTheme.typography.bodyMedium, color = NoCap.Bad)
            }
            Spacer(Modifier.height(18.dp))
            if (busy) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    CircularProgressIndicator(Modifier.size(22.dp), color = NoCap.Red, strokeWidth = 2.dp)
                    Text("Linking…", Modifier.padding(start = 12.dp), style = MaterialTheme.typography.bodyMedium)
                }
            } else {
                RedButton("Link it", { go() }, Modifier.fillMaxWidth())
            }
        }
    }

    probe?.let { p ->
        AlertDialog(
            onDismissRequest = { probe = null },
            containerColor = NoCap.GlassStrong,
            title = { Text("Trust ${p.info.name.ifBlank { "this server" }}?", style = MaterialTheme.typography.titleMedium) },
            text = {
                Column {
                    Text("NoCap ID: ${p.info.nocapId}", style = MaterialTheme.typography.bodyMedium)
                    Text("Check it matches Settings → Devices on your server.", style = MaterialTheme.typography.bodySmall)
                    Spacer(Modifier.height(10.dp))
                    if (p.pin != null) {
                        Text("Certificate (SHA-256) — this phone will only ever trust this one:", style = MaterialTheme.typography.bodyMedium)
                        Spacer(Modifier.height(4.dp))
                        Text(Pins.pretty(p.pin), style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace), color = NoCap.Text)
                    } else {
                        Text("Heads up: this address is plain http, so the connection isn't encrypted. Only do this on your own Wi-Fi.", style = MaterialTheme.typography.bodyMedium, color = NoCap.Warn)
                    }
                }
            },
            confirmButton = { TextButton(onClick = { probe = null; claim(p) }) { Text("Trust + link", color = NoCap.Red) } },
            dismissButton = { TextButton(onClick = { probe = null }) { Text("Nope", color = NoCap.Text) } },
        )
    }
}

@Composable
private fun Field(label: String, value: String, onChange: (String) -> Unit, hint: String, type: KeyboardType) {
    OutlinedTextField(
        value = value, onValueChange = onChange, singleLine = true, modifier = Modifier.fillMaxWidth(),
        label = { Text(label) }, placeholder = { Text(hint, color = NoCap.Muted) },
        keyboardOptions = KeyboardOptions(keyboardType = type, autoCorrectEnabled = false),
        textStyle = MaterialTheme.typography.bodyLarge,
        shape = RoundedCornerShape(12.dp),
        colors = OutlinedTextFieldDefaults.colors(
            focusedBorderColor = NoCap.Red, unfocusedBorderColor = NoCap.GlassEdge,
            focusedLabelColor = NoCap.Red, unfocusedLabelColor = NoCap.Muted, cursorColor = NoCap.Red,
        ),
    )
}

private fun deviceName(): String {
    val m = Build.MODEL.orEmpty().trim()
    val brand = Build.MANUFACTURER.orEmpty().replaceFirstChar { it.uppercase() }
    return (if (m.startsWith(brand, ignoreCase = true)) m else "$brand $m").trim().ifBlank { "My phone" }.take(64)
}
