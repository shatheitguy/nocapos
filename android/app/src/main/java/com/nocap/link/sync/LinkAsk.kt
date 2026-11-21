package com.nocap.link.sync

import com.nocap.link.net.NoCapApi
import org.json.JSONObject

/** A link request this phone sent: the server shows the same [check] digits to whoever accepts. */
data class LinkAsked(
    val id: String,
    val secret: String,
    val check: String,
    val nocapId: String,
    val serverName: String,
)

/** Where a link request stands, and the device token once it's accepted. */
data class AskState(val state: String, val check: String, val claimed: NoCapApi.Claimed?) {
    val pending get() = state == "pending"
    val accepted get() = state == "accepted" && claimed != null
    val declined get() = state == "declined"
}

/**
 * "Ask to link this phone": the phone finds a server, checks its
 * certificate, and asks to link with no code. The server's administrators
 * (or the person whose account it names) accept or decline on NoCap OS;
 * the phone checks back every couple of seconds. Plain functions here so
 * they're tested on their own; the calls are in NoCapApi.
 */
object LinkAsk {
    /** How often the phone checks on its request. */
    const val POLL_MS = 2000L

    /** Requests last ten minutes on the server. */
    const val TTL_MS = 10 * 60 * 1000L

    fun asked(o: JSONObject): LinkAsked {
        val id = o.optString("request_id")
        val secret = o.optString("secret")
        require(id.isNotBlank() && secret.length >= 32) { "That server didn't take the request." }
        return LinkAsked(id, secret, o.optString("check").filter(Char::isDigit).take(4), o.optString("nocap_id"), o.optString("server_name"))
    }

    fun state(o: JSONObject): AskState {
        val d = o.optJSONObject("device")
        val claimed = d?.let {
            val token = it.optString("token")
            if (token.isBlank()) null else NoCapApi.Claimed(
                it.optString("device_id"), token, it.optString("nocap_id"), it.optString("server_name"),
                it.optJSONObject("user")?.optString("username").orEmpty(),
            )
        }
        val st = o.optString("state").takeIf { it == "pending" || it == "accepted" || it == "declined" } ?: "pending"
        return AskState(st, o.optString("check"), claimed)
    }

    enum class Status { LINKED, NEW, READY, BLUETOOTH_ONLY }

    /** What a found server is to this phone. */
    fun status(s: FoundServer, linkedId: String?): Status = when {
        !linkedId.isNullOrBlank() && s.nocapId.equals(linkedId, ignoreCase = true) -> Status.LINKED
        s.origin == null -> Status.BLUETOOTH_ONLY
        s.isNew -> Status.NEW
        else -> Status.READY
    }

    /** Asking works over https only (the certificate gets pinned). */
    fun canAsk(s: FoundServer, linkedId: String?): Boolean =
        status(s, linkedId) == Status.READY && s.origin?.startsWith("https://") == true

    /** A stable number from a string (FNV-1a), the same as the web radar. */
    fun hashOf(s: String): Long {
        var h = 0x811c9dc5L
        for (c in s) {
            h = h xor c.code.toLong()
            h = (h * 0x01000193L) and 0xffffffffL
        }
        return h
    }

    /**
     * Where a server's blip sits on the radar, as fractions of its size:
     * always the same spot for the same server (the web radar puts it in
     * the same place).
     */
    fun blipSpot(id: String, index: Int): Pair<Float, Float> {
        val h = hashOf(id)
        val angle = Math.toRadians((h % 360).toDouble())
        val r = 20 + ((h ushr 9) % 18) + (index % 3) * 4 // percent from the centre
        fun round(v: Double) = Math.round(v * 10) / 10.0
        return Pair((round(50 + r * Math.cos(angle)) / 100).toFloat(), (round(50 + r * Math.sin(angle)) / 100).toFloat())
    }
}
