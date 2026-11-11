package com.nocap.link.nocap

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
        override fun onMetadataChanged(metadata: MediaMetadata?) = publish()
        override fun onPlaybackStateChanged(state: PlaybackState?) = publish()
        override fun onSessionDestroyed() { pick() }
    }

    private val sessions = MediaSessionManager.OnActiveSessionsChangedListener { pick() }

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
            art = md?.getBitmap(MediaMetadata.METADATA_KEY_ALBUM_ART) ?: md?.getBitmap(MediaMetadata.METADATA_KEY_ART) ?: md?.getBitmap(MediaMetadata.METADATA_KEY_DISPLAY_ICON),
            playing = st?.state == PlaybackState.STATE_PLAYING,
            position = st?.position ?: 0, updated = st?.lastPositionUpdateTime ?: 0, speed = st?.playbackSpeed ?: 1f,
            duration = md?.getLong(MediaMetadata.METADATA_KEY_DURATION) ?: 0,
        )
    }

    fun toggle() {
        val t = controller?.transportControls ?: return
        if (now?.playing == true) t.pause() else t.play()
    }

    fun next() = controller?.transportControls?.skipToNext()
    fun prev() = controller?.transportControls?.skipToPrevious()
    fun seek(ms: Long) = controller?.transportControls?.seekTo(ms)

    /** Opens the app that's playing. */
    fun open(ctx: Context) {
        val mc = controller ?: return
        runCatching { mc.sessionActivity?.send() }.onFailure {
            ctx.packageManager.getLaunchIntentForPackage(mc.packageName)?.let { ctx.startActivity(it.addFlags(android.content.Intent.FLAG_ACTIVITY_NEW_TASK)) }
        }.onSuccess { if (mc.sessionActivity == null) ctx.packageManager.getLaunchIntentForPackage(mc.packageName)?.let { ctx.startActivity(it.addFlags(android.content.Intent.FLAG_ACTIVITY_NEW_TASK)) } }
    }
}
