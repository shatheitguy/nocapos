package com.nocap.link.sync

import android.Manifest
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.provider.Settings
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import com.nocap.link.R
import com.nocap.link.data.t
import com.nocap.link.mirror.MirrorAsk
import com.nocap.link.mirror.MirrorConsentActivity
import com.nocap.link.net.NoCapApi
import com.nocap.link.net.PhoneAction

/**
 * Requests from NoCap OS (mirror my screen, ring, open this, take this file)
 * as heads-up notifications with buttons, over the lock screen too. They all
 * share one high-importance channel so one switch in Android's settings
 * covers them, and [problems] says what would keep them from showing.
 */
object Requests {
    /** New id: a channel's importance can't be raised once Android has it, so this one starts out high. */
    const val CHANNEL = "nocap_requests_v2"
    const val MIRROR_ID = 4200
    const val ACTION_DECLINE_MIRROR = "com.nocap.link.DECLINE_MIRROR"
    const val EXTRA_ASK = "ask"

    fun channel(ctx: Context) {
        val nm = ctx.getSystemService(NotificationManager::class.java) ?: return
        runCatching {
            nm.createNotificationChannel(
                NotificationChannel(CHANNEL, t("Asks from NoCap OS", "Requests from NoCap OS"), NotificationManager.IMPORTANCE_HIGH).apply {
                    description = t(
                        "Mirror my screen, ring, open this, take this file: NoCap OS asking this phone.",
                        "Screen mirroring, ring, open and send requests from NoCap OS.",
                    )
                    lockscreenVisibility = android.app.Notification.VISIBILITY_PUBLIC
                    enableVibration(true)
                    setShowBadge(true)
                },
            )
            // The old per-kind channels: their switches are now this one.
            nm.deleteNotificationChannel("nocap_mirror_ask")
            nm.deleteNotificationChannel("nocap_carry")
        }
    }

    /** May this app show a full-screen request over the lock screen? */
    fun fullScreenAllowed(ctx: Context): Boolean =
        Build.VERSION.SDK_INT < 34 || ctx.getSystemService(NotificationManager::class.java)?.canUseFullScreenIntent() == true

    /** Something that keeps requests from showing, with a way to fix it. */
    data class Problem(val id: String, val text: String, val fix: String)

    /** What stops NoCap OS's requests from reaching this phone right now (empty: all good). */
    fun problems(ctx: Context): List<Problem> {
        val out = ArrayList<Problem>()
        val runtime = Build.VERSION.SDK_INT < 33 ||
            ContextCompat.checkSelfPermission(ctx, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED
        if (!runtime || !NotificationManagerCompat.from(ctx).areNotificationsEnabled()) {
            out += Problem("notifications", t("Notifications are off for NoCap OS, so asks can't pop up.", "Notifications are turned off for NoCap OS."), t("Turn on", "Turn on"))
        } else {
            val ch = ctx.getSystemService(NotificationManager::class.java)?.getNotificationChannel(CHANNEL)
            if (ch != null && ch.importance < NotificationManager.IMPORTANCE_HIGH) {
                out += Problem("channel", t("“Asks from NoCap OS” is muted, so they won't pop up.", "“Requests from NoCap OS” notifications are set to silent."), t("Fix it", "Change"))
            }
        }
        if (!fullScreenAllowed(ctx)) {
            out += Problem("fullscreen", t("Asks can't show over the lock screen.", "Requests can't be shown over the lock screen."), t("Allow", "Allow"))
        }
        if (!LinkService.unrestricted(ctx)) {
            out += Problem("battery", t("Android may pause NoCap Link to save battery.", "Battery optimization may pause the connection."), t("Let it run", "Allow"))
        }
        return out
    }

    /** Opens the right place in Android's settings for [p]. */
    fun fix(ctx: Context, p: Problem) {
        val pkg = ctx.packageName
        val i = when (p.id) {
            "notifications" -> Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS).putExtra(Settings.EXTRA_APP_PACKAGE, pkg)
            "channel" -> Intent(Settings.ACTION_CHANNEL_NOTIFICATION_SETTINGS).putExtra(Settings.EXTRA_APP_PACKAGE, pkg).putExtra(Settings.EXTRA_CHANNEL_ID, CHANNEL)
            "fullscreen" -> if (Build.VERSION.SDK_INT >= 34) Intent(Settings.ACTION_MANAGE_APP_USE_FULL_SCREEN_INTENT, android.net.Uri.parse("package:$pkg")) else null
            "battery" -> { LinkService.askUnrestricted(ctx); return }
            else -> null
        } ?: Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, android.net.Uri.parse("package:$pkg"))
        i.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        if (runCatching { ctx.startActivity(i) }.isFailure) {
            runCatching { ctx.startActivity(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, android.net.Uri.parse("package:$pkg")).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
        }
    }

    private fun post(ctx: Context, id: Int, n: android.app.Notification): Boolean {
        if (!Notices.allowed(ctx)) return false
        return try {
            NotificationManagerCompat.from(ctx).notify(id, n)
            true
        } catch (_: SecurityException) {
            false // permission pulled in the meantime
        }
    }

