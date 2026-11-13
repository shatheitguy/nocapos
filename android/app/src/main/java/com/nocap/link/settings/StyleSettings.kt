package com.nocap.link.settings

import android.os.Build
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.Spacer
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.lifecycleScope
import com.nocap.link.control.EdgeStrip
import com.nocap.link.data.Lingo
import com.nocap.link.data.Opt
import com.nocap.link.data.t
import com.nocap.link.lock.LockWatch
import com.nocap.link.lock.NoCapWalls
import com.nocap.link.MainActivity
import com.nocap.link.ServerState
import com.nocap.link.system.DotsService
import com.nocap.link.ui.Label
import kotlinx.coroutines.launch

/** Settings → Language: Gen Z, Standard, or whatever your NoCap server uses. Everything switches at once. */
@Composable
fun LanguagePicker(a: MainActivity) {
    val linked = a.server !is ServerState.NotLinked
    Choice(
        listOf(t("Gen Z", "Gen Z") to Lingo.GENZ, t("Standard", "Standard") to Lingo.STANDARD, t("Same as my server", "Same as my server") to Lingo.SERVER),
        Opt.lingo.v.takeIf { it == Lingo.STANDARD || it == Lingo.SERVER } ?: Lingo.GENZ,
    ) { pick ->
        Opt.lingo.set(pick)
        if (pick == Lingo.SERVER) a.api()?.let { api -> a.lifecycleScope.launch { Lingo.refresh(api) } }
    }
    Hint(
        when (Opt.lingo.v) {
            Lingo.SERVER -> if (!linked) t("Not linked, so Gen Z for now. Link your server and it follows its Language setting.", "Not linked: the last known server language is used (Gen Z if none). Link a server to follow its Language setting.")
            else t("Follows your NoCap server's Language setting (Gen Z until it answers, or when it's offline).", "Follows the Language setting on your NoCap server (Gen Z while it can't be reached).")
            Lingo.STANDARD -> "Plain, professional wording."
            else -> t("Playful names, with a plain description where it matters.", "")
        },
    )
}

/** Look → the NoCap lock wallpaper and "Theme my whole phone NoCap". */
@Composable
fun ThemePhoneSettings(a: MainActivity) {
    val scope = a.lifecycleScope
    Action(t("Theme my whole phone NoCap", "Apply NoCap theme to my phone")) {
        a.busy = t("Painting it red…", "Applying…")
        scope.launch {
            val ok = NoCapWalls.apply(a, home = true, lock = true)
            a.busy = null
            if (ok) Opt.accent.reset()
            a.toast = if (ok) t("Phone's NoCap now. Home and lock, both red.", "NoCap wallpaper set on the home and lock screens.") else t("Your phone said no to that wallpaper.", "Couldn't set the phone wallpaper.")
        }
    }
    Hint(
        if (Build.VERSION.SDK_INT >= 31) t(
            "Sets NoCap wallpapers on your phone's home and lock screens and NoCap red in here. On Android 12+ your phone's own colors follow the wallpaper, so they go NoCap red too (if not, pick wallpaper colors in Android's Wallpaper & style).",
            "Sets NoCap wallpapers on the home and lock screens and resets the accent to NoCap red. On Android 12 and later, system colors are taken from the wallpaper, so they turn NoCap red (if they don't, choose wallpaper colors in Wallpaper & style).",
        ) else t(
            "Sets NoCap wallpapers on your phone's home and lock screens and NoCap red in here. Phone-wide colors from the wallpaper need Android 12+.",
            "Sets NoCap wallpapers on the home and lock screens and resets the accent to NoCap red. System colors from the wallpaper require Android 12 or later.",
        ),
    )
    Hint(
        t(
            "Real talk: without root, no app can restyle Android's own screens (Settings, the shade, the system lock screen). Wallpaper colors are as far as it goes.",
            "Note: without root access, apps can't restyle Android's own system screens (Settings, the notification shade, the system lock screen). Wallpaper-based colors are the limit.",
        ),
    )
    Action(t("Just the NoCap lock wallpaper", "Set NoCap lock screen wallpaper")) {
        scope.launch {
            a.toast = if (NoCapWalls.apply(a, home = false, lock = true)) t("Lock screen's NoCap now.", "Lock screen wallpaper set.") else t("Your phone said no to that wallpaper.", "Couldn't set the phone wallpaper.")
        }
    }
}

