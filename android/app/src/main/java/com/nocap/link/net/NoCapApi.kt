package com.nocap.link.net

import com.nocap.link.Link
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.MediaType
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.MultipartBody
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import okio.BufferedSink
import okio.source
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.io.IOException
import java.io.InputStream
import java.net.URLEncoder
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLException

data class PairInfo(val nocapId: String, val name: String, val version: String)

data class ServerApp(val id: String, val name: String, val iconUrl: String, val url: String)

data class Me(val deviceName: String, val username: String, val server: PairInfo)

data class Usage(val total: Long, val used: Long, val percent: Double)

data class Summary(
    val ready: Boolean, val uptime: Double, val cpuPercent: Double, val cores: Int,
    val memory: Usage, val storage: Usage, val server: PairInfo,
    /** Bytes/s down and up (null on servers that don't report it). */
    val net: Pair<Double, Double>? = null,
    val updates: Int = 0,
)

data class ServerPrefs(val wallpaper: String, val lingo: String, val accent: String)

data class Root(val id: String, val name: String, val kind: String, val readOnly: Boolean)

data class FileEntry(val root: String, val path: String, val name: String, val dir: Boolean, val size: Long, val modified: String)

data class ServerNotice(val id: Long, val level: String, val title: String, val body: String, val genzTitle: String?, val genzBody: String?)

data class Conversation(val id: String, val title: String, val preview: String, val updatedAt: String)

data class ChatMessage(val role: String, val content: String, val error: String = "")

/** The server answered with an error ({"error":{"code","message"}}). */
class ApiError(val status: Int, val code: String, message: String) : IOException(message)

/** The server's certificate isn't the pinned one anymore. */
class CertChanged(val seen: String) : IOException("certificate changed")

/**
 * The server doesn't know this endpoint: it runs an older NoCapOS. The app
 * shows "your server needs an update" instead of a raw error.
 */
class ServerTooOld(val path: String) : IOException("This needs a newer NoCapOS on your server.")

/** What probing an address found: who's there and which certificate. */
data class Probe(val server: String, val info: PairInfo, val pin: String?)

private val JSON = "application/json".toMediaType()

/**
 * The NoCapOS device API. Blocking calls: run them off the main thread.
 */
class NoCapApi(private val server: String, pin: String?, private val token: String? = null) {
    init {
        require(!server.startsWith("https://") || pin != null) { "https needs a pinned certificate" }
    }

    val http: OkHttpClient = Pins.client(pin)

    /** For uploads and AI replies, which can take a while. */
    private val slow: OkHttpClient by lazy {
        http.newBuilder().readTimeout(5, TimeUnit.MINUTES).writeTimeout(10, TimeUnit.MINUTES).build()
    }

    constructor(link: Link) : this(link.server, link.pin, link.token)

    fun pairInfo(): PairInfo = info(JSONObject(call("GET", "/api/v1/pair/info")))

    data class Claimed(val deviceId: String, val token: String, val nocapId: String, val serverName: String, val username: String)

    fun claim(code: String, deviceName: String): Claimed {
        val body = JSONObject().put("code", code).put("device_name", deviceName).put("platform", "android")
        val o = JSONObject(call("POST", "/api/v1/pair/claim", body))
        return Claimed(
            o.getString("device_id"), o.getString("token"), o.optString("nocap_id"),
            o.optString("server_name"), o.optJSONObject("user")?.optString("username") ?: "",
        )
    }

    fun me(): Me {
        val o = JSONObject(call("GET", "/api/v1/device/me"))
        return Me(
            o.getJSONObject("device").optString("name"),
            o.getJSONObject("user").optString("username"),
            info(o.getJSONObject("server")),
        )
    }

    fun apps(): List<ServerApp> {
        val a = JSONArray(call("GET", "/api/v1/device/apps"))
        return (0 until a.length()).map {
            val o = a.getJSONObject(it)
            ServerApp(o.optString("id"), o.optString("name"), o.optString("icon_url"), o.optString("url"))
        }
    }

