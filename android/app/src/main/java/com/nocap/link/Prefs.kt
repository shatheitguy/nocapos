package com.nocap.link

import android.content.Context
import android.content.SharedPreferences
import androidx.compose.runtime.mutableStateOf
import org.json.JSONArray
import org.json.JSONObject
import kotlin.reflect.KProperty

/** How the app talks: Gen Z (default) or Standard, like NoCapOS's own Language setting. */
fun t(genz: String, standard: String): String = if (Prefs.genz) genz else standard

/** Drawer styles people can pick in Settings. */
enum class DrawerStyle { PAGED, GRID, LIST, CATEGORIES, CLASSIC, HOME_PAGES }

enum class IconTheme { NOCAP, ORIGINAL, TINTED, MONO, THEMED }

enum class IconShape { ORIGINAL, CIRCLE, SQUIRCLE, ROUNDED }

enum class DockStyle { GLASS, FLOATING, NONE }

enum class LabelFont { NOCAP, SYSTEM, ORBITRON, SERIF, MONO }

enum class LabelColor { WHITE, ACCENT, SOFT, DARK }

enum class PageFx { SLIDE, FADE, ZOOM, CUBE, STACK, CAROUSEL }

enum class BarStyle { CLEAR, TINTED, HIDDEN }

enum class DrawerSort { AZ, MOST_USED, RECENT }

enum class FolderStyle { GRID, STACK }

enum class ThemeMode { DARK, LIGHT, AUTO }

enum class AppFont { NOCAP, SYSTEM, SERIF, MONO }

enum class Rotation { AUTO, PORTRAIT, LANDSCAPE }

/** Home-screen gestures that can be bound to an action. */
enum class Gesture { SWIPE_UP, SWIPE_DOWN, DOUBLE_TAP, PINCH_IN, PINCH_OUT, TWO_UP, TWO_DOWN, HOME }

/** What a gesture does. APP carries an app key ("APP:pkg/activity"). */
enum class Act { NONE, DRAWER, SEARCH, AI, NOTIFICATIONS, QUICK_SETTINGS, LOCK, APP, DESKTOP, HUB, SETTINGS, WIDGETS, FIRST_PAGE }

/**
 * One launcher setting, saved in plain SharedPreferences (nothing secret) and
 * held as Compose state so the screen redraws the moment it changes. Every
 * setting registers itself so backup/restore and "reset" cover all of them.
 */
abstract class Pref<T>(val key: String, val def: T, val backup: Boolean = true) {
    protected val state = mutableStateOf(def)
    val v: T get() = state.value

    operator fun getValue(thisRef: Any?, property: KProperty<*>): T = state.value

    open fun clean(x: T): T = x
    abstract fun read(sp: SharedPreferences): T
    abstract fun write(e: SharedPreferences.Editor, x: T)
    abstract fun toJson(): Any
    abstract fun fromJson(o: JSONObject)

    fun load(sp: SharedPreferences) { state.value = runCatching { clean(read(sp)) }.getOrDefault(def) }

    fun set(x: T) {
        val c = clean(x)
        state.value = c
        Opt.sp?.edit()?.also { write(it, c) }?.apply()
    }

    fun reset() = set(def)

    init { @Suppress("LeakingThis") Opt.all += this }
}

class IntPref(key: String, def: Int, val range: IntRange) : Pref<Int>(key, def) {
    override fun clean(x: Int) = x.coerceIn(range)
    override fun read(sp: SharedPreferences) = sp.getInt(key, def)
    override fun write(e: SharedPreferences.Editor, x: Int) { e.putInt(key, x) }
    override fun toJson(): Any = v
    override fun fromJson(o: JSONObject) { if (o.has(key)) set(o.optInt(key, def)) }
}

class BoolPref(key: String, def: Boolean) : Pref<Boolean>(key, def) {
    override fun read(sp: SharedPreferences) = sp.getBoolean(key, def)
    override fun write(e: SharedPreferences.Editor, x: Boolean) { e.putBoolean(key, x) }
    override fun toJson(): Any = v
    override fun fromJson(o: JSONObject) { if (o.has(key)) set(o.optBoolean(key, def)) }
}

class StrPref(key: String, def: String, backup: Boolean = true) : Pref<String>(key, def, backup) {
    override fun read(sp: SharedPreferences) = sp.getString(key, def) ?: def
    override fun write(e: SharedPreferences.Editor, x: String) { e.putString(key, x) }
    override fun toJson(): Any = v
    override fun fromJson(o: JSONObject) { if (o.has(key)) set(o.optString(key, def)) }
}

class EnumPref<E : Enum<E>>(key: String, def: E, private val values: Array<E>) : Pref<E>(key, def) {
    override fun read(sp: SharedPreferences) = values.firstOrNull { it.name == sp.getString(key, def.name) } ?: def
    override fun write(e: SharedPreferences.Editor, x: E) { e.putString(key, x.name) }
    override fun toJson(): Any = v.name
    override fun fromJson(o: JSONObject) { values.firstOrNull { it.name == o.optString(key) }?.let(::set) }
}

