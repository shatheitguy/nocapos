package com.nocap.link.notes

import org.json.JSONArray
import org.json.JSONObject

/*
 * Sticky Notes, as the server keeps them (backend/internal/notes): a note's
 * body is a list of lines (paragraph, bullet or checklist item) made of text
 * runs that can be bold, italic or a link. Plain logic, no Android, so it can
 * be unit tested.
 *
 * On the phone a note is edited as plain lines ("- " for a bullet, "[ ] " /
 * "[x] " for a checklist item); lines that weren't changed keep their bold,
 * italic and links.
 */

data class NoteSpan(val text: String, val bold: Boolean = false, val italic: Boolean = false, val href: String? = null)

data class NoteBlock(val type: String = P, val done: Boolean = false, val spans: List<NoteSpan> = emptyList()) {
    val text: String get() = spans.joinToString("") { it.text }

    companion object {
        const val P = "p"
        const val LI = "li"
        const val TODO = "todo"
    }
}

data class Note(
    val id: String,
    val title: String = "",
    val body: List<NoteBlock> = emptyList(),
    val color: String = "glass",
    val pinned: Boolean = false,
    val archived: Boolean = false,
    val trashedAt: Long? = null,
    val remindAt: Long? = null,
    val reminded: Boolean = false,
    val onDesk: Boolean = false,
    val createdAt: Long = 0,
    val updatedAt: Long = 0,
    val version: Long = 0,
) {
    val trashed get() = trashedAt != null
}

object NoteDoc {
    /** NoCap's note palette (the web app's), glass first. */
    val COLORS = listOf("glass", "red", "ember", "sun", "mint", "sky", "grape", "ink")

    fun tint(color: String): Long = when (color) {
        "red" -> 0xFFE8232B
        "ember" -> 0xFFFF7A3D
        "sun" -> 0xFFF5B82E
        "mint" -> 0xFF2FC6A0
        "sky" -> 0xFF3B9BFF
        "grape" -> 0xFF8A5CF6
        "ink" -> 0xFF0B0C10
        else -> 0xFF9AA3B2
    }

    // ---------- JSON ----------

    fun parseNote(o: JSONObject): Note {
        val body = o.optJSONArray("body")?.let { parseBody(it) } ?: emptyList()
        return Note(
            id = o.optString("id"),
            title = o.optString("title"),
            body = body,
            color = o.optString("color").ifBlank { "glass" },
            pinned = o.optBoolean("pinned"),
            archived = o.optBoolean("archived"),
            trashedAt = if (o.has("trashed_at") && !o.isNull("trashed_at")) o.optLong("trashed_at") else null,
            remindAt = if (o.has("remind_at") && !o.isNull("remind_at")) o.optLong("remind_at") else null,
            reminded = o.optBoolean("reminded"),
            onDesk = o.optJSONObject("desk") != null,
            createdAt = o.optLong("created_at"),
            updatedAt = o.optLong("updated_at"),
            version = o.optLong("version"),
        )
    }

    fun parseBody(a: JSONArray): List<NoteBlock> = (0 until a.length()).mapNotNull { i ->
        val b = a.optJSONObject(i) ?: return@mapNotNull null
        val sa = b.optJSONArray("spans") ?: JSONArray()
        val spans = (0 until sa.length()).mapNotNull { j ->
            val s = sa.optJSONObject(j) ?: return@mapNotNull null
            NoteSpan(s.optString("text"), s.optBoolean("bold"), s.optBoolean("italic"), s.optString("href").takeIf { it.isNotBlank() })
        }
        val type = when (b.optString("type")) { NoteBlock.LI -> NoteBlock.LI; NoteBlock.TODO -> NoteBlock.TODO; else -> NoteBlock.P }
        NoteBlock(type, type == NoteBlock.TODO && b.optBoolean("done"), spans)
    }

    fun bodyJson(blocks: List<NoteBlock>): JSONArray {
        val a = JSONArray()
        for (b in blocks) {
            val o = JSONObject().put("type", b.type)
            if (b.type == NoteBlock.TODO && b.done) o.put("done", true)
            val sa = JSONArray()
            for (s in b.spans) {
                if (s.text.isEmpty()) continue
                val so = JSONObject().put("text", s.text)
                if (s.bold) so.put("bold", true)
                if (s.italic) so.put("italic", true)
                if (s.href != null) so.put("href", s.href)
                sa.put(so)
            }
            a.put(o.put("spans", sa))
        }
        return a
    }

