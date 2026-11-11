package com.nocap.link.share

import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.provider.OpenableColumns
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
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
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.lifecycleScope
import com.nocap.link.data.Prefs
import com.nocap.link.data.t
import com.nocap.link.data.Vault
import com.nocap.link.MainActivity
import com.nocap.link.net.NoCapApi
import com.nocap.link.net.ServerTooOld
import com.nocap.link.R
import com.nocap.link.ui.GhostButton
import com.nocap.link.ui.glass
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.NoCapTheme
import com.nocap.link.ui.RedButton
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * "Send to NoCap" in Android's share sheet: files, photos, links or text
 * go into the NoCap Link folder in your home on your server.
 */
class ShareActivity : ComponentActivity() {
    private sealed interface State {
        data object Ready : State
        data object Sending : State
        data class Done(val msg: String) : State
        data class Failed(val msg: String) : State
    }

    private var state by mutableStateOf<State>(State.Ready)
    private var uris: List<Uri> = emptyList()
    private var text: String? = null
    private var subject: String? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        Prefs.init(this)
        read(intent)
        setContent { NoCapTheme { Sheet() } }
    }

    @Suppress("DEPRECATION")
    private fun read(i: Intent) {
        uris = when (i.action) {
            Intent.ACTION_SEND -> listOfNotNull(
                if (Build.VERSION.SDK_INT >= 33) i.getParcelableExtra(Intent.EXTRA_STREAM, Uri::class.java) else i.getParcelableExtra(Intent.EXTRA_STREAM),
            )
            Intent.ACTION_SEND_MULTIPLE -> (if (Build.VERSION.SDK_INT >= 33) i.getParcelableArrayListExtra(Intent.EXTRA_STREAM, Uri::class.java) else i.getParcelableArrayListExtra(Intent.EXTRA_STREAM)).orEmpty()
            else -> emptyList()
        }
        text = i.getCharSequenceExtra(Intent.EXTRA_TEXT)?.toString()?.takeIf { it.isNotBlank() }
        subject = i.getStringExtra(Intent.EXTRA_SUBJECT)
    }

    private fun name(u: Uri): String {
        contentResolver.query(u, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { c -> if (c.moveToFirst()) c.getString(0)?.let { return it } }
        return u.lastPathSegment ?: "file"
    }

    private fun size(u: Uri): Long {
        contentResolver.query(u, arrayOf(OpenableColumns.SIZE), null, null, null)?.use { c -> if (c.moveToFirst() && !c.isNull(0)) return c.getLong(0) }
        return -1
    }

    private fun send() {
        val link = Vault.get(this).link ?: return
        val api = NoCapApi(link)
        state = State.Sending
        lifecycleScope.launch {
            state = try {
                val saved = withContext(Dispatchers.IO) {
                    if (uris.isNotEmpty()) {
                        api.send(uris.map { u -> NoCapApi.Upload(name(u), contentResolver.getType(u), size(u)) { contentResolver.openInputStream(u) ?: error("can't read it") } })
                    } else {
                        api.sendText(text.orEmpty(), subject)
                    }
                }
                State.Done(t("Sent ${saved.size} to NoCap Link in your home. W.", "Saved ${saved.size} item(s) to NoCap Link in your home folder."))
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: ServerTooOld) {
                State.Failed(MainActivity.needsUpdate(runCatching { withContext(Dispatchers.IO) { api.pairInfo().version } }.getOrNull()))
            } catch (e: Exception) {
                State.Failed(MainActivity.friendly(e))
            }
        }
    }

    @Composable
    private fun Sheet() {
        val linked = remember { Vault.get(this).link != null }
        Box(
            Modifier.fillMaxSize().background(Color(0x99000000)).clickable(remember { MutableInteractionSource() }, null) { if (state !is State.Sending) finish() },
            contentAlignment = Alignment.BottomCenter,
        ) {
            Column(
                Modifier.fillMaxWidth().navigationBarsPadding().padding(12.dp).glass(RoundedCornerShape(24.dp), strong = true)
                    .clickable(remember { MutableInteractionSource() }, null) {}.padding(20.dp),
            ) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Image(painterResource(R.drawable.nocap_mark), null, Modifier.size(32.dp))
                    Text(t("SEND TO NOCAP", "SEND TO NOCAP"), Modifier.padding(start = 10.dp), style = MaterialTheme.typography.headlineSmall)
                }
                Spacer(Modifier.height(12.dp))
                if (!linked) {
                    Text(t("Link this phone to your NoCap server first (open NoCap OS → NoCap Link).", "Link this phone to a NoCap server first (NoCap OS → NoCap Link)."), style = MaterialTheme.typography.bodyMedium)
                    Spacer(Modifier.height(14.dp))
                    GhostButton(t("Close", "Close"), { finish() }, Modifier.fillMaxWidth())
                    return@Column
                }
                val what = when {
                    uris.size == 1 -> name(uris[0])
                    uris.size > 1 -> t("${uris.size} files", "${uris.size} files")
                    text != null -> text!!.take(140)
                    else -> null
                }
                if (what == null) {
                    Text(t("Nothing to send here.", "There's nothing to send."), style = MaterialTheme.typography.bodyMedium)
                    Spacer(Modifier.height(14.dp))
                    GhostButton(t("Close", "Close"), { finish() }, Modifier.fillMaxWidth())
                    return@Column
                }
                Text(what, style = MaterialTheme.typography.titleSmall, maxLines = 3, overflow = TextOverflow.Ellipsis)
                Text(t("Lands in NoCap Link, in your home on ${Vault.get(this@ShareActivity).link?.serverName ?: "your server"}.", "Saved to the NoCap Link folder in your home folder."), style = MaterialTheme.typography.bodySmall)
                Spacer(Modifier.height(16.dp))
                when (val s = state) {
                    State.Ready -> Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                        GhostButton(t("Nah", "Cancel"), { finish() }, Modifier.weight(1f))
                        RedButton(t("Send it", "Send"), { send() }, Modifier.weight(1f))
                    }
                    State.Sending -> Row(verticalAlignment = Alignment.CenterVertically) {
                        CircularProgressIndicator(Modifier.size(22.dp), color = NoCap.Red, strokeWidth = 2.dp)
                        Text(t("Sending…", "Sending…"), Modifier.padding(start = 12.dp), style = MaterialTheme.typography.titleSmall)
                    }
                    is State.Done -> {
                        Text(s.msg, style = MaterialTheme.typography.titleSmall, color = NoCap.Ok)
                        Spacer(Modifier.height(12.dp))
                        RedButton(t("Done", "Done"), { finish() }, Modifier.fillMaxWidth())
                    }
                    is State.Failed -> {
                        Text(s.msg, style = MaterialTheme.typography.bodyMedium, color = NoCap.Warn)
                        Spacer(Modifier.height(12.dp))
                        Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                            GhostButton(t("Close", "Close"), { finish() }, Modifier.weight(1f))
                            RedButton(t("Run it back", "Try again"), { send() }, Modifier.weight(1f))
                        }
                    }
                }
            }
        }
    }
}
