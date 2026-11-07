package com.nocap.link.launcher

import android.content.Context
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.nocap.link.Prefs
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.util.UUID

/** Where something sits on a home page, in grid cells. */
data class Cell(val x: Int, val y: Int, val w: Int = 1, val h: Int = 1) {
    fun overlaps(o: Cell) = x < o.x + o.w && o.x < x + w && y < o.y + o.h && o.y < y + h
}

sealed interface Item {
    val id: String
    val cell: Cell
    fun at(c: Cell): Item = when (this) {
        is AppItem -> copy(cell = c)
        is FolderItem -> copy(cell = c)
        is WidgetItem -> copy(cell = c)
        is NoCapItem -> copy(cell = c)
    }
}

data class AppItem(override val id: String, override val cell: Cell, val key: String) : Item
data class FolderItem(override val id: String, override val cell: Cell, val name: String, val keys: List<String>) : Item
/** An Android home-screen widget (from AppWidgetHost). */
data class WidgetItem(override val id: String, override val cell: Cell, val widgetId: Int) : Item
/** One of NoCap OS's own widgets. */
data class NoCapItem(override val id: String, override val cell: Cell, val kind: String) : Item

data class Page(val items: List<Item> = emptyList())

data class Layout(val pages: List<Page>, val dock: List<String>)

object NoCapKinds {
    const val CLOCK = "clock"
    const val SERVER = "server"
    const val QUICK = "quick"
    const val FILES = "files"
    const val AI = "ai"
    val all = listOf(CLOCK, SERVER, QUICK, FILES, AI)
    fun size(kind: String): Pair<Int, Int> = when (kind) {
        QUICK, AI -> 4 to 1
        else -> 4 to 2
    }
}

fun newId(): String = UUID.randomUUID().toString().take(12)

/**
 * The home screen: pages of icons, folders and widgets, plus the dock.
 * Kept as one immutable value in Compose state; every change is saved.
 */
object Home {
    var layout by mutableStateOf(Layout(listOf(Page()), emptyList()))
        private set
    private var file: File? = null
    var loaded = false
        private set

    fun load(ctx: Context, firstRun: () -> Layout) {
        if (loaded) return
        file = File(ctx.filesDir, "home_layout.json")
        val f = file!!
        layout = runCatching { if (f.exists()) fromJson(JSONObject(f.readText())) else null }.getOrNull() ?: firstRun().also { save(it) }
        loaded = true
    }

    fun set(l: Layout) {
        val clean = l.copy(pages = l.pages.ifEmpty { listOf(Page()) })
        layout = clean
        save(clean)
    }

    private fun save(l: Layout) {
        runCatching { file?.writeText(toJson(l).toString()) }
    }

    fun page(i: Int) = layout.pages.getOrElse(i) { Page() }

    fun find(id: String): Pair<Int, Item>? {
        layout.pages.forEachIndexed { p, page -> page.items.firstOrNull { it.id == id }?.let { return p to it } }
        return null
    }

    private fun updatePage(i: Int, f: (List<Item>) -> List<Item>) {
        val pages = layout.pages.toMutableList()
        while (pages.size <= i) pages.add(Page())
        pages[i] = Page(f(pages[i].items))
        set(layout.copy(pages = pages))
    }

    fun remove(id: String) {
        set(layout.copy(pages = layout.pages.map { p -> Page(p.items.filter { it.id != id }) }))
    }

    fun replace(item: Item) {
        set(layout.copy(pages = layout.pages.map { p -> Page(p.items.map { if (it.id == item.id) item else it }) }))
    }

    /** First free spot for a w×h thing, searching from page [from]; adds a page if all are full. */
    fun freeSpot(w: Int, h: Int, from: Int = 0): Pair<Int, Cell> {
        val cols = Prefs.columns
        val rows = Prefs.rows
        for (p in from until layout.pages.size + 1) {
            val items = page(p).items
            for (y in 0..rows - h) for (x in 0..cols - w) {
                val c = Cell(x, y, w.coerceAtMost(cols), h)
                if (items.none { it.cell.overlaps(c) }) return p to c
            }
        }
        return layout.pages.size to Cell(0, 0, w.coerceAtMost(cols), h)
    }

    fun add(item: (Cell) -> Item, w: Int, h: Int, from: Int = 0): Int {
        val (p, c) = freeSpot(w, h, from)
        updatePage(p) { it + item(c) }
        return p
    }

    fun addApp(key: String, from: Int = 0) = add({ AppItem(newId(), it, key) }, 1, 1, from)

    fun isFree(page: Int, c: Cell, ignore: String?): Boolean {
        if (c.x < 0 || c.y < 0 || c.x + c.w > Prefs.columns || c.y + c.h > Prefs.rows) return false
        return page(page).items.none { it.id != ignore && it.cell.overlaps(c) }
    }

    fun itemAt(page: Int, x: Int, y: Int): Item? = page(page).items.firstOrNull { it.cell.overlaps(Cell(x, y)) }

    /** Moves something to another spot (any page). */
    fun move(id: String, toPage: Int, c: Cell) {
        val (_, item) = find(id) ?: return
        val pages = layout.pages.map { p -> Page(p.items.filter { it.id != id }) }.toMutableList()
        while (pages.size <= toPage) pages.add(Page())
        pages[toPage] = Page(pages[toPage].items + item.at(c))
        set(layout.copy(pages = pages))
    }