/** Look → the opt-in NoCap lock screen. */
@Composable
fun LockScreenSettings(a: MainActivity) {
    val canShow = remember(a.resumeTick) { LockWatch.canShow(a) }
    val access = remember(a.resumeTick) { DotsService.allowed(a) }
    LaunchedEffect(a.resumeTick) { if (Opt.lockScreen.v) LockWatch.start(a) }
    Toggle(
        t("NoCap lock screen", "NoCap lock screen"),
        t(
            "Clock, NoCap logo, server status, music and notifs when the screen turns on. Swipe up and Android still asks for your PIN or fingerprint — it never gets skipped.",
            "Shows a clock, the NoCap logo, server status, media controls and notifications when the screen turns on. Swiping up asks Android to unlock, so your PIN or fingerprint is still required.",
        ),
        Opt.lockScreen.v,
    ) { on ->
        Opt.lockScreen.set(on)
        if (on) {
            LockWatch.start(a)
            if (!LockWatch.canShow(a)) runCatching { a.startActivity(EdgeStrip.permissionIntent(a)) }
        }
    }
    if (Opt.lockScreen.v && !canShow) {
        Hint(
            t(
                "Android only lets an app pop up when the screen turns on with “Display over other apps”. Allow it for NoCap OS.",
                "Android requires “Display over other apps” for an app to appear when the screen turns on. Allow it for NoCap OS.",
            ),
        )
        Action(t("Allow display over other apps", "Allow display over other apps")) { runCatching { a.startActivity(EdgeStrip.permissionIntent(a)) } }
    }
    if (Opt.lockScreen.v) {
        Toggle(
            t("Show notif text on the lock screen", "Show notification content when locked"),
            t("Off: just which apps and how many. On: anyone holding your phone can read them.", "Off: only app names and counts. On: message text is visible without unlocking."),
            Opt.lockNotifText.v,
        ) { Opt.lockNotifText.set(it) }
        if (!access) {
            Hint(t("Music and notifs need notification access.", "Media controls and notifications require notification access."))
            Action(t("Allow notification access", "Allow notification access")) { DotsService.openSettings(a) }
        }
    }
}

/** Gestures → the NoCap control center and the opt-in edge swipe from any app. */
@Composable
fun ControlCenterSettings(a: MainActivity) {
    val allowed = remember(a.resumeTick) { EdgeStrip.allowed(a) }
    LaunchedEffect(a.resumeTick, Opt.edgeSwipe.v) { EdgeStrip.sync(a) }
    Hint(
        t(
            "Swipe down on the home screen opens it: toggles, brightness, music, NoCap Link and your notifs. Want Android's own shade instead? Set Swipe down to it in Gestures.",
            "Swipe down on the home screen opens it, with quick toggles, brightness, media, NoCap Link and notifications. To use the system notification shade instead, change Swipe down above.",
        ),
    )
    Spacer(Modifier.height(4.dp))
    Label(t("From any app", "In other apps"))
    Toggle(
        t("Pull down from the top in any app", "Open from the top edge in any app"),
        t(
            "Adds an invisible strip just under the status bar (middle of the screen). Pull down on it to open the control center anywhere. Needs “Display over other apps”; it only sees touches on that strip, never your screen.",
            "Places an invisible strip below the status bar, in the middle of the screen; pulling down on it opens the control center. Requires “Display over other apps”. It only receives touches on the strip itself and can't see screen content.",
        ),
        Opt.edgeSwipe.v && allowed,
    ) { on ->
        Opt.edgeSwipe.set(on)
        if (on && !EdgeStrip.allowed(a)) runCatching { a.startActivity(EdgeStrip.permissionIntent(a)) }
        EdgeStrip.sync(a)
    }
}
