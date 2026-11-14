package com.nocap.link.settings

import android.Manifest
import android.os.Build
import androidx.activity.compose.BackHandler
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideOutHorizontally
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.scale
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.lifecycleScope
import com.nocap.link.MainActivity
import com.nocap.link.R
import com.nocap.link.Screen
import com.nocap.link.ServerState
import com.nocap.link.data.Lingo
import com.nocap.link.data.Opt
import com.nocap.link.data.PhoneApps
import com.nocap.link.data.Prefs
import com.nocap.link.data.ThemeMode
import com.nocap.link.data.t
import com.nocap.link.home.WallpaperLayer
import com.nocap.link.home.Wallpapers
import com.nocap.link.lock.LockWatch
import com.nocap.link.lock.NoCapWalls
import com.nocap.link.ui.Chip
import com.nocap.link.ui.IconTile
import com.nocap.link.ui.NavRow
import com.nocap.link.ui.NcIcon
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.PillButton
import com.nocap.link.ui.SectionCard
import com.nocap.link.ui.SwitchRow
import com.nocap.link.ui.glass
import kotlinx.coroutines.launch

/** The first-run steps, in order (names are saved, so old ones stay valid). */
enum class SetupStep { WELCOME, PRIVACY, VOICE, LOOK, LINK, NOCAP, GUIDE, DONE }

/** The steps shown in the progress strip (the intro and the finish aren't steps). */
private val counted = listOf(SetupStep.PRIVACY, SetupStep.VOICE, SetupStep.LOOK, SetupStep.LINK, SetupStep.NOCAP, SetupStep.GUIDE)

private fun stepName(s: SetupStep) = when (s) {
    SetupStep.PRIVACY -> t("Privacy", "Privacy")
    SetupStep.VOICE -> t("Voice", "Voice")
    SetupStep.LOOK -> t("Look", "Look")
    SetupStep.LINK -> t("Link", "Link")
    SetupStep.NOCAP -> t("Make it NoCap", "NoCap phone")
    SetupStep.GUIDE -> t("Access", "Permissions")
    else -> ""
}

/**
 * First run, like the NoCapOS setup wizard: an animated hello, then one
 * step per screen with a progress strip (privacy, voice, look, link your
 * server, make the phone NoCap, permissions) and a recap. Everything can be
 * skipped, picks up where you left off, and runs again from Settings.
 */
@Composable
fun SetupScreen(a: MainActivity, step: SetupStep) {
    var forward by remember { mutableStateOf(true) }
    val go = { s: SetupStep -> forward = s.ordinal >= step.ordinal; Guide.saveStep(a, s); a.screen = Screen.Setup(s) }
    LaunchedEffect(step) { Guide.saveStep(a, step) }
    BackHandler(step != SetupStep.WELCOME) { go(SetupStep.entries[step.ordinal - 1]) }
    Box(Modifier.fillMaxSize().background(NoCap.Bg)) {
        WallpaperLayer(extraBlur = 18.dp)
        Box(Modifier.fillMaxSize().background(Color.Black.copy(alpha = if (NoCap.light) 0.15f else 0.35f)))
        Column(Modifier.fillMaxSize().statusBarsPadding().navigationBarsPadding(), horizontalAlignment = Alignment.CenterHorizontally) {
            // The wizard window: a frosted sheet, like the web OS's.
            Column(
                Modifier.widthIn(max = 620.dp).fillMaxWidth().weight(1f).padding(12.dp)
                    .glass(RoundedCornerShape(26.dp), strong = true),
            ) {
                if (step in counted) Progress(step) { s -> if (s.ordinal < step.ordinal) go(s) }
                AnimatedContent(
                    step,
                    Modifier.weight(1f),
                    transitionSpec = {
                        val dir = if (forward) 1 else -1
                        (slideInHorizontally(tween(380, easing = FastOutSlowInEasing)) { it / 12 * dir } + fadeIn(tween(380)))
                            .togetherWith(slideOutHorizontally(tween(220)) { -it / 16 * dir } + fadeOut(tween(200)))
                    },
                    label = "wizard",
                ) { s ->
                    Column(
                        Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 22.dp, vertical = 18.dp),
                        verticalArrangement = if (s == SetupStep.WELCOME || s == SetupStep.DONE) Arrangement.Center else Arrangement.Top,
                    ) {
                        when (s) {
                            SetupStep.WELCOME -> Intro(a) { go(if (Guide.termsOk(a)) SetupStep.VOICE else SetupStep.PRIVACY) }
                            SetupStep.PRIVACY -> TermsCard(a) { Guide.agree(a); go(SetupStep.VOICE) }
                            SetupStep.VOICE -> VoiceStep { go(SetupStep.LOOK) }
                            SetupStep.LOOK -> LookStep { go(SetupStep.LINK) }
                            SetupStep.LINK -> LinkStep(a) { go(SetupStep.NOCAP) }
                            SetupStep.NOCAP -> NoCapStep(a) { go(SetupStep.GUIDE) }
                            SetupStep.GUIDE -> GuideStep(a) { go(SetupStep.DONE) }
                            SetupStep.DONE -> Finish(a)
                        }
                    }
                }
            }
        }
    }
}

