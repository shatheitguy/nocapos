package com.nocap.link

import android.Manifest
import android.app.Application
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat

class NoCapApp : Application() {
    override fun onCreate() {
        super.onCreate()
        Prefs.init(this)
        Notices.channel(this)
        if (Vault.get(this).link != null) HeartbeatWorker.schedule(this)
        com.nocap.link.sync.Sync.start(this)
    }
}

/** The app's notifications: unlinked, downloads saved, and (opt-in) your server's own. */
object Notices {
    private const val CHANNEL = "nocap_link"
    private const val SERVER = "nocap_server"

    fun channel(ctx: Context) {
        val nm = ctx.getSystemService(NotificationManager::class.java)
        nm.createNotificationChannel(NotificationChannel(CHANNEL, "NoCap Link", NotificationManager.IMPORTANCE_DEFAULT).apply {
            description = "When your phone gets unlinked, and when a download from your NoCap desktop is saved"
        })
        nm.createNotificationChannel(NotificationChannel(SERVER, "NoCap server", NotificationManager.IMPORTANCE_DEFAULT).apply {
            description = "Notifications from your NoCapOS server (if you turned them on)"
        })
    }

    fun allowed(ctx: Context): Boolean =
        Build.VERSION.SDK_INT < 33 ||
            ContextCompat.checkSelfPermission(ctx, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED

    fun show(ctx: Context, id: Int, title: String, text: String, tap: Intent? = null, server: Boolean = false) {
        if (!allowed(ctx)) return
        val b = NotificationCompat.Builder(ctx, if (server) SERVER else CHANNEL)
            .setSmallIcon(R.drawable.ic_launcher_fg)
            .setContentTitle(title)
            .setContentText(text)
            .setStyle(NotificationCompat.BigTextStyle().bigText(text))
            .setAutoCancel(true)
        if (tap != null) {
            b.setContentIntent(PendingIntent.getActivity(ctx, id, tap, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT))
        }
        try {
            NotificationManagerCompat.from(ctx).notify(id, b.build())
        } catch (_: SecurityException) {
            // Permission was pulled in the meantime.
        }
    }
}
