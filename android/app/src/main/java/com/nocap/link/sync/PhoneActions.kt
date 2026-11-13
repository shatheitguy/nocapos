package com.nocap.link.sync

import android.Manifest
import android.app.ActivityManager
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.RemoteInput
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.service.notification.StatusBarNotification
import android.telecom.TelecomManager
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import com.nocap.link.R
import com.nocap.link.data.LinkOpt
import com.nocap.link.data.t
import com.nocap.link.desktop.DownloadBridge
import com.nocap.link.net.FileEntry
import com.nocap.link.net.NoCapApi
import com.nocap.link.net.PhoneAction
import com.nocap.link.net.PhoneNote
import com.nocap.link.system.DotsService
import java.io.File

/**
 * NoCap OS → this phone: reply to a message, decline a call, ring ("Find
 * my phone"), open a link ("Open on phone") and take a file ("Send to
 * phone", saved in Downloads/NoCap). Also works out what kind of
 * notification is going over (a call, a message you can answer, or other).
 */
object PhoneActions {
    private const val CARRY = "nocap_carry"

    fun handle(ctx: Context, api: NoCapApi, a: PhoneAction) {
        when (a.kind) {
            "reply" -> reply(ctx, a.key, a.text)
            "decline" -> decline(ctx, a.key)
            "ring" -> FindPhone.ring(ctx)
            "stop_ring" -> FindPhone.stop(ctx)
            "open" -> if (a.url.startsWith("https://") || a.url.startsWith("http://")) {
                open(ctx, Intent(Intent.ACTION_VIEW, Uri.parse(a.url)), t("Open on this phone", "Open on this phone"), a.url, a.id)
            }
            "file" -> file(ctx, api, a)
        }
    }

    // ---------- what a notification is ----------

    /** Notification categories (constants newer than our minimum Android). */
    private const val CATEGORY_MISSED_CALL = "missed_call"
    private const val EXTRA_CALL_TYPE = "android.callType" // 1 incoming, 2 ongoing, 3 screening
    private const val EXTRA_DECLINE = "android.declineIntent"
    private const val EXTRA_HANG_UP = "android.hangUpIntent"

    fun isCall(n: Notification): Boolean = n.category == Notification.CATEGORY_CALL || n.category == CATEGORY_MISSED_CALL

    /** Fills in kind, call state, sender and what NoCap OS may do with it. */
    fun describe(ctx: Context, sbn: StatusBarNotification, base: PhoneNote): PhoneNote {
        val n = sbn.notification
        if (isCall(n)) {
            val state = when {
                n.category == CATEGORY_MISSED_CALL -> "missed"
                n.extras.getInt(EXTRA_CALL_TYPE, 0) == 1 -> "incoming"
                n.extras.getInt(EXTRA_CALL_TYPE, 0) == 2 -> "ongoing"
                !sbn.isOngoing -> "missed"
                n.fullScreenIntent != null -> "incoming"
                else -> "ongoing"
            }
            return base.copy(kind = "call", callState = state, canDecline = state != "missed" && canDecline(ctx, n))
        }
        val reply = replyAction(n) != null
        val message = n.category == Notification.CATEGORY_MESSAGE || reply ||
            n.extras.getString(Notification.EXTRA_TEMPLATE)?.contains("MessagingStyle") == true
        if (!message) return base
        val sender = NotificationCompat.MessagingStyle.extractMessagingStyleFromNotification(n)
            ?.messages?.lastOrNull()?.person?.name?.toString().orEmpty()
        return base.copy(kind = "message", sender = sender, canReply = reply)
    }

    private fun replyAction(n: Notification): Notification.Action? =
        n.actions?.firstOrNull { act -> act.remoteInputs?.any { it.allowFreeFormInput } == true }

    private fun mayEndCalls(ctx: Context) = Build.VERSION.SDK_INT >= 28 &&
        ContextCompat.checkSelfPermission(ctx, Manifest.permission.ANSWER_PHONE_CALLS) == PackageManager.PERMISSION_GRANTED

    private fun declineIntent(n: Notification): PendingIntent? {
        @Suppress("DEPRECATION")
        val fromStyle = (n.extras.get(EXTRA_DECLINE) ?: n.extras.get(EXTRA_HANG_UP)) as? PendingIntent
        if (fromStyle != null) return fromStyle
        val words = listOf("decline", "reject", "hang up", "end call", "dismiss call")
        return n.actions?.firstOrNull { a -> words.any { a.title?.toString()?.lowercase()?.contains(it) == true } }?.actionIntent
    }