/** "Step n of total", a filling bar and the step names (done ones in the accent). */
@Composable
private fun Progress(step: SetupStep, jump: (SetupStep) -> Unit) {
    val n = counted.indexOf(step) + 1
    val fill by animateFloatAsState(n / counted.size.toFloat(), tween(420), label = "progress")
    Column(Modifier.fillMaxWidth().padding(start = 22.dp, end = 22.dp, top = 18.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Image(painterResource(R.drawable.nocap_mark), null, Modifier.size(24.dp))
            Text(t("Set up NoCap OS", "Set up NoCap OS"), Modifier.padding(start = 8.dp).weight(1f), style = MaterialTheme.typography.titleSmall)
            Text(t("Step $n of ${counted.size}", "Step $n of ${counted.size}"), style = MaterialTheme.typography.labelSmall, color = NoCap.Muted)
        }
        Box(Modifier.fillMaxWidth().height(4.dp).clip(RoundedCornerShape(4.dp)).background(NoCap.Field)) {
            Box(Modifier.fillMaxWidth(fill).height(4.dp).clip(RoundedCornerShape(4.dp)).background(Brush.horizontalGradient(listOf(NoCap.RedDeep, NoCap.Red))))
        }
        Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(4.dp)) {
            counted.forEach { s ->
                val on = s == step
                val done = s.ordinal < step.ordinal
                Text(
                    stepName(s),
                    Modifier.clip(RoundedCornerShape(50)).background(if (on) NoCap.Red.copy(alpha = 0.16f) else Color.Transparent)
                        .clickable(enabled = done) { jump(s) }.padding(horizontal = 10.dp, vertical = 4.dp),
                    style = MaterialTheme.typography.labelSmall.copy(fontWeight = if (on) FontWeight.Bold else FontWeight.SemiBold),
                    color = when { on -> NoCap.Text; done -> NoCap.Red; else -> NoCap.Muted },
                    maxLines = 1,
                )
            }
        }
    }
}

@Composable
private fun Title(text: String, sub: String?) {
    Text(text, style = MaterialTheme.typography.titleMedium.copy(fontSize = 26.sp, fontWeight = FontWeight.Bold), color = NoCap.Text)
    if (sub != null) Text(sub, Modifier.padding(top = 6.dp), style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted)
    Spacer(Modifier.height(18.dp))
}

@Composable
private fun Buttons(next: String, onNext: () -> Unit, skip: String? = null, onSkip: (() -> Unit)? = null) {
    Spacer(Modifier.height(22.dp))
    PillButton(next, onNext, Modifier.fillMaxWidth())
    if (skip != null && onSkip != null) {
        Spacer(Modifier.height(8.dp))
        PillButton(skip, onSkip, Modifier.fillMaxWidth(), primary = false)
    }
}

// ---------- intro ----------

