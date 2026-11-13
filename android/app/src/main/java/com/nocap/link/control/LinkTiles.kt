package com.nocap.link.control

import android.content.Context
import com.nocap.link.data.Vault
import com.nocap.link.data.t

/**
 * "Find my phone" for the control center's NoCap Link tiles. The feature
 * itself (ring this phone / see where it is from NoCap OS) belongs to NoCap
 * Link's live link, which plugs in here.
 */
interface FindMyPhone {
    /** Opens or starts Find my phone; false if it couldn't. */
    fun open(ctx: Context): Boolean
}

object LinkTiles {
    /** The tile stops the ringing when NoCap OS is ringing this phone, and otherwise plays a test ring. */
    @Volatile var findMyPhone: FindMyPhone? = object : FindMyPhone {
        override fun open(ctx: Context): Boolean {
            if (com.nocap.link.sync.FindPhone.ringing) com.nocap.link.sync.FindPhone.stop(ctx)
            else com.nocap.link.sync.FindPhone.ring(ctx)
            return true
        }
    }
}

/** The server's status as last seen (from the numbers the status widgets keep), for screens outside the launcher. */
object ServerGlance {
    data class Line(val text: String, val ok: Boolean?)

    /** [ok] null = unknown / not linked, false = stale, true = online recently. */
    fun read(ctx: Context): Line {
        val linked = runCatching { Vault.get(ctx).link != null }.getOrDefault(false)
        if (!linked) return Line(t("Not linked yet", "Not linked"), null)
        val sp = ctx.getSharedPreferences("nocap_widgets", Context.MODE_PRIVATE)
        val name = sp.getString("name", null)?.ifBlank { null } ?: "NoCapOS"
        val at = sp.getLong("at", 0)
        return describe(name, at, System.currentTimeMillis(), sp.getInt("cpu", 0), sp.getInt("updates", 0))
    }

    /** Pure, for tests: online if seen in the last 30 minutes. */
    fun describe(name: String, at: Long, now: Long, cpu: Int, updates: Int): Line = when {
        at == 0L -> Line(t("$name · checking in…", "$name · status unknown"), null)
        now - at > 30 * 60_000L -> Line(t("$name · last seen a while ago", "$name · not updated recently"), false)
        updates > 0 -> Line(t("$name · online · $updates glow ups waiting", "$name · online · $updates updates"), true)
        else -> Line(t("$name · online · CPU $cpu%", "$name · online · CPU $cpu%"), true)
    }
}