    fun heartbeat(battery: Int?, appVersion: String) {
        val body = JSONObject().put("app_version", appVersion)
        if (battery != null) body.put("battery", battery)
        call("POST", "/api/v1/device/heartbeat", body)
    }

    /** A one-time, 60-second address that opens the web desktop signed in. */
    fun webSession(): String = JSONObject(call("POST", "/api/v1/device/session", JSONObject())).getString("url")

    // ---------- v2: widgets, look, files, send, backup, notices, AI ----------

    fun summary(): Summary {
        val o = JSONObject(call("GET", "/api/v1/device/summary"))
        fun usage(k: String) = o.optJSONObject(k).let { u -> Usage(u?.optLong("total") ?: 0, u?.optLong("used") ?: 0, u?.optDouble("percent", 0.0) ?: 0.0) }
        val cpu = o.optJSONObject("cpu")
        return Summary(
            o.optBoolean("ready"), o.optDouble("uptime", 0.0), cpu?.optDouble("percent", 0.0) ?: 0.0, cpu?.optInt("cores") ?: 0,
            usage("memory"), usage("storage"), info(o.optJSONObject("server") ?: JSONObject()),
            net = o.optJSONObject("network")?.let { n -> n.optDouble("rx_rate", 0.0) to n.optDouble("tx_rate", 0.0) },
            updates = o.optInt("updates", 0),
        )
    }

    fun prefs(): ServerPrefs {
        val o = JSONObject(call("GET", "/api/v1/device/prefs"))
        return ServerPrefs(o.optString("wallpaper"), o.optString("lingo"), o.optString("accent"))
    }

    fun roots(): List<Root> {
        val a = JSONArray(call("GET", "/api/v1/device/files/roots"))
        return (0 until a.length()).map {
            val o = a.getJSONObject(it)
            Root(o.optString("id"), o.optString("name"), o.optString("kind"), o.optBoolean("read_only"))
        }
    }

    fun list(root: String, path: String): List<FileEntry> {
        val o = JSONObject(call("GET", "/api/v1/device/files/list?root=${enc(root)}&path=${enc(path)}"))
        val a = o.optJSONArray("entries") ?: JSONArray()
        val dir = o.optString("path", path).trimEnd('/')
        return (0 until a.length()).map {
            val e = a.getJSONObject(it)
            FileEntry(root, "$dir/${e.optString("name")}", e.optString("name"), e.optBoolean("dir"), e.optLong("size"), e.optString("mod_time"))
        }.sortedWith(compareBy({ !it.dir }, { it.name.lowercase() }))
    }

    fun recent(limit: Int = 8): List<FileEntry> = hits(JSONObject(call("GET", "/api/v1/device/files/recent?limit=$limit")))

    fun search(q: String, limit: Int = 12): List<FileEntry> = hits(JSONObject(call("GET", "/api/v1/device/files/search?q=${enc(q)}&limit=$limit")))

    private fun hits(o: JSONObject): List<FileEntry> {
        val a = o.optJSONArray("results") ?: JSONArray()
        return (0 until a.length()).map {
            val e = a.getJSONObject(it)
            FileEntry(e.optString("root"), e.optString("path"), e.optString("name"), e.optBoolean("dir"), e.optLong("size"), e.optString("mod_time"))
        }
    }

    /** Downloads one file (through a short-lived ticket) into [dest]. */
    fun download(f: FileEntry, dest: File) {
        val t = JSONObject(call("POST", "/api/v1/device/files/ticket", JSONObject().put("root", f.root).put("path", f.path))).getString("ticket")
        val url = "$server/api/v1/files/raw?root=${enc(f.root)}&path=${enc(f.path)}&t=${enc(t)}&download=1"
        slow.newCall(Request.Builder().url(url).build()).execute().use { r ->
            if (!r.isSuccessful) throw ApiError(r.code, "http_${r.code}", "Couldn't download ${f.name} (HTTP ${r.code}).")
            dest.parentFile?.mkdirs()
            r.body!!.byteStream().use { input -> dest.outputStream().use { input.copyTo(it) } }
        }
    }