/** The NoCap mark with rings rippling out, then the title word by word (the web wizard's intro). */
@Composable
private fun LogoStage(size: androidx.compose.ui.unit.Dp = 170.dp) {
    val inf = rememberInfiniteTransition(label = "rings")
    val phase by inf.animateFloat(0f, 1f, infiniteRepeatable(tween(2600, easing = LinearEasing), RepeatMode.Restart), label = "phase")
    val glow by inf.animateFloat(0.85f, 1.15f, infiniteRepeatable(tween(1800), RepeatMode.Reverse), label = "glow")
    var shown by remember { mutableStateOf(false) }
    LaunchedEffect(Unit) { shown = true }
    val pop by animateFloatAsState(if (shown) 1f else 0.4f, tween(900, easing = FastOutSlowInEasing), label = "pop")
    val red = NoCap.Red
    Box(Modifier.size(size), contentAlignment = Alignment.Center) {
        Canvas(Modifier.fillMaxSize()) {
            drawCircle(Brush.radialGradient(listOf(red.copy(alpha = 0.35f), Color.Transparent)), radius = this.size.minDimension / 2 * glow)
            for (k in 0 until 3) {
                val p = (phase + k / 3f) % 1f
                drawCircle(red.copy(alpha = (1f - p) * 0.55f), radius = this.size.minDimension * (0.22f + p * 0.3f), style = Stroke(width = 2.dp.toPx()))
            }
        }
        Image(painterResource(R.drawable.nocap_mark), null, Modifier.size(size * 0.46f).scale(pop).alpha(pop.coerceIn(0f, 1f)))
    }
}

/** Words that rise in one after another. */
@Composable
@OptIn(androidx.compose.foundation.layout.ExperimentalLayoutApi::class)
private fun Words(text: String, delayMs: Int = 450) {
    val words = text.split(' ')
    androidx.compose.foundation.layout.FlowRow(horizontalArrangement = Arrangement.Center) {
        words.forEachIndexed { i, w ->
            var on by remember(text) { mutableStateOf(false) }
            LaunchedEffect(text) { kotlinx.coroutines.delay((delayMs + i * 90).toLong()); on = true }
            val k by animateFloatAsState(if (on) 1f else 0f, tween(520, easing = FastOutSlowInEasing), label = "word")
            Text(
                "$w ",
                Modifier.graphicsLayer { alpha = k; translationY = (1f - k) * 28f },
                style = MaterialTheme.typography.titleMedium.copy(fontSize = 30.sp, fontWeight = FontWeight.Bold),
                color = NoCap.Text,
            )
        }
    }
}

@Composable
private fun Rise(delayMs: Int, content: @Composable () -> Unit) {
    var on by remember { mutableStateOf(false) }
    LaunchedEffect(Unit) { kotlinx.coroutines.delay(delayMs.toLong()); on = true }
    val k by animateFloatAsState(if (on) 1f else 0f, tween(600), label = "rise")
    Box(Modifier.graphicsLayer { alpha = k; translationY = (1f - k) * 24f }) { content() }
}

@Composable
private fun Intro(a: MainActivity, next: () -> Unit) {
    Column(Modifier.fillMaxWidth(), horizontalAlignment = Alignment.CenterHorizontally) {
        LogoStage()
        Spacer(Modifier.height(6.dp))
        Words(t("Welcome to NoCap OS", "Welcome to NoCap OS"))
        Spacer(Modifier.height(10.dp))
        Rise(950) {
            Text(
                t(
                    "Your phone, but make it NoCap. A few quick steps to make it yours — skip anything, it's all in Settings later.",
                    "A few quick steps to set up this phone. You can skip any of them and change everything later in Settings.",
                ),
                Modifier.widthIn(max = 420.dp), style = MaterialTheme.typography.bodyLarge, color = NoCap.Muted, textAlign = TextAlign.Center,
            )
        }
        Spacer(Modifier.height(22.dp))
        Rise(1100) {
            Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(8.dp)) {
                PillButton(t("Let's go", "Get started"), next, Modifier.widthIn(min = 220.dp), icon = "chevronRight")
                Text(
                    t("Skip setup for now", "Skip setup"),
                    Modifier.clip(RoundedCornerShape(50)).clickable { a.finishSetup() }.padding(horizontal = 14.dp, vertical = 8.dp),
                    style = MaterialTheme.typography.labelLarge, color = NoCap.Muted,
                )
            }
        }
    }
}

// ---------- privacy ----------

