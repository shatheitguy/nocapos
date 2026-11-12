package com.nocap.link.settings

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
import com.nocap.link.MainActivity
import com.nocap.link.ServerState
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
