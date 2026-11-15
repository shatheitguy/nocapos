package com.nocap.link.shield

import android.content.Context
import android.content.SharedPreferences
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import java.io.File
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import org.json.JSONArray
import org.json.JSONObject

/** A name Shield blocked (local log). */
data class BlockedName(val at: Long, val name: String, val app: String?, val bySelf: Boolean)

/** A blocked app trying to connect (local log). */
data class Attempt(val at: Long, val app: String, val dest: String)

/**
 * Shield's settings and its logs. Everything stays on this phone (the
 * logs are in the app's private files, which Android never backs up for
 * NoCap OS). The only thing that ever leaves is a DNS question to your own
 * server.
 */
object ShieldStore {
    private var sp: SharedPreferences? = null
    private var dir: File? = null

    // Settings, as Compose state so the Shield page redraws on change.
    var enabled by mutableStateOf(false); private set
    var dns by mutableStateOf(true); private set
    var firewall by mutableStateOf(false); private set
    var hardBlock by mutableStateOf(false); private set
    var fallback by mutableStateOf(Fallback.OPEN); private set
    var trusted by mutableStateOf<Set<String>>(emptySet()); private set
    var rules by mutableStateOf<Map<String, AppRule>>(emptyMap()); private set
    var blockNew by mutableStateOf(false); private set
    var known: Set<String> = emptySet(); private set

    // Live state from the service.
    var running by mutableStateOf(false)
    var mode by mutableStateOf(Mode.OFF)
    var serverUp by mutableStateOf(true)
    var serverNote by mutableStateOf<String?>(null)
    var net by mutableStateOf(Net(NetKind.NONE))
    var dnsPaused by mutableStateOf(false)
    /** Bumps when the logs change (at most every second). */
    var logTick by mutableIntStateOf(0)

    private val lock = Any()
    private val names = ArrayDeque<BlockedName>()
    private val attempts = ArrayDeque<Attempt>()
    private var day = ""
    private var blockedToday = 0
    private var askedToday = 0
    private var dirty = false
    private var lastTick = 0L

    const val MAX_LOG = 200

    fun init(ctx: Context) {
        if (sp != null) return
        synchronized(this) {
            if (sp != null) return
            val p = ctx.applicationContext.getSharedPreferences("nocap_shield", Context.MODE_PRIVATE)
            dir = File(ctx.applicationContext.filesDir, "shield").apply { mkdirs() }
            enabled = p.getBoolean("enabled", false)
            dns = p.getBoolean("dns", true)
            firewall = p.getBoolean("firewall", false)
            hardBlock = p.getBoolean("hard_block", false)
            fallback = runCatching { Fallback.valueOf(p.getString("fallback", null) ?: "OPEN") }.getOrDefault(Fallback.OPEN)
            trusted = p.getStringSet("trusted", emptySet())?.toSet() ?: emptySet()
            rules = decodeRules(p.getString("rules", null))
            blockNew = p.getBoolean("block_new", false)
            known = p.getStringSet("known", emptySet())?.toSet() ?: emptySet()
            sp = p
            loadLogs()
        }
    }

    fun saveEnabled(v: Boolean) { enabled = v; sp?.edit()?.putBoolean("enabled", v)?.apply() }
    fun saveDns(v: Boolean) { dns = v; sp?.edit()?.putBoolean("dns", v)?.apply() }
    fun saveFirewall(v: Boolean) { firewall = v; sp?.edit()?.putBoolean("firewall", v)?.apply() }
    fun saveHardBlock(v: Boolean) { hardBlock = v; sp?.edit()?.putBoolean("hard_block", v)?.apply() }
    fun saveFallback(v: Fallback) { fallback = v; sp?.edit()?.putString("fallback", v.name)?.apply() }

    fun trust(ssid: String, on: Boolean) {
        trusted = if (on) trusted + ssid else trusted - ssid
        sp?.edit()?.putStringSet("trusted", trusted)?.apply()
    }

    fun setRule(pkg: String, rule: AppRule) {
        rules = if (rule == AppRule.ALLOW && !blockNew) rules - pkg else rules + (pkg to rule)
        sp?.edit()?.putString("rules", encodeRules(rules))?.apply()
    }

    /** Turns "block new apps" on or off; what's installed now counts as known. */
    fun setBlockNew(v: Boolean, installed: Set<String>) {
        blockNew = v
        known = installed
        sp?.edit()?.putBoolean("block_new", v)?.putStringSet("known", installed)?.apply()
    }

