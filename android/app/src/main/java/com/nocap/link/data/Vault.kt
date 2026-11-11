package com.nocap.link.data

import android.content.Context
import android.content.SharedPreferences
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey

/** What this phone keeps about the NoCapOS server it's linked to. */
data class Link(
    val server: String, // scheme://host[:port], no trailing slash
    val token: String, // device token: "Authorization: Device <token>"
    val deviceId: String,
    val nocapId: String,
    val serverName: String,
    val username: String,
    val pin: String?, // SHA-256 (hex) of the server's leaf certificate; null for http
)

/**
 * Everything sensitive lives in EncryptedSharedPreferences (AES-256, key in
 * the Android Keystore). allowBackup is off, so none of it leaves the phone.
 */
class Vault private constructor(ctx: Context) {
    private val prefs: SharedPreferences = run {
        val key = MasterKey.Builder(ctx).setKeyScheme(MasterKey.KeyScheme.AES256_GCM).build()
        EncryptedSharedPreferences.create(
            ctx, "nocap_link_vault", key,
            EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
            EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM,
        )
    }

    var link: Link?
        get() {
            val server = prefs.getString("server", null) ?: return null
            val token = prefs.getString("token", null) ?: return null
            return Link(
                server = server,
                token = token,
                deviceId = prefs.getString("device_id", "") ?: "",
                nocapId = prefs.getString("nocap_id", "") ?: "",
                serverName = prefs.getString("server_name", "") ?: "",
                username = prefs.getString("username", "") ?: "",
                pin = prefs.getString("pin", null),
            )
        }
        set(v) {
            val e = prefs.edit()
            if (v == null) {
                listOf("server", "token", "device_id", "nocap_id", "server_name", "username", "pin", "unlinked").forEach(e::remove)
            } else {
                e.putString("server", v.server).putString("token", v.token).putString("device_id", v.deviceId)
                    .putString("nocap_id", v.nocapId).putString("server_name", v.serverName)
                    .putString("username", v.username).putString("pin", v.pin).putBoolean("unlinked", false)
            }
            e.apply()
        }

    /** Replace only the pinned certificate (after the person re-trusts it). */
    fun repin(pin: String?) {
        prefs.edit().putString("pin", pin).apply()
    }

    /** The server said this device's token is no good anymore. */
    var unlinked: Boolean
        get() = prefs.getBoolean("unlinked", false)
        set(v) = prefs.edit().putBoolean("unlinked", v).apply()

    /** The intro (permissions) screen was seen. */
    var introDone: Boolean
        get() = prefs.getBoolean("intro_done", false)
        set(v) = prefs.edit().putBoolean("intro_done", v).apply()

    /** Dock apps, as "package/activity" keys. Null = not chosen yet. */
    var dock: List<String>?
        get() = prefs.getString("dock", null)?.split('\n')?.filter { it.isNotBlank() }
        set(v) = prefs.edit().putString("dock", v?.joinToString("\n")).apply()

    companion object {
        @Volatile private var instance: Vault? = null
        fun get(ctx: Context): Vault = instance ?: synchronized(this) {
            instance ?: Vault(ctx.applicationContext).also { instance = it }
        }
    }
}