/** Short and warm: what we do with your data (nothing). Agreeing moves on. */
@Composable
fun TermsCard(a: MainActivity, agree: () -> Unit) {
    Title(t("Real talk: your data", "Terms & privacy"), null)
    SectionCard {
        Text(
            t(
                "NoCap OS doesn't collect a single piece of your data. No tracking, no analytics, no ads. Everything stays between your phone and your own NoCap server. We respect your privacy, always. No cap.",
                "NoCap OS doesn't collect any of your data. No tracking, no analytics, no ads. Everything stays between your phone and your own NoCap server. We respect your privacy — always.",
            ),
            Modifier.padding(16.dp), style = MaterialTheme.typography.bodyLarge,
        )
        Text(
            t(
                "Weather (if you turn it on) asks Open-Meteo with a rough location, and update checks talk to GitHub. That's the whole list.",
                "The only exceptions: weather (if enabled) asks Open-Meteo using an approximate location, and update checks contact GitHub.",
            ),
            Modifier.padding(16.dp), style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted,
        )
        NavRow(t("Read the full privacy page", "Read the full privacy page"), icon = "shield", iconColor = Color(0xFF30A0FF)) { Extras.privacy = true }
    }
    Buttons(t("Join the NoCap Fam", "Join the NoCap Community"), agree)
    Text(
        t("Tapping it means you're good with the above.", "By continuing you agree to these terms."),
        Modifier.fillMaxWidth().padding(top = 8.dp), style = MaterialTheme.typography.bodySmall, color = NoCap.Muted, textAlign = TextAlign.Center,
    )
}

// ---------- voice ----------

@Composable
private fun VoiceStep(next: () -> Unit) {
    Title(t("Pick your vibe", "Choose a voice"), t("How should NoCap OS talk to you? Switch any time in Settings.", "How should NoCap OS word things? You can switch any time in Settings."))
    val genz = Opt.lingo.v != Lingo.STANDARD
    VoiceCard("Gen Z", "Bet — your files are synced. No cap.", genz) { Opt.lingo.set(Lingo.GENZ) }
    Spacer(Modifier.height(12.dp))
    VoiceCard("Standard", "Your files are synced.", !genz) { Opt.lingo.set(Lingo.STANDARD) }
    Buttons(t("Next", "Next"), next)
}

@Composable
private fun VoiceCard(name: String, sample: String, on: Boolean, pick: () -> Unit) {
    val shape = RoundedCornerShape(18.dp)
    Column(
        Modifier.fillMaxWidth().clip(shape).background(if (on) NoCap.Red.copy(alpha = 0.12f) else NoCap.Card)
            .border(if (on) 2.dp else 1.dp, if (on) NoCap.Red else NoCap.GlassEdge, shape).clickable(onClick = pick).padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(name, Modifier.weight(1f), style = MaterialTheme.typography.titleSmall)
            if (on) NcIcon("check", NoCap.Red, 20.dp)
        }
        // A chat bubble with how it sounds.
        Text(
            sample,
            Modifier.clip(RoundedCornerShape(16.dp, 16.dp, 16.dp, 4.dp)).background(NoCap.Red).padding(horizontal = 13.dp, vertical = 9.dp),
            style = MaterialTheme.typography.bodyMedium, color = Color.White,
        )
    }
}

// ---------- look ----------