    /** One file for "Send to NoCap". */
    class Upload(val name: String, val mime: String?, val size: Long, val open: () -> InputStream)

    /** Sends files into NoCap Link in your home folder; returns where they landed. */
    fun send(files: List<Upload>): List<String> {
        val mb = MultipartBody.Builder().setType(MultipartBody.FORM)
        for (f in files) mb.addFormDataPart("file", f.name, StreamBody(f.mime?.toMediaTypeOrNull(), f.size, f.open))
        return sendResult(call("POST", "/api/v1/device/send", raw = mb.build(), client = slow))
    }

    fun sendText(text: String, title: String?): List<String> {
        val b = JSONObject().put("text", text)
        if (!title.isNullOrBlank()) b.put("title", title)
        return sendResult(call("POST", "/api/v1/device/send", b))
    }

    private fun sendResult(text: String): List<String> {
        val a = JSONObject(text).optJSONArray("paths") ?: JSONArray()
        return (0 until a.length()).map { a.getString(it) }
    }

    /** The home-screen backup on the server, or null when there's none. */
    fun layout(): JSONObject? = JSONObject(call("GET", "/api/v1/device/layout")).optJSONObject("layout")

    fun saveLayout(layout: JSONObject) {
        call("PUT", "/api/v1/device/layout", JSONObject().put("layout", layout))
    }

    fun notices(after: Long): Pair<Boolean, List<ServerNotice>> {
        val o = JSONObject(call("GET", "/api/v1/device/notifications?after=$after&limit=20"))
        val a = o.optJSONArray("items") ?: JSONArray()
        return o.optBoolean("admin") to (0 until a.length()).map {
            val n = a.getJSONObject(it)
            val g = n.optJSONObject("genz")
            ServerNotice(
                n.optLong("id"), n.optString("level"), n.optString("title"), n.optString("body"),
                g?.optString("title")?.takeIf { s -> s.isNotBlank() }, g?.optString("body")?.takeIf { s -> s.isNotBlank() },
            )
        }
    }

    /** Unlink this phone on the server too. */
    fun unlink() {
        call("DELETE", "/api/v1/device/me")
    }

    fun conversations(): List<Conversation> {
        val a = JSONArray(call("GET", "/api/v1/device/ai/conversations"))
        return (0 until a.length()).map {
            val o = a.getJSONObject(it)
            Conversation(o.optString("id"), o.optString("title"), o.optString("preview"), o.optString("updated_at"))
        }
    }

    fun newConversation(): Conversation {
        val o = JSONObject(call("POST", "/api/v1/device/ai/conversations", JSONObject()))
        return Conversation(o.optString("id"), o.optString("title"), "", o.optString("updated_at"))
    }

    fun conversation(id: String): List<ChatMessage> {
        val o = JSONObject(call("GET", "/api/v1/device/ai/conversations/${enc(id)}"))
        val a = o.optJSONArray("messages") ?: JSONArray()
        return (0 until a.length()).map {
            val m = a.getJSONObject(it)
            ChatMessage(m.optString("role"), m.optString("content"), m.optString("error"))
        }.filter { it.role == "user" || it.role == "assistant" }
    }

    fun deleteConversation(id: String) {
        call("DELETE", "/api/v1/device/ai/conversations/${enc(id)}")
    }

    /**
     * Sends a message and streams the reply: [onDelta] gets each chunk.
     * Returns the full reply; throws with the server's message on failure.
     */
    fun chat(id: String, text: String, onDelta: (String) -> Unit): String {
        val req = request("POST", "/api/v1/device/ai/conversations/${enc(id)}/send", JSONObject().put("text", text).toString().toRequestBody(JSON))
        val sb = StringBuilder()
        guarded {
            slow.newCall(req).execute().use { r ->
                if (!r.isSuccessful) fail(r.code, r.body?.string() ?: "", "/ai/send")
                val src = r.body!!.source()
                while (true) {
                    val line = src.readUtf8Line() ?: break
                    if (line.isBlank()) continue
                    val o = runCatching { JSONObject(line) }.getOrNull() ?: continue
                    when {
                        o.has("delta") -> { val d = o.optString("delta"); sb.append(d); onDelta(d) }
                        o.has("error") -> throw ApiError(500, "ai_error", o.optString("error"))
                        o.optBoolean("done") -> {
                            val m = o.optJSONObject("message")
                            val err = m?.optString("error").orEmpty()
                            if (sb.isEmpty() && err.isNotBlank()) throw ApiError(500, "ai_error", err)
                            if (sb.isEmpty()) sb.append(m?.optString("content").orEmpty())
                        }
                    }
                }
            }
        }
        return sb.toString()
    }

