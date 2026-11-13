package com.nocap.link.net

import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.json.JSONObject
import java.io.IOException

/**
 * Setting up a brand-new NoCap server from the phone, with the same API
 * the server's own setup wizard uses: create the first administrator (no
 * token needed from the home network), then, signed in as them, link this
 * phone and save the name, time zone and look. Only over the pinned
 * certificate the person accepted. Blocking calls: run them off the main
 * thread.
 */
class SetupApi(val server: String, pin: String?) {
    init {
        require(!server.startsWith("https://") || pin != null) { "https needs a pinned certificate" }
    }

    private val http: OkHttpClient = Pins.client(pin)

    /** The administrator's session (an access token), once created. */
    @Volatile private var bearer: String? = null

    /** Kept in memory for the few minutes of setup, to sign in again if the session runs out. */
    @Volatile private var creds: Pair<String, String>? = null

    val signedIn get() = bearer != null

    /** Creates the first administrator and signs in as them. */
    fun createAdmin(username: String, password: String) {
        val o = JSONObject(call("POST", "/api/v1/auth/setup", JSONObject().put("username", username).put("password", password), auth = false))
        bearer = o.getString("access_token")
        creds = username to password
    }

    /** Links this phone to the new administrator: its first device. */
    fun linkSelf(deviceName: String): NoCapApi.Claimed {
        val body = JSONObject().put("device_name", deviceName).put("platform", "android")
            .put("model", deviceModel()).put("os_version", osVersion())
        val o = JSONObject(call("POST", "/api/v1/devices/link-self", body))
        return NoCapApi.Claimed(
            o.getString("device_id"), o.getString("token"), o.optString("nocap_id"),
            o.optString("server_name"), o.optJSONObject("user")?.optString("username") ?: "",
        )
    }

    fun setHostname(name: String): String =
        JSONObject(call("PUT", "/api/v1/system/hostname", JSONObject().put("hostname", name))).optString("hostname", name)

    fun setTimeZone(zone: String): String =
        JSONObject(call("PUT", "/api/v1/system/timezone", JSONObject().put("zone", zone))).optString("zone", zone)

    /** The administrator's NoCapOS look, as the desktop keeps it. */
    fun savePrefs(prefs: JSONObject) {
        call("PUT", "/api/v1/me/prefs", JSONObject().put("prefs", prefs))
    }

    /** The machine-wide setup is done (the desktop won't ask again). */
    fun machineDone() {
        call("POST", "/api/v1/setup/machine/done", JSONObject())
    }

    private fun signIn() {
        val (u, p) = creds ?: throw IOException("Not signed in.")
        val o = JSONObject(call("POST", "/api/v1/auth/login", JSONObject().put("username", u).put("password", p), auth = false))
        bearer = o.optString("access_token").ifBlank { throw IOException("Couldn't sign in again.") }
    }

    private fun call(method: String, path: String, body: JSONObject, auth: Boolean = true, again: Boolean = true): String {
        val b = Request.Builder().url(server + path).header("Accept", "application/json")
            .method(method, body.toString().toRequestBody(JSON))
        if (auth) b.header("Authorization", "Bearer " + (bearer ?: throw IOException("Not signed in.")))
        val (code, text) = try {
            http.newCall(b.build()).execute().use { r -> r.code to (r.body?.string() ?: "") }
        } catch (e: javax.net.ssl.SSLException) {
            val pin = generateSequence<Throwable>(e) { it.cause }.filterIsInstance<Pins.PinMismatch>().firstOrNull()
            if (pin != null) throw CertChanged(pin.seen)
            throw e
        }
        if (code in 200..299) return text
        if (code == 401 && auth && again && creds != null) {
            signIn()
            return call(method, path, body, auth, again = false)
        }
        val err = runCatching { JSONObject(text).getJSONObject("error") }.getOrNull()
        if (code == 404 && (err == null || err.optString("message") == "no such endpoint")) throw ServerTooOld(path)
        throw ApiError(code, err?.optString("code") ?: "http_$code", err?.optString("message")?.takeIf { it.isNotBlank() } ?: "Server said HTTP $code")
    }

    private companion object {
        val JSON = "application/json".toMediaType()
    }
}
