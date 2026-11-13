package com.nocap.link.sync

import android.Manifest
import android.content.pm.PackageManager
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import com.nocap.link.MainActivity
import com.nocap.link.data.LinkOpt
import com.nocap.link.data.t
import com.nocap.link.net.Route
import com.nocap.link.settings.Action
import com.nocap.link.settings.Hint
import com.nocap.link.settings.Toggle
import com.nocap.link.system.DotsService
import com.nocap.link.ui.Chip
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.glass

/** "Nearby" / "Remote" / offline, as a chip. */
@Composable
fun LinkRouteChip(modifier: Modifier = Modifier) {
    val s = LinkService.status.value
    when {
        !s.connected -> Chip(t("Offline", "Not connected"), NoCap.Muted, modifier)
        s.route == Route.NEARBY -> Chip(t("Nearby", "Nearby"), NoCap.Ok, modifier)
        else -> Chip(t("Remote", "Remote"), NoCap.Warn, modifier)
    }
}

/** NoCap Link hub card: how this phone is connected right now. */
@Composable
fun LinkStatusCard(a: MainActivity) {
    if (a.link == null) return
    Column(Modifier.fillMaxWidth().glass(RoundedCornerShape(18.dp)).padding(14.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                if (LinkService.status.value.connected) t("Connected, no cap", "Connected") else t("Not connected rn", "Not connected"),
                Modifier.weight(1f), style = MaterialTheme.typography.titleSmall,
            )
            LinkRouteChip()
        }
        Text(
            when (LinkService.status.value.route) {
                Route.NEARBY -> t("Talking to your server straight over your home network.", "Connected directly over your home network.")
                Route.REMOTE -> t("Away from home: going through your remote address.", "Away from home: connected through the remote address.")
                null -> t("Looking for your server…", "Looking for your server…")
            },
            style = MaterialTheme.typography.bodySmall,
        )
    }
}

/** Settings → Notifications and clipboard: staying connected and calls. */
@Composable
fun LinkSettings(a: MainActivity) {
    var tick by remember { mutableIntStateOf(0) }
    val askCalls = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { tick++ }
    Toggle(
        t("Always connected", "Always connected"),
        t("Stays linked in the background, after a restart too, with a quiet “NoCap Link connected” notification. Only talks to your own server.", "Keeps a connection to your server in the background and after a restart, shown by a quiet “NoCap Link connected” notification. Only your server is contacted."),
        LinkOpt.alwaysOn.v,
    ) { LinkOpt.alwaysOn.set(it); Sync.start(a); tick++ }
    if (LinkOpt.alwaysOn.v) {
        val free = remember(tick) { LinkService.unrestricted(a) }
        if (!free) {
            Hint(t("Android may pause the connection to save battery. Let NoCap OS stay connected:", "Android may pause the connection to save battery. Allow NoCap OS to stay connected:"))
            Action(t("Don't put NoCap to sleep", "Allow background connection")) { LinkService.askUnrestricted(a); tick++ }
        }
    }
    Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(vertical = 4.dp)) {
        Text(t("Right now", "Status"), Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium)
        LinkRouteChip()
    }
    val access = remember(tick) { DotsService.allowed(a) }
    Toggle(
        t("Calls on NoCap OS", "Show calls on NoCap OS"),
        if (!access) t("Needs notification access. Incoming, ongoing and missed calls show on NoCap OS.", "Requires notification access. Incoming, ongoing and missed calls are shown on NoCap OS.")
        else t("Incoming, ongoing and missed calls show on NoCap OS, with Decline.", "Incoming, ongoing and missed calls are shown on NoCap OS, with a Decline button."),
        LinkOpt.calls.v && access,
    ) { on ->
        LinkOpt.calls.set(on)
        if (on && !access) DotsService.openSettings(a)
        if (on && Build.VERSION.SDK_INT >= 28 &&
            ContextCompat.checkSelfPermission(a, Manifest.permission.ANSWER_PHONE_CALLS) != PackageManager.PERMISSION_GRANTED
        ) askCalls.launch(Manifest.permission.ANSWER_PHONE_CALLS)
        tick++
    }
}