    /** Gives apps installed since last time their default rule. True when something changed. */
    fun adoptNewApps(installed: Set<String>): Boolean {
        if (!blockNew) return false
        val fresh = ShieldRules.newApps(installed, known, true)
        val gone = known - installed
        if (fresh.isEmpty() && gone.isEmpty()) return false
        known = installed
        rules = rules - gone + fresh
        sp?.edit()?.putStringSet("known", known)?.putString("rules", encodeRules(rules))?.apply()
        return fresh.isNotEmpty()
    }

    fun config(knowsApps: Boolean, lockdown: Boolean) = ShieldConfig(dns, firewall, hardBlock, trusted, rules, knowsApps, lockdown)

    // ---------- local logs ----------

    private fun today(now: Long): String = SimpleDateFormat("yyyy-MM-dd", Locale.US).format(Date(now))

    private fun rollDay(now: Long) {
        val d = today(now)
        if (d != day) {
            day = d
            blockedToday = 0
            askedToday = 0
        }
    }

    /** One question answered (counted for "blocked today"). */
    fun asked(name: String, blocked: Boolean, app: String?, bySelf: Boolean, now: Long = System.currentTimeMillis()) {
        synchronized(lock) {
            rollDay(now)
            askedToday++
            if (blocked) {
                blockedToday++
                names.addFirst(BlockedName(now, name, app, bySelf))
                while (names.size > MAX_LOG) names.removeLast()
            }
            dirty = true
        }
        if (blocked) tick(now)
    }

    fun attempt(app: String, dest: String, now: Long = System.currentTimeMillis()) {
        synchronized(lock) {
            val last = attempts.firstOrNull { it.app == app && it.dest == dest }
            if (last != null && now - last.at < 60_000) return // once a minute is enough
            attempts.addFirst(Attempt(now, app, dest))
            while (attempts.size > MAX_LOG) attempts.removeLast()
            dirty = true
        }
        tick(now)
    }

    private fun tick(now: Long) {
        if (now - lastTick >= 1000) {
            lastTick = now
            logTick++
        }
    }

    fun blockedToday(now: Long = System.currentTimeMillis()): Int = synchronized(lock) { rollDay(now); blockedToday }
    fun askedToday(now: Long = System.currentTimeMillis()): Int = synchronized(lock) { rollDay(now); askedToday }
    fun recentNames(): List<BlockedName> = synchronized(lock) { names.toList() }
    fun recentAttempts(): List<Attempt> = synchronized(lock) { attempts.toList() }

    fun clearLogs() {
        synchronized(lock) {
            names.clear()
            attempts.clear()
            dirty = true
        }
        saveLogs()
        logTick++
    }

    /** Writes the logs if they changed (called now and then, and when Shield stops). */
    fun saveLogs() {
        val d = dir ?: return
        val o = synchronized(lock) {
            if (!dirty) return
            dirty = false
            JSONObject()
                .put("day", day).put("blocked", blockedToday).put("asked", askedToday)
                .put("names", JSONArray(names.map { JSONObject().put("t", it.at).put("n", it.name).put("a", it.app ?: "").put("s", it.bySelf) }))
                .put("attempts", JSONArray(attempts.map { JSONObject().put("t", it.at).put("a", it.app).put("d", it.dest) }))
        }
        runCatching {
            val tmp = File(d, "log.json.tmp")
            tmp.writeText(o.toString())
            tmp.renameTo(File(d, "log.json"))
        }
    }

    private fun loadLogs() {
        val f = File(dir ?: return, "log.json")
        val o = runCatching { JSONObject(f.readText()) }.getOrNull() ?: return
        synchronized(lock) {
            day = o.optString("day")
            blockedToday = o.optInt("blocked")
            askedToday = o.optInt("asked")
            o.optJSONArray("names")?.let { a ->
                for (i in 0 until minOf(a.length(), MAX_LOG)) {
                    val x = a.optJSONObject(i) ?: continue
                    names.addLast(BlockedName(x.optLong("t"), x.optString("n"), x.optString("a").ifBlank { null }, x.optBoolean("s")))
                }
            }
            o.optJSONArray("attempts")?.let { a ->
                for (i in 0 until minOf(a.length(), MAX_LOG)) {
                    val x = a.optJSONObject(i) ?: continue
                    attempts.addLast(Attempt(x.optLong("t"), x.optString("a"), x.optString("d")))
                }
            }
        }
    }

    internal fun encodeRules(r: Map<String, AppRule>): String = JSONObject(r.mapValues { it.value.name }).toString()

    internal fun decodeRules(s: String?): Map<String, AppRule> {
        val o = runCatching { JSONObject(s ?: return emptyMap()) }.getOrNull() ?: return emptyMap()
        val out = HashMap<String, AppRule>()
        for (k in o.keys()) runCatching { AppRule.valueOf(o.getString(k)) }.getOrNull()?.let { out[k] = it }
        return out
    }
}