    private fun canDecline(ctx: Context, n: Notification) = LinkOpt.calls.v && (mayEndCalls(ctx) || declineIntent(n) != null)

    private fun find(key: String): StatusBarNotification? =
        runCatching { DotsService.instance?.activeNotifications?.firstOrNull { it.key == key } }.getOrNull()

    // ---------- actions ----------

    private fun reply(ctx: Context, key: String, text: String) {
        val sbn = find(key) ?: return
        val act = replyAction(sbn.notification) ?: return
        val inputs = act.remoteInputs ?: return
        val fill = Intent().addFlags(Intent.FLAG_RECEIVER_FOREGROUND)
        val results = Bundle()
        for (r in inputs) results.putCharSequence(r.resultKey, text)
        RemoteInput.addResultsToIntent(inputs, fill, results)
        runCatching { act.actionIntent.send(ctx, 0, fill) }
            .onFailure { com.nocap.link.system.CrashLog.warn("reply from NoCap OS", it) }
    }

    private fun decline(ctx: Context, key: String) {
        val sbn = find(key)
        val pi = sbn?.notification?.let { declineIntent(it) }
        if (pi != null && runCatching { pi.send() }.isSuccess) return
        if (mayEndCalls(ctx)) {
            @Suppress("DEPRECATION", "MissingPermission")
            runCatching { ctx.getSystemService(TelecomManager::class.java)?.endCall() }
        }
    }

    private fun file(ctx: Context, api: NoCapApi, a: PhoneAction) {
        val name = a.name.ifBlank { a.path.substringAfterLast('/') }.ifBlank { "file" }
        val tmp = File(ctx.cacheDir, "incoming/${a.id}")
        try {
            api.download(FileEntry(a.root, a.path, name, false, a.size, ""), tmp)
            val mime = android.webkit.MimeTypeMap.getSingleton().getMimeTypeFromExtension(name.substringAfterLast('.', "").lowercase()) ?: "application/octet-stream"
            val uri = DownloadBridge.saveToDownloads(ctx, name.replace(Regex("[\\\\/:*?\"<>|\\p{Cntrl}]"), "_").take(120), mime) { out ->
                tmp.inputStream().use { it.copyTo(out) }
            }
            val view = uri?.let { Intent(Intent.ACTION_VIEW).setDataAndType(it, mime).addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION) }
            if (a.open && view != null) {
                open(ctx, view, t("Open $name", "Open $name"), t("From NoCap OS, saved in Downloads/NoCap.", "From NoCap OS, saved in Downloads/NoCap."), a.id)
            } else {
                Notices.show(
                    ctx, 3000 + (a.id % 1000).toInt(), t("$name landed 📥", "File received: $name"),
                    t("From NoCap OS → Downloads/NoCap.", "From NoCap OS, saved in Downloads/NoCap."), view?.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
                )
            }
        } catch (e: Exception) {
            Notices.show(ctx, 3000 + (a.id % 1000).toInt(), t("Couldn't grab $name", "Couldn't receive $name"), e.message ?: "")
        } finally {
            tmp.delete()
        }
    }

    /**
     * Opens right away when NoCap OS is in front; otherwise Android doesn't
     * let a background app open things, so a notification asks for a tap.
     */
    private fun open(ctx: Context, intent: Intent, title: String, text: String, id: Long) {
        intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        if (inFront() && runCatching { ctx.startActivity(intent) }.isSuccess) return
        if (!Notices.allowed(ctx)) return
        val nm = ctx.getSystemService(NotificationManager::class.java) ?: return
        runCatching {
            nm.createNotificationChannel(NotificationChannel(CARRY, "Open on phone", NotificationManager.IMPORTANCE_HIGH).apply {
                description = "Links and files NoCap OS sends to this phone"
            })
        }
        val nid = 5000 + (id % 1000).toInt()
        val pi = PendingIntent.getActivity(ctx, nid, intent, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        val n = NotificationCompat.Builder(ctx, CARRY)
            .setSmallIcon(R.drawable.ic_stat_nocap)
            .setContentTitle(title)
            .setContentText(text)
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setAutoCancel(true)
            .setContentIntent(pi)
            .build()
        try {
            NotificationManagerCompat.from(ctx).notify(nid, n)
        } catch (_: SecurityException) {
        }
    }

    private fun inFront(): Boolean {
        val info = ActivityManager.RunningAppProcessInfo()
        ActivityManager.getMyMemoryState(info)
        return info.importance <= ActivityManager.RunningAppProcessInfo.IMPORTANCE_FOREGROUND
    }
}