@Composable
private fun LookStep(next: () -> Unit) {
    Title(t("Make it look like yours", "Choose a look"), t("Everything applies right away, so you see it live.", "Changes apply right away."))
    SectionCard(t("Mode", "Mode")) {
        Row(Modifier.fillMaxWidth().padding(12.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            listOf(Triple(ThemeMode.DARK, t("Dark", "Dark"), "moon"), Triple(ThemeMode.LIGHT, t("Light", "Light"), "sun"), Triple(ThemeMode.AUTO, t("Auto", "Automatic"), "auto")).forEach { (m, name, icon) ->
                val on = Opt.theme.v == m
                Column(
                    Modifier.weight(1f).clip(RoundedCornerShape(14.dp)).background(if (on) NoCap.Red.copy(alpha = 0.14f) else NoCap.Field)
                        .border(if (on) 2.dp else 1.dp, if (on) NoCap.Red else NoCap.GlassEdge, RoundedCornerShape(14.dp))
                        .clickable { Opt.theme.set(m) }.padding(vertical = 14.dp),
                    horizontalAlignment = Alignment.CenterHorizontally,
                ) {
                    NcIcon(icon, if (on) NoCap.Red else NoCap.Text, 24.dp)
                    Text(name, Modifier.padding(top = 6.dp), style = MaterialTheme.typography.labelLarge)
                }
            }
        }
    }
    Spacer(Modifier.height(16.dp))
    SectionCard(t("Wallpaper", "Wallpaper")) {
        Row(Modifier.fillMaxWidth().padding(12.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            Wallpapers.builtIn.forEach { w ->
                val on = Prefs.wallpaper == w.id
                Column(Modifier.weight(1f), horizontalAlignment = Alignment.CenterHorizontally) {
                    Image(
                        painterResource(w.thumb), null,
                        Modifier.fillMaxWidth().aspectRatio(0.62f).clip(RoundedCornerShape(12.dp))
                            .border(if (on) 2.dp else 1.dp, if (on) NoCap.Red else NoCap.GlassEdge, RoundedCornerShape(12.dp))
                            .clickable { Prefs.setWallpaper(w.id) },
                        contentScale = ContentScale.Crop,
                    )
                    Text(t(w.genz, w.standard), Modifier.padding(top = 6.dp), style = MaterialTheme.typography.labelSmall, color = if (on) NoCap.Red else NoCap.Muted, maxLines = 1)
                }
            }
        }
    }
    Spacer(Modifier.height(16.dp))
    SectionCard(t("Accent", "Accent color")) {
        Row(Modifier.fillMaxWidth().padding(14.dp), horizontalArrangement = Arrangement.SpaceBetween) {
            NoCap.presets.take(7).forEach { c ->
                val col = Color(c)
                val on = NoCap.hex(col).equals(Opt.accent.v, ignoreCase = true)
                Box(
                    Modifier.size(32.dp).clip(CircleShape).background(col).border(if (on) 3.dp else 1.dp, if (on) NoCap.Text else NoCap.GlassEdge, CircleShape)
                        .clickable { Opt.accent.set(NoCap.hex(col)) },
                )
            }
        }
    }
    Buttons(t("Next", "Next"), next)
}

// ---------- link ----------

@Composable
private fun LinkStep(a: MainActivity, next: () -> Unit) {
    Title(
        t("Link your NoCap server", "Link your NoCap server"),
        t(
            "On your NoCap desktop open Settings → Your Squad → Add to the squad. Scan the QR code, or type the address and code.",
            "On the NoCap desktop, open Settings → Linked Devices → Link a device, then scan the QR code or enter the address and code.",
        ),
    )
    val s = a.server
    if (a.link != null && s !is ServerState.NotLinked) {
        SectionCard {
            NavRow(t("Linked. W.", "Linked"), sub = a.link?.serverName?.ifBlank { a.link?.server } ?: "", icon = "check", iconColor = NoCap.Ok, chevron = false) {}
        }
        Buttons(t("Next", "Next"), next)
        return
    }
    SectionCard {
        NavRow(t("Scan the QR code", "Scan QR code"), sub = t("Fastest. Point it at the code on the desktop.", "Point the camera at the code shown on the desktop."), icon = "camera", iconColor = Color(0xFF30D158)) {
            Permissions.ask(a, Manifest.permission.CAMERA) { ok ->
                if (ok) a.screen = Screen.Scan else a.toast = t("No camera, no scan. Type it in instead.", "Camera access is needed to scan. Enter the code instead.")
            }
        }
        NavRow(
            t("Find NoCap servers nearby", "Find NoCap servers nearby"),
            sub = t("Fresh server that's never been set up? Set it up right from here.", "Includes new servers, which you can set up from this phone."),
            icon = "search", iconColor = Color(0xFF0A84FF),
        ) { a.screen = Screen.Find }
        NavRow(t("Type it in", "Enter address and code"), icon = "edit", iconColor = Color(0xFF8E8E93)) { a.screen = Screen.LinkForm() }
    }
    Buttons(t("Later", "Skip for now"), next)
}

// ---------- make the phone NoCap ----------

/**
 * The recommended "whole phone NoCap" choices, all on unless you switch
 * them off: NoCap OS as the home app, NoCap wallpapers on home and lock
 * (so Android's own colors turn NoCap red), and the NoCap lock screen layer.
 */
@Composable
private fun NoCapStep(a: MainActivity, next: () -> Unit) {
    Title(
        t("Make it NoCap", "Make this phone NoCap"),
        t("Recommended, all on. Switch off anything you don't want.", "Recommended settings, all turned on. Turn off anything you don't want."),
    )
    var home by rememberSaveable { mutableStateOf(true) }
    var theme by rememberSaveable { mutableStateOf(true) }
    var lock by rememberSaveable { mutableStateOf(true) }
    val isHome = remember(a.resumeTick) { runCatching { PhoneApps.isDefaultHome(a) }.getOrDefault(false) }
    SectionCard {
        SwitchRow(
            t("NoCap OS as my home screen", "Set as default home app"),
            if (isHome) t("Already your home screen. W.", "Already the default home app.") else t("Home button takes you here. Android asks you to confirm.", "The Home button opens NoCap OS. Android asks you to confirm."),
            home || isHome, icon = "home", iconColor = Color(0xFF30D158), enabled = !isHome,
        ) { home = it }
        SwitchRow(
            t("Theme my whole phone NoCap", "Apply NoCap theme to my phone"),
            t("NoCap wallpapers on home and lock. On Android 12+ your phone's own colors follow, so they go NoCap red too.", "Sets NoCap wallpapers on the home and lock screens. On Android 12 and later, system colors follow the wallpaper."),
            theme, icon = "palette", iconColor = Color(0xFF5E5CE6),
        ) { theme = it }
        SwitchRow(
            t("NoCap lock screen layer", "NoCap lock screen"),
            t("Clock, music and notifs when the screen wakes. Your PIN or fingerprint still guards the phone.", "Shows a clock, media and notifications when the screen turns on. Your PIN or fingerprint is still required."),
            lock, icon = "lock", iconColor = Color(0xFF48484A),
        ) { lock = it }
    }
    if (lock && !LockWatch.canShow(a)) {
        Text(
            t("The lock layer needs “Display over other apps”; the access step helps with that.", "The lock screen needs “Display over other apps”, which the next step helps you allow."),
            Modifier.padding(start = 8.dp, top = 8.dp), style = MaterialTheme.typography.bodySmall, color = NoCap.Muted,
        )
    }
    Buttons(
        t("Make it NoCap", "Apply"),
        {
            if (theme) {
                a.busy = t("Painting it red…", "Applying…")
                a.lifecycleScope.launch {
                    val ok = runCatching { NoCapWalls.apply(a, home = true, lock = true) }.getOrDefault(false)
                    a.busy = null
                    if (ok) Opt.accent.reset() else a.toast = t("Your phone said no to that wallpaper.", "Couldn't set the phone wallpaper.")
                }
            }
            Opt.lockScreen.set(lock)
            if (lock) LockWatch.start(a)
            if (home && !isHome) PhoneApps.askToBeHome(a)
            next()
        },
        t("Not now", "Skip"), next,
    )
}

// ---------- permissions ----------

/** One permission per screen, with a progress bar and live status. */
@Composable
private fun GuideStep(a: MainActivity, done: () -> Unit) {
    val tick = a.resumeTick
    val items = remember { Guide.items(a) }
    var i by remember { mutableIntStateOf(Guide.savedIndex(a).coerceIn(0, items.size - 1)) }
    LaunchedEffect(i) { Guide.saveIndex(a, i) }
    val item = items[i]
    val (ok, total) = remember(tick, i) { Guide.progress(a, items) }
    val status = remember(tick, i) { runCatching { item.status(a) }.getOrNull() }

    Title(t("Full access, one switch at a time", "Permissions"), t("$ok of $total on. Everything's optional.", "$ok of $total allowed. Everything is optional."))
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
    Spacer(Modifier.height(16.dp))
    SectionCard {
        Row(Modifier.fillMaxWidth().padding(16.dp), verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text("${i + 1}/${items.size}", style = MaterialTheme.typography.labelMedium, color = NoCap.Muted)
                Text(item.title(), style = MaterialTheme.typography.titleMedium)
            }
            when {
                item.info -> Chip(t("Heads up", "Info"), NoCap.Muted)
                status == true -> Chip(t("Done", "Done"), NoCap.Ok)
                status == false -> Chip(t("Not yet", "Not yet"), NoCap.Warn)
                else -> Chip(t("Not needed", "Not needed"), NoCap.Muted)
            }
        }
        Text(item.why(), Modifier.padding(16.dp), style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted)
        Column(Modifier.padding(16.dp)) {
            item.steps().forEach { Text(it, Modifier.padding(vertical = 3.dp), style = MaterialTheme.typography.bodyMedium) }
        }
        if (item.restricted && status == false) {
            Column(Modifier.padding(horizontal = 4.dp)) { RestrictedHelp(a, remember(tick) { Restricted.help(a, false) }) { item.doIt(a) } }
        }
    }
    Spacer(Modifier.height(18.dp))
    if (!item.info && status == false) {
        PillButton(item.doLabel(), { item.doIt(a) }, Modifier.fillMaxWidth())
        Spacer(Modifier.height(8.dp))
    }
    Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
        PillButton(t("Back", "Back"), { i-- }, Modifier.weight(1f), primary = false, enabled = i > 0)
        if (i < items.size - 1) {
            PillButton(if (status == true || status == null) t("Next", "Next") else t("Skip this", "Skip"), { i++ }, Modifier.weight(1f), primary = false)
        } else {
            PillButton(t("Done", "Finish"), { Guide.saveIndex(a, 0); done() }, Modifier.weight(1f))
        }
    }
    Text(
        t("Change any of it later in Settings → Privacy.", "You can change these later in Settings → Privacy & Permissions."),
        Modifier.fillMaxWidth().padding(top = 10.dp), style = MaterialTheme.typography.bodySmall, color = NoCap.Muted, textAlign = TextAlign.Center,
    )
}

