package com.nocap.link.net

import com.nocap.link.Link
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.json.JSONArray
import org.json.JSONObject
import java.io.IOException
import javax.net.ssl.SSLException

data class PairInfo(val nocapId: String, val name: String, val version: String)

data class ServerApp(val id: String, val name: String, val iconUrl: String, val url: String)

data class Me(val deviceName: String, val username: String, val server: PairInfo)

/** The server answered with an error ({"error":{"code","message"}}). */
class ApiError(val status: Int, val code: String, message: String) : IOException(message)

/** The server's certificate isn't the pinned one anymore. */
class CertChanged(val seen: String) : IOException("certificate changed")

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

    /** Raw bytes from this server (app icons), or null. */
    fun bytes(url: String): ByteArray? {
        if (!sameServer(url)) return null
        return runCatching {
            http.newCall(Request.Builder().url(url).build()).execute().use { r -> if (r.isSuccessful) r.body?.bytes() else null }
        }.getOrNull()
    }

    fun sameServer(url: String): Boolean = url == server || url.startsWith("$server/")

    private fun info(o: JSONObject) = PairInfo(o.optString("nocap_id"), o.optString("name"), o.optString("version"))

    private fun call(method: String, path: String, body: JSONObject? = null): String {
        val b = Request.Builder().url(server + path).header("Accept", "application/json")
        if (token != null) b.header("Authorization", "Device $token")
        b.method(method, body?.toString()?.toRequestBody(JSON))
        try {
            http.newCall(b.build()).execute().use { r ->
                val text = r.body?.string() ?: ""
                if (r.isSuccessful) return text
                val err = runCatching { JSONObject(text).getJSONObject("error") }.getOrNull()
                throw ApiError(r.code, err?.optString("code") ?: "http_${r.code}", err?.optString("message")?.takeIf { it.isNotBlank() } ?: "Server said HTTP ${r.code}")
            }
        } catch (e: SSLException) {
            val pin = generateSequence<Throwable>(e) { it.cause }.filterIsInstance<Pins.PinMismatch>().firstOrNull()
            if (pin != null) throw CertChanged(pin.seen)
            throw e
        }
    }

    companion object {
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