    /** The cache file's form of a note (what the server sends, near enough). */
    fun noteJson(n: Note): JSONObject {
        val o = JSONObject().put("id", n.id).put("title", n.title).put("body", bodyJson(n.body)).put("color", n.color)
            .put("pinned", n.pinned).put("archived", n.archived).put("reminded", n.reminded)
            .put("created_at", n.createdAt).put("updated_at", n.updatedAt).put("version", n.version)
        n.trashedAt?.let { o.put("trashed_at", it) }
        n.remindAt?.let { o.put("remind_at", it) }
        if (n.onDesk) o.put("desk", JSONObject())
        return o
    }

    // ---------- plain lines ----------

    fun line(b: NoteBlock): String = when (b.type) {
        NoteBlock.LI -> "- "
        NoteBlock.TODO -> if (b.done) "[x] " else "[ ] "
        else -> ""
    } + b.text

    /** The body as plain lines, for editing and widgets. */
    fun plain(blocks: List<NoteBlock>): String = blocks.joinToString("\n") { line(it) }

    private val marker = Regex("^(?:([-*•])|\\[( |x|X)?])[ \\u00a0]")

    /** One typed line as a block. */
    fun parseLine(s: String): NoteBlock {
        val m = marker.find(s) ?: return NoteBlock(NoteBlock.P, false, spans(s))
        val rest = s.substring(m.value.length)
        return if (m.groupValues[1].isNotEmpty()) NoteBlock(NoteBlock.LI, false, spans(rest))
        else NoteBlock(NoteBlock.TODO, m.groupValues[2].equals("x", ignoreCase = true), spans(rest))
    }

    private fun spans(s: String) = if (s.isEmpty()) emptyList() else listOf(NoteSpan(s))

    /**
     * Typed text back into blocks. A line that still reads exactly like one
     * of the note's lines keeps that line's bold, italic and links.
     */
    fun fromPlain(text: String, original: List<NoteBlock> = emptyList()): List<NoteBlock> {
        val lines = text.replace("\r\n", "\n").replace('\r', '\n').split('\n').toMutableList()
        while (lines.size > 1 && lines.last().isBlank()) lines.removeAt(lines.lastIndex)
        val pool = original.groupBy { line(it) }.mapValues { it.value.toMutableList() }.toMutableMap()
        return lines.map { l ->
            val parsed = parseLine(l)
            val keep = pool[line(parsed)]?.removeFirstOrNull()
            keep ?: parsed
        }
    }

    fun toggle(blocks: List<NoteBlock>, index: Int): List<NoteBlock> =
        blocks.mapIndexed { i, b -> if (i == index && b.type == NoteBlock.TODO) b.copy(done = !b.done) else b }

    fun progress(blocks: List<NoteBlock>): Pair<Int, Int> {
        val todos = blocks.filter { it.type == NoteBlock.TODO }
        return todos.count { it.done } to todos.size
    }

    fun isEmpty(n: Note) = n.title.isBlank() && n.body.all { it.text.isBlank() }

    /** The title, or the first line, or "". */
    fun title(n: Note, max: Int = 80): String {
        val t = n.title.trim().ifEmpty { n.body.firstOrNull { it.text.isNotBlank() }?.text?.trim().orEmpty() }
        return if (t.length > max) t.take(max - 1) + "…" else t
    }

    // ---------- lists ----------

    enum class View { NOTES, ARCHIVE, TRASH }

    fun inView(n: Note, v: View) = when (v) {
        View.TRASH -> n.trashed
        View.ARCHIVE -> !n.trashed && n.archived
        View.NOTES -> !n.trashed && !n.archived
    }

    fun matches(n: Note, q: String): Boolean {
        val words = q.lowercase().split(Regex("\\s+")).filter { it.isNotEmpty() }
        if (words.isEmpty()) return true
        val hay = (n.title + "\n" + plain(n.body)).lowercase()
        return words.all { hay.contains(it) }
    }

    /** What a view shows: newest edit first, pinned on top of the notes. */
    fun visible(all: List<Note>, v: View, q: String): List<Note> =
        all.filter { inView(it, v) && matches(it, q) }
            .sortedWith(compareByDescending<Note> { v == View.NOTES && it.pinned }.thenByDescending { it.updatedAt }.thenBy { it.id })

    /** The note a home-screen widget shows: the one picked, else the first pinned, else the latest. */
    fun forWidget(all: List<Note>, picked: String?): Note? {
        val live = visible(all, View.NOTES, "")
        return live.firstOrNull { it.id == picked } ?: live.firstOrNull()
    }

    fun newId(): String {
        val r = java.security.SecureRandom()
        return (1..16).joinToString("") { "%02x".format(r.nextInt(256)) }
    }
}
