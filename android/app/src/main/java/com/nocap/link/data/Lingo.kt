package com.nocap.link.data

import com.nocap.link.net.NoCapApi
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/**
 * Settings → Language: Gen Z (default), Standard, or "Same as my server",
 * which follows the Language setting saved on your NoCap desktop (read from
 * the device API's saved look, `GET /api/v1/device/prefs`). The last answer
 * is kept for offline use; until there is one it's Gen Z.
 */
object Lingo {
    const val GENZ = "genz"
    const val STANDARD = "standard"
    const val SERVER = "server"

    /** Whether the app talks Gen Z for a [choice] and the server's last known language. */
    fun isGenz(choice: String, server: String): Boolean = when (choice) {
        STANDARD -> false
        SERVER -> server != STANDARD
        else -> true
    }

    /** Takes the server's language: remembered when following the server, otherwise applied directly. */
    fun fromServer(lingo: String) {
        if (lingo != GENZ && lingo != STANDARD) return
        if (Opt.lingo.v == SERVER) Opt.serverLingo.set(lingo) else Opt.lingo.set(lingo)
    }

    /** Re-reads the server's language when following it (cheap; one small GET). */
    suspend fun refresh(api: NoCapApi) {
        if (Opt.lingo.v != SERVER) return
        val l = withContext(Dispatchers.IO) { runCatching { api.prefs().lingo }.getOrNull() } ?: return
        if (l == GENZ || l == STANDARD) Opt.serverLingo.set(l)
    }
}
