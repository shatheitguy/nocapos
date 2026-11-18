package com.nocap.link.notes

import android.content.Context
import android.content.Intent
import com.nocap.link.data.t
import com.nocap.link.net.NoCapApi
import com.nocap.link.sync.Notices

/** Sticky Notes reminders from the server, as phone notifications that open the note. */
object NoteReminders {
    /** One notification per note (a newer reminder for it replaces the old one). */
    fun notificationId(noteId: String): Int = 20_000 + (noteId.hashCode() and 0x7fffffff) % 5_000

    fun show(ctx: Context, r: NoCapApi.NoteReminder) {
        val name = r.title.ifBlank { r.text }.ifBlank { t("Untitled note", "Untitled note") }
        val body = if (r.title.isNotBlank()) r.text else ""
        val open = Intent(ctx, NotesActivity::class.java).putExtra(NotesActivity.EXTRA_NOTE, r.noteId).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        Notices.show(ctx, notificationId(r.noteId), t("Don't forget: $name", "Reminder: $name"), body, open)
    }
}