class SetPref(key: String) : Pref<Set<String>>(key, emptySet()) {
    override fun read(sp: SharedPreferences) = sp.getStringSet(key, emptySet())!!.toSet()
    override fun write(e: SharedPreferences.Editor, x: Set<String>) { e.putStringSet(key, x) }
    override fun toJson(): Any = JSONArray(v.toList())
    override fun fromJson(o: JSONObject) {
        o.optJSONArray(key)?.let { a -> set((0 until a.length()).map { a.getString(it) }.toSet()) }
    }
}

/** A small string → string map (custom labels, gesture bindings), stored as JSON. */
class MapPref(key: String, def: Map<String, String> = emptyMap(), backup: Boolean = true) : Pref<Map<String, String>>(key, def, backup) {
    override fun read(sp: SharedPreferences): Map<String, String> {
        val s = sp.getString(key, null) ?: return def
        val o = JSONObject(s)
        return def + o.keys().asSequence().associateWith { o.optString(it) }
    }
    override fun write(e: SharedPreferences.Editor, x: Map<String, String>) { e.putString(key, JSONObject(x).toString()) }
    override fun toJson(): Any = JSONObject(v)
    override fun fromJson(o: JSONObject) {
        o.optJSONObject(key)?.let { j -> set(def + j.keys().asSequence().associateWith { j.optString(it) }) }
    }
    fun put(k: String, value: String?) = set(if (value == null) v - k else v + (k to value))
}

/** Every launcher option. Read with `.v`, change with `.set(…)`. */
object Opt {
    internal var sp: SharedPreferences? = null
    val all = mutableListOf<Pref<*>>()

    // Look
    val lingo = StrPref("lingo", "genz")
    val theme = EnumPref("theme_mode", ThemeMode.DARK, ThemeMode.entries.toTypedArray())
    val accent = StrPref("accent", "#FF2A2A")
    val font = EnumPref("app_font", AppFont.NOCAP, AppFont.entries.toTypedArray())
    val haptics = BoolPref("haptics", true)
    val rotation = EnumPref("rotation", Rotation.AUTO, Rotation.entries.toTypedArray())

    // Wallpaper
    val wallpaper = StrPref("wallpaper", "tide")
    val dim = IntPref("dim", 15, 0..80)
    val blur = IntPref("wall_blur", 0, 0..30)
    val parallax = BoolPref("parallax", true)
    val statusBar = EnumPref("status_bar", BarStyle.CLEAR, BarStyle.entries.toTypedArray())
    val navBar = EnumPref("nav_bar", BarStyle.CLEAR, arrayOf(BarStyle.CLEAR, BarStyle.TINTED))

    // Home
    val columns = IntPref("columns", 4, 3..7)
    val rows = IntPref("rows", 6, 4..9)
    val pageFx = EnumPref("page_fx", PageFx.SLIDE, PageFx.entries.toTypedArray())
    val lockLayout = BoolPref("lock_layout", false)
    val searchBar = BoolPref("top_search", true)
    val pageDots = BoolPref("page_dots", true)
    val folderStyle = EnumPref("folder_style", FolderStyle.GRID, FolderStyle.entries.toTypedArray())

    // Icons
    val iconSize = IntPref("icon_size", 54, 36..72)
    val iconShape = EnumPref("icon_shape", IconShape.ORIGINAL, IconShape.entries.toTypedArray())
    val iconTheme = EnumPref("icon_theme", IconTheme.NOCAP, IconTheme.entries.toTypedArray())
    val nocapGlyph = BoolPref("nocap_glyph", false)
    val labels = BoolPref("labels", true)
    val labelFont = EnumPref("label_font", LabelFont.NOCAP, LabelFont.entries.toTypedArray())
    val labelSize = IntPref("label_size", 12, 9..16)
    val labelColor = EnumPref("label_color", LabelColor.WHITE, LabelColor.entries.toTypedArray())
    val labelShadow = BoolPref("label_shadow", true)
    val dots = BoolPref("dots", true)
    val customLabels = MapPref("custom_labels")
    val customIcons = MapPref("custom_icons", backup = false) // files on this phone

    // Dock
    val dockCount = IntPref("dock_count", 5, 3..7)
    val dockStyle = EnumPref("dock_style", DockStyle.GLASS, DockStyle.entries.toTypedArray())
    val dockButtons = BoolPref("dock_buttons", true)
    val dockLabels = BoolPref("dock_labels", false)

    // Drawer
    val drawer = EnumPref("drawer", DrawerStyle.PAGED, DrawerStyle.entries.toTypedArray())
    val drawerColumns = IntPref("drawer_columns", 4, 3..7)
    val drawerSort = EnumPref("drawer_sort", DrawerSort.AZ, DrawerSort.entries.toTypedArray())
    val drawerOpacity = IntPref("drawer_opacity", 92, 30..100)
    val drawerBlur = BoolPref("drawer_blur", true)
    val drawerRecents = BoolPref("drawer_recents", true)
    val hidden = SetPref("hidden")

