package com.nocap.link.notes

import android.content.Context
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.nocap.link.data.Vault
import com.nocap.link.net.NoCapApi
import java.io.File
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject

// ---------- the device API (/api/v1/device/notes, behind this phone's token) ----------

fun NoCapApi.notes(): List<Note> {
    val a = JSONObject(call("GET", "/api/v1/device/notes")).optJSONArray("items") ?: JSONArray()
    return (0 until a.length()).map { NoteDoc.parseNote(a.getJSONObject(it)) }
}

fun NoCapApi.createNote(id: String, title: String, body: List<NoteBlock>, color: String, pinned: Boolean): Note {
    val b = JSONObject().put("id", id).put("title", title).put("body", NoteDoc.bodyJson(body)).put("color", color).put("pinned", pinned)
    return NoteDoc.parseNote(JSONObject(call("POST", "/api/v1/device/notes", b)))
}

fun NoCapApi.patchNote(id: String, patch: JSONObject): Note =
    NoteDoc.parseNote(JSONObject(call("PATCH", "/api/v1/device/notes/$id", patch)))

fun NoCapApi.deleteNote(id: String) {
    call("DELETE", "/api/v1/device/notes/$id")
}

fun NoCapApi.emptyNotesTrash() {
    call("DELETE", "/api/v1/device/notes/trash")
}

/** A change to send: only the fields set are sent (the server leaves the rest). */
data class NoteChange(
    val title: String? = null,
    val body: List<NoteBlock>? = null,
    val color: String? = null,
    val pinned: Boolean? = null,
    val archived: Boolean? = null,
    val trashed: Boolean? = null,
    /** 0 clears the reminder. */
    val remindAt: Long? = null,
) {
    fun json(): JSONObject {
        val o = JSONObject()
        title?.let { o.put("title", it) }
        body?.let { o.put("body", NoteDoc.bodyJson(it)) }
        color?.let { o.put("color", it) }
        pinned?.let { o.put("pinned", it) }
        archived?.let { o.put("archived", it) }
        trashed?.let { o.put("trashed", it) }
        remindAt?.let { o.put("remind_at", if (it <= 0) JSONObject.NULL else it) }
        return o
    }

    /** The same change on the phone's copy, so it shows at once. */
    fun applyTo(n: Note, now: Long): Note {
        var out = n.copy(
            title = title ?: n.title, body = body ?: n.body, color = color ?: n.color,
            pinned = pinned ?: n.pinned, archived = archived ?: n.archived,
        )
        if (trashed != null) out = out.copy(trashedAt = if (trashed) (n.trashedAt ?: now) else null, onDesk = if (trashed) false else n.onDesk)
        if (remindAt != null) out = out.copy(remindAt = remindAt.takeIf { it > 0 }, reminded = false)
        return if (out != n) out.copy(updatedAt = now) else n
    }
}

/**
 * Your notes on this phone: the last list from the server (kept in a private
 * file, so the screen and the widget work offline), refreshed when the
 * server says they changed. Edits show at once and are sent right after.
 */
object NotesStore {
    var notes by mutableStateOf<List<Note>>(emptyList())
        private set
    var loading by mutableStateOf(false)
        private set
    /** "" when the last refresh worked; else why not (offline, older server…). */
    var problem by mutableStateOf("")
        private set

    private val lock = Mutex()
    private var loadedCache = false

    private fun file(ctx: Context) = File(ctx.filesDir, "notes.json")

    private fun api(ctx: Context): NoCapApi? = runCatching { Vault.get(ctx).link?.let { NoCapApi(it) } }.getOrNull()

    /** The cached list, once per run. */
    fun init(ctx: Context) {
        if (loadedCache) return
        loadedCache = true
        notes = readCache(ctx)
    }

    fun readCache(ctx: Context): List<Note> = runCatching {
        val a = JSONArray(file(ctx).readText())
        (0 until a.length()).map { NoteDoc.parseNote(a.getJSONObject(it)) }
    }.getOrDefault(emptyList())

    private fun keep(ctx: Context, list: List<Note>) {
        notes = list
        runCatching {
            val a = JSONArray()
            list.forEach { a.put(NoteDoc.noteJson(it)) }
            val f = file(ctx)
            val tmp = File(f.parentFile, f.name + ".tmp")
            tmp.writeText(a.toString())
            tmp.renameTo(f)
        }
        NotesWidget.update(ctx)
    }

    /** Fetches the list from the server. */
    suspend fun refresh(ctx: Context): Boolean = withContext(Dispatchers.IO) {
        init(ctx)
        val api = api(ctx) ?: run { problem = "unlinked"; return@withContext false }
        loading = true
        try {
            val list = lock.withLock { api.notes() }
            keep(ctx, list)
            problem = ""
            true
        } catch (e: com.nocap.link.net.ServerTooOld) {
            problem = "old"
            false
        } catch (e: Exception) {
            problem = "offline"
            false
        } finally {
            loading = false
        }
    }

    /** Makes a note (shown at once) and saves it. */
    suspend fun create(ctx: Context, title: String = "", body: List<NoteBlock> = listOf(NoteBlock())): Note? = withContext(Dispatchers.IO) {
        init(ctx)
        val now = System.currentTimeMillis()
        val local = Note(NoteDoc.newId(), title, body, createdAt = now, updatedAt = now)
        val api = api(ctx) ?: return@withContext null
        keep(ctx, listOf(local) + notes)
        runCatching { lock.withLock { api.createNote(local.id, title, body, local.color, false) } }
            .onSuccess { saved -> keep(ctx, notes.map { if (it.id == saved.id) saved else it }) }
            .onFailure { keep(ctx, notes.filterNot { it.id == local.id }); problem = it.message ?: "error" }
            .getOrNull()
    }

    /** Changes a note here at once and on the server. */
    suspend fun change(ctx: Context, id: String, c: NoteChange): Boolean = withContext(Dispatchers.IO) {
        init(ctx)
        val now = System.currentTimeMillis()
        keep(ctx, notes.map { if (it.id == id) c.applyTo(it, now) else it })
        val api = api(ctx) ?: return@withContext false
        runCatching { lock.withLock { api.patchNote(id, c.json()) } }
            .onSuccess { saved -> keep(ctx, notes.map { if (it.id == id && saved.version >= it.version) saved else it }) }
            .onFailure { problem = "offline" }
            .isSuccess
    }

    suspend fun deleteForever(ctx: Context, id: String): Boolean = withContext(Dispatchers.IO) {
        val api = api(ctx) ?: return@withContext false
        val ok = runCatching { lock.withLock { api.deleteNote(id) } }.isSuccess
        if (ok) keep(ctx, notes.filterNot { it.id == id })
        ok
    }

    suspend fun emptyTrash(ctx: Context): Boolean = withContext(Dispatchers.IO) {
        val api = api(ctx) ?: return@withContext false
        val ok = runCatching { lock.withLock { api.emptyNotesTrash() } }.isSuccess
        if (ok) keep(ctx, notes.filterNot { it.trashed })
        ok
    }

    /** Forgets everything (the phone was unlinked). */
    fun clear(ctx: Context) {
        notes = emptyList()
        runCatching { file(ctx).delete() }
        NotesWidget.update(ctx)
    }
}
