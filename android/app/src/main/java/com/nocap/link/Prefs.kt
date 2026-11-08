package com.nocap.link

import android.content.Context
import android.content.SharedPreferences
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue

/** How the app talks: Gen Z (default) or Standard, like NoCapOS's own Language setting. */
fun t(genz: String, standard: String): String = if (Prefs.genz) genz else standard

/** Drawer styles people can pick in Settings. */
enum class DrawerStyle { PAGED, GRID, LIST, CATEGORIES }

enum class IconTheme { ORIGINAL, TINTED, MONO }

enum class SwipeDown { NOTIFICATIONS, SEARCH, NOTHING }

/**
 * Launcher settings: nothing secret, so plain SharedPreferences (the link
 * itself stays in [Vault]). Each value is Compose state, so the home
 * screen redraws the moment something changes.
 */
object Prefs {
    private lateinit var sp: SharedPreferences

    var genz by mutableStateOf(true); private set
    var drawer by mutableStateOf(DrawerStyle.PAGED); private set
    var columns by mutableStateOf(4); private set
    var rows by mutableStateOf(6); private set
    var iconSize by mutableStateOf(54); private set
    var labels by mutableStateOf(true); private set
    var iconTheme by mutableStateOf(IconTheme.ORIGINAL); private set
    var wallpaper by mutableStateOf("tide"); private set
    var dim by mutableStateOf(15); private set
    var dots by mutableStateOf(true); private set
    var swipeDown by mutableStateOf(SwipeDown.NOTIFICATIONS); private set
    var doubleTapLock by mutableStateOf(false); private set
    var serverNotices by mutableStateOf(false); private set
    var hidden by mutableStateOf<Set<String>>(emptySet()); private set
    var homeAsked by mutableStateOf(false); private set
    var lastNotice: Long
        get() = sp.getLong("last_notice", 0)
        set(v) = sp.edit().putLong("last_notice", v).apply()

    fun init(ctx: Context) {
        if (::sp.isInitialized) return
        sp = ctx.applicationContext.getSharedPreferences("nocap_launcher", Context.MODE_PRIVATE)
        genz = sp.getString("lingo", "genz") != "standard"
        drawer = runCatching { DrawerStyle.valueOf(sp.getString("drawer", "PAGED")!!) }.getOrDefault(DrawerStyle.PAGED)
        columns = sp.getInt("columns", 4).coerceIn(3, 6)
        rows = sp.getInt("rows", 6).coerceIn(4, 8)
        iconSize = sp.getInt("icon_size", 54).coerceIn(40, 68)
        labels = sp.getBoolean("labels", true)
        iconTheme = runCatching { IconTheme.valueOf(sp.getString("icon_theme", "ORIGINAL")!!) }.getOrDefault(IconTheme.ORIGINAL)
        wallpaper = sp.getString("wallpaper", "tide")!!
        dim = sp.getInt("dim", 15).coerceIn(0, 60)
        dots = sp.getBoolean("dots", true)
        swipeDown = runCatching { SwipeDown.valueOf(sp.getString("swipe_down", "NOTIFICATIONS")!!) }.getOrDefault(SwipeDown.NOTIFICATIONS)
        doubleTapLock = sp.getBoolean("double_tap_lock", false)
        serverNotices = sp.getBoolean("server_notices", false)
        hidden = sp.getStringSet("hidden", emptySet())!!.toSet()
        homeAsked = sp.getBoolean("home_asked", false)
    }

    @JvmName("putGenz") fun setGenz(v: Boolean) { genz = v; sp.edit().putString("lingo", if (v) "genz" else "standard").apply() }
    @JvmName("putDrawer") fun setDrawer(v: DrawerStyle) { drawer = v; sp.edit().putString("drawer", v.name).apply() }
    @JvmName("putColumns") fun setColumns(v: Int) { columns = v.coerceIn(3, 6); sp.edit().putInt("columns", columns).apply() }
    @JvmName("putRows") fun setRows(v: Int) { rows = v.coerceIn(4, 8); sp.edit().putInt("rows", rows).apply() }
    @JvmName("putIconSize") fun setIconSize(v: Int) { iconSize = v.coerceIn(40, 68); sp.edit().putInt("icon_size", iconSize).apply() }
    @JvmName("putLabels") fun setLabels(v: Boolean) { labels = v; sp.edit().putBoolean("labels", v).apply() }
    @JvmName("putIconTheme") fun setIconTheme(v: IconTheme) { iconTheme = v; sp.edit().putString("icon_theme", v.name).apply() }
    @JvmName("putWallpaper") fun setWallpaper(v: String) { wallpaper = v; sp.edit().putString("wallpaper", v).apply() }
    @JvmName("putDim") fun setDim(v: Int) { dim = v.coerceIn(0, 60); sp.edit().putInt("dim", dim).apply() }
    @JvmName("putDots") fun setDots(v: Boolean) { dots = v; sp.edit().putBoolean("dots", v).apply() }
    @JvmName("putSwipeDown") fun setSwipeDown(v: SwipeDown) { swipeDown = v; sp.edit().putString("swipe_down", v.name).apply() }
    @JvmName("putDoubleTapLock") fun setDoubleTapLock(v: Boolean) { doubleTapLock = v; sp.edit().putBoolean("double_tap_lock", v).apply() }
    @JvmName("putServerNotices") fun setServerNotices(v: Boolean) { serverNotices = v; sp.edit().putBoolean("server_notices", v).apply() }
    @JvmName("putHomeAsked") fun setHomeAsked() { homeAsked = true; sp.edit().putBoolean("home_asked", true).apply() }

    fun hide(key: String, hide: Boolean) {
        hidden = if (hide) hidden + key else hidden - key
        sp.edit().putStringSet("hidden", hidden).apply()
    }

    /** Settings that travel with a home-screen backup. */
    fun export(): Map<String, Any> = mapOf(
        "lingo" to if (genz) "genz" else "standard", "drawer" to drawer.name, "columns" to columns, "rows" to rows,
        "icon_size" to iconSize, "labels" to labels, "icon_theme" to iconTheme.name, "wallpaper" to wallpaper,
        "dim" to dim, "swipe_down" to swipeDown.name, "hidden" to hidden.toList(),
    )

    fun import(o: org.json.JSONObject) {
        o.optString("lingo").takeIf { it.isNotBlank() }?.let { setGenz(it != "standard") }
        o.optString("drawer").let { s -> DrawerStyle.entries.firstOrNull { it.name == s }?.let(::setDrawer) }
        if (o.has("columns")) setColumns(o.optInt("columns"))
        if (o.has("rows")) setRows(o.optInt("rows"))
        if (o.has("icon_size")) setIconSize(o.optInt("icon_size"))
        if (o.has("labels")) setLabels(o.optBoolean("labels"))
        o.optString("icon_theme").let { s -> IconTheme.entries.firstOrNull { it.name == s }?.let(::setIconTheme) }
        o.optString("wallpaper").takeIf { it in Wallpapers.builtIn.map { w -> w.id } }?.let(::setWallpaper)
        if (o.has("dim")) setDim(o.optInt("dim"))
        o.optString("swipe_down").let { s -> SwipeDown.entries.firstOrNull { it.name == s }?.let(::setSwipeDown) }
        o.optJSONArray("hidden")?.let { a ->
            hidden = (0 until a.length()).map { a.getString(it) }.toSet()
            sp.edit().putStringSet("hidden", hidden).apply()
        }
    }
}