    // Gestures
    val gestures = MapPref(
        "gestures",
        mapOf(
            Gesture.SWIPE_UP.name to Act.DRAWER.name, Gesture.SWIPE_DOWN.name to Act.NOTIFICATIONS.name,
            Gesture.DOUBLE_TAP.name to Act.NONE.name, Gesture.PINCH_IN.name to Act.SETTINGS.name,
            Gesture.PINCH_OUT.name to Act.NONE.name, Gesture.TWO_UP.name to Act.AI.name,
            Gesture.TWO_DOWN.name to Act.QUICK_SETTINGS.name, Gesture.HOME.name to Act.FIRST_PAGE.name,
        ),
    )

    // Widgets
    val weatherPlace = StrPref("weather_place", "")
    val fahrenheit = BoolPref("fahrenheit", false)

    // NoCap Link sync
    val forward = BoolPref("forward_notifications", false)
    val forwardApps = SetPref("forward_apps")
    val clipSync = BoolPref("clip_sync", false)
    val liveNotices = BoolPref("live_notices", true)

    // NoCap Link + one-offs (not part of a backup)
    val serverNotices = BoolPref("server_notices", false)
    val homeAsked = BoolPref("home_asked", false)

    fun init(ctx: Context) {
        if (sp != null) return
        val s = ctx.applicationContext.getSharedPreferences("nocap_launcher", Context.MODE_PRIVATE)
        sp = s
        all.forEach { it.load(s) }
        // v2 → v3: "double-tap to lock" and "swipe down" became gesture bindings.
        if (!s.contains("gestures")) {
            val m = gestures.v.toMutableMap()
            if (s.getBoolean("double_tap_lock", false)) m[Gesture.DOUBLE_TAP.name] = Act.LOCK.name
            when (s.getString("swipe_down", null)) {
                "SEARCH" -> m[Gesture.SWIPE_DOWN.name] = Act.SEARCH.name
                "NOTHING" -> m[Gesture.SWIPE_DOWN.name] = Act.NONE.name
            }
            gestures.set(m)
        }
    }

    private val skip = setOf("server_notices", "home_asked", "custom_icons", "forward_notifications", "clip_sync")

    /** Every setting that travels with a backup. */
    fun export(): JSONObject {
        val o = JSONObject()
        for (p in all) if (p.backup && p.key !in skip) o.put(p.key, p.toJson())
        return o
    }

    fun import(o: JSONObject) {
        for (p in all) if (p.backup && p.key !in skip) runCatching { p.fromJson(o) }
        if (wallpaper.v != Wallpapers.CUSTOM && wallpaper.v != Wallpapers.GRID && Wallpapers.builtIn.none { it.id == wallpaper.v }) wallpaper.reset()
    }

    /** Back to how NoCap OS ships (layout and link untouched). */
    fun resetAll() {
        for (p in all) if (p.key !in skip && p.key != "hidden" && p.key != "custom_labels") p.reset()
    }

    fun binding(g: Gesture): String = gestures.v[g.name] ?: Act.NONE.name
    fun bind(g: Gesture, action: String) = gestures.put(g.name, action)
}

/**
 * The older, simpler face of [Opt] that much of the app reads. Values are
 * Compose state, so the home screen redraws the moment something changes.
 */
object Prefs {
    val genz get() = Opt.lingo.v != "standard"
    val drawer get() = Opt.drawer.v
    val columns get() = Opt.columns.v
    val rows get() = Opt.rows.v
    val iconSize get() = Opt.iconSize.v
    val labels get() = Opt.labels.v
    val iconTheme get() = Opt.iconTheme.v
    val wallpaper get() = Opt.wallpaper.v
    val dim get() = Opt.dim.v
    val dots get() = Opt.dots.v
    val serverNotices get() = Opt.serverNotices.v
    val hidden get() = Opt.hidden.v
    val homeAsked get() = Opt.homeAsked.v

    private var sp: SharedPreferences? = null
    var lastNotice: Long
        get() = sp?.getLong("last_notice", 0) ?: 0
        set(v) { sp?.edit()?.putLong("last_notice", v)?.apply() }

    fun init(ctx: Context) {
        Opt.init(ctx)
        if (sp == null) sp = ctx.applicationContext.getSharedPreferences("nocap_launcher", Context.MODE_PRIVATE)
    }

    fun setGenz(v: Boolean) = Opt.lingo.set(if (v) "genz" else "standard")
    fun setWallpaper(v: String) = Opt.wallpaper.set(v)
    fun setServerNotices(v: Boolean) = Opt.serverNotices.set(v)
    fun setHomeAsked() = Opt.homeAsked.set(true)

    fun hide(key: String, hide: Boolean) = Opt.hidden.set(if (hide) Opt.hidden.v + key else Opt.hidden.v - key)

    /** Settings that travel with a home-screen backup. */
    fun export(): JSONObject = Opt.export()
    fun import(o: JSONObject) = Opt.import(o)
}
