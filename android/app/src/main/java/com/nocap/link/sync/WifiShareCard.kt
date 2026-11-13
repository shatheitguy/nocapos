package com.nocap.link.sync

import android.content.ClipData
import android.content.ClipDescription
import android.content.ClipboardManager
import android.graphics.Bitmap
import android.os.Build
import android.os.PersistableBundle
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.FilterQuality
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.google.zxing.BarcodeFormat
import com.google.zxing.EncodeHintType
import com.google.zxing.qrcode.QRCodeWriter
import com.nocap.link.MainActivity
import com.nocap.link.data.t
import com.nocap.link.net.ApiError
import com.nocap.link.net.SavedWifi
import com.nocap.link.net.WifiQr
import com.nocap.link.net.WifiShare
import com.nocap.link.ui.Label
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.glass
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * "Share the server's Wi-Fi": an administrator picks a network saved on the
 * server, confirms with their account password, and gets the same join QR
 * code as Settings → Network → Wi-Fi on the web, drawn on the phone.
 */
@Composable
fun WifiShareCard(a: MainActivity) {
    var open by remember { mutableStateOf(false) }
    var list by remember { mutableStateOf<List<SavedWifi>?>(null) }
    var note by remember { mutableStateOf<String?>(null) }
    var pick by remember { mutableStateOf<SavedWifi?>(null) }
    var shown by remember { mutableStateOf<WifiShare?>(null) }
    val scope = rememberCoroutineScope()

    fun load() {
        val api = a.api() ?: return
        note = null
        scope.launch {
            try {
                list = withContext(Dispatchers.IO) { api.wifiSaved() }
            } catch (e: CancellationException) {
                throw e
            } catch (e: ApiError) {
                note = if (e.status == 403) t("Only admins can share the server's Wi-Fi.", "Only administrators can share the server's Wi-Fi.") else e.message
            } catch (e: Exception) {
                note = a.say(e)
            }
        }
    }

    Column(Modifier.fillMaxWidth().glass(RoundedCornerShape(18.dp)).padding(14.dp)) {
        Row(
            Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).clickable { open = !open; if (open && list == null) load() },
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Column(Modifier.weight(1f)) {
                Label(t("Share Wi-Fi", "Share Wi-Fi"))
                Text(
                    t("Show a QR code so a phone can hop on your server's Wi-Fi.", "Show a QR code so another phone can join the server's Wi-Fi."),
                    Modifier.padding(top = 4.dp), style = MaterialTheme.typography.bodySmall,
                )
            }
            Text(if (open) "−" else "+", Modifier.padding(start = 10.dp), style = MaterialTheme.typography.titleLarge, color = NoCap.Red)
        }
        if (open) {
            Spacer(Modifier.height(10.dp))
            when {
                note != null -> Text(note!!, style = MaterialTheme.typography.bodyMedium, color = NoCap.Warn)
                list == null -> Text(t("Loading…", "Loading…"), style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted)
                list!!.isEmpty() -> Text(
                    t("No shareable Wi-Fi saved on the server. Enterprise networks can't go in a QR code.", "No shareable Wi-Fi networks are saved on the server. Enterprise networks can't be shared with a QR code."),
                    style = MaterialTheme.typography.bodyMedium,
                )
                else -> Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    list!!.forEach { n ->
                        Row(
                            Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).clickable { pick = n }.padding(vertical = 10.dp, horizontal = 4.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            Text(n.ssid, Modifier.weight(1f), style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            if (n.active) Text(t("Connected", "Connected"), style = MaterialTheme.typography.labelMedium, color = NoCap.Ok)
                        }
                    }
                }
            }
        }
    }

    pick?.let { n -> ReauthDialog(a, n, onDone = { pick = null }, onShown = { pick = null; shown = it }) }
    shown?.let { s -> ShareDialog(a, s) { shown = null } }
}

