package com.nocap.link.update

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.draw.clip
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.lifecycleScope
import com.nocap.link.BuildConfig
import com.nocap.link.data.t
import com.nocap.link.MainActivity
import com.nocap.link.ServerState
import com.nocap.link.settings.Action
import com.nocap.link.settings.Hint
import com.nocap.link.settings.Toggle
import com.nocap.link.ui.Chip
import com.nocap.link.ui.NoCap
import kotlinx.coroutines.launch

private fun mb(b: Long) = "%.1f MB".format(b / 1_048_576.0)

/** The server's version when it's online and linked. */
fun serverVersion(a: MainActivity): String? = (a.server as? ServerState.Online)?.me?.server?.version?.ifBlank { null }

/** True when Settings should show the "update available" banner and badge. */
fun updateBadge(a: MainActivity): Boolean = Updater.state is UpdateState.Available || Updater.serverBehind(serverVersion(a))

/** Checks once when Settings opens, if automatic checks are on. */
@Composable
fun AutoCheck(a: MainActivity) {
    LaunchedEffect(Unit) { if (Updater.autoOn(a)) Updater.refresh(a, force = false) }
}

/** The banner at the top of Settings when an app or server update is out. */
@Composable
fun UpdateBanner(a: MainActivity, onOpen: () -> Unit) {
    val app = Updater.state as? UpdateState.Available
    val server = Updater.serverBehind(serverVersion(a))
    if (app == null && !server) return
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(14.dp)).background(NoCap.Field)
            .border(1.dp, NoCap.Red.copy(alpha = 0.45f), RoundedCornerShape(14.dp))
            .clickable(onClick = onOpen).padding(horizontal = 14.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(
                when {
                    app != null -> t("NoCap OS update dropped", "NoCap OS update available")
                    else -> t("Server update available", "Server update available")
                },
                style = MaterialTheme.typography.titleSmall,
            )
            Text(
                when {
                    app != null && server -> t("For this phone and your NoCap server.", "For this app and your NoCap server.")
                    app != null -> (app.version?.name ?: app.release.tag).let { t("$it is out. Tap to see what's new.", "Version $it is available.") }
                    else -> t("Your server can Glow Up to ${Updater.latest?.tag}.", "Your NoCapOS server can update to ${Updater.latest?.tag}.")
                },
                style = MaterialTheme.typography.bodySmall, color = NoCap.Muted,
            )
        }
        Text(t("View", "View"), style = MaterialTheme.typography.labelLarge, color = NoCap.Red)
    }
}

