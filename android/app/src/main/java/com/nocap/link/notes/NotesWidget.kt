package com.nocap.link.notes

import android.app.PendingIntent
import android.appwidget.AppWidgetManager
import android.appwidget.AppWidgetProvider
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.widget.RemoteViews
import com.nocap.link.R
import com.nocap.link.data.Prefs
import com.nocap.link.data.t

/**
 * The Sticky Notes home-screen widget: your pinned note (or the latest one)
 * on glass with a NoCap tint, ticked items struck through. Tapping it opens
 * the note on the phone.
 */
object NotesWidget {
    private fun ids(ctx: Context): IntArray =
        runCatching { AppWidgetManager.getInstance(ctx)?.getAppWidgetIds(ComponentName(ctx, NoteAppWidget::class.java)) }.getOrNull() ?: IntArray(0)

    /** The lines as the widget shows them: ☐ / ☑ for checklists, • for bullets. */
    fun lines(n: Note, max: Int = 12): String {
        val body = if (n.title.isBlank()) n.body.drop(1) else n.body
        return body.take(max).joinToString("\n") { b ->
            when (b.type) {
                NoteBlock.TODO -> (if (b.done) "☑ " else "☐ ") + b.text
                NoteBlock.LI -> "• " + b.text
                else -> b.text
            }
        }
    }

    fun update(ctx: Context) {
        val ids = ids(ctx)
        if (ids.isEmpty()) return
        Prefs.init(ctx)
        val m = AppWidgetManager.getInstance(ctx) ?: return
        val list = NotesStore.notes.ifEmpty { NotesStore.readCache(ctx) }
        val n = NoteDoc.forWidget(list, null)
        for (id in ids) {
            val v = RemoteViews(ctx.packageName, R.layout.widget_note)
            v.setTextViewText(R.id.w_title, t("BRAIN DUMP", "STICKY NOTES"))
            if (n == null) {
                v.setTextViewText(R.id.w_name, t("No notes yet", "No notes yet"))
                v.setTextViewText(R.id.w_text, t("Tap to dump a thought.", "Tap to write one."))
            } else {
                v.setTextViewText(R.id.w_name, NoteDoc.title(n, 60).ifEmpty { t("Untitled note", "Untitled note") })
                v.setTextViewText(R.id.w_text, lines(n))
            }
            v.setInt(R.id.w_tint, "setColorFilter", NoteDoc.tint(n?.color ?: "red").toInt())
            v.setInt(R.id.w_tint, "setImageAlpha", if (n == null || n.color == "glass") 40 else 90)
            val open = Intent(ctx, NotesActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            if (n != null) open.putExtra(NotesActivity.EXTRA_NOTE, n.id)
            v.setOnClickPendingIntent(R.id.w_root, PendingIntent.getActivity(ctx, 71, open, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT))
            runCatching { m.updateAppWidget(id, v) }.onFailure { com.nocap.link.system.CrashLog.warn("notes widget", it) }
        }
    }
}

class NoteAppWidget : AppWidgetProvider() {
    override fun onUpdate(ctx: Context, manager: AppWidgetManager, ids: IntArray) = NotesWidget.update(ctx)
}
