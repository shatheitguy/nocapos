package com.nocap.link.widget

import android.app.PendingIntent
import android.appwidget.AppWidgetManager
import android.appwidget.AppWidgetProvider
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.provider.AlarmClock
import android.widget.RemoteViews
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.NetworkType
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkerParameters
import androidx.work.WorkManager
import com.nocap.link.ai.AiActivity
import com.nocap.link.data.Opt
import com.nocap.link.data.Prefs
import com.nocap.link.data.t
import com.nocap.link.desktop.DesktopActivity
import com.nocap.link.MainActivity
import com.nocap.link.net.Summary
import com.nocap.link.R
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/**
 * NoCap OS widgets for any home screen (including other launchers):
 * clock, weather, NoCap status, NoCap Link actions and search. Plain
 * RemoteViews, refreshed by the app and a light background job.
 */
object AppWidgets {
    private fun pi(ctx: Context, code: Int, i: Intent): PendingIntent =
        PendingIntent.getActivity(ctx, code, i.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK), PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)

    private fun main(ctx: Context, action: String) = Intent(ctx, MainActivity::class.java).setAction(action)

    private fun ids(ctx: Context, cls: Class<*>): IntArray =
        runCatching { AppWidgetManager.getInstance(ctx)?.getAppWidgetIds(ComponentName(ctx, cls)) }.getOrNull() ?: IntArray(0)

    /** Pushes one widget; a launcher that went away or a too-big update must not crash us. */
    private fun push(m: AppWidgetManager, id: Int, v: RemoteViews) {
        runCatching { m.updateAppWidget(id, v) }.onFailure { com.nocap.link.system.CrashLog.warn("home-screen widget", it) }
    }

    /** Remembers the latest server numbers (nothing secret) so the status widget can show them. */
    fun saveServer(ctx: Context, s: Summary, name: String) {
        val sp = ctx.getSharedPreferences("nocap_widgets", Context.MODE_PRIVATE)
        // The app refreshes every 10-30 s; other home screens only need a redraw
        // when a number moved or a few minutes passed (widget updates are rate-limited).
        val same = sp.getInt("cpu", -1) == s.cpuPercent.toInt() && sp.getInt("ram", -1) == s.memory.percent.toInt() &&
            sp.getInt("disk", -1) == s.storage.percent.toInt() && sp.getInt("updates", -1) == s.updates
        if (same && System.currentTimeMillis() - sp.getLong("at", 0) < 5 * 60_000L) return
        sp.edit()
            .putString("name", name.ifBlank { s.server.name })
            .putInt("cpu", s.cpuPercent.toInt()).putInt("ram", s.memory.percent.toInt()).putInt("disk", s.storage.percent.toInt())
            .putInt("updates", s.updates).putBoolean("ready", s.ready).putLong("at", System.currentTimeMillis()).apply()
        updateStatus(ctx)
    }

    fun updateAll(ctx: Context) {
        runCatching { updateClock(ctx); updateStatus(ctx); updateLink(ctx); updateSearch(ctx); updateWeather(ctx, null) }
    }

    fun updateClock(ctx: Context) {
        Prefs.init(ctx)
        val m = AppWidgetManager.getInstance(ctx) ?: return
        for (id in ids(ctx, ClockAppWidget::class.java)) {
            val v = RemoteViews(ctx.packageName, R.layout.widget_clock)
            v.setOnClickPendingIntent(R.id.w_root, pi(ctx, 31, Intent(AlarmClock.ACTION_SHOW_ALARMS)))
            push(m, id, v)
        }
    }

    fun updateStatus(ctx: Context) {
        Prefs.init(ctx)
        val m = AppWidgetManager.getInstance(ctx) ?: return
        val sp = ctx.getSharedPreferences("nocap_widgets", Context.MODE_PRIVATE)
        val linked = runCatching { com.nocap.link.data.Vault.get(ctx).link != null }.getOrDefault(false)
        val fresh = System.currentTimeMillis() - sp.getLong("at", 0) < 30 * 60_000L
        for (id in ids(ctx, StatusAppWidget::class.java)) {
            val v = RemoteViews(ctx.packageName, R.layout.widget_status)
            v.setTextViewText(R.id.w_title, t("NOCAP STATUS", "NOCAP STATUS"))
            v.setTextViewText(R.id.w_name, if (!linked) t("Not linked yet", "Not linked") else sp.getString("name", "NoCapOS")?.ifBlank { "NoCapOS" })
            val state = when {
                !linked -> t("Tap to link", "Tap to link")
                !fresh -> t("Last seen a while ago", "Not updated recently")
                sp.getInt("updates", 0) > 0 -> t("${sp.getInt("updates", 0)} glow ups waiting", "${sp.getInt("updates", 0)} updates available")
                else -> t("Online", "Online")
            }
            v.setTextViewText(R.id.w_state, state)
            fun row(bar: Int, txt: Int, label: String, value: Int) {
                v.setProgressBar(bar, 100, value.coerceIn(0, 100), false)
                v.setTextViewText(txt, "$label ${value}%")
            }
            row(R.id.w_cpu_bar, R.id.w_cpu, "CPU", sp.getInt("cpu", 0))
            row(R.id.w_ram_bar, R.id.w_ram, "RAM", sp.getInt("ram", 0))
            row(R.id.w_disk_bar, R.id.w_disk, t("Disk", "Storage"), sp.getInt("disk", 0))
            v.setOnClickPendingIntent(R.id.w_root, pi(ctx, 32, main(ctx, MainActivity.ACTION_HUB)))
            push(m, id, v)
        }
    }

    fun updateLink(ctx: Context) {
        Prefs.init(ctx)
        val m = AppWidgetManager.getInstance(ctx) ?: return
        for (id in ids(ctx, LinkAppWidget::class.java)) {
            val v = RemoteViews(ctx.packageName, R.layout.widget_link)
            v.setTextViewText(R.id.w_a1, t("Desktop", "Desktop"))
            v.setTextViewText(R.id.w_a2, t("Ask AI", "NoCap AI"))
            v.setTextViewText(R.id.w_a3, t("Send", "Send file"))
            v.setTextViewText(R.id.w_a4, t("Files", "Files"))
            v.setOnClickPendingIntent(R.id.w_b1, pi(ctx, 41, Intent(ctx, DesktopActivity::class.java)))
            v.setOnClickPendingIntent(R.id.w_b2, pi(ctx, 42, Intent(ctx, AiActivity::class.java)))
            v.setOnClickPendingIntent(R.id.w_b3, pi(ctx, 43, main(ctx, MainActivity.ACTION_SEND_FILE)))
            v.setOnClickPendingIntent(R.id.w_b4, pi(ctx, 44, main(ctx, MainActivity.ACTION_HUB)))
            push(m, id, v)
        }
    }

    fun updateSearch(ctx: Context) {
        Prefs.init(ctx)
        val m = AppWidgetManager.getInstance(ctx) ?: return
        for (id in ids(ctx, SearchAppWidget::class.java)) {
            val v = RemoteViews(ctx.packageName, R.layout.widget_search)
            v.setTextViewText(R.id.w_text, t("Search apps, files, settings…", "Search apps, files and settings"))
            v.setOnClickPendingIntent(R.id.w_root, pi(ctx, 51, main(ctx, MainActivity.ACTION_SEARCH)))
            v.setOnClickPendingIntent(R.id.w_ai, pi(ctx, 52, Intent(ctx, AiActivity::class.java)))
            push(m, id, v)
        }
    }

    /** Draws the weather widget from the last report (fetching one first when [fetch]). */
    fun updateWeather(ctx: Context, report: Weather.Report?) {
        Prefs.init(ctx)
        val m = AppWidgetManager.getInstance(ctx) ?: return
        val place = Weather.Place.decode(Opt.weatherPlace.v)
        val r = report ?: place?.let { Weather.cached(it, Opt.fahrenheit.v) }
        for (id in ids(ctx, WeatherAppWidget::class.java)) {
            val v = RemoteViews(ctx.packageName, R.layout.widget_weather)
            if (r == null) {
                v.setTextViewText(R.id.w_temp, "--°")
                v.setTextViewText(R.id.w_place, place?.name ?: t("Pick a place in NoCap OS", "Set a place in NoCap OS"))
                v.setTextViewText(R.id.w_desc, t("Widgets → Weather", "Settings → Widgets"))
                v.setTextViewText(R.id.w_hilo, "")
            } else {
                v.setImageViewBitmap(R.id.w_icon, Weather.bitmap(144, Weather.kind(r.code, r.isDay), 0xFFFF2A2A.toInt()))
                v.setTextViewText(R.id.w_temp, Weather.deg(r.temp))
                v.setTextViewText(R.id.w_place, r.place.name)
                v.setTextViewText(R.id.w_desc, Weather.describe(r.code))
                v.setTextViewText(R.id.w_hilo, "↑${Weather.deg(r.hi)}  ↓${Weather.deg(r.lo)}")
            }
            v.setOnClickPendingIntent(R.id.w_root, pi(ctx, 61, Intent(ctx, MainActivity::class.java)))
            push(m, id, v)
        }
    }

    fun hasWeather(ctx: Context) = ids(ctx, WeatherAppWidget::class.java).isNotEmpty()

    fun scheduleWeather(ctx: Context) {
        val net = Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build()
        WorkManager.getInstance(ctx).enqueueUniquePeriodicWork(
            "nocap-weather", ExistingPeriodicWorkPolicy.KEEP,
            PeriodicWorkRequestBuilder<WeatherWorker>(45, TimeUnit.MINUTES).setConstraints(net).build(),
        )
    }
}