@Composable
private fun ReauthDialog(a: MainActivity, n: SavedWifi, onDone: () -> Unit, onShown: (WifiShare) -> Unit) {
    var pw by remember { mutableStateOf("") }
    var err by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    AlertDialog(
        onDismissRequest = onDone,
        containerColor = NoCap.GlassStrong,
        title = { Text(t("Share ${n.ssid}", "Share ${n.ssid}"), style = MaterialTheme.typography.titleMedium) },
        text = {
            Column {
                Text(
                    t("Type your NoCapOS account password to see this network's password and QR code.", "Enter your NoCapOS account password to show this network's password and QR code."),
                    style = MaterialTheme.typography.bodyMedium,
                )
                Spacer(Modifier.height(10.dp))
                OutlinedTextField(
                    pw, { pw = it }, Modifier.fillMaxWidth(), singleLine = true,
                    placeholder = { Text(t("Your account password", "Your account password")) },
                    visualTransformation = PasswordVisualTransformation(),
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
                    colors = OutlinedTextFieldDefaults.colors(focusedBorderColor = NoCap.Red, cursorColor = NoCap.Red),
                )
                err?.let { Text(it, Modifier.padding(top = 8.dp), style = MaterialTheme.typography.bodySmall, color = NoCap.Bad) }
            }
        },
        confirmButton = {
            TextButton(enabled = pw.isNotEmpty() && !busy, onClick = {
                val api = a.api() ?: return@TextButton
                busy = true
                scope.launch {
                    try {
                        val s = withContext(Dispatchers.IO) { api.wifiShare(n.uuid, pw) }
                        onShown(s)
                    } catch (e: CancellationException) {
                        throw e
                    } catch (e: ApiError) {
                        err = if (e.code == "reauth_failed") t("That's not your account password.", "That's not your account password.") else e.message
                    } catch (e: Exception) {
                        err = a.say(e)
                    } finally {
                        busy = false
                        pw = ""
                    }
                }
            }) { Text(t("Show it", "Show password"), color = NoCap.Red) }
        },
        dismissButton = { TextButton(onClick = onDone) { Text(t("Nah", "Cancel"), color = NoCap.Text) } },
    )
}

@Composable
private fun ShareDialog(a: MainActivity, s: WifiShare, onDone: () -> Unit) {
    val qr = remember(s) { runCatching { qrBitmap(WifiQr.payload(s.ssid, s.password, s.type, s.hidden), 720) }.getOrNull() }
    var reveal by remember { mutableStateOf(false) }
    AlertDialog(
        onDismissRequest = onDone,
        containerColor = NoCap.GlassStrong,
        title = { Text(s.ssid, style = MaterialTheme.typography.titleMedium, maxLines = 2, overflow = TextOverflow.Ellipsis) },
        text = {
            Column(horizontalAlignment = Alignment.CenterHorizontally) {
                Text(t("Point another phone's camera at this to join.", "Scan with another phone's camera to join."), style = MaterialTheme.typography.bodyMedium)
                Spacer(Modifier.height(12.dp))
                qr?.let {
                    Image(
                        it.asImageBitmap(), t("QR code to join ${s.ssid}", "QR code to join ${s.ssid}"),
                        Modifier.size(240.dp).clip(RoundedCornerShape(12.dp)).background(Color.White).padding(8.dp),
                        filterQuality = FilterQuality.None,
                    )
                }
                Spacer(Modifier.height(12.dp))
                if (s.type == "nopass") {
                    Text(t("Open network: no password needed.", "Open network: no password needed."), style = MaterialTheme.typography.bodySmall)
                } else {
                    Text(
                        if (reveal) s.password else "•".repeat(minOf(16, s.password.length)),
                        Modifier.clip(RoundedCornerShape(8.dp)).clickable { reveal = !reveal }.padding(6.dp),
                        style = MaterialTheme.typography.bodyLarge, fontFamily = FontFamily.Monospace,
                    )
                    Text(t("Tap to show or hide", "Tap to show or hide"), style = MaterialTheme.typography.labelSmall, color = NoCap.Muted)
                }
            }
        },
        confirmButton = { TextButton(onClick = onDone) { Text(t("Done", "Done"), color = NoCap.Red) } },
        dismissButton = {
            if (s.type != "nopass") {
                TextButton(onClick = {
                    copySensitive(a, s.password)
                    a.toast = t("Password copied", "Password copied")
                }) { Text(t("Copy password", "Copy password"), color = NoCap.Text) }
            }
        },
    )
}

/** Copies a password, flagged sensitive so Android 13+ hides it in the clipboard preview. */
private fun copySensitive(a: MainActivity, text: String) {
    val cm = a.getSystemService(ClipboardManager::class.java) ?: return
    val clip = ClipData.newPlainText("Wi-Fi password", text)
    if (Build.VERSION.SDK_INT >= 33) {
        clip.description.extras = PersistableBundle().apply { putBoolean(ClipDescription.EXTRA_IS_SENSITIVE, true) }
    }
    runCatching { cm.setPrimaryClip(clip) }
}

private fun qrBitmap(text: String, size: Int): Bitmap {
    val m = QRCodeWriter().encode(text, BarcodeFormat.QR_CODE, size, size, mapOf(EncodeHintType.MARGIN to 0, EncodeHintType.CHARACTER_SET to "UTF-8"))
    val px = IntArray(m.width * m.height)
    for (y in 0 until m.height) for (x in 0 until m.width) px[y * m.width + x] = if (m[x, y]) android.graphics.Color.BLACK else android.graphics.Color.WHITE
    return Bitmap.createBitmap(px, m.width, m.height, Bitmap.Config.ARGB_8888)
}
