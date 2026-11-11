package com.nocap.link.sync

import android.app.Activity
import android.app.Notification
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.Canvas
import android.os.Bundle
import android.service.notification.StatusBarNotification
import android.service.quicksettings.TileService
import android.util.Base64
import android.widget.Toast
import com.nocap.link.MainActivity
import com.nocap.link.Notices
import com.nocap.link.Opt
import com.nocap.link.Prefs
import com.nocap.link.Vault
import com.nocap.link.net.ApiError
import com.nocap.link.net.CertChanged
import com.nocap.link.net.NoCapApi
import com.nocap.link.net.ServerTooOld
import com.nocap.link.system.DotsService
import com.nocap.link.t
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.ByteArrayOutputStream
import java.util.concurrent.Executors

/**
 * NoCap Link sync: this phone's notifications → your NoCap OS (opt-in, per
 * app), NoCap OS notifications and clipboard → this phone, and this phone's
 * clipboard → NoCap OS. Everything goes only to your own linked server.
 */
object Sync {
    private val io = Executors.newSingleThreadExecutor()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private var loop: Job? = null
    /** Last notification content sent per key, so updates that change nothing aren't re-sent. */
    private val sent = HashMap<String, Int>()
    private var lastClipHash = 0
    @Volatile private var ourClip: String? = null

    private fun api(ctx: Context): NoCapApi? = Vault.get(ctx).link?.let { NoCapApi(it) }

    // ---------- phone notifications → NoCap OS ----------

    fun forwardable(ctx: Context, sbn: StatusBarNotification): Boolean {
        if (!Opt.forward.v || sbn.packageName == ctx.packageName) return false
        if (sbn.packageName !in Opt.forwardApps.v) return false
        val n = sbn.notification
        if (!sbn.isClearable || sbn.isOngoing) return false
        if (n.flags and Notification.FLAG_GROUP_SUMMARY != 0) return false
        return true
    }

    fun posted(ctx: Context, sbn: StatusBarNotification) {
        Prefs.init(ctx)
        if (!forwardable(ctx, sbn)) return
        val api = api(ctx) ?: return
        val ex = sbn.notification.extras
        val title = ex.getCharSequence(Notification.EXTRA_TITLE)?.toString().orEmpty()
        val text = (ex.getCharSequence(Notification.EXTRA_BIG_TEXT) ?: ex.getCharSequence(Notification.EXTRA_TEXT))?.toString().orEmpty()
        if (title.isBlank() && text.isBlank()) return
        val hash = (title + "\u0000" + text).hashCode()
        synchronized(sent) {
            if (sent[sbn.key] == hash) return
            sent[sbn.key] = hash
        }
        val pm = ctx.packageManager
        val label = runCatching { pm.getApplicationLabel(pm.getApplicationInfo(sbn.packageName, 0)).toString() }.getOrDefault(sbn.packageName)
        io.execute {
            runCatching {
                api.postPhoneNotification(sbn.key, label, sbn.packageName, title, text, icon(ctx, sbn.packageName), sbn.postTime)
            }
        }
    }

    fun removed(ctx: Context, sbn: StatusBarNotification) {
        val had = synchronized(sent) { sent.remove(sbn.key) != null }
        if (!had || !Opt.forward.v) return
        val api = api(ctx) ?: return
        io.execute { runCatching { api.deletePhoneNotification(sbn.key) } }
    }

    private val icons = HashMap<String, String?>()

    private fun icon(ctx: Context, pkg: String): String? = synchronized(icons) {
        icons.getOrPut(pkg) {
            runCatching {
                val d = ctx.packageManager.getApplicationIcon(pkg)
                val b = Bitmap.createBitmap(64, 64, Bitmap.Config.ARGB_8888)
                d.setBounds(0, 0, 64, 64)
                d.draw(Canvas(b))
                val out = ByteArrayOutputStream()
                b.compress(Bitmap.CompressFormat.PNG, 100, out)
                Base64.encodeToString(out.toByteArray(), Base64.NO_WRAP)
            }.getOrNull()
        }
    }

    // ---------- NoCap OS → phone (long-poll while the app runs) ----------

    /** Starts (or restarts) listening to your server, if anything needs it. */
    fun start(ctx: Context) {
        val app = ctx.applicationContext
        Prefs.init(app)
        loop?.cancel()
        if (Vault.get(app).link == null) return
        if (!Opt.liveNotices.v && !Opt.clipSync.v && !Opt.forward.v) return
        loop = scope.launch { listen(app) }
    }

    fun stop() { loop?.cancel(); loop = null }

