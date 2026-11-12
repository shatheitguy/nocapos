package com.nocap.link.settings

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
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

/** On the start after a crash: offer (once) to send the report to your own server. */
@Composable
fun CrashPrompt(a: MainActivity, report: String, onClose: () -> Unit) {
    var show by remember { mutableStateOf(false) }
    val linked = a.link != null
    if (show) {
        CrashViewer(report) { show = false }
        return
    }
    AlertDialog(
        onDismissRequest = { CrashLog.settle(a); onClose() },
        containerColor = NoCap.GlassStrong,
        title = { Text(t("NoCap OS crashed last time", "NoCap OS stopped unexpectedly"), style = MaterialTheme.typography.titleMedium) },
        text = {
            Text(
                if (linked) {
                    t(
                        "Send the crash report to your NoCap server? It lands in NoCap Link in your home folder. Nothing goes anywhere else.",
                        "Send the crash report to your NoCap server? It's saved in NoCap Link in your home folder and sent nowhere else.",
                    )
                } else {
                    t("The report is kept on this phone (Settings → About).", "The report is kept on this phone under Settings → About.")
                },
                style = MaterialTheme.typography.bodyMedium,
            )
        },
        confirmButton = {
            if (linked) {
                TextButton(onClick = { a.sendCrashReport(report); onClose() }) { Text(t("Send it", "Send"), color = NoCap.Red) }
            } else {
                TextButton(onClick = { CrashLog.settle(a); onClose() }) { Text(t("OK", "OK"), color = NoCap.Red) }
            }
        },
        dismissButton = {
            Column {
                TextButton(onClick = { show = true }) { Text(t("Show me", "View report"), color = NoCap.Text) }
                if (linked) TextButton(onClick = { CrashLog.settle(a); onClose() }) { Text(t("Nah", "Not now"), color = NoCap.Text) }
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
