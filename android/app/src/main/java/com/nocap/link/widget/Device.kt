package com.nocap.link.widget

import android.app.ActivityManager
import android.content.ContentUris
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.PackageManager
import android.hardware.camera2.CameraCharacteristics
import android.hardware.camera2.CameraManager
import android.Manifest
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.net.TrafficStats
import android.net.Uri
import android.os.BatteryManager
import android.os.Environment
import android.os.Handler
import android.os.Looper
import android.os.StatFs
import android.os.SystemClock
import android.provider.CalendarContract
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.core.content.ContextCompat
import java.io.File

/** This phone's vitals for the status widgets: battery, memory, storage, network. */
object DeviceStats {
    data class Snap(
        val battery: Int, val charging: Boolean, val tempC: Float, val ramUsed: Long, val ramTotal: Long,
        val diskUsed: Long, val diskTotal: Long, val net: String, val online: Boolean, val down: Double, val up: Double, val uptime: Long,
    ) {
        val ramPct get() = if (ramTotal > 0) ramUsed * 100.0 / ramTotal else 0.0
        val diskPct get() = if (diskTotal > 0) diskUsed * 100.0 / diskTotal else 0.0
    }

    var snap by mutableStateOf<Snap?>(null)
        private set
    /** Recent network speed (bytes/s, down + up), newest last. */
    val netHist = mutableStateListOf<Float>()
    private var last = 0L
    private var lastRx = -1L
    private var lastTx = -1L

    /** Takes a fresh reading (at most every 2 s, however many widgets ask). */
    fun refresh(ctx: Context) {
        val now = SystemClock.elapsedRealtime()
        if (now - last < 1800 && snap != null) return
        val dt = (now - last).coerceAtLeast(1) / 1000.0
        last = now
        val b = runCatching { ctx.applicationContext.registerReceiver(null, IntentFilter(Intent.ACTION_BATTERY_CHANGED)) }.getOrNull()
        val level = b?.let { val l = it.getIntExtra(BatteryManager.EXTRA_LEVEL, -1); val s = it.getIntExtra(BatteryManager.EXTRA_SCALE, 100); if (l >= 0 && s > 0) l * 100 / s else -1 } ?: -1
        val status = b?.getIntExtra(BatteryManager.EXTRA_STATUS, -1) ?: -1
        val charging = status == BatteryManager.BATTERY_STATUS_CHARGING || status == BatteryManager.BATTERY_STATUS_FULL
        val temp = (b?.getIntExtra(BatteryManager.EXTRA_TEMPERATURE, 0) ?: 0) / 10f
        val mi = ActivityManager.MemoryInfo()
        runCatching { ctx.getSystemService(ActivityManager::class.java).getMemoryInfo(mi) }
        val st = runCatching { StatFs(Environment.getDataDirectory().path) }.getOrNull()
        val total = st?.totalBytes ?: 0
        val free = st?.availableBytes ?: 0
        val cm = ctx.getSystemService(ConnectivityManager::class.java)
        val caps = runCatching { cm?.getNetworkCapabilities(cm.activeNetwork) }.getOrNull()
        val net = when {
            caps == null -> "offline"
            caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) -> "wifi"
            caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) -> "cell"
            caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET) -> "ethernet"
            caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN) -> "vpn"
            else -> "other"
        }
        val rx = runCatching { TrafficStats.getTotalRxBytes() }.getOrDefault(-1L)
        val tx = runCatching { TrafficStats.getTotalTxBytes() }.getOrDefault(-1L)
        var down = 0.0
        var up = 0.0
        if (lastRx >= 0 && lastTx >= 0 && rx >= lastRx && tx >= lastTx) { down = (rx - lastRx) / dt; up = (tx - lastTx) / dt }
        lastRx = rx; lastTx = tx
        if (snap != null) {
            netHist.add((down + up).toFloat())
            while (netHist.size > 40) netHist.removeAt(0)
        }
        snap = Snap(level, charging, temp, mi.totalMem - mi.availMem, mi.totalMem, total - free, total, net, caps != null, down, up, SystemClock.elapsedRealtime())
    }
}

/** The flashlight, with its on/off state kept in sync with the system. */
object Torch {
    var on by mutableStateOf(false)
        private set
    private var id: String? = null
    private var registered = false

    fun available(ctx: Context): Boolean = camera(ctx) != null

    private fun camera(ctx: Context): String? {
        id?.let { return it }
        val cm = ctx.applicationContext.getSystemService(CameraManager::class.java) ?: return null
        id = runCatching { cm.cameraIdList.firstOrNull { cm.getCameraCharacteristics(it).get(CameraCharacteristics.FLASH_INFO_AVAILABLE) == true } }.getOrNull()
        if (!registered && id != null) {
            registered = runCatching {
                cm.registerTorchCallback(object : CameraManager.TorchCallback() {
                    override fun onTorchModeChanged(cameraId: String, enabled: Boolean) { if (cameraId == id) on = enabled }
                }, Handler(Looper.getMainLooper()))
            }.isSuccess
        }
        return id
    }

    fun toggle(ctx: Context): Boolean {
        val c = camera(ctx) ?: return false
        return runCatching { ctx.applicationContext.getSystemService(CameraManager::class.java).setTorchMode(c, !on) }.isSuccess
    }
}

/** Your calendar's next events (only with the calendar permission you grant). */
object Agenda {
    data class Event(val id: Long, val title: String, val begin: Long, val end: Long, val allDay: Boolean, val color: Int, val where: String)