    private suspend fun listen(ctx: Context) {
        var cursor = 0L
        var backoff = 5_000L
        while (isActiveSafe()) {
            val api = api(ctx) ?: return
            try {
                val ev = api.events(cursor, 25)
                backoff = 5_000L
                if (cursor != 0L) handle(ctx, ev)
                cursor = ev.cursor
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: ServerTooOld) {
                return // older server: the 15-minute heartbeat still brings notices
            } catch (e: ApiError) {
                if (e.status == 401) return
                delay(backoff); backoff = (backoff * 2).coerceAtMost(300_000L)
            } catch (e: CertChanged) {
                return
            } catch (_: Exception) {
                delay(backoff); backoff = (backoff * 2).coerceAtMost(300_000L)
            }
        }
    }

    private suspend fun isActiveSafe() = kotlin.coroutines.coroutineContext.isActive

    private suspend fun handle(ctx: Context, ev: NoCapApi.Events) {
        if (Opt.liveNotices.v) {
            val last = Prefs.lastNotice
            for (n in ev.notices.filter { it.id > last }.sortedBy { it.id }.takeLast(5)) {
                val title = (if (Prefs.genz) n.genzTitle else null) ?: n.title
                val body = (if (Prefs.genz) n.genzBody else null) ?: n.body
                Notices.show(
                    ctx, 1000 + (n.id % 10_000).toInt(), title.ifBlank { "NoCapOS" }, body,
                    Intent(ctx, MainActivity::class.java).setAction(MainActivity.ACTION_HUB), server = true,
                )
            }
            ev.notices.maxOfOrNull { it.id }?.let { if (it > last) Prefs.lastNotice = it }
        }
        if (Opt.clipSync.v) {
            ev.clips.lastOrNull()?.let { c ->
                withContext(Dispatchers.Main) { setClip(ctx, c.text) }
                Notices.show(
                    ctx, 77, t("Clipboard from ${c.from.ifBlank { "NoCap OS" }}", "Clipboard from ${c.from.ifBlank { "NoCap OS" }}"),
                    t("It's on your clipboard now. Paste away.", "Copied to this phone's clipboard.") + "\n" + c.text.take(140),
                )
            }
        }
        if (ev.dismissed.isNotEmpty()) {
            val svc = DotsService.instance
            for (k in ev.dismissed) {
                synchronized(sent) { sent.remove(k) }
                runCatching { svc?.cancelNotification(k) }
            }
        }
    }

    // ---------- clipboard ----------

    private fun setClip(ctx: Context, text: String) {
        val cm = ctx.getSystemService(ClipboardManager::class.java) ?: return
        ourClip = text
        lastClipHash = text.hashCode()
        cm.setPrimaryClip(ClipData.newPlainText("NoCap OS", text))
    }

    /**
     * Sends the phone's clipboard to NoCap OS. Android only lets the app in
     * front (with focus) read the clipboard, so this runs when NoCap OS gets
     * focus, or from the quick-settings tile. [force] sends even if unchanged.
     */
    fun sendClipboard(ctx: Context, force: Boolean, done: ((Boolean, String) -> Unit)? = null) {
        val cm = ctx.getSystemService(ClipboardManager::class.java)
        val text = runCatching { cm?.primaryClip?.getItemAt(0)?.coerceToText(ctx)?.toString() }.getOrNull()?.trim().orEmpty()
        if (text.isEmpty()) { done?.invoke(false, t("Clipboard's empty.", "The clipboard is empty.")); return }
        if (!force && (text.hashCode() == lastClipHash || text == ourClip)) return
        lastClipHash = text.hashCode()
        val api = api(ctx) ?: run { done?.invoke(false, t("Link your NoCap server first.", "Link a NoCap server first.")); return }
        scope.launch {
            val ok = runCatching { api.postClip(text.take(60_000)) }.isSuccess
            withContext(Dispatchers.Main) {
                done?.invoke(ok, if (ok) t("Clipboard sent to NoCap OS.", "Clipboard sent to NoCap OS.") else t("Couldn't reach your server.", "Couldn't reach your server."))
            }
        }
    }
}

/** Quick-settings tile: "Send clipboard to NoCap". */
class ClipTileService : TileService() {
    override fun onClick() {
        super.onClick()
        val i = Intent(this, ClipActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        if (android.os.Build.VERSION.SDK_INT >= 34) {
            startActivityAndCollapse(android.app.PendingIntent.getActivity(this, 0, i, android.app.PendingIntent.FLAG_IMMUTABLE))
        } else {
            @Suppress("DEPRECATION")
            startActivityAndCollapse(i)
        }
    }
}

/** A see-through moment in front, so Android lets us read the clipboard; then it's gone. */
class ClipActivity : Activity() {
    private var sent = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        Prefs.init(this)
    }

    override fun onWindowFocusChanged(hasFocus: Boolean) {
        super.onWindowFocusChanged(hasFocus)
        if (!hasFocus || sent) return
        sent = true
        Sync.sendClipboard(this, force = true) { _, msg ->
            Toast.makeText(applicationContext, msg, Toast.LENGTH_SHORT).show()
            finish()
        }
    }
}
