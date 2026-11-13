package com.nocap.link.settings

import android.Manifest
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
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
import androidx.compose.runtime.getValue
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.unit.dp
import com.nocap.link.data.t
import com.nocap.link.MainActivity
import com.nocap.link.R
import com.nocap.link.Screen
import com.nocap.link.ServerState
import com.nocap.link.ui.Chip
import com.nocap.link.ui.GhostButton
import com.nocap.link.ui.GlassCard
import com.nocap.link.ui.Label
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.RedButton
import com.nocap.link.ui.Wallpaper

/** The first-run steps, in order. */
enum class SetupStep { WELCOME, PRIVACY, LINK, GUIDE }

/**
 * First run: hello, the privacy promise, link your NoCap server, then the
 * full-access guide, one permission per screen. Everything can be skipped,
 * picks up where you left off, and runs again from Settings → Setup guide.
 */
@Composable
fun SetupScreen(a: MainActivity, step: SetupStep) {
    val go = { s: SetupStep -> Guide.saveStep(a, s); a.screen = Screen.Setup(s) }
    LaunchedEffect(step) { Guide.saveStep(a, step) }
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
                    Text(t("Skip", "Skip"), Modifier.clip(RoundedCornerShape(10.dp)).clickable { a.finishSetup() }.padding(8.dp), style = MaterialTheme.typography.labelLarge, color = NoCap.Muted)
                }
                Spacer(Modifier.height(22.dp))
                when (step) {
                    SetupStep.WELCOME -> Welcome(a) { go(if (Guide.termsOk(a)) SetupStep.LINK else SetupStep.PRIVACY) }
                    SetupStep.PRIVACY -> TermsCard(a) { Guide.agree(a); go(SetupStep.LINK) }
                    SetupStep.LINK -> LinkStep(a) { go(SetupStep.GUIDE) }
                    SetupStep.GUIDE -> GuideStep(a)
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
            Label(t("Setup takes a couple minutes", "Setup steps"))
            Spacer(Modifier.height(6.dp))
            listOf(
                t("1. The privacy promise", "1. Privacy"),
                t("2. Link your NoCap server", "2. Link your NoCap server"),
                t("3. Full access, one switch at a time", "3. Permissions, one at a time"),
            ).forEach { Text(it, Modifier.padding(vertical = 2.dp), style = MaterialTheme.typography.bodyMedium) }
        }
    }
    Spacer(Modifier.height(22.dp))
    RedButton(t("Let's go", "Start"), next, Modifier.fillMaxWidth())
    Spacer(Modifier.height(8.dp))
    GhostButton(t("Skip setup", "Skip setup"), { a.finishSetup() }, Modifier.fillMaxWidth())
}

