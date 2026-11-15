package com.nocap.link.shield

import android.Manifest
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.content.pm.ApplicationInfo
import android.content.pm.PackageManager
import android.net.VpnService
import android.service.quicksettings.TileService
import androidx.core.content.ContextCompat
import com.nocap.link.data.Link
import com.nocap.link.net.NoCapApi
import com.nocap.link.system.CrashLog
import org.json.JSONArray
import org.json.JSONObject

/** Turning Shield on and off from anywhere (page, tile, widget, boot). */
object Shield {
    /** Android needs your OK (the VPN prompt) before Shield can start. */
    fun needsConsent(ctx: Context): Boolean = VpnService.prepare(ctx) != null

    /** Starts Shield; false when Android's VPN prompt has to be shown first. */
    fun start(ctx: Context): Boolean {
        ShieldStore.init(ctx)
        if (needsConsent(ctx)) return false
        ShieldStore.saveEnabled(true)
        ShieldStore.serverNote = null
        val running = ShieldService.instance
        if (running != null) {
            running.reload()
            return true
        }
        return try {
            ContextCompat.startForegroundService(ctx, Intent(ctx, ShieldService::class.java).setAction(ShieldService.ACTION_START))
            true
        } catch (e: Exception) {
            CrashLog.warn("shield start", e)
            ShieldStore.saveEnabled(false)
            false
        }
    }

    fun stop(ctx: Context) {
        ShieldStore.init(ctx)
        ShieldStore.saveEnabled(false)
        ShieldService.instance?.stopShield(userTurnedOff = true)
        changed(ctx)
    }

    /** Settings changed: the running service works out its plan again. */
    fun reload() {
        ShieldService.instance?.reload()
    }

    /** Tells the tile and the widget to redraw. */
    fun changed(ctx: Context) {
        runCatching { TileService.requestListeningState(ctx, ComponentName(ctx, ShieldTile::class.java)) }
        ShieldWidget.update(ctx)
    }

    /** Apps that can use the internet (what the firewall lists), without NoCap OS itself. */
    fun installedApps(ctx: Context): Set<String> = apps(ctx).map { it.pkg }.toSet()

    data class App(val pkg: String, val label: String, val system: Boolean)

    fun apps(ctx: Context): List<App> {
        val pm = ctx.packageManager
        val list = runCatching { pm.getInstalledApplications(0) }.getOrDefault(emptyList())
        return list.asSequence()
            .filter { it.packageName != ctx.packageName }
            .filter { pm.checkPermission(Manifest.permission.INTERNET, it.packageName) == PackageManager.PERMISSION_GRANTED }
            .map { info ->
                val system = info.flags and (ApplicationInfo.FLAG_SYSTEM or ApplicationInfo.FLAG_UPDATED_SYSTEM_APP) != 0
                App(info.packageName, runCatching { pm.getApplicationLabel(info).toString() }.getOrDefault(info.packageName), system)
            }
            .sortedBy { it.label.lowercase() }
            .toList()
    }

    /** Android's VPN settings (Always-on VPN lives there). */
    fun vpnSettings(ctx: Context) {
        runCatching { ctx.startActivity(Intent("android.net.vpn.SETTINGS").addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
            .recoverCatching { ctx.startActivity(Intent(android.provider.Settings.ACTION_WIRELESS_SETTINGS).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
    }
}

internal fun installedApps(ctx: Context): Set<String> = Shield.installedApps(ctx)

/** Your own allow and block lists on the server (this phone's, and all your devices'). */
data class ServerLists(val allow: List<String>, val block: List<String>)

data class ServerShield(
    val available: Boolean,
    val device: ServerLists,
    val user: ServerLists,
    val blockedToday: Int,
)

/** The server side of Shield on the phone (device token). */
class ShieldApi(private val api: NoCapApi) {
    constructor(link: Link) : this(NoCapApi(link))

    fun get(): ServerShield = parse(JSONObject(api.call("GET", "/api/v1/device/shield")))

    /** action: allow | block | clear; scope: device | user. */
    fun rule(domain: String, action: String, scope: String): ServerShield =
        parse(JSONObject(api.call("POST", "/api/v1/device/shield/rules", JSONObject().put("domain", domain).put("action", action).put("scope", scope))))

    companion object {
        private fun lists(o: JSONObject?): ServerLists = ServerLists(strings(o?.optJSONArray("allow")), strings(o?.optJSONArray("block")))

        private fun strings(a: JSONArray?): List<String> = if (a == null) emptyList() else (0 until a.length()).map { a.optString(it) }.filter { it.isNotBlank() }

        fun parse(o: JSONObject) = ServerShield(o.optBoolean("available"), lists(o.optJSONObject("device")), lists(o.optJSONObject("user")), o.optInt("blocked_today"))
    }
}

/** What to call a domain someone typed ("https://Ads.Example.com/x" → "ads.example.com"); null if it isn't one. */
fun cleanDomain(input: String): String? {
    var s = input.trim().lowercase()
    s = s.substringAfter("://").substringBefore('/').substringBefore('?').substringBefore('#')
    s = s.substringAfterLast('@').substringBefore(':').trimEnd('.')
    if (s.startsWith("*.")) s = s.drop(2)
    if (s.length !in 3..253 || '.' !in s) return null
    return s.takeIf { d -> d.split('.').all { l -> l.length in 1..63 && l.first() != '-' && l.last() != '-' && l.all { it in 'a'..'z' || it in '0'..'9' || it == '-' || it == '_' } } }
}

/** Starts Shield after a reboot when it was on (Android's Always-on VPN does it by itself). */
class ShieldBootReceiver : android.content.BroadcastReceiver() {
    override fun onReceive(ctx: Context, intent: Intent) {
        if (intent.action != Intent.ACTION_BOOT_COMPLETED && intent.action != Intent.ACTION_MY_PACKAGE_REPLACED) return
        ShieldStore.init(ctx)
        if (ShieldStore.enabled && ShieldService.instance == null) Shield.start(ctx)
    }
}
