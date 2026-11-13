package com.nocap.link.settings

import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.lifecycleScope
import com.nocap.link.MainActivity
import com.nocap.link.data.t
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * Pages that open over whatever is showing (setup or Settings): the privacy
 * page and the bug report. Kept here so MainActivity only draws one layer.
 */
object Extras {
    var privacy by mutableStateOf(false)
    var bug by mutableStateOf(false)
}

@Composable
fun ExtrasLayer(a: MainActivity) {
    if (Extras.privacy) PrivacyScreen(a) { Extras.privacy = false }
    if (Extras.bug) BugReport(a) { Extras.bug = false }
}

/** Sends a bug report (already scrubbed) to your own server, like a crash report. */
fun MainActivity.sendBugReport(body: String) {
    val api = api() ?: run { toast = t("Link your NoCap server first.", "Link a NoCap server first."); return }
    lifecycleScope.launch {
        toast = try {
            withContext(Dispatchers.IO) { api.sendText(body, "NoCap OS bug report") }
            t("Bug report sent to your server's NoCap Link folder.", "Bug report saved to NoCap Link on your server.")
        } catch (e: kotlinx.coroutines.CancellationException) {
            throw e
        } catch (e: Exception) {
            say(e)
        }
    }
}
