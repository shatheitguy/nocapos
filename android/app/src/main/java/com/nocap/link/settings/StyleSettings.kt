package com.nocap.link.settings

import androidx.compose.runtime.Composable
import androidx.lifecycle.lifecycleScope
import com.nocap.link.data.Lingo
import com.nocap.link.data.Opt
import com.nocap.link.data.t
import com.nocap.link.MainActivity
import com.nocap.link.ServerState
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