/** Settings → About → Updates → Updates: this app, and the linked server. */
@Composable
fun UpdatesSection(a: MainActivity) {
    val s = Updater.state
    val run = { force: Boolean -> a.lifecycleScope.launch { Updater.refresh(a, force) }; Unit }
    Row(verticalAlignment = Alignment.CenterVertically) {
        Text(t("This app", "This app"), Modifier.weight(1f), style = MaterialTheme.typography.titleSmall)
        Text("${BuildConfig.VERSION_NAME} (${BuildConfig.VERSION_CODE})", style = MaterialTheme.typography.labelLarge, color = NoCap.Muted)
    }
    val lv = Updater.latestVersion
    if (Updater.latest != null) Row(Modifier.padding(top = 4.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(t("Latest", "Latest version"), Modifier.weight(1f), style = MaterialTheme.typography.titleSmall)
        Text(
            lv?.let { v -> listOfNotNull(v.name, v.code?.let { "($it)" }).joinToString(" ") } ?: t("in ${Updater.latest?.tag}", "in release ${Updater.latest?.tag}"),
            style = MaterialTheme.typography.labelLarge, color = NoCap.Muted,
        )
    }
    when (s) {
        UpdateState.Idle -> Hint(t("Not checked yet.", "Not checked yet."))
        UpdateState.Checking -> Hint(t("Checking…", "Checking for updates…"))
        UpdateState.UpToDate -> Row(Modifier.padding(vertical = 6.dp)) { Chip(t("You're on the latest. W.", "Up to date"), NoCap.Ok) }
        is UpdateState.NoApk -> Hint(t("The newest release (${s.release.tag}) has no phone app attached yet. Check again later.", "The latest release (${s.release.tag}) doesn't include the app yet. Try again later."))
        is UpdateState.Available -> {
            Row(Modifier.padding(vertical = 6.dp)) { Chip(t("Update available", "Update available"), NoCap.Red) }
            if (s.version == null) Hint(t("Can't tell the version from here. It's downloaded and checked before anything installs.", "The version can't be determined in advance; the download is checked before installing."))
            val notes = remember(s.release.body) { Releases.whatsNew(s.release.body) }
            if (notes.isNotBlank()) {
                Text(t("What's new", "What's new"), Modifier.padding(top = 4.dp), style = MaterialTheme.typography.labelLarge, color = NoCap.Muted)
                Text(notes, Modifier.padding(top = 2.dp), style = MaterialTheme.typography.bodySmall)
            }
            Action(t("Download and install", "Download and install")) { a.lifecycleScope.launch { Updater.update(a, s) } }
        }
        is UpdateState.Downloading -> {
            val p = if (s.total > 0) (s.done.toFloat() / s.total).coerceIn(0f, 1f) else null
            Text(t("Downloading… ", "Downloading… ") + if (s.total > 0) "${mb(s.done)} / ${mb(s.total)}" else mb(s.done), Modifier.padding(top = 6.dp), style = MaterialTheme.typography.bodySmall)
            if (p != null) LinearProgressIndicator(progress = { p }, modifier = Modifier.fillMaxWidth().padding(vertical = 6.dp), color = NoCap.Red, trackColor = NoCap.Field)
            else LinearProgressIndicator(Modifier.fillMaxWidth().padding(vertical = 6.dp), color = NoCap.Red, trackColor = NoCap.Field)
        }
        UpdateState.Installing -> Hint(t("Installing… NoCap OS restarts when it's done.", "Installing… NoCap OS will restart."))
        UpdateState.WaitingForYou -> Hint(t("Confirm the install in Android's prompt.", "Confirm the install in the Android prompt."))
        is UpdateState.Failed -> Text(s.why, Modifier.padding(vertical = 4.dp), style = MaterialTheme.typography.bodySmall, color = NoCap.Warn)
    }
    if (!Updater.busy) Action(t("Check for updates", "Check for updates")) { run(true) }

    // The linked server.
    val sv = serverVersion(a)
    if (sv != null) {
        Row(Modifier.padding(top = 6.dp), verticalAlignment = Alignment.CenterVertically) {
            Text(t("NoCap server", "NoCapOS server"), Modifier.weight(1f), style = MaterialTheme.typography.titleSmall)
            Text("v$sv", style = MaterialTheme.typography.labelLarge, color = NoCap.Muted)
        }
        if (Updater.serverBehind(sv)) {
            Row(Modifier.padding(vertical = 6.dp)) { Chip(t("Server update available: ${Updater.latest?.tag}", "Server update available (${Updater.latest?.tag})"), NoCap.Red) }
            Action(t("Open Glow Up on my server", "Open Glow Up on the server")) { a.openDesktop(app = "settings", section = "update") }
            Hint(t("Glow Up needs an admin account on the server.", "Updating the server requires an administrator account."))
        }
    }

    var auto by remember { mutableStateOf(Updater.autoOn(a)) }
    Toggle(
        t("Check by itself", "Check automatically"),
        t("When Settings opens and once a day, with a notification when something new drops.", "When Settings opens and once a day, with a notification when an update is available."),
        auto,
    ) { auto = it; Updater.setAuto(a, it) }
    Hint(
        t(
            "Updates come from the NoCapOS releases on GitHub and are only installed if they're signed with the same key as this app. Installed from a file? After the first update from here, Android usually stops blocking accessibility, notification access and device admin for NoCap OS. Not guaranteed: some phones and Android 15 keep the block, and then App info → ⋮ → Allow restricted settings still does it.",
            "Updates are downloaded from the NoCapOS releases on GitHub and installed only if signed with the same key as this app. If you installed from a file: after the first in-app update, Android usually lifts the restricted-settings block on accessibility, notification access and device admin. This isn't guaranteed; some manufacturers and Android 15 keep it, in which case use App info → ⋮ → Allow restricted settings.",
        ),
    )
}
