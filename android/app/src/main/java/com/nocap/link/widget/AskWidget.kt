package com.nocap.link.widget

import android.app.PendingIntent
import android.appwidget.AppWidgetManager
import android.appwidget.AppWidgetProvider
import android.content.Context
import android.content.Intent
import android.widget.RemoteViews
import com.nocap.link.ai.AiActivity
import com.nocap.link.data.Prefs
import com.nocap.link.data.t
import com.nocap.link.R

/** "Ask NoCap AI" for any home screen: one tap opens NoCap AI. */
class AskWidget : AppWidgetProvider() {
    override fun onUpdate(ctx: Context, manager: AppWidgetManager, ids: IntArray) {
        Prefs.init(ctx)
        val open = PendingIntent.getActivity(
            ctx, 7, Intent(ctx, AiActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        for (id in ids) {
            val v = RemoteViews(ctx.packageName, R.layout.widget_ask)
            v.setTextViewText(R.id.ask_text, t("Ask NoCap AI anything…", "Ask NoCap AI…"))
            v.setOnClickPendingIntent(R.id.ask_root, open)
            manager.updateAppWidget(id, v)
        }
    }
}
