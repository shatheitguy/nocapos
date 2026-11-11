package com.nocap.link.sync

import android.app.Activity
import android.app.AlertDialog
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.widget.Toast
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import com.nocap.link.MainActivity
import com.nocap.link.Notices
import com.nocap.link.Prefs
import com.nocap.link.R
import com.nocap.link.Vault
import com.nocap.link.net.NoCapApi
import com.nocap.link.net.SignedIn
import com.nocap.link.net.SigninAlert
import com.nocap.link.t
import java.util.concurrent.Executors

/**
 * Sign-in alerts: when someone signs in to your NoCap account somewhere
 * else, this phone says so, with a "Not you? Sign it out" button. They're
 * security alerts, so they come whether or not server notifications are on.
 */
object Signins {
    private const val CHANNEL = "nocap_signins"
    private val io = Executors.newSingleThreadExecutor()

    private fun channel(ctx: Context) {
        ctx.getSystemService(NotificationManager::class.java)?.createNotificationChannel(
            NotificationChannel(CHANNEL, "NoCap sign-ins", NotificationManager.IMPORTANCE_HIGH).apply {
                description = "When someone signs in to your NoCap account on another device"
            },
        )
    }

    /**
     * Shows the alerts not shown yet. [live] alerts (from the long-poll) are
     * new by definition; a periodic check's first run only remembers where
     * things are, so linking doesn't dump the day's history on you.
     */
    @Synchronized
    fun deliver(ctx: Context, alerts: List<SigninAlert>, live: Boolean) {
        Prefs.init(ctx)
        val last = Prefs.lastSignin
        val newest = alerts.maxOfOrNull { it.id }
        if (newest == null) {
            if (last == 0L) Prefs.lastSignin = -1
            return
        }
        if (live || last != 0L) {
            for (a in alerts.filter { it.id > last }.sortedBy { it.id }.takeLast(3)) show(ctx, a)
        }
        if (newest > last) Prefs.lastSignin = newest
    }

    private fun notifId(a: SigninAlert) = 3000 + (a.id % 10_000).toInt().let { if (it < 0) -it else it }

    private fun show(ctx: Context, a: SigninAlert) {
        if (!Notices.allowed(ctx)) return
        channel(ctx)
        val id = notifId(a)
        val title = (if (Prefs.genz) a.genzTitle else null) ?: a.title.ifBlank { t("New sign-in just dropped", "New sign-in to your account") }
        val body = (if (Prefs.genz) a.genzBody else null) ?: a.body.ifBlank { label(a.deviceName, a.browser, a.os, a.kind) }
        val open = PendingIntent.getActivity(
            ctx, id, Intent(ctx, MainActivity::class.java).setAction(MainActivity.ACTION_HUB),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val signOut = PendingIntent.getActivity(
            ctx, id + 1,
            Intent(ctx, SignOutActivity::class.java)
                .putExtra(SignOutActivity.SESSION, a.sessionId)
                .putExtra(SignOutActivity.LABEL, label(a.deviceName, a.browser, a.os, a.kind) + if (a.ip.isNotBlank()) " · ${a.ip}" else "")
                .putExtra(SignOutActivity.NOTIF, id)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val n = NotificationCompat.Builder(ctx, CHANNEL)
            .setSmallIcon(R.drawable.ic_launcher_fg)
            .setContentTitle(title)
            .setContentText(body)
            .setStyle(NotificationCompat.BigTextStyle().bigText(body))
            .setCategory(NotificationCompat.CATEGORY_STATUS)
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setContentIntent(open)
            .setAutoCancel(true)
            .addAction(0, t("Not you? Sign it out", "Not you? Sign it out"), signOut)
            .build()
        try {
            NotificationManagerCompat.from(ctx).notify(id, n)
        } catch (_: SecurityException) {
            // Permission was pulled in the meantime.
        }
    }

    /** "Pixel", "Chrome 131 on Windows", "NoCap Desktop": what signed in. */
    fun label(deviceName: String, browser: String, os: String, kind: String): String = when {
        deviceName.isNotBlank() -> deviceName
        browser.isNotBlank() && os.isNotBlank() -> t("$browser on $os", "$browser on $os")
        browser.isNotBlank() -> browser
        kind == "desktop" -> t("NoCap Desktop on your server", "NoCap Desktop on the server")
        os.isNotBlank() -> os
        else -> t("Mystery device", "Unknown device")
    }

    fun label(s: SignedIn) = label(s.deviceName, s.browser, s.os, s.kind)

    fun kindName(kind: String) = when (kind) {
        "phone" -> t("Phone", "Phone")
        "desktop" -> t("Desktop", "Desktop")
        "cli" -> t("CLI", "Command line")
        else -> t("Web", "Web")
    }

    /** Ends a session in the background; [done] runs on the main thread. */
    fun end(ctx: Context, sessionId: String, done: (Boolean, String) -> Unit) {
        val app = ctx.applicationContext
        val link = Vault.get(app).link
        val main = android.os.Handler(android.os.Looper.getMainLooper())
        if (link == null) {
            done(false, t("Link your NoCap server first.", "Link a NoCap server first."))
            return
        }
        io.execute {
            val r = runCatching { NoCapApi(link).endSession(sessionId) }
            val (ok, msg) = when {
                r.isFailure -> false to t("Couldn't reach your server. Try again?", "Couldn't reach your server. Try again.")
                r.getOrDefault(false) -> true to t("Signed out. They're gone.", "That session is signed out.")
                else -> true to t("That one already signed out.", "That session had already ended.")
            }
            main.post { done(ok, msg) }
        }
    }
}

/** "Not you? Sign it out": asks once, then ends that session on the server. */
class SignOutActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        Prefs.init(this)
        val sid = intent.getStringExtra(SESSION).orEmpty()
        val label = intent.getStringExtra(LABEL).orEmpty()
        val notif = intent.getIntExtra(NOTIF, 0)
        if (sid.isBlank()) { finish(); return }
        AlertDialog.Builder(this)
            .setTitle(t("Sign it out?", "Sign out this session?"))
            .setMessage(
                t(
                    "$label gets kicked off your NoCap account right now. If it was you, just sign in again.",
                    "$label will be signed out of your NoCap account right away. If it was you, you can sign in again.",
                ),
            )
            .setPositiveButton(t("Sign it out", "Sign out")) { _, _ ->
                Signins.end(this, sid) { ok, msg ->
                    Toast.makeText(applicationContext, msg, Toast.LENGTH_SHORT).show()
                    if (ok && notif != 0) NotificationManagerCompat.from(applicationContext).cancel(notif)
                    finish()
                }
            }
            .setNegativeButton(t("Nah, it was me", "Cancel")) { _, _ -> finish() }
            .setOnCancelListener { finish() }
            .show()
    }

    companion object {
        const val SESSION = "session_id"
        const val LABEL = "label"
        const val NOTIF = "notification_id"
    }
}
