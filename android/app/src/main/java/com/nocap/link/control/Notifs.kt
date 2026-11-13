package com.nocap.link.control

import android.app.Notification
import android.app.PendingIntent
import android.app.RemoteInput
import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.service.notification.StatusBarNotification
import com.nocap.link.system.CrashLog
import com.nocap.link.system.DotsService

/**
 * The phone's notifications for the NoCap control center and lock screen,
 * read from the notification listener you already allowed (DotsService).
 * Nothing is stored or sent anywhere.
 */
object Notifs {
    /** One notification, reduced to what the panels show (pure data, so grouping is testable). */
    data class Item(
        val key: String, val pkg: String, val title: String, val text: String, val time: Long,
        val clearable: Boolean, val canReply: Boolean,
    )

    data class Group(val pkg: String, val items: List<Item>) {
        val latest get() = items.maxOfOrNull { it.time } ?: 0L
    }

    /** Groups by app: newest app first, newest notification first inside each app. */
    fun group(items: List<Item>): List<Group> =
        items.groupBy { it.pkg }
            .map { (pkg, list) -> Group(pkg, list.sortedByDescending { it.time }) }
            .sortedByDescending { it.latest }

    /** What the lock screen shows when notification text is hidden: "3 notifications". */
    fun countLine(n: Int, genz: Boolean): String = when {
        genz && n == 1 -> "1 new notif"
        genz -> "$n new notifs"
        n == 1 -> "1 notification"
        else -> "$n notifications"
    }

    /** Live notifications (empty without notification access), skipping ongoing ones like music and our own. */
    fun active(ctx: Context): List<StatusBarNotification> {
        val svc = DotsService.instance ?: return emptyList()
        val all = runCatching { svc.activeNotifications }.getOrNull() ?: return emptyList()
        return all.filter { !it.isOngoing && it.packageName != ctx.packageName && it.notification.flags and Notification.FLAG_GROUP_SUMMARY == 0 }
    }

    fun item(sbn: StatusBarNotification): Item {
        val ex = sbn.notification.extras
        val title = ex?.getCharSequence(Notification.EXTRA_TITLE)?.toString().orEmpty()
        val text = (ex?.getCharSequence(Notification.EXTRA_BIG_TEXT) ?: ex?.getCharSequence(Notification.EXTRA_TEXT))?.toString().orEmpty()
        return Item(sbn.key, sbn.packageName, title, text, sbn.postTime, sbn.isClearable, replyAction(sbn) != null)
    }

    private fun replyAction(sbn: StatusBarNotification): Notification.Action? =
        sbn.notification.actions?.firstOrNull { a -> a.remoteInputs?.any { it.allowFreeFormInput } == true }

    private fun find(key: String): StatusBarNotification? = runCatching { DotsService.instance?.activeNotifications?.firstOrNull { it.key == key } }.getOrNull()

    /** Dismisses it (like swiping it away in the shade). */
    fun dismiss(key: String) {
        runCatching { DotsService.instance?.cancelNotification(key) }.onFailure { CrashLog.warn("dismiss notification", it) }
    }

    fun dismissAll() {
        runCatching { DotsService.instance?.cancelAllNotifications() }.onFailure { CrashLog.warn("clear notifications", it) }
    }

    /** Opens what the notification points at. */
    fun open(ctx: Context, key: String): Boolean {
        val n = find(key) ?: return false
        return runCatching {
            n.notification.contentIntent?.send() ?: run {
                ctx.packageManager.getLaunchIntentForPackage(n.packageName)?.let { ctx.startActivity(it.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
            }
            if (n.notification.flags and Notification.FLAG_AUTO_CANCEL != 0) dismiss(key)
        }.isSuccess
    }

    /** Sends a reply through the app's own reply action (RemoteInput), like the shade's reply box. */
    fun reply(ctx: Context, key: String, text: String): Boolean {
        val n = find(key) ?: return false
        val action = replyAction(n) ?: return false
        val inputs = action.remoteInputs ?: return false
        return runCatching {
            val results = Bundle()
            inputs.filter { it.allowFreeFormInput }.forEach { results.putCharSequence(it.resultKey, text) }
            val fill = Intent()
            RemoteInput.addResultsToIntent(inputs, fill, results)
            action.actionIntent.send(ctx, 0, fill)
        }.onFailure { if (it !is PendingIntent.CanceledException) CrashLog.warn("notification reply", it) }.isSuccess
    }

    /** The app's name for a package (falls back to the package). */
    fun appName(ctx: Context, pkg: String): String = runCatching {
        val pm = ctx.packageManager
        pm.getApplicationLabel(pm.getApplicationInfo(pkg, 0)).toString()
    }.getOrDefault(pkg)
}
