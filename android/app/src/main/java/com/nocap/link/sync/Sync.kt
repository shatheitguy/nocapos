package com.nocap.link.sync

import android.app.Activity
import android.app.Notification
import android.content.ClipboardManager
import android.content.ClipData
import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.Canvas
import android.os.Bundle
import android.service.notification.StatusBarNotification
import android.service.quicksettings.TileService
import android.util.Base64
import android.widget.Toast
import com.nocap.link.data.LinkOpt
import com.nocap.link.data.Opt
import com.nocap.link.data.Prefs
import com.nocap.link.data.t
import com.nocap.link.data.Vault
import com.nocap.link.MainActivity
import com.nocap.link.net.ApiError
import com.nocap.link.net.CertChanged
import com.nocap.link.net.NoCapApi
import com.nocap.link.net.PhoneNote
import com.nocap.link.net.liveEvents
import com.nocap.link.net.postNote
import com.nocap.link.net.ServerTooOld
import com.nocap.link.system.DotsService
import java.io.ByteArrayOutputStream
import java.util.concurrent.Executors
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.isActive
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.withContext

/**
 * NoCap Link sync: this phone's notifications → your NoCap OS (opt-in, per
 * app), NoCap OS notifications and clipboard → this phone, and this phone's
 * clipboard → NoCap OS. Everything goes only to your own linked server.
 */