/** Short and warm: what we do with your data (nothing). Agreeing moves on. */
@Composable
fun TermsCard(a: MainActivity, agree: () -> Unit) {
    Text(t("Real talk: your data", "Terms & privacy"), style = MaterialTheme.typography.displaySmall)
    Spacer(Modifier.height(14.dp))
    GlassCard(Modifier.fillMaxWidth()) {
        Column {
            Text(
                t(
                    "NoCap OS doesn't collect a single piece of your data. No tracking, no analytics, no ads. Everything stays between your phone and your own NoCap server. We respect your privacy, always. No cap.",
                    "NoCap OS doesn't collect any of your data. No tracking, no analytics, no ads. Everything stays between your phone and your own NoCap server. We respect your privacy — always.",
                ),
                style = MaterialTheme.typography.bodyLarge,
            )
            Spacer(Modifier.height(10.dp))
            Text(
                t(
                    "Weather (if you turn it on) asks Open-Meteo with a rough location, and update checks talk to GitHub. That's the whole list.",
                    "The only exceptions: weather (if enabled) asks Open-Meteo using an approximate location, and update checks contact GitHub.",
                ),
                style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted,
            )
            Text(
                t("Read the full privacy page", "Read the full privacy page"),
                Modifier.padding(top = 10.dp).clip(RoundedCornerShape(8.dp)).clickable { Extras.privacy = true }.padding(vertical = 6.dp),
                style = MaterialTheme.typography.labelLarge, color = NoCap.Red,
            )
        }
    }
    Spacer(Modifier.height(22.dp))
    // The agree button: primary red, with a soft glow.
    Box(Modifier.fillMaxWidth().shadow(18.dp, RoundedCornerShape(14.dp), ambientColor = NoCap.Red, spotColor = NoCap.Red)) {
        RedButton(t("Join the NoCap Fam", "Join the NoCap Community"), agree, Modifier.fillMaxWidth())
    }
    Text(
        t("Tapping it means you're good with the above.", "By continuing you agree to these terms."),
        Modifier.fillMaxWidth().padding(top = 8.dp), style = MaterialTheme.typography.bodySmall, color = NoCap.Muted,
    )
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
            "On your NoCap desktop open Settings → Your Squad → Add to the squad. Scan the QR code, or type the address and code.",
            "On the NoCap desktop, open Settings → Linked Devices → Link a device, then scan the QR code or enter the address and code.",
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

/** One permission per screen, with a progress bar and live status. */
@Composable
private fun GuideStep(a: MainActivity) {
    val tick = a.resumeTick
    val items = remember { Guide.items(a) }
    var i by remember { mutableIntStateOf(Guide.savedIndex(a).coerceIn(0, items.size - 1)) }
    LaunchedEffect(i) { Guide.saveIndex(a, i) }
    val item = items[i]
    val (done, total) = remember(tick, i) { Guide.progress(a, items) }
    val status = remember(tick, i) { runCatching { item.status(a) }.getOrNull() }

    Text(t("$done of $total done", "$done of $total done"), style = MaterialTheme.typography.labelLarge, color = NoCap.Muted)
    Spacer(Modifier.height(8.dp))
    Row(horizontalArrangement = Arrangement.spacedBy(4.dp)) {
        items.forEachIndexed { k, it ->
            val on = remember(tick, k) { runCatching { it.status(a) }.getOrNull() }
            val c = when {
                k == i -> NoCap.Text
                on == true -> NoCap.Ok
                else -> NoCap.Field
            }
            Box(Modifier.weight(1f).height(4.dp).clip(RoundedCornerShape(50)).background(c).clickable { i = k })
        }
    }
    Spacer(Modifier.height(20.dp))
    Text("${i + 1}/${items.size}", style = MaterialTheme.typography.labelMedium, color = NoCap.Muted)
    Row(verticalAlignment = Alignment.CenterVertically) {
        Text(item.title(), Modifier.weight(1f), style = MaterialTheme.typography.headlineSmall)
        when {
            item.info -> Chip(t("Heads up", "Info"), NoCap.Muted)
            status == true -> Chip(t("Done", "Done"), NoCap.Ok)
            status == false -> Chip(t("Not yet", "Not yet"), NoCap.Warn)
            else -> Chip(t("Not needed", "Not needed"), NoCap.Muted)
        }
    }
    Spacer(Modifier.height(8.dp))
    Text(item.why(), style = MaterialTheme.typography.bodyLarge, color = NoCap.Muted)
    Spacer(Modifier.height(14.dp))
    GlassCard(Modifier.fillMaxWidth()) {
        Column {
            Label(t("How", "Steps"))
            Spacer(Modifier.height(4.dp))
            item.steps().forEach { Text(it, Modifier.padding(vertical = 3.dp), style = MaterialTheme.typography.bodyMedium) }
            if (item.restricted && status == false) RestrictedHelp(a, remember(tick) { Restricted.help(a, false) }) { item.doIt(a) }
        }
    }
    Spacer(Modifier.height(18.dp))
    if (!item.info && status == false) {
        RedButton(item.doLabel(), { item.doIt(a) }, Modifier.fillMaxWidth())
        Spacer(Modifier.height(8.dp))
    }
    Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
        GhostButton(t("Back", "Back"), { i-- }, Modifier.weight(1f), enabled = i > 0)
        if (i < items.size - 1) {
            GhostButton(if (status == true || status == null) t("Next", "Next") else t("Skip this", "Skip"), { i++ }, Modifier.weight(1f))
        } else {
            RedButton(t("Done", "Finish"), { Guide.saveIndex(a, 0); a.finishSetup() }, Modifier.weight(1f))
        }
    }
    Spacer(Modifier.height(10.dp))
    Text(
        t("Everything's optional. Change any of it later in Settings → Privacy & permissions.", "Everything is optional and can be changed later in Settings → Privacy & permissions."),
        style = MaterialTheme.typography.bodySmall, color = NoCap.Muted,
    )
}
