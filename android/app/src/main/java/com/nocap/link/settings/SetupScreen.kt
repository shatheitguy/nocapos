package com.nocap.link.settings

import android.Manifest
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.Image
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
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.draw.clip
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.unit.dp
import com.nocap.link.data.t
import com.nocap.link.MainActivity
import com.nocap.link.R
import com.nocap.link.Screen
import com.nocap.link.ServerState
import com.nocap.link.ui.GhostButton
import com.nocap.link.ui.GlassCard
import com.nocap.link.ui.Label
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.RedButton
import com.nocap.link.ui.Wallpaper

/** The first-run steps, in order. */
enum class SetupStep { WELCOME, LINK, BASICS, EXTRAS }

/**
 * First run: link your NoCap server, then switch on what you want, one step
 * at a time. Everything can be skipped and done later in Settings.
 */
@Composable
fun SetupScreen(a: MainActivity, step: SetupStep) {
    val go = { s: SetupStep -> a.screen = Screen.Setup(s) }
    BackHandler(step != SetupStep.WELCOME) { go(SetupStep.entries[step.ordinal - 1]) }
    Wallpaper {
        Column(
            Modifier.fillMaxSize().statusBarsPadding().navigationBarsPadding().verticalScroll(rememberScrollState()).padding(22.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Column(Modifier.widthIn(max = 560.dp).fillMaxWidth()) {
                Spacer(Modifier.height(16.dp))
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Image(painterResource(R.drawable.nocap_mark), null, Modifier.size(44.dp))
                    Text("NOCAP OS", Modifier.padding(start = 12.dp).weight(1f), style = MaterialTheme.typography.headlineSmall)
                    Text("${step.ordinal + 1}/${SetupStep.entries.size}", style = MaterialTheme.typography.labelMedium, color = NoCap.Muted)
                }
                Spacer(Modifier.height(10.dp))
                Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    SetupStep.entries.forEach { s ->
                        Box(Modifier.weight(1f).height(4.dp).clip(RoundedCornerShape(50)).background(if (s.ordinal <= step.ordinal) NoCap.Red else NoCap.Field))
                    }
                }
                Spacer(Modifier.height(22.dp))
                when (step) {
                    SetupStep.WELCOME -> Welcome(a) { go(SetupStep.LINK) }
                    SetupStep.LINK -> LinkStep(a) { go(SetupStep.BASICS) }
                    SetupStep.BASICS -> PermStep(a, Permissions.Kind.CORE, t("The basics", "Recommended permissions"), t("Each one is optional. Here's what they do.", "Each is optional; here's what each one is for.")) { go(SetupStep.EXTRAS) }
                    SetupStep.EXTRAS -> PermStep(a, Permissions.Kind.OPTIONAL, t("Extras", "Optional extras"), t("Only for the widgets and gestures you use. Skip freely.", "Only needed for specific widgets and gestures.")) { a.finishSetup() }
                }
            }
        }
    }
}

@Composable
private fun Welcome(a: MainActivity, next: () -> Unit) {
    Text(t("Your phone, but make it NoCap.", "Welcome to NoCap OS."), style = MaterialTheme.typography.displaySmall)
    Spacer(Modifier.height(10.dp))
    Text(
        t(
            "A home screen that matches your NoCap desktop, linked to your own server. Nothing goes anywhere else, fr.",
            "A home screen that matches the NoCap desktop and links to your own NoCap server. Nothing is sent anywhere else.",
        ),
        style = MaterialTheme.typography.bodyLarge, color = NoCap.Muted,
    )
    Spacer(Modifier.height(18.dp))
    GlassCard(Modifier.fillMaxWidth()) {
        Column {
            Label(t("Setup takes a minute", "Setup steps"))
            Spacer(Modifier.height(6.dp))
            listOf(
                t("1. Link your NoCap server", "1. Link your NoCap server"),
                t("2. The basics: notifications, camera, home screen", "2. Recommended: notifications, camera, home app"),
                t("3. Extras for widgets and gestures", "3. Optional extras for widgets and gestures"),
            ).forEach { Text(it, Modifier.padding(vertical = 2.dp), style = MaterialTheme.typography.bodyMedium) }
        }
    }
    Spacer(Modifier.height(22.dp))
    RedButton(t("Let's go", "Start"), next, Modifier.fillMaxWidth())
    Spacer(Modifier.height(8.dp))
    GhostButton(t("Skip setup", "Skip setup"), { a.finishSetup() }, Modifier.fillMaxWidth())
}

@Composable
private fun LinkStep(a: MainActivity, next: () -> Unit) {
    Text(t("Link your NoCap server", "Link your NoCap server"), style = MaterialTheme.typography.displaySmall)
    Spacer(Modifier.height(10.dp))
    val s = a.server
    if (a.link != null && s !is ServerState.NotLinked) {
        GlassCard(Modifier.fillMaxWidth()) {
            Column {
                Text(t("Linked. W.", "Linked."), style = MaterialTheme.typography.titleMedium, color = NoCap.Ok)
                Text(a.link?.serverName?.ifBlank { a.link?.server } ?: "", style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted)
            }
        }
        Spacer(Modifier.height(22.dp))
        RedButton(t("Next", "Next"), next, Modifier.fillMaxWidth())
        return
    }
    Text(
        t(
            "On your NoCap desktop open Settings → Devices → Link a phone. Scan the QR code, or type the address and code.",
            "On the NoCap desktop, open Settings → Devices → Link a phone, then scan the QR code or enter the address and code.",
        ),
        style = MaterialTheme.typography.bodyLarge, color = NoCap.Muted,
    )
    Spacer(Modifier.height(22.dp))
    RedButton(t("Scan the QR code", "Scan QR code"), {
        Permissions.ask(a, Manifest.permission.CAMERA) { ok ->
            if (ok) a.screen = Screen.Scan else a.toast = t("No camera, no scan. Type it in instead.", "Camera access is needed to scan. Enter the code instead.")
        }
    }, Modifier.fillMaxWidth())
    Spacer(Modifier.height(8.dp))
    GhostButton(t("Type it in", "Enter address and code"), { a.screen = Screen.LinkForm() }, Modifier.fillMaxWidth())
    Spacer(Modifier.height(8.dp))
    GhostButton(t("Later", "Skip for now"), next, Modifier.fillMaxWidth())
}

@Composable
private fun PermStep(a: MainActivity, kind: Permissions.Kind, title: String, sub: String, next: () -> Unit) {
    Text(title, style = MaterialTheme.typography.displaySmall)
    Spacer(Modifier.height(8.dp))
    Text(sub, style = MaterialTheme.typography.bodyLarge, color = NoCap.Muted)
    Spacer(Modifier.height(16.dp))
    Permissions.all.filter { it.kind == kind }.forEach { p ->
        PermissionCard(a, p, a.resumeTick)
        Spacer(Modifier.height(10.dp))
    }
    Spacer(Modifier.height(12.dp))
    RedButton(if (kind == Permissions.Kind.OPTIONAL) t("Done", "Finish") else t("Next", "Next"), next, Modifier.fillMaxWidth())
}
