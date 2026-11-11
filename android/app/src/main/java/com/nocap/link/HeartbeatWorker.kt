package com.nocap.link

import android.content.Context
import android.content.Intent
import android.os.BatteryManager
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkerParameters
import androidx.work.WorkManager
import com.nocap.link.data.Link
import com.nocap.link.data.Prefs
import com.nocap.link.data.t
import com.nocap.link.data.Vault
import com.nocap.link.net.ApiError
import com.nocap.link.net.NoCapApi
import com.nocap.link.sync.Notices
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/**
 * Tells the server this phone is still around (battery + app version), so
 * Settings → Devices shows when it was last seen. Every 15 minutes, only
 * with a network.
 */
class HeartbeatWorker(ctx: Context, params: WorkerParameters) : CoroutineWorker(ctx, params) {
    override suspend fun doWork(): Result = withContext(Dispatchers.IO) {
        val vault = Vault.get(applicationContext)
        val link = vault.link ?: return@withContext Result.success()
        try {
            val api = NoCapApi(link)
            api.heartbeat(battery(applicationContext), BuildConfig.VERSION_NAME)
            if (vault.unlinked) vault.unlinked = false
            Prefs.init(applicationContext)
            if (Prefs.serverNotices) runCatching { notices(api, link.serverName) }
            // Sign-in alerts always (a server without them answers ServerTooOld: skip).
            runCatching { com.nocap.link.sync.Signins.deliver(applicationContext, api.sessions().second, live = false) }
            Result.success()
        } catch (e: ApiError) {
            if (e.status == 401 && !vault.unlinked) {
                vault.unlinked = true
                Notices.show(
                    applicationContext, 1, "NoCap Link: phone unlinked",
                    "${link.serverName.ifBlank { "Your NoCapOS" }} doesn't know this phone anymore. Open NoCap OS to link it again.",
                    Intent(applicationContext, MainActivity::class.java),
                )
            }
            Result.success() // nothing to retry: the server answered
        } catch (_: Exception) {
            Result.success() // offline or cert changed: the next run tries again
        }
    }

    /**
     * Your server's notifications as phone notifications (opt-in). The first
     * check only remembers where things are, so turning it on doesn't dump
     * the whole history on you.
     */
    private fun notices(api: NoCapApi, serverName: String) {
        val last = Prefs.lastNotice
        val (_, items) = api.notices(last.coerceAtLeast(0))
        val newest = items.maxOfOrNull { it.id }
        if (newest == null) {
            if (last == 0L) Prefs.lastNotice = -1 // checked once: nothing yet
            return
        }
        if (last != 0L) {
            for (n in items.filter { it.id > last }.sortedBy { it.id }.takeLast(5)) {
                val title = (if (Prefs.genz) n.genzTitle else null) ?: n.title
                val body = (if (Prefs.genz) n.genzBody else null) ?: n.body
                Notices.show(
                    applicationContext, 1000 + (n.id % 10_000).toInt(), title.ifBlank { serverName.ifBlank { "NoCapOS" } }, body.ifBlank { serverName },
                    Intent(applicationContext, MainActivity::class.java).setAction(MainActivity.ACTION_HUB), server = true,
                )
            }
        }
        Prefs.lastNotice = newest
    }

    companion object {
        private const val NAME = "nocap-heartbeat"

        fun schedule(ctx: Context) {
            val net = Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build()
            val wm = WorkManager.getInstance(ctx)
            wm.enqueueUniquePeriodicWork(
                NAME, ExistingPeriodicWorkPolicy.KEEP,
                PeriodicWorkRequestBuilder<HeartbeatWorker>(15, TimeUnit.MINUTES).setConstraints(net).build(),
            )
        }

        /** One right now (after linking). */
        fun now(ctx: Context) {
            WorkManager.getInstance(ctx).enqueueUniqueWork(
                "$NAME-now", ExistingWorkPolicy.REPLACE, OneTimeWorkRequestBuilder<HeartbeatWorker>().build(),
            )
        }

        fun cancel(ctx: Context) {
            WorkManager.getInstance(ctx).cancelUniqueWork(NAME)
        }

        fun battery(ctx: Context): Int? {
            val bm = ctx.getSystemService(BatteryManager::class.java) ?: return null
            val v = bm.getIntProperty(BatteryManager.BATTERY_PROPERTY_CAPACITY)
            return v.takeIf { it in 0..100 }
        }
    }
}
