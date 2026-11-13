package com.nocap.link.lock

import android.app.KeyguardManager
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.media.AudioManager
import android.provider.Settings
import androidx.core.content.ContextCompat
import com.nocap.link.data.Opt
import com.nocap.link.system.CrashLog

/**
 * Opt-in NoCap lock screen: when the screen turns on and the phone is
 * locked, shows [LockActivity] on top of Android's own lock screen. It never
 * replaces, disables or bypasses the system keyguard — swiping up asks
 * Android to unlock (your PIN, pattern or fingerprint as usual).
 *
 * Screen-on can only be heard by a running app, so this is registered by the
 * launcher's process (NoCap OS as your home app, and the notification
 * listener, keep it alive).
 */
object LockWatch {
    @Volatile private var registered = false

    private val receiver = object : BroadcastReceiver() {
        override fun onReceive(ctx: Context, intent: Intent) {
            if (intent.action == Intent.ACTION_SCREEN_ON) show(ctx)
        }
    }

    /** Registers once per process (cheap; does nothing until the screen turns on). */
    fun start(ctx: Context) {
        if (registered) return
        registered = runCatching {
            ContextCompat.registerReceiver(ctx.applicationContext, receiver, IntentFilter(Intent.ACTION_SCREEN_ON), ContextCompat.RECEIVER_NOT_EXPORTED)
        }.onFailure { CrashLog.warn("lock screen watch", it) }.isSuccess
    }

    /** Android only lets an app open a screen from the background with "Display over other apps". */
    fun canShow(ctx: Context): Boolean = Settings.canDrawOverlays(ctx)

    /** Pure, for tests: show only when switched on, the phone is actually locked, and no call is going. */
    fun shouldShow(enabled: Boolean, keyguardLocked: Boolean, inCall: Boolean): Boolean = enabled && keyguardLocked && !inCall

    private fun show(ctx: Context) {
        val km = ctx.getSystemService(KeyguardManager::class.java) ?: return
        val audio = ctx.getSystemService(AudioManager::class.java)
        val inCall = audio?.mode == AudioManager.MODE_IN_CALL || audio?.mode == AudioManager.MODE_IN_COMMUNICATION || audio?.mode == AudioManager.MODE_RINGTONE
        if (!shouldShow(Opt.lockScreen.v, km.isKeyguardLocked, inCall)) return
        runCatching {
            ctx.startActivity(
                Intent(ctx, LockActivity::class.java)
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_NO_ANIMATION or Intent.FLAG_ACTIVITY_SINGLE_TOP),
            )
        }.onFailure { CrashLog.warn("lock screen", it) }
    }
}