    // ---------- v3: NoCap Link sync ----------

    /** A notification from this phone, shown in the web OS notification center. */
    fun postPhoneNotification(key: String, app: String, pkg: String, title: String, text: String, iconB64: String?, posted: Long) {
        val b = JSONObject().put("key", key).put("app", app).put("package", pkg).put("title", title).put("text", text).put("posted", posted)
        if (!iconB64.isNullOrBlank()) b.put("icon", iconB64)
        call("POST", "/api/v1/device/phone-notifications", b)
    }

    fun deletePhoneNotification(key: String) {
        call("DELETE", "/api/v1/device/phone-notifications/${enc(key)}")
    }

    /** Puts text on your NoCap OS shared clipboard. */
    fun postClip(text: String): Long = JSONObject(call("POST", "/api/v1/device/clipboard", JSONObject().put("text", text))).optLong("id")

    data class Clip(val id: Long, val text: String, val from: String)
    data class Events(val cursor: Long, val notices: List<ServerNotice>, val clips: List<Clip>, val dismissed: List<String>)

    private val poll: OkHttpClient by lazy { http.newBuilder().readTimeout(45, TimeUnit.SECONDS).build() }

    /** Waits (up to [wait] s) for anything new: server notices, clipboard from the web, dismissals. */
    fun events(after: Long, wait: Int = 25): Events {
        val o = JSONObject(call("GET", "/api/v1/device/events?after=$after&wait=$wait", client = poll))
        val na = o.optJSONArray("notices") ?: JSONArray()
        val ca = o.optJSONArray("clipboard") ?: JSONArray()
        val da = o.optJSONArray("dismissed") ?: JSONArray()
        return Events(
            o.optLong("cursor"),
            (0 until na.length()).map { notice(na.getJSONObject(it)) },
            (0 until ca.length()).map { val c = ca.getJSONObject(it); Clip(c.optLong("id"), c.optString("text"), c.optString("from")) },
            (0 until da.length()).map { da.getString(it) },
        )
    }

    private fun notice(n: JSONObject): ServerNotice {
        val g = n.optJSONObject("genz")
        return ServerNotice(
            n.optLong("id"), n.optString("level"), n.optString("title"), n.optString("body"),
            g?.optString("title")?.takeIf { s -> s.isNotBlank() }, g?.optString("body")?.takeIf { s -> s.isNotBlank() },
        )
    }

    /** Raw bytes from this server (app icons), or null. */
    fun bytes(url: String): ByteArray? {
        if (!sameServer(url)) return null
        return runCatching {
            http.newCall(Request.Builder().url(url).build()).execute().use { r -> if (r.isSuccessful) r.body?.bytes() else null }
        }.getOrNull()
    }

    fun sameServer(url: String): Boolean = url == server || url.startsWith("$server/")

    private fun info(o: JSONObject) = PairInfo(o.optString("nocap_id"), o.optString("name"), o.optString("version"))

    private fun request(method: String, path: String, body: RequestBody?): Request {
        val b = Request.Builder().url(server + path).header("Accept", "application/json")
        if (token != null) b.header("Authorization", "Device $token")
        b.method(method, body ?: if (method == "POST" || method == "PUT") ByteArray(0).toRequestBody(null) else null)
        return b.build()
    }