// ---------- finish ----------

@Composable
private fun Finish(a: MainActivity) {
    Column(Modifier.fillMaxWidth(), horizontalAlignment = Alignment.CenterHorizontally) {
        LogoStage(140.dp)
        Words(t("You're all set", "You're all set"), delayMs = 250)
        Spacer(Modifier.height(8.dp))
        Rise(700) {
            Text(
                t("Your phone, your rules. Swipe up for The Roster, swipe down for the control center.", "Swipe up for your apps and down for the control center."),
                Modifier.widthIn(max = 420.dp), style = MaterialTheme.typography.bodyLarge, color = NoCap.Muted, textAlign = TextAlign.Center,
            )
        }
        Spacer(Modifier.height(18.dp))
        Rise(850) {
            SectionCard(modifier = Modifier.widthIn(max = 460.dp)) {
                Recap("sparkles", t("Voice", "Voice"), if (Opt.lingo.v == Lingo.STANDARD) "Standard" else "Gen Z")
                Recap(
                    "palette", t("Look", "Look"),
                    when (Opt.theme.v) { ThemeMode.DARK -> t("Dark", "Dark"); ThemeMode.LIGHT -> t("Light", "Light"); ThemeMode.AUTO -> t("Auto", "Automatic") } +
                        " · " + (Wallpapers.builtIn.firstOrNull { it.id == Prefs.wallpaper }?.let { t(it.genz, it.standard) } ?: t("Your pic", "Custom")),
                )
                Recap("phone", t("NoCap server", "NoCap server"), a.link?.serverName?.ifBlank { null } ?: if (a.link != null) t("Linked", "Linked") else t("Not linked yet", "Not linked"))
                Recap("lock", t("Lock screen layer", "Lock screen"), if (Opt.lockScreen.v) t("On", "On") else t("Off", "Off"))
            }
        }
        Spacer(Modifier.height(20.dp))
        Rise(1000) {
            Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(8.dp)) {
                PillButton(t("Start using NoCap OS", "Start using NoCap OS"), { a.finishSetup() }, Modifier.widthIn(min = 240.dp))
                Text(
                    t("Open Settings", "Open Settings"),
                    Modifier.clip(RoundedCornerShape(50)).clickable { a.finishSetup(); a.layer = com.nocap.link.Layer.SETTINGS }.padding(horizontal = 14.dp, vertical = 8.dp),
                    style = MaterialTheme.typography.labelLarge, color = NoCap.Muted,
                )
            }
        }
    }
}

@Composable
private fun Recap(icon: String, name: String, value: String) {
    Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 10.dp), verticalAlignment = Alignment.CenterVertically) {
        IconTile(icon, NoCap.Red, 26.dp)
        Text(name, Modifier.padding(start = 12.dp).weight(1f), style = MaterialTheme.typography.titleSmall)
        Text(value, style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted, maxLines = 1)
    }
}

