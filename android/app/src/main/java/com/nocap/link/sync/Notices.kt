package com.nocap.link.sync

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.Manifest
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import com.nocap.link.R

/** The app's notifications: unlinked, downloads saved, and (opt-in) your server's own. */
object Notices {
    private const val CHANNEL = "nocap_link"
    private const val SERVER = "nocap_server"

    fun channel(ctx: Context) {
        val nm = ctx.getSystemService(NotificationManager::class.java) ?: return
        runCatching {
            nm.createNotificationChannel(NotificationChannel(CHANNEL, "NoCap Link", NotificationManager.IMPORTANCE_DEFAULT).apply {
                description = "When your phone gets unlinked, and when a download from your NoCap desktop is saved"
            })
            nm.createNotificationChannel(NotificationChannel(SERVER, "NoCap server", NotificationManager.IMPORTANCE_DEFAULT).apply {
                description = "Notifications from your NoCapOS server (if you turned them on)"
            })
        }
    }

    /** Android 13+ asks before an app may post notifications; older versions only have the per-app switch. */
    fun allowed(ctx: Context): Boolean {
        val runtime = Build.VERSION.SDK_INT < 33 ||
            ContextCompat.checkSelfPermission(ctx, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED
        return runtime && NotificationManagerCompat.from(ctx).areNotificationsEnabled()
    }

    fun show(ctx: Context, id: Int, title: String, text: String, tap: Intent? = null, server: Boolean = false) {
        if (!allowed(ctx)) return
        channel(ctx)
        val b = NotificationCompat.Builder(ctx, if (server) SERVER else CHANNEL)
            .setSmallIcon(R.drawable.ic_stat_nocap)
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