    private fun call(method: String, path: String, body: JSONObject? = null, raw: RequestBody? = null, client: OkHttpClient = http): String {
        val req = request(method, path, raw ?: body?.toString()?.toRequestBody(JSON))
        return guarded {
            client.newCall(req).execute().use { r ->
                val text = r.body?.string() ?: ""
                if (r.isSuccessful) return@use text
                fail(r.code, text, path)
            }
        }
    }

    private fun <T> guarded(block: () -> T): T {
        try {
            return block()
        } catch (e: SSLException) {
            val pin = generateSequence<Throwable>(e) { it.cause }.filterIsInstance<Pins.PinMismatch>().firstOrNull()
            if (pin != null) throw CertChanged(pin.seen)
            throw e
        }
    }

    private fun fail(code: Int, text: String, path: String): Nothing {
        val err = runCatching { JSONObject(text).getJSONObject("error") }.getOrNull()
        // An older NoCapOS: its catch-all says "no such endpoint", and Go's
        // router answers a known path with the wrong method with plain 405.
        if ((code == 404 && (err == null || err.optString("message") == "no such endpoint")) || (code == 405 && err == null)) {
            throw ServerTooOld(path)
        }
        throw ApiError(code, err?.optString("code") ?: "http_$code", err?.optString("message")?.takeIf { it.isNotBlank() } ?: "Server said HTTP $code")
    }

    private class StreamBody(private val type: MediaType?, private val size: Long, private val open: () -> InputStream) : RequestBody() {
        override fun contentType() = type ?: "application/octet-stream".toMediaType()
        override fun contentLength() = size
        override fun writeTo(sink: BufferedSink) {
            open().source().use { sink.writeAll(it) }
        }
    }

    companion object {
        private fun enc(s: String) = URLEncoder.encode(s, "UTF-8").replace("+", "%20")

        /**
         * Turns what someone typed ("192.168.1.20:8443", "nocap.local",
         * "https://…/") into candidate origins: https first, then http.
         */
        fun candidates(typed: String): List<String> {
            val t = typed.trim().trimEnd('/')
            if (t.isEmpty()) return emptyList()
            if (t.startsWith("https://", true) || t.startsWith("http://", true)) return listOfNotNull(origin(t))
            return listOfNotNull(origin("https://$t"), origin("http://$t"))
        }

        /** scheme://host[:port] of a URL, or null when it isn't one. */
        fun origin(url: String): String? {
            val u = url.toHttpUrlOrNull() ?: return null
            val default = (u.scheme == "https" && u.port == 443) || (u.scheme == "http" && u.port == 80)
            val host = if (u.host.contains(':')) "[${u.host}]" else u.host
            return u.scheme + "://" + host + if (default) "" else ":${u.port}"
        }

        /**
         * Finds the NoCapOS server at an address. Any certificate is
         * accepted here, but only to read the public /pair/info and to
         * show the certificate to the person before anything secret is
         * sent.
         */
        fun probe(typed: String): Probe {
            var last: Exception = IOException("Type your NoCapOS address, like 192.168.1.20:8443")
            for (origin in candidates(typed)) {
                try {
                    val trust = Pins.PinTrust(null)
                    val client = Pins.client(null, trust)
                    val text = client.newCall(Request.Builder().url("$origin/api/v1/pair/info").header("Accept", "application/json").build())
                        .execute().use { r ->
                            if (!r.isSuccessful) throw IOException("That address answered, but it's not a NoCapOS server (HTTP ${r.code}).")
                            r.body?.string() ?: ""
                        }
                    val o = runCatching { JSONObject(text) }.getOrNull()
                        ?: throw IOException("That address answered, but it's not a NoCapOS server.")
                    val info = PairInfo(o.optString("nocap_id"), o.optString("name"), o.optString("version"))
                    if (!info.nocapId.startsWith("NC-")) throw IOException("That address answered, but it's not a NoCapOS server.")
                    val pin = if (origin.startsWith("https://")) trust.seen ?: throw IOException("No certificate from the server.") else null
                    return Probe(origin, info, pin)
                } catch (e: Exception) {
                    last = e
                }
            }
            throw last
        }
    }
}
