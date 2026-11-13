package com.nocap.link.sync

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.ServiceInfo
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.os.BatteryManager
import android.os.Build
import android.os.IBinder
import android.os.PowerManager
import android.provider.Settings
import androidx.compose.runtime.mutableStateOf
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import com.nocap.link.MainActivity
import com.nocap.link.R
import com.nocap.link.data.LinkOpt
import com.nocap.link.data.Prefs
import com.nocap.link.data.Vault
import com.nocap.link.data.t
import com.nocap.link.net.Endpoint
import com.nocap.link.net.NoCapApi
import com.nocap.link.net.Route
import com.nocap.link.net.RoutePlan
import com.nocap.link.net.addresses
import com.nocap.link.net.postState
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * NoCap Link, always connected: a foreground service with a quiet "NoCap
 * Link connected" notification that keeps one long-poll open to your own
 * server, so its notifications, clipboard and requests (ring, open, files,
 * replies) reach the phone in the background, after a reboot and after the
 * app is swiped away. It picks Nearby (home network, found with mDNS or
 * listed by the server) before Remote, and checks again whenever the
 * network changes. No push service: only your server.
 */
class LinkService : Service() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO + com.nocap.link.system.CrashLog.handler("NoCap Link service"))
    private var loop: Job? = null
    private var netCb: ConnectivityManager.NetworkCallback? = null
    private var lastNetKey = ""
    private var lastState = ""

    private val battery = object : BroadcastReceiver() {
        override fun onReceive(context: Context, intent: Intent) { report() }
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        Prefs.init(this)
        LinkOpt.init(this)
        running = true
        foreground()
        ContextCompat.registerReceiver(
            this, battery,
            IntentFilter().apply { addAction(Intent.ACTION_BATTERY_CHANGED); addAction(Intent.ACTION_POWER_CONNECTED); addAction(Intent.ACTION_POWER_DISCONNECTED) },
            ContextCompat.RECEIVER_NOT_EXPORTED,
        )
        val cm = getSystemService(ConnectivityManager::class.java)
        netCb = object : ConnectivityManager.NetworkCallback() {
            override fun onCapabilitiesChanged(network: Network, caps: NetworkCapabilities) { networkChanged() }
            override fun onLost(network: Network) { networkChanged() }
        }.also { cb -> runCatching { cm?.registerDefaultNetworkCallback(cb) } }
        FindPhone.onChange = { report(force = true) }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        foreground()
        if (runCatching { Vault.get(this).link }.getOrNull() == null || !LinkOpt.alwaysOn.v) {
            stopSelf()
            return START_NOT_STICKY
        }
        if (loop?.isActive != true) restart()
        return START_STICKY
    }

    override fun onDestroy() {
        running = false
        status.value = Status(false, null)
        runCatching { unregisterReceiver(battery) }
        netCb?.let { cb -> runCatching { getSystemService(ConnectivityManager::class.java)?.unregisterNetworkCallback(cb) } }
        FindPhone.onChange = null
        scope.cancel()
        super.onDestroy()
    }

    private fun networkChanged() {
        val key = netKind(this)
        if (key == lastNetKey) return
        lastNetKey = key
        restart()
    }

    /** Pick the route again, then follow the server. */
    private fun restart() {
        loop?.cancel()
        loop = scope.launch {
            delay(800) // let the new network settle
            chooseRoute(this@LinkService)
            update()
            report(force = true)
            Sync.listen(this@LinkService, inService = true)
        }
    }

    private fun foreground() {
        val n = notification(this)
        val type = if (Build.VERSION.SDK_INT >= 34) ServiceInfo.FOREGROUND_SERVICE_TYPE_REMOTE_MESSAGING else 0
        runCatching { ServiceCompat.startForeground(this, ID, n, type) }
            .onFailure { com.nocap.link.system.CrashLog.warn("link service", it) }
    }

    private fun update() {
        try {
            NotificationManagerCompat.from(this).notify(ID, notification(this))
        } catch (_: SecurityException) {
        }
    }

    /** Battery, network and route for NoCap OS (only when something changed). */
    private fun report(force: Boolean = false) {
        val link = runCatching { Vault.get(this).link }.getOrNull() ?: return
        val bi = registerReceiver(null, IntentFilter(Intent.ACTION_BATTERY_CHANGED))
        val level = bi?.let { val l = it.getIntExtra(BatteryManager.EXTRA_LEVEL, -1); val s = it.getIntExtra(BatteryManager.EXTRA_SCALE, 100); if (l >= 0 && s > 0) l * 100 / s else null }
        val status = bi?.getIntExtra(BatteryManager.EXTRA_STATUS, -1) ?: -1
        val charging = status == BatteryManager.BATTERY_STATUS_CHARGING || status == BatteryManager.BATTERY_STATUS_FULL
        val route = when (Endpoint.route) { Route.NEARBY -> "local"; Route.REMOTE -> "remote"; null -> "" }
        val net = netKind(this)
        val key = "$level|$charging|$net|$route|${FindPhone.ringing}"
        if (!force && key == lastState) return
        lastState = key
        scope.launch { runCatching { NoCapApi(link).postState(level, charging, net, route, FindPhone.ringing) } }
    }

    /** What the person sees in the app and the notification. */
    data class Status(val connected: Boolean, val route: Route?)

    companion object {
        private const val ID = 4100
        private const val CHANNEL = "nocap_link_live"
        @Volatile private var running = false
        val status = mutableStateOf(Status(false, null))

        /** Starts the service; false when Android won't let it start right now. */
        fun start(ctx: Context): Boolean {
            if (running) return true
            return runCatching { ContextCompat.startForegroundService(ctx, Intent(ctx, LinkService::class.java)) }
                .onFailure { com.nocap.link.system.CrashLog.warn("link service start", it) }
                .isSuccess.also { if (it) LinkWatchdog.schedule(ctx) }
        }

        fun stop(ctx: Context) {
            if (running) runCatching { ctx.stopService(Intent(ctx, LinkService::class.java)) }
        }

        /** The long-poll answered (or failed). */
        fun connected(ok: Boolean) {
            if (status.value.connected != ok || status.value.route != Endpoint.route) status.value = Status(ok, Endpoint.route)
        }

        fun netKind(ctx: Context): String {
            val cm = ctx.getSystemService(ConnectivityManager::class.java) ?: return ""
            val caps = runCatching { cm.getNetworkCapabilities(cm.activeNetwork) }.getOrNull() ?: return "none"
            return when {
                caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) -> "wifi"
                caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET) -> "ethernet"
                caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) -> "cellular"
                else -> "other"
            }
        }

        /**
         * Nearby or Remote: tries the home-network addresses first when on
         * Wi-Fi or Ethernet, then the remote one. Each must be this same
         * server (NoCap ID) behind the pinned certificate.
         */
        fun chooseRoute(ctx: Context) {
            val link = runCatching { Vault.get(ctx).link }.getOrNull() ?: return
            val net = netKind(ctx)
            val atHome = net == "wifi" || net == "ethernet"
            val known = runCatching { NoCapApi(link).addresses() }.getOrNull()
                ?: runCatching { NoCapApi(link.server, link.pin, link.token).addresses() }.getOrNull()
            val found = if (atHome) runCatching { Nearby.find(ctx, link.nocapId) }.getOrDefault(emptyList()) else emptyList()
            val lan = known?.lan.orEmpty()
            Endpoint.lan = (found + lan).toSet()
            for (origin in RoutePlan.order(atHome, found, lan, known?.remote, link.server)) {
                val ok = runCatching { NoCapApi(origin, link.pin, link.token).pairInfo().nocapId == link.nocapId }.getOrDefault(false)
                if (ok) {
                    Endpoint.use(origin, RoutePlan.routeOf(origin, Endpoint.lan))
                    connected(status.value.connected)
                    return
                }
            }
            Endpoint.reset()
        }

        private fun notification(ctx: Context): android.app.Notification {
            val nm = ctx.getSystemService(NotificationManager::class.java)
            runCatching {
                nm?.createNotificationChannel(NotificationChannel(CHANNEL, "NoCap Link connection", NotificationManager.IMPORTANCE_MIN).apply {
                    description = "Keeps this phone connected to your NoCap server"
                    setShowBadge(false)
                })
            }
            val where = when (Endpoint.route) {
                Route.NEARBY -> t("Nearby", "Nearby")
                Route.REMOTE -> t("Remote", "Remote")
                null -> t("finding your server…", "connecting…")
            }
            val tap = PendingIntent.getActivity(
                ctx, ID, Intent(ctx, MainActivity::class.java).setAction(MainActivity.ACTION_HUB),
                PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
            )
            return NotificationCompat.Builder(ctx, CHANNEL)
                .setSmallIcon(R.drawable.ic_stat_nocap)
                .setContentTitle(t("NoCap Link connected", "NoCap Link connected"))
                .setContentText(where)
                .setPriority(NotificationCompat.PRIORITY_MIN)
                .setCategory(NotificationCompat.CATEGORY_SERVICE)
                .setOngoing(true)
                .setShowWhen(false)
                .setSilent(true)
                .setContentIntent(tap)
                .build()
        }

        // ---------- battery optimisation ----------

        /** Android leaves the connection alone in Doze only when this is true. */
        fun unrestricted(ctx: Context): Boolean =
            ctx.getSystemService(PowerManager::class.java)?.isIgnoringBatteryOptimizations(ctx.packageName) == true

        /** Asks Android to let NoCap OS stay connected (the system shows its own dialog). */
        @android.annotation.SuppressLint("BatteryLife")
        fun askUnrestricted(ctx: Context) {
            val ask = Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, android.net.Uri.parse("package:${ctx.packageName}"))
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            if (runCatching { ctx.startActivity(ask) }.isFailure) {
                runCatching { ctx.startActivity(Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
            }
        }
    }
}

/** Starts the link again after a reboot or an app update. */
class LinkBootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent?) {
        when (intent?.action) {
            Intent.ACTION_BOOT_COMPLETED, Intent.ACTION_MY_PACKAGE_REPLACED -> Sync.start(context)
        }
    }
}

/** Every 15 minutes: if the link service died (or was killed), start it again. */
class LinkWatchdog(ctx: Context, params: WorkerParameters) : CoroutineWorker(ctx, params) {
    override suspend fun doWork(): Result {
        Prefs.init(applicationContext)
        LinkOpt.init(applicationContext)
        if (LinkOpt.alwaysOn.v && runCatching { Vault.get(applicationContext).link }.getOrNull() != null) {
            LinkService.start(applicationContext)
        }
        return Result.success()
    }

    companion object {
        fun schedule(ctx: Context) {
            WorkManager.getInstance(ctx).enqueueUniquePeriodicWork(
                "nocap-link-watchdog", ExistingPeriodicWorkPolicy.KEEP,
                PeriodicWorkRequestBuilder<LinkWatchdog>(15, TimeUnit.MINUTES).setConstraints(Constraints.NONE).build(),
            )
        }
    }
}