    /** "<server> wants to mirror your screen" with Accept / Decline, full screen when the phone is locked. */
    fun mirror(ctx: Context, server: String, ask: String): Boolean {
        channel(ctx)
        val flags = PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        val open = Intent(ctx, MirrorConsentActivity::class.java).putExtra(MirrorConsentActivity.ASK, ask)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP)
        val accept = Intent(open).putExtra(MirrorConsentActivity.ACCEPTED, true)
        val decline = Intent(ctx, RequestReceiver::class.java).setAction(ACTION_DECLINE_MIRROR).putExtra(EXTRA_ASK, ask)
        val b = NotificationCompat.Builder(ctx, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat_nocap)
            .setColor(0xFFE8232B.toInt())
            .setContentTitle(t("$server wants to mirror your screen", "$server wants to mirror your screen"))
            .setContentText(t("Let it or nah? Android asks once more after you say yes.", "Accept or decline. Android will ask once more after you accept."))
            .setCategory(NotificationCompat.CATEGORY_CALL)
            .setPriority(NotificationCompat.PRIORITY_MAX)
            .setVisibility(NotificationCompat.VISIBILITY_PUBLIC)
            .setContentIntent(PendingIntent.getActivity(ctx, MIRROR_ID, open, flags))
            .addAction(0, t("Nah", "Decline"), PendingIntent.getBroadcast(ctx, MIRROR_ID + 1, decline, flags))
            .addAction(0, t("Let it", "Accept"), PendingIntent.getActivity(ctx, MIRROR_ID + 2, accept, flags))
            .setAutoCancel(true)
            .setOngoing(true)
            .setTimeoutAfter(120_000)
        if (fullScreenAllowed(ctx)) b.setFullScreenIntent(PendingIntent.getActivity(ctx, MIRROR_ID + 4, open, flags), true)
        return post(ctx, MIRROR_ID, b.build())
    }

    fun cancelMirror(ctx: Context) = runCatching { NotificationManagerCompat.from(ctx).cancel(MIRROR_ID) }

    /** "Open on this phone" / "Open <file>": a tap opens it (Android doesn't let background apps open things). */
    fun carry(ctx: Context, id: Int, title: String, text: String, open: Intent): Boolean {
        channel(ctx)
        val flags = PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        val pi = PendingIntent.getActivity(ctx, id, open, flags)
        val n = NotificationCompat.Builder(ctx, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat_nocap)
            .setColor(0xFFE8232B.toInt())
            .setContentTitle(title)
            .setContentText(text)
            .setCategory(NotificationCompat.CATEGORY_RECOMMENDATION)
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setVisibility(NotificationCompat.VISIBILITY_PRIVATE)
            .setAutoCancel(true)
            .setContentIntent(pi)
            .addAction(0, t("Open", "Open"), pi)
            .build()
        return post(ctx, id, n)
    }

    /** A test ask from Settings, so you can see how requests look (and that they show up). */
    fun test(ctx: Context): Boolean {
        channel(ctx)
        val n = NotificationCompat.Builder(ctx, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat_nocap)
            .setColor(0xFFE8232B.toInt())
            .setContentTitle(t("Test ask from NoCap OS", "Test request from NoCap OS"))
            .setContentText(t("If you see this pop up, asks reach you. W.", "Requests from NoCap OS will appear like this."))
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setAutoCancel(true)
            .setTimeoutAfter(20_000)
            .build()
        return post(ctx, 4299, n)
    }

    /** What one long-poll answer asks this phone to do, in order (pure, so it can be tested). */
    sealed interface Step {
        data class Mirror(val ask: String) : Step
        data class Act(val action: PhoneAction) : Step
        /** Notices, clipboard, dismissals and sign-in alerts. */
        data object Feed : Step
    }

    /**
     * Mirror asks and actions are handled on every answer, the first one too
     * (the server only sends what's new for this phone); the feed only once
     * there is a cursor, so a fresh start doesn't replay history.
     */
    fun plan(ev: NoCapApi.Events, actions: List<PhoneAction>, hadCursor: Boolean, seen: Set<Long>): List<Step> {
        val out = ArrayList<Step>()
        ev.mirror?.let { out += Step.Mirror(it) }
        for (a in actions) if (a.id !in seen && a.kind.isNotBlank()) out += Step.Act(a)
        if (hadCursor) out += Step.Feed
        return out
    }

    /** "Nah" on the mirror ask (or it was swiped away). */
    fun declineMirror(ctx: Context, ask: String?) {
        cancelMirror(ctx)
        MirrorAsk.postState(ctx, "denied", ask)
    }
}

/** Buttons on request notifications that don't need to open anything. */
class RequestReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent?) {
        when (intent?.action) {
            Requests.ACTION_DECLINE_MIRROR -> Requests.declineMirror(context, intent.getStringExtra(Requests.EXTRA_ASK))
        }
    }
}