    /** Drops an app on another app → a new folder; on a folder → into it. */
    fun mergeInto(dragged: Item, target: Item) {
        val keys = when (dragged) {
            is AppItem -> listOf(dragged.key)
            is FolderItem -> dragged.keys
            else -> return
        }
        val merged: Item = when (target) {
            is AppItem -> FolderItem(newId(), target.cell, com.nocap.link.t("Squad", "Folder"), (listOf(target.key) + keys).distinct())
            is FolderItem -> target.copy(keys = (target.keys + keys).distinct())
            else -> return
        }
        set(layout.copy(pages = layout.pages.map { p -> Page(p.items.filter { it.id != dragged.id }.map { if (it.id == target.id) merged else it }) }))
    }

    fun removeFromFolder(folder: FolderItem, key: String, page: Int) {
        val left = folder.keys - key
        when {
            left.size >= 2 -> replace(folder.copy(keys = left))
            left.size == 1 -> replace(AppItem(folder.id, folder.cell, left[0]))
            else -> remove(folder.id)
        }
        addApp(key, page)
    }

    fun addPage() = set(layout.copy(pages = layout.pages + Page()))

    fun removePage(i: Int) {
        if (layout.pages.size <= 1) return
        set(layout.copy(pages = layout.pages.filterIndexed { idx, _ -> idx != i }))
    }

    fun setDock(keys: List<String>) = set(layout.copy(dock = keys.distinct().take(5)))

    /** Removes apps that were uninstalled. */
    fun prune(installed: Set<String>) {
        if (installed.isEmpty()) return
        val pages = layout.pages.map { p ->
            Page(p.items.mapNotNull { i ->
                when (i) {
                    is AppItem -> i.takeIf { it.key in installed }
                    is FolderItem -> {
                        val k = i.keys.filter { it in installed }
                        when (k.size) { 0 -> null; 1 -> AppItem(i.id, i.cell, k[0]); else -> i.copy(keys = k) }
                    }
                    else -> i
                }
            })
        }
        val dock = layout.dock.filter { it in installed }
        if (pages != layout.pages || dock != layout.dock) set(Layout(pages, dock))
    }

    /** After the grid got smaller: anything that no longer fits moves to a free spot. */
    fun refit() {
        val cols = Prefs.columns
        val rows = Prefs.rows
        val misfits = mutableListOf<Item>()
        val pages = layout.pages.map { p ->
            Page(p.items.filter { i ->
                val fits = i.cell.x + i.cell.w <= cols && i.cell.y + i.cell.h <= rows
                if (!fits) misfits.add(i)
                fits
            })
        }
        if (misfits.isEmpty()) return
        set(layout.copy(pages = pages))
        for (m in misfits) {
            val w = m.cell.w.coerceAtMost(cols)
            val h = m.cell.h.coerceAtMost(rows)
            add({ m.at(it) }, w, h)
        }
    }

    // ---------- JSON (local file and server backup) ----------

    fun toJson(l: Layout = layout, portable: Boolean = false): JSONObject {
        val pages = JSONArray()
        for (p in l.pages) {
            val items = JSONArray()
            for (i in p.items) {
                if (portable && i is WidgetItem) continue // widget ids only mean something on this phone
                val o = JSONObject().put("id", i.id).put("x", i.cell.x).put("y", i.cell.y).put("w", i.cell.w).put("h", i.cell.h)
                when (i) {
                    is AppItem -> o.put("type", "app").put("key", i.key)
                    is FolderItem -> o.put("type", "folder").put("name", i.name).put("keys", JSONArray(i.keys))
                    is WidgetItem -> o.put("type", "widget").put("widget_id", i.widgetId)
                    is NoCapItem -> o.put("type", "nocap").put("kind", i.kind)
                }
                items.put(o)
            }
            pages.put(JSONObject().put("items", items))
        }
        return JSONObject().put("version", 1).put("pages", pages).put("dock", JSONArray(l.dock))
    }

    fun fromJson(o: JSONObject): Layout {
        val pa = o.optJSONArray("pages") ?: JSONArray()
        val pages = (0 until pa.length()).map { pi ->
            val ia = pa.getJSONObject(pi).optJSONArray("items") ?: JSONArray()
            Page((0 until ia.length()).mapNotNull { ii ->
                val j = ia.getJSONObject(ii)
                val c = Cell(j.optInt("x"), j.optInt("y"), j.optInt("w", 1).coerceAtLeast(1), j.optInt("h", 1).coerceAtLeast(1))
                val id = j.optString("id").ifBlank { newId() }
                when (j.optString("type")) {
                    "app" -> AppItem(id, c, j.optString("key"))
                    "folder" -> {
                        val ka = j.optJSONArray("keys") ?: JSONArray()
                        FolderItem(id, c, j.optString("name"), (0 until ka.length()).map { ka.getString(it) })
                    }
                    "widget" -> WidgetItem(id, c, j.optInt("widget_id"))
                    "nocap" -> NoCapItem(id, c, j.optString("kind"))
                    else -> null
                }
            })
        }
        val da = o.optJSONArray("dock") ?: JSONArray()
        return Layout(pages.ifEmpty { listOf(Page()) }, (0 until da.length()).map { da.getString(it) })
    }
}
