package com.nocap.link.widget

import android.content.ComponentName
import android.content.Context
import android.graphics.Bitmap
import android.media.MediaMetadata
import android.media.session.MediaController
import android.media.session.MediaSessionManager
import android.media.session.PlaybackState
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.nocap.link.system.DotsService

/**
 * Now playing, from Android's media sessions. Only works after you allow
 * notification access for NoCap OS (the same opt-in as notification dots).
 */
object Media {
    data class Now(
        val title: String, val artist: String, val app: String, val pkg: String, val art: Bitmap?,
        val playing: Boolean, val position: Long, val updated: Long, val speed: Float, val duration: Long,
    ) {
        /** Where playback is right now (it moves on while playing). */
        fun pos(): Long = if (!playing || updated == 0L) position else (position + ((SystemClock.elapsedRealtime() - updated) * speed).toLong()).coerceAtMost(if (duration > 0) duration else Long.MAX_VALUE)
    }

    var now by mutableStateOf<Now?>(null)
        private set
    private var controller: MediaController? = null
    private var msm: MediaSessionManager? = null
    private val main = Handler(Looper.getMainLooper())
    private var appCtx: Context? = null

    private val cb = object : MediaController.Callback() {
        override fun onMetadataChanged(metadata: MediaMetadata?) = safe { publish() }
        override fun onPlaybackStateChanged(state: PlaybackState?) = safe { publish() }
        override fun onSessionDestroyed() = safe { pick() }
    }

    private val sessions = MediaSessionManager.OnActiveSessionsChangedListener { safe { pick() } }

    /** Media sessions belong to other apps and can vanish mid-call (DeadObjectException, SecurityException). */
    private inline fun safe(block: () -> Unit) {
        try { block() } catch (e: Exception) { com.nocap.link.system.CrashLog.warn("now playing", e) }
    }

    /** Album art can be full-size; the widget never needs more than this. */
    private fun small(b: Bitmap?): Bitmap? {
        if (b == null || b.width <= 0 || b.height <= 0) return null
        val max = 512
        if (b.width <= max && b.height <= max) return b
        val k = max.toFloat() / maxOf(b.width, b.height)
        return runCatching { Bitmap.createScaledBitmap(b, (b.width * k).toInt().coerceAtLeast(1), (b.height * k).toInt().coerceAtLeast(1), true) }.getOrNull()
    }

    fun start(ctx: Context) {
        val c = ctx.applicationContext
        appCtx = c
        if (!DotsService.allowed(c)) return
        main.post {
            runCatching {
                val m = msm ?: c.getSystemService(MediaSessionManager::class.java).also { msm = it }
                m.removeOnActiveSessionsChangedListener(sessions)
                m.addOnActiveSessionsChangedListener(sessions, ComponentName(c, DotsService::class.java), main)
                pick()
            }
        }
    }

    private fun pick() {
        val c = appCtx ?: return
        val list = runCatching { msm?.getActiveSessions(ComponentName(c, DotsService::class.java)) }.getOrNull().orEmpty()
        val best = list.firstOrNull { it.playbackState?.state == PlaybackState.STATE_PLAYING } ?: list.firstOrNull()
        if (best?.sessionToken != controller?.sessionToken) {
            controller?.unregisterCallback(cb)
            controller = best
            best?.registerCallback(cb, main)
        }
        publish()
    }

    private fun publish() {
        val mc = controller ?: run { now = null; return }
        val md = mc.metadata
        val st = mc.playbackState
        val pm = appCtx?.packageManager
        val app = runCatching { pm?.getApplicationLabel(pm.getApplicationInfo(mc.packageName, 0))?.toString() }.getOrNull() ?: mc.packageName
        now = Now(
            title = md?.getString(MediaMetadata.METADATA_KEY_TITLE) ?: md?.getString(MediaMetadata.METADATA_KEY_DISPLAY_TITLE) ?: "",
            artist = md?.getString(MediaMetadata.METADATA_KEY_ARTIST) ?: md?.getString(MediaMetadata.METADATA_KEY_ALBUM_ARTIST) ?: md?.getString(MediaMetadata.METADATA_KEY_DISPLAY_SUBTITLE) ?: "",
            app = app, pkg = mc.packageName,
            art = small(md?.getBitmap(MediaMetadata.METADATA_KEY_ALBUM_ART) ?: md?.getBitmap(MediaMetadata.METADATA_KEY_ART) ?: md?.getBitmap(MediaMetadata.METADATA_KEY_DISPLAY_ICON)),
            playing = st?.state == PlaybackState.STATE_PLAYING,
            position = st?.position ?: 0, updated = st?.lastPositionUpdateTime ?: 0, speed = st?.playbackSpeed ?: 1f,
            duration = md?.getLong(MediaMetadata.METADATA_KEY_DURATION) ?: 0,
        )
    }

    fun toggle() = safe {
        val t = controller?.transportControls ?: return@safe
        if (now?.playing == true) t.pause() else t.play()
    }

    fun next() = safe { controller?.transportControls?.skipToNext() }
    fun prev() = safe { controller?.transportControls?.skipToPrevious() }
    fun seek(ms: Long) = safe { controller?.transportControls?.seekTo(ms) }

    /** Opens the app that's playing. */
    fun open(ctx: Context) {
        val mc = controller ?: return
        val sent = runCatching { mc.sessionActivity?.send() != null }.getOrDefault(false)
        if (!sent) runCatching {
            ctx.packageManager.getLaunchIntentForPackage(mc.packageName)?.let { ctx.startActivity(it.addFlags(android.content.Intent.FLAG_ACTIVITY_NEW_TASK)) }
        }
    }
}
