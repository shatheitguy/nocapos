package com.nocap.link.notes

import com.nocap.link.net.parseEvents
import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class NoteDocTest {
    private val serverNote = """
        {"id":"abc12345","title":"Groceries","color":"mint","pinned":true,"archived":false,
         "remind_at":1700000000000,"reminded":false,"desk":{"x":1,"y":2,"w":200,"h":200},
         "created_at":1,"updated_at":5,"version":3,
         "body":[{"type":"p","spans":[{"text":"buy "},{"text":"today","bold":true},{"text":" site","href":"https://example.org"}]},
                 {"type":"li","spans":[{"text":"milk"}]},
                 {"type":"todo","done":true,"spans":[{"text":"eggs","italic":true}]},
                 {"type":"weird","spans":[]}]}
    """.trimIndent()

    @Test
    fun parsesWhatTheServerSends() {
        val n = NoteDoc.parseNote(JSONObject(serverNote))
        assertEquals("abc12345", n.id)
        assertEquals("mint", n.color)
        assertTrue(n.pinned && n.onDesk && !n.trashed)
        assertEquals(1700000000000L, n.remindAt)
        assertEquals(4, n.body.size)
        assertEquals(NoteBlock.P, n.body[3].type) // unknown kinds read as paragraphs
        assertTrue(n.body[0].spans[1].bold)
        assertEquals("https://example.org", n.body[0].spans[2].href)
        assertEquals("buy today site\n- milk\n[x] eggs\n", NoteDoc.plain(n.body))
        // And back: the same JSON body the server checks.
        val again = NoteDoc.parseBody(NoteDoc.bodyJson(n.body))
        assertEquals(n.body, again)
        val cached = NoteDoc.parseNote(NoteDoc.noteJson(n))
        assertEquals(n, cached)
    }

    @Test
    fun linesAndMarkers() {
        assertEquals(NoteBlock(NoteBlock.LI, false, listOf(NoteSpan("milk"))), NoteDoc.parseLine("- milk"))
        assertEquals(NoteBlock(NoteBlock.TODO, false, listOf(NoteSpan("eggs"))), NoteDoc.parseLine("[ ] eggs"))
        assertEquals(NoteBlock(NoteBlock.TODO, true, listOf(NoteSpan("eggs"))), NoteDoc.parseLine("[x] eggs"))
        assertEquals(NoteBlock(NoteBlock.TODO, false, emptyList()), NoteDoc.parseLine("[] "))
        assertEquals(NoteBlock.P, NoteDoc.parseLine("-milk").type)
        assertEquals(NoteBlock.P, NoteDoc.parseLine("plain - text").type)
    }

    @Test
    fun editingKeepsMarksOnUnchangedLines() {
        val n = NoteDoc.parseNote(JSONObject(serverNote))
        val text = NoteDoc.plain(n.body).replace("- milk", "- oat milk") + "[ ] bread\n\n"
        val out = NoteDoc.fromPlain(text, n.body)
        assertEquals(4, out.size) // the blank line at the end is dropped
        assertEquals(n.body[0], out[0]) // bold and link kept
        assertEquals(NoteBlock(NoteBlock.LI, false, listOf(NoteSpan("oat milk"))), out[1])
        assertEquals(n.body[2], out[2]) // italic kept
        assertEquals(NoteBlock(NoteBlock.TODO, false, listOf(NoteSpan("bread"))), out[3])
        // Ticking an item off in text changes it.
        assertTrue(NoteDoc.fromPlain("[x] buy today site", n.body)[0].done)
    }

    @Test
    fun checklistsTickAndCount() {
        val b = NoteDoc.fromPlain("[ ] a\n[x] b\nc")
        assertEquals(1 to 2, NoteDoc.progress(b))
        val t = NoteDoc.toggle(b, 0)
        assertTrue(t[0].done)
        assertFalse(b[0].done)
        assertEquals(b[2], NoteDoc.toggle(b, 2)[2]) // paragraphs have no box
    }

    @Test
    fun viewsSearchAndTheWidgetNote() {
        val notes = listOf(
            Note("a", "Old", updatedAt = 10),
            Note("b", "Pinned", pinned = true, updatedAt = 1),
            Note("c", "Vault", archived = true, updatedAt = 20),
            Note("d", "Binned", trashedAt = 5, updatedAt = 30),
            Note("e", "", NoteDoc.fromPlain("buy Milk"), updatedAt = 15),
        )
        assertEquals(listOf("b", "e", "a"), NoteDoc.visible(notes, NoteDoc.View.NOTES, "").map { it.id })
        assertEquals(listOf("c"), NoteDoc.visible(notes, NoteDoc.View.ARCHIVE, "").map { it.id })
        assertEquals(listOf("d"), NoteDoc.visible(notes, NoteDoc.View.TRASH, "").map { it.id })
        assertEquals(listOf("e"), NoteDoc.visible(notes, NoteDoc.View.NOTES, "milk BUY").map { it.id })
        assertEquals("b", NoteDoc.forWidget(notes, null)?.id)
        assertEquals("a", NoteDoc.forWidget(notes, "a")?.id)
        assertEquals("b", NoteDoc.forWidget(notes, "d")?.id) // a trashed pick falls back
        assertNull(NoteDoc.forWidget(emptyList(), null))
        assertEquals("buy Milk", NoteDoc.title(notes[4]))
        assertTrue(NoteDoc.isEmpty(Note("x", body = NoteDoc.fromPlain(""))))
        assertEquals(32, NoteDoc.newId().length)
    }

    @Test
    fun changesSendOnlyWhatChanged() {
        val c = NoteChange(pinned = true, remindAt = 0)
        val j = c.json()
        assertEquals(setOf("pinned", "remind_at"), j.keySet())
        assertTrue(j.isNull("remind_at"))
        val n = Note("a", "t", remindAt = 99, reminded = true, onDesk = true, updatedAt = 1)
        val out = c.applyTo(n, 50)
        assertTrue(out.pinned)
        assertNull(out.remindAt)
        assertEquals(50, out.updatedAt)
        val trashed = NoteChange(trashed = true).applyTo(n, 60)
        assertEquals(60L, trashed.trashedAt)
        assertFalse(trashed.onDesk)
        assertEquals(n, NoteChange(pinned = false).applyTo(n, 70)) // nothing changed
        assertEquals(JSONArray::class.java, NoteChange(body = NoteDoc.fromPlain("x")).json().get("body").javaClass)
    }

    @Test
    fun longPollCarriesNotesAndReminders() {
        val (ev, _) = parseEvents("""{"cursor":9,"notices":[],"clipboard":[],"dismissed":[],"signins":[],"actions":[],
            "notes":true,"reminders":[{"id":8,"note_id":"abc12345","title":"Call mom","text":"about sunday"}]}""")
        assertTrue(ev.notes)
        assertEquals(1, ev.reminders.size)
        assertEquals("abc12345", ev.reminders[0].noteId)
        assertEquals("Call mom", ev.reminders[0].title)
        val (old, _) = parseEvents("""{"cursor":9}""")
        assertFalse(old.notes)
        assertTrue(old.reminders.isEmpty())
    }
}
