package com.nocap.link.sync

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.media.AudioAttributes
import android.media.AudioManager
import android.media.MediaPlayer
import android.media.RingtoneManager
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.os.VibrationEffect
import android.os.Vibrator
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import com.nocap.link.R
import com.nocap.link.data.t

/**
 * "Find my phone" from NoCap OS: rings at full alarm volume, even on silent
 * (the alarm stream), and through Do not disturb only when NoCap OS has Do
 * not disturb access. Stops from the notification, from NoCap OS, or after
 * two minutes; the volume and Do not disturb go back to how they were.
 */
object FindPhone {
    private const val CHANNEL = "nocap_find"
    private const val ID = 4242
    private const val RING_MS = 120_000L
    private var player: MediaPlayer? = null
    private var oldVolume = -1
    private var oldFilter = -1
    private val main = Handler(Looper.getMainLooper())
    private val timeout = Runnable { stop(appCtx ?: return@Runnable) }
    private var appCtx: Context? = null
    /** Told when ringing starts or stops (the link service reports it). */
    var onChange: ((Boolean) -> Unit)? = null

    val ringing: Boolean get() = player != null

    fun ring(ctx: Context) = main.post { start(ctx.applicationContext) }

    private fun start(ctx: Context) {
        if (player != null) return
        appCtx = ctx
        val am = ctx.getSystemService(AudioManager::class.java)
        val nm = ctx.getSystemService(NotificationManager::class.java)
        if (nm != null && nm.isNotificationPolicyAccessGranted && nm.currentInterruptionFilter != NotificationManager.INTERRUPTION_FILTER_ALL) {
            oldFilter = nm.currentInterruptionFilter
            runCatching { nm.setInterruptionFilter(NotificationManager.INTERRUPTION_FILTER_ALL) }
        }
        if (am != null) {
            oldVolume = am.getStreamVolume(AudioManager.STREAM_ALARM)
            runCatching { am.setStreamVolume(AudioManager.STREAM_ALARM, am.getStreamMaxVolume(AudioManager.STREAM_ALARM), 0) }
        }
        val uri = RingtoneManager.getActualDefaultRingtoneUri(ctx, RingtoneManager.TYPE_ALARM)
            ?: RingtoneManager.getDefaultUri(RingtoneManager.TYPE_RINGTONE)
        player = runCatching {
            MediaPlayer().apply {
                setAudioAttributes(AudioAttributes.Builder().setUsage(AudioAttributes.USAGE_ALARM).setContentType(AudioAttributes.CONTENT_TYPE_SONIFICATION).build())
                setDataSource(ctx, uri)
                isLooping = true
                prepare()
                start()
            }
        }.getOrNull() ?: MediaPlayer() // no sound available: still show the notification and vibrate
        vibrate(ctx, true)
        notify(ctx)
        main.postDelayed(timeout, RING_MS)
        onChange?.invoke(true)
    }

    fun stop(ctx: Context) = main.post { halt(ctx.applicationContext) }

    private fun halt(ctx: Context) {
        val p = player ?: return
        player = null
        main.removeCallbacks(timeout)
        runCatching { p.stop() }
        runCatching { p.release() }
        vibrate(ctx, false)
        val am = ctx.getSystemService(AudioManager::class.java)
        if (am != null && oldVolume >= 0) runCatching { am.setStreamVolume(AudioManager.STREAM_ALARM, oldVolume, 0) }
        oldVolume = -1
        val nm = ctx.getSystemService(NotificationManager::class.java)
        if (nm != null && oldFilter >= 0 && nm.isNotificationPolicyAccessGranted) runCatching { nm.setInterruptionFilter(oldFilter) }
        oldFilter = -1
        NotificationManagerCompat.from(ctx).cancel(ID)
        onChange?.invoke(false)
    }

    private fun vibrate(ctx: Context, on: Boolean) {
        val v = ctx.getSystemService(Vibrator::class.java) ?: return
        runCatching {
            if (!on) v.cancel()
            else v.vibrate(VibrationEffect.createWaveform(longArrayOf(0, 800, 600), 0))
        }
    }

    private fun notify(ctx: Context) {
        if (!Notices.allowed(ctx)) return
        val nm = ctx.getSystemService(NotificationManager::class.java) ?: return
        runCatching {
            nm.createNotificationChannel(NotificationChannel(CHANNEL, "Find my phone", NotificationManager.IMPORTANCE_HIGH).apply {
                description = "When NoCap OS rings this phone"
                setSound(null, null)
            })
        }
        val stop = PendingIntent.getBroadcast(
            ctx, ID, Intent(ctx, FindPhoneStop::class.java), PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val n = NotificationCompat.Builder(ctx, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat_nocap)
            .setContentTitle(t("Found it? 👋", "Find my phone"))
            .setContentText(t("NoCap OS is ringing this phone. Tap to stop.", "NoCap OS is ringing this phone. Tap to stop."))
            .setPriority(NotificationCompat.PRIORITY_MAX)
            .setCategory(NotificationCompat.CATEGORY_ALARM)
            .setOngoing(true)
            .setContentIntent(stop)
            .setDeleteIntent(stop)
            .addAction(0, t("Stop", "Stop"), stop)
            .build()
        try {
            NotificationManagerCompat.from(ctx).notify(ID, n)
        } catch (_: SecurityException) {
        }
    }
}

/** The Stop button on the "Find my phone" notification. */
class FindPhoneStop : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent?) {
        FindPhone.stop(context)
    }
}