/** Keeps the weather widget fresh (only while one is on a home screen). */
class WeatherWorker(ctx: Context, params: WorkerParameters) : CoroutineWorker(ctx, params) {
    override suspend fun doWork(): Result = withContext(Dispatchers.IO) {
        Prefs.init(applicationContext)
        if (!AppWidgets.hasWeather(applicationContext)) {
            WorkManager.getInstance(applicationContext).cancelUniqueWork("nocap-weather")
            return@withContext Result.success()
        }
        val place = Weather.Place.decode(Opt.weatherPlace.v) ?: return@withContext Result.success()
        val r = runCatching { Weather.forecast(place, Opt.fahrenheit.v, force = true) }.getOrNull()
        withContext(Dispatchers.Main) { AppWidgets.updateWeather(applicationContext, r) }
        Result.success()
    }
}

class ClockAppWidget : AppWidgetProvider() {
    override fun onUpdate(ctx: Context, manager: AppWidgetManager, ids: IntArray) = AppWidgets.updateClock(ctx)
}

class StatusAppWidget : AppWidgetProvider() {
    override fun onUpdate(ctx: Context, manager: AppWidgetManager, ids: IntArray) = AppWidgets.updateStatus(ctx)
}

class LinkAppWidget : AppWidgetProvider() {
    override fun onUpdate(ctx: Context, manager: AppWidgetManager, ids: IntArray) = AppWidgets.updateLink(ctx)
}

class SearchAppWidget : AppWidgetProvider() {
    override fun onUpdate(ctx: Context, manager: AppWidgetManager, ids: IntArray) = AppWidgets.updateSearch(ctx)
}

class WeatherAppWidget : AppWidgetProvider() {
    override fun onUpdate(ctx: Context, manager: AppWidgetManager, ids: IntArray) {
        AppWidgets.updateWeather(ctx, null)
        AppWidgets.scheduleWeather(ctx)
    }
}
