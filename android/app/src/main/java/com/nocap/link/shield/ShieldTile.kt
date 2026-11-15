package com.nocap.link.shield

import android.app.PendingIntent
import android.appwidget.AppWidgetManager
import android.appwidget.AppWidgetProvider
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.os.Build
import android.service.quicksettings.Tile
import android.service.quicksettings.TileService
import android.widget.RemoteViews
import com.nocap.link.R
import com.nocap.link.data.Prefs
import com.nocap.link.data.t
import com.nocap.link.system.CrashLog

/** Quick-settings tile: Shield on/off (the first time it opens the Shield page for Android's VPN prompt). */
class ShieldTile : TileService() {
    override fun onStartListening() {
        super.onStartListening()
        draw()
    }

    private fun draw() {
        runCatching {
            Prefs.init(this)
            ShieldStore.init(this)
            val tile = qsTile ?: return
            val on = ShieldStore.enabled
            tile.state = if (on) Tile.STATE_ACTIVE else Tile.STATE_INACTIVE
            tile.label = t("Shield", "Shield")
            if (Build.VERSION.SDK_INT >= 29) {
                tile.subtitle = if (on) t("${ShieldStore.blockedToday()} blocked", "${ShieldStore.blockedToday()} blocked") else t("Off", "Off")
            }
            tile.updateTile()
        }
    }

    override fun onClick() {
        super.onClick()
        ShieldStore.init(this)
        if (ShieldStore.enabled) {
            Shield.stop(this)
            draw()
            return
        }
        if (Shield.start(this)) {
            draw()
            return
        }
        // Android's VPN prompt has to come from a screen: open the Shield page.
        val i = Intent(this, ShieldActivity::class.java).setAction(ShieldActivity.ACTION_TURN_ON).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        runCatching {
            if (Build.VERSION.SDK_INT >= 34) {
                startActivityAndCollapse(PendingIntent.getActivity(this, 0, i, PendingIntent.FLAG_IMMUTABLE))
            } else {
                @Suppress("DEPRECATION", "StartActivityAndCollapseDeprecated")
                startActivityAndCollapse(i)
            }
        }.onFailure { CrashLog.warn("shield tile", it) }
    }
}

/** Home-screen widget: Shield's state, today's count, and an on/off button. */
class ShieldWidget : AppWidgetProvider() {
    override fun onUpdate(ctx: Context, manager: AppWidgetManager, ids: IntArray) = update(ctx)

    override fun onReceive(ctx: Context, intent: Intent) {
        if (intent.action == ACTION_TOGGLE) {
            ShieldStore.init(ctx)
            if (ShieldStore.enabled) {
                Shield.stop(ctx)
            } else if (!Shield.start(ctx)) {
                runCatching {
                    ctx.startActivity(Intent(ctx, ShieldActivity::class.java).setAction(ShieldActivity.ACTION_TURN_ON).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
                }
            }
            update(ctx)
            return
        }
        super.onReceive(ctx, intent)
    }

    companion object {
        const val ACTION_TOGGLE = "com.nocap.link.shield.TOGGLE"

        fun update(ctx: Context) {
            runCatching {
                Prefs.init(ctx)
                ShieldStore.init(ctx)
                val m = AppWidgetManager.getInstance(ctx) ?: return
                val ids = m.getAppWidgetIds(ComponentName(ctx, ShieldWidget::class.java))
                if (ids.isEmpty()) return
                val on = ShieldStore.enabled
                val v = RemoteViews(ctx.packageName, R.layout.widget_shield)
                v.setTextViewText(R.id.w_title, "NOCAP SHIELD")
                v.setTextViewText(R.id.w_state, if (on) t("Locked in", "On") else t("Chilling (off)", "Off"))
                val n = ShieldStore.blockedToday()
                v.setTextViewText(R.id.w_count, if (on) t("$n yeeted today", "$n blocked today") else t("Tap to turn it on", "Tap to turn on"))
                v.setTextViewText(R.id.w_toggle, if (on) t("Off", "Turn off") else t("On", "Turn on"))
                val open = PendingIntent.getActivity(ctx, 61, Intent(ctx, ShieldActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK), PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
                val toggle = PendingIntent.getBroadcast(ctx, 62, Intent(ctx, ShieldWidget::class.java).setAction(ACTION_TOGGLE), PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
                v.setOnClickPendingIntent(R.id.w_root, open)
                v.setOnClickPendingIntent(R.id.w_toggle, toggle)
                ids.forEach { m.updateAppWidget(it, v) }
            }.onFailure { CrashLog.warn("shield widget", it) }
        }
    }
}
