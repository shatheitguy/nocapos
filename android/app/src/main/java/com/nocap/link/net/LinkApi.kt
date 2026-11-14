package com.nocap.link.net

import org.json.JSONArray
import org.json.JSONObject

// NoCap Link, always connected: the device API calls for live state,
// addresses, actions from NoCap OS and "Open on NoCap".

/** Something NoCap OS asked this phone to do. */
data class PhoneAction(
    val id: Long, val kind: String, val key: String, val text: String, val url: String,
    val root: String, val path: String, val name: String, val size: Long, val open: Boolean,
)

/** Where the server can be reached. */
data class Addresses(val nocapId: String, val lan: List<String>, val remote: String?)

/** A phone notification as NoCap OS shows it. */
data class PhoneNote(
    val key: String, val app: String, val pkg: String, val title: String, val text: String, val icon: String?, val posted: Long,
    val kind: String = "", val callState: String = "", val sender: String = "", val canReply: Boolean = false, val canDecline: Boolean = false,
)

fun NoCapApi.postNote(n: PhoneNote) {
    val b = JSONObject().put("key", n.key).put("app", n.app).put("package", n.pkg).put("title", n.title).put("text", n.text).put("posted", n.posted)
    if (!n.icon.isNullOrBlank()) b.put("icon", n.icon)
    if (n.kind.isNotEmpty()) b.put("kind", n.kind)
    if (n.callState.isNotEmpty()) b.put("call_state", n.callState)
    if (n.sender.isNotEmpty()) b.put("sender", n.sender)
    if (n.canReply) b.put("can_reply", true)
    if (n.canDecline) b.put("can_decline", true)
    call("POST", "/api/v1/device/phone-notifications", b)
}

/** Battery, network and route, for NoCap OS's devices panel. */
fun NoCapApi.postState(battery: Int?, charging: Boolean, network: String, route: String, ringing: Boolean) {
    val b = JSONObject().put("charging", charging).put("network", network).put("route", route).put("ringing", ringing)
    if (battery != null) b.put("battery", battery)
    call("POST", "/api/v1/device/state", b)
}

fun NoCapApi.addresses(): Addresses {
    val o = JSONObject(call("GET", "/api/v1/device/addresses"))
    val a = o.optJSONArray("lan") ?: JSONArray()
    return Addresses(o.optString("nocap_id"), (0 until a.length()).map { a.getString(it) }, o.optString("remote").takeIf { it.isNotBlank() })
}

/** "Open on NoCap": a web link, or a file already on the server. */
fun NoCapApi.openOnNoCap(url: String? = null, title: String? = null, root: String? = null, path: String? = null) {
    val b = JSONObject()
    if (url != null) b.put("url", url)
    if (!title.isNullOrBlank()) b.put("title", title)
    if (path != null) b.put("path", path)
    if (root != null) b.put("root", root)
    call("POST", "/api/v1/device/open", b)
}

/** The long-poll, with actions too. */
fun NoCapApi.liveEvents(after: Long, wait: Int = 25, images: Boolean = false): Pair<NoCapApi.Events, List<PhoneAction>> {
    val text = call("GET", "/api/v1/device/events?after=$after&wait=$wait" + (if (images) "&images=1" else ""), client = poll)
    return parseEvents(text)
}

/** Parses the long-poll's answer (separate so it can be tested). */
fun parseEvents(text: String): Pair<NoCapApi.Events, List<PhoneAction>> {
    val o = JSONObject(text)
    fun arr(k: String) = o.optJSONArray(k) ?: JSONArray()
    val na = arr("notices")
    val ca = arr("clipboard")
    val da = arr("dismissed")
    val aa = arr("actions")
    val notices = (0 until na.length()).map {
        val n = na.getJSONObject(it)
        val g = n.optJSONObject("genz")
        ServerNotice(
            n.optLong("id"), n.optString("level"), n.optString("title"), n.optString("body"),
            g?.optString("title")?.takeIf { s -> s.isNotBlank() }, g?.optString("body")?.takeIf { s -> s.isNotBlank() },
        )
    }
    val signins = (0 until arr("signins").length()).map {
        val s = arr("signins").getJSONObject(it)
        val g = s.optJSONObject("genz")
        SigninAlert(
            s.optLong("id"), s.optString("session_id"), s.optString("source"), s.optString("kind"), s.optString("device_name"),
            s.optString("browser"), s.optString("os"), s.optString("ip"), s.optString("network"), s.optString("at"),
            s.optString("title"), s.optString("body"),
            g?.optString("title")?.takeIf { x -> x.isNotBlank() }, g?.optString("body")?.takeIf { x -> x.isNotBlank() },
        )
    }
    val ev = NoCapApi.Events(
        o.optLong("cursor"), notices,
        (0 until ca.length()).map {
            val c = ca.getJSONObject(it)
            NoCapApi.Clip(c.optLong("id"), c.optString("text"), c.optString("from"), c.optString("kind").ifBlank { "text" }, c.optString("mime"))
        },
        (0 until da.length()).map { da.getString(it) },
        signins,
    )
    val actions = (0 until aa.length()).map {
        val a = aa.getJSONObject(it)
        PhoneAction(
            a.optLong("id"), a.optString("kind"), a.optString("key"), a.optString("text"), a.optString("url"),
            a.optString("root"), a.optString("path"), a.optString("name"), a.optLong("size"), a.optBoolean("open"),
        )
    }
    return ev to actions
}
