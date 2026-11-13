package com.nocap.link.settings

import android.content.Intent
import android.net.Uri
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
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
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.nocap.link.data.t
import com.nocap.link.MainActivity
import com.nocap.link.system.CrashLog
import com.nocap.link.ui.NoCap

/** Opens a prefilled GitHub issue in the browser. Only ever from a tap. */
private fun reportToDevelopers(a: MainActivity, title: String, body: String) {
    val url = CrashLog.issueUrl(title, body)
    runCatching { a.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
        .onFailure { a.toast = t("No browser to open GitHub.", "No browser available to open GitHub.") }
}

/** Exactly what will be sent, scrollable. */
@Composable
private fun Preview(text: String) {
    Text(t("What gets sent (exactly this):", "Exactly what will be sent:"), style = MaterialTheme.typography.labelLarge, color = NoCap.Muted)
    Text(
        text,
        Modifier.fillMaxWidth().padding(top = 6.dp).heightIn(max = 260.dp).background(NoCap.Field, RoundedCornerShape(10.dp))
            .verticalScroll(rememberScrollState()).padding(10.dp),
        style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace, fontSize = 10.sp),
    )
}

/**
 * On the start after a crash, ANR or repeated failure: show the report, and
 * send it only if you pick where. Never sent automatically.
 */
@Composable
fun CrashPrompt(a: MainActivity, report: String, onClose: () -> Unit) {
    val clean = remember(report) { CrashLog.sanitize(report) }
    val linked = a.link != null
    val done = { CrashLog.settle(a); onClose() }
    AlertDialog(
        onDismissRequest = done,
        containerColor = NoCap.GlassStrong,
        title = { Text(t("NoCap OS crashed last time. Send a report?", "NoCap OS stopped unexpectedly. Send a report?"), style = MaterialTheme.typography.titleMedium) },
        text = {
            Column {
                Text(
                    t(
                        "It has the error, app and Android version and phone model. No personal stuff: addresses, emails and file paths are blanked out.",
                        "It contains the error, app and Android versions and the device model. Addresses, emails and file paths are removed.",
                    ),
                    Modifier.padding(bottom = 10.dp), style = MaterialTheme.typography.bodySmall,
                )
                Preview(clean)
            }
        },
        confirmButton = {
            Column {
                if (linked) TextButton(onClick = { a.sendCrashReport(clean); onClose() }) { Text(t("Send to my server", "Send to my server"), color = NoCap.Red) }
                TextButton(onClick = { reportToDevelopers(a, CrashLog.headline(clean), clean); done() }) { Text(t("Report to NoCap developers", "Report to NoCap developers"), color = NoCap.Red) }
                TextButton(onClick = done) { Text(t("Don't send", "Don't send"), color = NoCap.Text) }
            }
        },
    )
}

/** Settings → About → Report a bug: what happened, plus the same send choices. */
@Composable
fun BugReport(a: MainActivity, onClose: () -> Unit) {
    var what by remember { mutableStateOf("") }
    val last = remember { CrashLog.last(a) }
    val body = CrashLog.sanitize(CrashLog.bugReport(what, last))
    AlertDialog(
        onDismissRequest = onClose,
        containerColor = NoCap.GlassStrong,
        title = { Text(t("Report a bug", "Report a bug"), style = MaterialTheme.typography.titleMedium) },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState())) {
                OutlinedTextField(
                    what, { what = it.take(2000) }, Modifier.fillMaxWidth().heightIn(min = 100.dp),
                    placeholder = { Text(t("What went wrong? What did you tap?", "What happened, and what were you doing?")) },
                    colors = OutlinedTextFieldDefaults.colors(focusedBorderColor = NoCap.Red, cursorColor = NoCap.Red),
                )
                Text(
                    t("Don't put passwords or private stuff in here.", "Please don't include passwords or personal information."),
                    Modifier.padding(vertical = 6.dp), style = MaterialTheme.typography.bodySmall, color = NoCap.Muted,
                )
                Preview(body)
            }
        },
        confirmButton = {
            Column {
                if (a.link != null) TextButton(onClick = { a.sendBugReport(body); onClose() }, enabled = what.isNotBlank()) { Text(t("Send to my server", "Send to my server"), color = NoCap.Red) }
                TextButton(onClick = { reportToDevelopers(a, CrashLog.bugTitle(what), body); onClose() }, enabled = what.isNotBlank()) {
                    Text(t("Report to NoCap developers", "Report to NoCap developers"), color = NoCap.Red)
                }
                TextButton(onClick = onClose) { Text(t("Don't send", "Cancel"), color = NoCap.Text) }
            }
        },
    )
}

/** The raw report, scrollable. */
@Composable
fun CrashViewer(report: String, onClose: () -> Unit) {
    AlertDialog(
        onDismissRequest = onClose,
        containerColor = NoCap.GlassStrong,
        title = { Text(t("Crash report", "Crash report"), style = MaterialTheme.typography.titleMedium) },
        text = {
            Text(
                report,
                Modifier.heightIn(max = 420.dp).verticalScroll(rememberScrollState()),
                style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace, fontSize = 11.sp),
            )
        },
        confirmButton = { TextButton(onClick = onClose) { Text(t("Close", "Close"), color = NoCap.Red) } },
    )
}