object Sync {
    private val io = Executors.newSingleThreadExecutor()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO + com.nocap.link.system.CrashLog.handler("NoCap Link sync"))
    private var loop: Job? = null
    /** Last notification content sent per key, so updates that change nothing aren't re-sent. */
    private val sent = HashMap<String, Int>()
    private var lastClipHash = 0
    @Volatile private var ourClip: String? = null

    private fun api(ctx: Context): NoCapApi? = runCatching { Vault.get(ctx).link?.let { NoCapApi(it) } }.getOrNull()

    // ---------- phone notifications → NoCap OS ----------

    fun forwardable(ctx: Context, sbn: StatusBarNotification): Boolean {
        if (sbn.packageName == ctx.packageName) return false
        // Calls (incoming, ongoing, missed) when that's on, whatever the app list says.
        if (LinkOpt.calls.v && PhoneActions.isCall(sbn.notification)) return true
        if (!Opt.forward.v) return false
        if (sbn.packageName !in Opt.forwardApps.v) return false
        val n = sbn.notification
        if (!sbn.isClearable || sbn.isOngoing) return false
        if (n.flags and Notification.FLAG_GROUP_SUMMARY != 0) return false
        return true
    }

    fun posted(ctx: Context, sbn: StatusBarNotification) {
        Prefs.init(ctx)
        LinkOpt.init(ctx)
        if (!forwardable(ctx, sbn)) return
        val api = api(ctx) ?: return
        val ex = sbn.notification.extras
        val title = ex.getCharSequence(Notification.EXTRA_TITLE)?.toString().orEmpty()
        val text = (ex.getCharSequence(Notification.EXTRA_BIG_TEXT) ?: ex.getCharSequence(Notification.EXTRA_TEXT))?.toString().orEmpty()
        if (title.isBlank() && text.isBlank()) return
        val pm = ctx.packageManager
        val label = runCatching { pm.getApplicationLabel(pm.getApplicationInfo(sbn.packageName, 0)).toString() }.getOrDefault(sbn.packageName)
        val note = PhoneActions.describe(ctx, sbn, PhoneNote(sbn.key, label, sbn.packageName, title, text, null, sbn.postTime))
        val hash = listOf(title, text, note.kind, note.callState, note.canReply, note.canDecline).joinToString("\u0000").hashCode()
        synchronized(sent) {
            if (sent[sbn.key] == hash) return
            if (sent.size > 500) sent.clear() // keys of long-gone notifications
            sent[sbn.key] = hash
        }
        io.execute {
            runCatching { api.postNote(note.copy(icon = icon(ctx, sbn.packageName))) }
        }
    }

    fun removed(ctx: Context, sbn: StatusBarNotification) {
        val had = synchronized(sent) { sent.remove(sbn.key) != null }
        if (!had) return
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

    /**
     * Starts (or restarts) listening to your server while linked: sign-in
     * alerts always come this way (they're about your account's safety), plus
     * notices and the clipboard when those are on.
     */
    fun start(ctx: Context) {
        val app = ctx.applicationContext
        Prefs.init(app)
        LinkOpt.init(app)
        loop?.cancel()
        loop = null
        if (runCatching { Vault.get(app).link }.getOrNull() == null) {
            LinkService.stop(app)
            com.nocap.link.net.Endpoint.reset()
            return
        }
        // Always connected: the link service holds the connection, in the
        // background too. Without it, only while the app runs.
        if (LinkOpt.alwaysOn.v && LinkService.start(app)) return
        LinkService.stop(app)
        loop = scope.launch { listen(app, inService = false) }
    }

    fun stop() { loop?.cancel(); loop = null }

    private fun wantsNotices() = Opt.liveNotices.v && Opt.serverNotices.v

    /** Follows the server until cancelled (or until it's clear it never will work). */
    suspend fun listen(ctx: Context, inService: Boolean) {
        var cursor = 0L
        var backoff = 5_000L
        val power = ctx.getSystemService(android.os.PowerManager::class.java)
        while (isActiveSafe()) {
            // Screen off: rest (the 15-minute heartbeat still checks for server notices).
            if (!inService && power != null && !power.isInteractive) { delay(60_000); continue }
            val api = api(ctx) ?: return
            try {
                val (ev, actions) = api.liveEvents(cursor, 25)
                backoff = 5_000L
                LinkService.connected(true)
                if (cursor != 0L) {
                    handle(ctx, ev)
                    for (a in actions) runCatching { PhoneActions.handle(ctx, api, a) }
                }
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
            } catch (e: Exception) {
                LinkService.connected(false)
                // Offline is normal; anything else failing over and over gets a report.
                if (e !is java.io.IOException) com.nocap.link.system.CrashLog.failure("NoCap Link sync", e)
                delay(backoff); backoff = (backoff * 2).coerceAtMost(300_000L)
            }
        }
    }

    private suspend fun isActiveSafe() = kotlin.coroutines.coroutineContext.isActive

    private suspend fun handle(ctx: Context, ev: NoCapApi.Events) {
        if (ev.signins.isNotEmpty()) Signins.deliver(ctx, ev.signins, live = true)
        if (wantsNotices()) {
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
        runCatching { cm.setPrimaryClip(ClipData.newPlainText("NoCap OS", text)) }
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
    override fun onStartListening() {
        super.onStartListening()
        runCatching {
            val tile = qsTile ?: return
            val linked = runCatching { Vault.get(this).link != null }.getOrDefault(false)
            tile.state = if (linked) android.service.quicksettings.Tile.STATE_INACTIVE else android.service.quicksettings.Tile.STATE_UNAVAILABLE
            tile.updateTile()
        }
    }

    override fun onClick() {
        super.onClick()
        val i = Intent(this, ClipActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        runCatching {
            if (android.os.Build.VERSION.SDK_INT >= 34) {
                startActivityAndCollapse(android.app.PendingIntent.getActivity(this, 0, i, android.app.PendingIntent.FLAG_IMMUTABLE))
            } else {
                @Suppress("DEPRECATION", "StartActivityAndCollapseDeprecated")
                startActivityAndCollapse(i)
            }
        }.onFailure { com.nocap.link.system.CrashLog.warn("clipboard tile", it) }
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
        // Nothing to send (or no link): sendClipboard answered right away; also make sure we never linger.
        window?.decorView?.postDelayed({ if (!isFinishing) finish() }, 15_000)
    }
}