    fun allowed(ctx: Context) = ContextCompat.checkSelfPermission(ctx, Manifest.permission.READ_CALENDAR) == PackageManager.PERMISSION_GRANTED

    /** Blocking (a content-provider query): call it off the main thread. Empty if access was revoked. */
    fun upcoming(ctx: Context, days: Int, allDay: Boolean, limit: Int = 12): List<Event> {
        if (!allowed(ctx)) return emptyList()
        val now = System.currentTimeMillis()
        val b = CalendarContract.Instances.CONTENT_URI.buildUpon()
        ContentUris.appendId(b, now - 60 * 60_000L)
        ContentUris.appendId(b, now + days.coerceIn(1, 366) * 86_400_000L)
        val proj = arrayOf(
            CalendarContract.Instances.EVENT_ID, CalendarContract.Instances.TITLE, CalendarContract.Instances.BEGIN,
            CalendarContract.Instances.END, CalendarContract.Instances.ALL_DAY, CalendarContract.Instances.DISPLAY_COLOR,
            CalendarContract.Instances.EVENT_LOCATION,
        )
        val out = mutableListOf<Event>()
        runCatching {
            ctx.contentResolver.query(b.build(), proj, "${CalendarContract.Instances.VISIBLE} = 1", null, "${CalendarContract.Instances.BEGIN} ASC")?.use { c ->
                while (c.moveToNext() && out.size < limit) {
                    val e = Event(c.getLong(0), c.getString(1) ?: "", c.getLong(2), c.getLong(3), c.getInt(4) == 1, c.getInt(5), c.getString(6) ?: "")
                    if (e.end < now) continue
                    if (e.allDay && !allDay) continue
                    out += e
                }
            }
        }
        return out
    }

    fun open(ctx: Context, e: Event) {
        val i = Intent(Intent.ACTION_VIEW, ContentUris.withAppendedId(CalendarContract.Events.CONTENT_URI, e.id))
            .putExtra(CalendarContract.EXTRA_EVENT_BEGIN_TIME, e.begin).putExtra(CalendarContract.EXTRA_EVENT_END_TIME, e.end)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        runCatching { ctx.startActivity(i) }
    }

    fun openCalendar(ctx: Context) {
        val u = CalendarContract.CONTENT_URI.buildUpon().appendPath("time").build()
        runCatching { ctx.startActivity(Intent(Intent.ACTION_VIEW, ContentUris.withAppendedId(u, System.currentTimeMillis())).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
    }

    fun add(ctx: Context) {
        runCatching { ctx.startActivity(Intent(Intent.ACTION_INSERT, CalendarContract.Events.CONTENT_URI).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
    }
}

/** Photo-frame pictures, copied into app storage (scaled down). */
object Photos {
    fun dir(ctx: Context) = File(ctx.filesDir, "photos").apply { mkdirs() }

    fun save(ctx: Context, uri: Uri): String? = runCatching {
        val o = android.graphics.BitmapFactory.Options().apply { inJustDecodeBounds = true }
        ctx.contentResolver.openInputStream(uri)?.use { android.graphics.BitmapFactory.decodeStream(it, null, o) }
        var sample = 1
        while (o.outWidth / (sample * 2) >= 1440 || o.outHeight / (sample * 2) >= 1440) sample *= 2
        val bmp = ctx.contentResolver.openInputStream(uri)?.use { android.graphics.BitmapFactory.decodeStream(it, null, android.graphics.BitmapFactory.Options().apply { inSampleSize = sample }) } ?: return null
        val name = "p" + System.nanoTime().toString(36) + ".jpg"
        File(dir(ctx), name).outputStream().use { bmp.compress(android.graphics.Bitmap.CompressFormat.JPEG, 88, it) }
        name
    }.getOrNull()

    /** Deletes pictures no photo frame uses anymore. */
    fun prune(ctx: Context, keep: Set<String>) {
        dir(ctx).listFiles()?.filter { it.name !in keep }?.forEach { it.delete(); cache.remove(it.name) }
    }

    /** Recently shown pictures, so rotating frames don't decode the same file again (sized by bytes). */
    private val cache = object : android.util.LruCache<String, android.graphics.Bitmap>(
        (Runtime.getRuntime().maxMemory() / 16).coerceIn(4L shl 20, 48L shl 20).toInt(),
    ) {
        override fun sizeOf(key: String, value: android.graphics.Bitmap) = value.byteCount
    }

    /**
     * A saved picture scaled down to about [maxPx] on its longer side (blocking: call it
     * off the main thread). Null if the file is gone or unreadable.
     */
    fun load(ctx: Context, name: String, maxPx: Int = 1080): android.graphics.Bitmap? {
        cache.get(name)?.let { return it }
        return runCatching {
            val f = File(dir(ctx), name)
            if (!f.isFile) return null
            val o = android.graphics.BitmapFactory.Options().apply { inJustDecodeBounds = true }
            android.graphics.BitmapFactory.decodeFile(f.path, o)
            if (o.outWidth <= 0 || o.outHeight <= 0) return null
            var sample = 1
            val target = maxPx.coerceAtLeast(64)
            while (maxOf(o.outWidth, o.outHeight) / (sample * 2) >= target) sample *= 2
            android.graphics.BitmapFactory.decodeFile(f.path, android.graphics.BitmapFactory.Options().apply { inSampleSize = sample })
        }.getOrNull()?.also { cache.put(name, it) }
    }
}
