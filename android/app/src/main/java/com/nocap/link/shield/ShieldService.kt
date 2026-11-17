package com.nocap.link.shield

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.PackageManager
import android.content.pm.ServiceInfo
import android.net.ConnectivityManager
import android.net.LinkProperties
import android.net.Network
import android.net.NetworkCapabilities
import android.net.VpnService
import android.net.wifi.WifiInfo
import android.net.wifi.WifiManager
import android.os.Build
import android.os.Handler
import android.os.HandlerThread
import android.os.ParcelFileDescriptor
import android.system.ErrnoException
import android.system.Os
import android.system.OsConstants
import android.system.StructPollfd
import com.nocap.link.R
import com.nocap.link.data.Prefs
import com.nocap.link.data.Vault
import com.nocap.link.data.t
import com.nocap.link.net.Endpoint
import com.nocap.link.net.Pins
import com.nocap.link.system.CrashLog
import java.io.FileDescriptor
import java.net.InetAddress
import java.net.InetSocketAddress
import java.util.concurrent.ArrayBlockingQueue
import java.util.concurrent.RejectedExecutionException
import java.util.concurrent.ThreadPoolExecutor
import java.util.concurrent.TimeUnit

/**
 * Shield on the phone: one VpnService doing the DNS filter and the app
 * firewall. See [Mode] for how traffic is routed; in short, only Shield's
 * DNS address (DNS mode) or only blocked apps (firewall mode) ever enter
 * the VPN, so nothing else is slowed down or seen.
 */
class ShieldService : VpnService() {
    private lateinit var cm: ConnectivityManager
    private lateinit var bg: Handler
    private var bgThread: HandlerThread? = null

    @Volatile private var tun: ParcelFileDescriptor? = null
    @Volatile private var gen = 0
    @Volatile private var plan = Plan(Mode.OFF)
    @Volatile private var underlying: Network? = null
    private var netNow = Net(NetKind.NONE)
    private val tcp = TcpDns()
    private val writeLock = Any()
    private val workers = ThreadPoolExecutor(2, 6, 30, TimeUnit.SECONDS, ArrayBlockingQueue(256))
    private val apps = AppNames()
    private val lookups = Bucket(40.0, 80.0)

    private lateinit var resolver: ShieldResolver
    private lateinit var passResolver: ShieldResolver

    override fun onCreate() {
        super.onCreate()
        Prefs.init(this)
        ShieldStore.init(this)
        cm = getSystemService(ConnectivityManager::class.java)
        bgThread = HandlerThread("shield").also { it.start() }
        bg = Handler(bgThread!!.looper)
        val normal = PlainUpstream({ dnsServers() }, { protect(it) })
        resolver = ShieldResolver(null, normal, { ShieldStore.fallback })
        passResolver = ShieldResolver(null, normal, { Fallback.OPEN })
        workers.allowCoreThreadTimeOut(true)
        instance = this
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            stopShield(userTurnedOff = true)
            return START_NOT_STICKY
        }
        if (!foreground()) {
            stopSelf()
            return START_NOT_STICKY
        }
        // Started by Android's Always-on VPN or after a reboot: Shield is on.
        if (!ShieldStore.enabled) ShieldStore.saveEnabled(true)
        bg.post { startWatching(); apply("start") }
        return START_STICKY
    }

    override fun onRevoke() {
        // Another VPN took the slot, or it was turned off in Android's settings.
        ShieldStore.serverNote = t("Another VPN took over (Android allows one at a time).", "Another VPN app was turned on. Android allows only one VPN at a time.")
        stopShield(userTurnedOff = true)
    }

    override fun onDestroy() {
        stopWatching()
        closeTun()
        workers.shutdownNow()
        bgThread?.quitSafely()
        ShieldStore.running = false
        ShieldStore.mode = Mode.OFF
        ShieldStore.saveLogs()
        if (instance === this) instance = null
        super.onDestroy()
    }

    // ---------- foreground ----------

    private fun foreground(): Boolean {
        val n = notification()
        return try {
            if (Build.VERSION.SDK_INT >= 34) {
                try {
                    startForeground(NOTE_ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_SYSTEM_EXEMPTED)
                } catch (e: SecurityException) {
                    startForeground(NOTE_ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
                }
            } else {
                startForeground(NOTE_ID, n)
            }
            true
        } catch (e: Exception) {
            CrashLog.warn("shield foreground", e)
            false
        }
    }

    private fun notification(): Notification {
        val nm = getSystemService(NotificationManager::class.java)
        if (nm.getNotificationChannel(CHANNEL) == null) {
            nm.createNotificationChannel(NotificationChannel(CHANNEL, "Shield", NotificationManager.IMPORTANCE_LOW).apply { setShowBadge(false) })
        }
        val open = PendingIntent.getActivity(this, 0, Intent(this, ShieldActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK), PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        val off = PendingIntent.getService(this, 1, Intent(this, ShieldService::class.java).setAction(ACTION_STOP), PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        val blocked = ShieldStore.blockedToday()
        val text = when (plan.mode) {
            Mode.DNS -> if (ShieldStore.serverUp) t("$blocked blocked today", "$blocked blocked today")
            else if (ShieldStore.fallback == Fallback.OPEN) t("Server's out of reach: normal DNS for now", "Server unreachable: using normal DNS")
            else t("Server's out of reach: blocking everything", "Server unreachable: blocking all lookups")
            Mode.FIREWALL -> t("Firewall on: ${plan.blocked.size} apps blocked here", "Firewall: ${plan.blocked.size} apps blocked on this network")
            Mode.PASS -> t("Paused on this Wi-Fi", "Paused on this Wi-Fi network")
            Mode.OFF -> t("Standing by", "Standing by")
        }
        return Notification.Builder(this, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat_nocap)
            .setContentTitle(t("Shield is on", "Shield is on"))
            .setContentText(text)
            .setContentIntent(open)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .addAction(Notification.Action.Builder(null as android.graphics.drawable.Icon?, t("Turn off", "Turn off"), off).build())
            .build()
    }

    private fun refreshNote() {
        runCatching { getSystemService(NotificationManager::class.java).notify(NOTE_ID, notification()) }
    }

    // ---------- watching the network and apps ----------

    private var netCallback: ConnectivityManager.NetworkCallback? = null
    private var pkgReceiver: BroadcastReceiver? = null
    private val replan = Runnable { apply("network") }
    private val saver = object : Runnable {
        override fun run() {
            ShieldStore.saveLogs()
            refreshNote()
            ShieldWidget.update(this@ShieldService)
            ShieldStore.serverUp = resolver.serverUp || resolver.server == null
            bg.postDelayed(this, 30_000)
        }
    }

    private fun startWatching() {
        if (netCallback != null) return
        // Android 12+ only tells the Wi-Fi name to callbacks that ask for it (and have location access).
        val cb = if (Build.VERSION.SDK_INT >= 31) {
            object : ConnectivityManager.NetworkCallback(ConnectivityManager.NetworkCallback.FLAG_INCLUDE_LOCATION_INFO) {
                override fun onAvailable(network: Network) = changed(network)
                override fun onCapabilitiesChanged(network: Network, caps: NetworkCapabilities) = changed(network, caps)
                override fun onLinkPropertiesChanged(network: Network, lp: LinkProperties) = changed(network)
                override fun onLost(network: Network) = lost(network)
            }
        } else {
            object : ConnectivityManager.NetworkCallback() {
                override fun onAvailable(network: Network) = changed(network)
                override fun onCapabilitiesChanged(network: Network, caps: NetworkCapabilities) = changed(network, caps)
                override fun onLinkPropertiesChanged(network: Network, lp: LinkProperties) = changed(network)
                override fun onLost(network: Network) = lost(network)
            }
        }
        // This app stays outside its own VPN, so its default network is the real one underneath.
        runCatching { cm.registerDefaultNetworkCallback(cb, bg) }.onSuccess { netCallback = cb }
        val r = object : BroadcastReceiver() {
            override fun onReceive(c: Context, i: Intent) {
                bg.post {
                    apps.clear()
                    if (ShieldStore.adoptNewApps(installedApps(this@ShieldService)) || ShieldStore.firewall) apply("apps")
                }
            }
        }
        val f = IntentFilter().apply {
            addAction(Intent.ACTION_PACKAGE_ADDED)
            addAction(Intent.ACTION_PACKAGE_REMOVED)
            addDataScheme("package")
        }
        runCatching { androidx.core.content.ContextCompat.registerReceiver(this, r, f, androidx.core.content.ContextCompat.RECEIVER_NOT_EXPORTED) }.onSuccess { pkgReceiver = r }
        bg.postDelayed(saver, 30_000)
        ShieldStore.adoptNewApps(installedApps(this))
    }

    private fun stopWatching() {
        netCallback?.let { runCatching { cm.unregisterNetworkCallback(it) } }
        netCallback = null
        pkgReceiver?.let { runCatching { unregisterReceiver(it) } }
        pkgReceiver = null
        if (::bg.isInitialized) bg.removeCallbacksAndMessages(null)
    }

    private fun lost(network: Network) {
        if (network == underlying) changed(null)
    }

    private fun changed(network: Network?, caps: NetworkCapabilities? = null) {
        underlying = network
        val c = caps ?: network?.let { runCatching { cm.getNetworkCapabilities(it) }.getOrNull() }
        val next = when {
            network == null || c == null -> Net(NetKind.NONE)
            c.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) -> Net(NetKind.WIFI, ssidOf(c))
            c.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) -> Net(NetKind.MOBILE)
            else -> Net(NetKind.OTHER)
        }
        if (next != netNow) {
            netNow = next
            resolver.retryNow()
            ShieldStore.net = next
        }
        bg.removeCallbacks(replan)
        bg.postDelayed(replan, 400)
    }

    private fun ssidOf(c: NetworkCapabilities): String? {
        if (Build.VERSION.SDK_INT >= 29) {
            (c.transportInfo as? WifiInfo)?.ssid?.let { s -> ShieldRules.cleanSsid(s)?.let { return it } }
        }
        @Suppress("DEPRECATION")
        return runCatching { ShieldRules.cleanSsid(applicationContext.getSystemService(WifiManager::class.java)?.connectionInfo?.ssid) }.getOrNull()
    }

    private fun dnsServers(): List<InetAddress> {
        val n = underlying ?: return emptyList()
        return runCatching { cm.getLinkProperties(n)?.dnsServers.orEmpty() }.getOrDefault(emptyList())
    }

    // ---------- the VPN interface ----------

    /** Works out the plan for this network and (re)builds the interface if it changed. */
    private fun apply(why: String) {
        if (!ShieldStore.enabled) return
        val link = runCatching { Vault.get(this).link }.getOrNull()
        resolver.server = link?.let { l -> DohUpstream(Pins.client(l.pin), { Endpoint.server(l) }, l.token) }
        val lockdown = Build.VERSION.SDK_INT >= 29 && runCatching { isLockdownEnabled }.getOrDefault(false)
        val cfg = ShieldStore.config(knowsApps = Build.VERSION.SDK_INT >= 29, lockdown = lockdown)
        val next = ShieldRules.plan(cfg, netNow)
        ShieldStore.dnsPaused = next.dnsPausedForFirewall(cfg, netNow)
        val same = next == plan && (tun != null) == (next.mode != Mode.OFF)
        if (!same) {
            if (next.mode == Mode.OFF) {
                closeTun()
            } else {
                val fd = build(next)
                if (fd == null) {
                    // Android said no (consent gone): nothing else to do.
                    if (VpnService.prepare(this) != null) {
                        stopShield(userTurnedOff = true)
                        return
                    }
                    closeTun()
                    plan = Plan(Mode.OFF)
                } else {
                    swapTun(fd)
                }
            }
            if (fd() != null || next.mode == Mode.OFF) plan = next
            tcp.clear()
        }
        underlying?.let { n -> runCatching { setUnderlyingNetworks(arrayOf(n)) } }
        ShieldStore.mode = plan.mode
        ShieldStore.running = true
        ShieldStore.serverUp = resolver.server != null && resolver.serverUp
        refreshNote()
        Shield.changed(this)
    }

    private fun fd(): ParcelFileDescriptor? = tun

    private fun build(p: Plan): ParcelFileDescriptor? {
        val b = Builder().setSession("NoCap Shield").setMtu(MTU)
        runCatching { b.setConfigureIntent(PendingIntent.getActivity(this, 2, Intent(this, ShieldActivity::class.java), PendingIntent.FLAG_IMMUTABLE)) }
        if (Build.VERSION.SDK_INT >= 29) b.setMetered(false)
        underlying?.let { b.setUnderlyingNetworks(arrayOf(it)) }
        when (p.mode) {
            Mode.DNS, Mode.PASS -> {
                b.addAddress(TUN_V4, 32)
                b.addDnsServer(DNS_V4)
                b.addRoute(DNS_V4, 32)
                runCatching { b.addDisallowedApplication(packageName) }
            }
            Mode.FIREWALL -> {
                b.addAddress(TUN_V4, 32)
                b.addAddress(TUN_V6, 128)
                b.addRoute("0.0.0.0", 0)
                b.addRoute("::", 0)
                var added = 0
                for (pkg in p.blocked) {
                    if (pkg == packageName) continue
                    try {
                        b.addAllowedApplication(pkg)
                        added++
                    } catch (_: PackageManager.NameNotFoundException) {
                    }
                }
                // With no app named, a VPN takes every app: never do that here.
                if (added == 0) return null
            }
            Mode.OFF -> return null
        }
        return try {
            b.establish()
        } catch (e: Exception) {
            CrashLog.warn("shield establish", e)
            null
        }
    }

    private fun swapTun(fd: ParcelFileDescriptor) {
        val old = tun
        // VpnService interfaces are non-blocking by default: the reader polls with a timeout.
        tun = fd
        val g = ++gen
        Thread({ readLoop(fd, g) }, "shield-tun").apply { isDaemon = true }.start()
        runCatching { old?.close() }
    }

    private fun closeTun() {
        gen++
        val old = tun
        tun = null
        runCatching { old?.close() }
    }

    private fun readLoop(pfd: ParcelFileDescriptor, g: Int) {
        val fd = pfd.fileDescriptor
        val buf = ByteArray(MTU + 64)
        val poll = arrayOf(StructPollfd().apply { this.fd = fd; events = OsConstants.POLLIN.toShort() })
        while (g == gen) {
            try {
                poll[0].revents = 0
                if (Os.poll(poll, 1000) <= 0) continue
                val n = Os.read(fd, buf, 0, buf.size)
                if (n <= 0) continue
                handle(buf, n, fd, g)
            } catch (e: ErrnoException) {
                if (e.errno == OsConstants.EAGAIN || e.errno == OsConstants.EINTR) continue
                break // closed
            } catch (e: Exception) {
                CrashLog.warn("shield read", e)
                break
            }
        }
    }

    private fun write(fd: FileDescriptor, g: Int, pkt: ByteArray) {
        if (g != gen) return
        synchronized(writeLock) {
            try {
                Os.write(fd, pkt, 0, pkt.size)
            } catch (_: Exception) {
                // Interface gone or full: the asker retries.
            }
        }
    }

    private fun handle(buf: ByteArray, n: Int, fd: FileDescriptor, g: Int) {
        val p = IpPacket.parse(buf, n) ?: return
        val mode = plan.mode
        if (mode == Mode.FIREWALL) {
            noteAttempt(p)
            return // dropped: that's the point
        }
        if (p.v6 || !p.dst.contentEquals(DNS_V4_BYTES)) return
        when {
            p.proto == PROTO_UDP && p.dstPort == 53 -> {
                val query = p.payload()
                val src = p.src; val sport = p.srcPort; val dst = p.dst
                val local = InetSocketAddress(InetAddress.getByAddress(src), sport)
                submit {
                    val answer = answer(query, local, InetSocketAddress(InetAddress.getByAddress(dst), 53), OsConstants.IPPROTO_UDP) ?: return@submit
                    val fit = Dns.truncated(query, answer, MTU - 28)
                    write(fd, g, Packets.udp(dst, 53, src, sport, fit))
                }
            }
            p.proto == PROTO_TCP && p.dstPort == 53 -> {
                val now = System.currentTimeMillis()
                val out = synchronized(tcp) { tcp.onPacket(p, now) }
                out.send.forEach { write(fd, g, it) }
                val local = InetSocketAddress(InetAddress.getByAddress(p.src), p.srcPort)
                val remote = InetSocketAddress(InetAddress.getByAddress(p.dst), 53)
                for ((key, q) in out.ask) {
                    submit {
                        val a = answer(q, local, remote, OsConstants.IPPROTO_TCP)
                        val segs = synchronized(tcp) { tcp.answer(key, a, System.currentTimeMillis()) }
                        segs.forEach { write(fd, g, it) }
                    }
                }
            }
            p.proto == PROTO_TCP && p.tcpFlags and Packets.TCP_SYN != 0 -> {
                // Encrypted DNS (853) or anything else to Shield's address: say no right away.
                write(fd, g, Packets.tcp(p.dst, p.dstPort, p.src, p.srcPort, 0, (p.tcpSeq + 1) and TcpDns.MASK, Packets.TCP_RST or Packets.TCP_ACK))
            }
        }
    }

    private fun submit(job: () -> Unit) {
        try {
            workers.execute {
                try {
                    job()
                } catch (e: Exception) {
                    CrashLog.warn("shield dns", e)
                }
            }
        } catch (_: RejectedExecutionException) {
            // Too busy: dropped, the asker retries.
        }
    }

    /** The answer for one question from the app at [local]. */
    private fun answer(query: ByteArray, local: InetSocketAddress, remote: InetSocketAddress, proto: Int): ByteArray? {
        val q = Dns.question(query) ?: return null
        val p = plan
        if (p.mode == Mode.PASS) return passResolver.resolve(query)?.msg
        // Blocked apps (firewall, DNS mode): no answers at all.
        var app: String? = null
        if (p.blocked.isNotEmpty()) {
            val pkg = ownerOf(proto, local, remote)
            if (pkg != null && pkg in p.blocked) {
                ShieldStore.attempt(apps.label(this, pkg), q.name)
                return Dns.reply(query, Dns.RCODE_REFUSED)
            }
            app = pkg
        }
        val a = resolver.resolve(query) ?: return null
        val wasUp = ShieldStore.serverUp
        val up = a.via == Via.SERVER
        if (wasUp != up) {
            ShieldStore.serverUp = up
            ShieldStore.serverNote = resolver.lastError
            bg.post { refreshNote() }
        }
        if (a.blocked && app == null) app = ownerOf(proto, local, remote)
        ShieldStore.asked(q.name, a.blocked, app?.let { apps.label(this, it) }, a.via == Via.REFUSED)
        return a.msg
    }

    /** The package behind a connection (Android 10+; rate-limited). */
    private fun ownerOf(proto: Int, local: InetSocketAddress, remote: InetSocketAddress): String? {
        if (Build.VERSION.SDK_INT < 29 || !lookups.take()) return null
        val uid = runCatching { cm.getConnectionOwnerUid(proto, local, remote) }.getOrDefault(-1)
        if (uid < 0) return null
        return apps.packageOf(this, uid)
    }

    private fun noteAttempt(p: IpPacket) {
        val first = when (p.proto) {
            PROTO_TCP -> p.tcpFlags and Packets.TCP_SYN != 0 && p.tcpFlags and Packets.TCP_ACK == 0
            PROTO_UDP -> true
            else -> false
        }
        if (!first) return
        val dest = runCatching { InetAddress.getByAddress(p.dst).hostAddress }.getOrNull() ?: return
        val shown = if (p.v6) "[$dest]:${p.dstPort}" else "$dest:${p.dstPort}"
        val blocked = plan.blocked
        var pkg: String? = if (blocked.size == 1) blocked.first() else null
        if (pkg == null) {
            val proto = if (p.proto == PROTO_TCP) OsConstants.IPPROTO_TCP else OsConstants.IPPROTO_UDP
            pkg = ownerOf(proto, InetSocketAddress(InetAddress.getByAddress(p.src), p.srcPort), InetSocketAddress(InetAddress.getByAddress(p.dst), p.dstPort))
        }
        ShieldStore.attempt(pkg?.let { apps.label(this, it) } ?: t("A blocked app", "A blocked app"), shown)
    }

    /** Turns Shield off (from the page, the tile, the notification or Android). */
    fun stopShield(userTurnedOff: Boolean) {
        if (userTurnedOff) ShieldStore.saveEnabled(false)
        stopWatching()
        closeTun()
        plan = Plan(Mode.OFF)
        ShieldStore.running = false
        ShieldStore.mode = Mode.OFF
        ShieldStore.saveLogs()
        runCatching { stopForeground(STOP_FOREGROUND_REMOVE) }
        stopSelf()
        Shield.changed(this)
    }

    /** Settings changed: work out the plan again. */
    fun reload() {
        bg.post { apply("settings") }
    }

    companion object {
        const val ACTION_START = "com.nocap.link.shield.START"
        const val ACTION_STOP = "com.nocap.link.shield.STOP"
        private const val CHANNEL = "shield"
        private const val NOTE_ID = 7301
        const val MTU = 1500
        const val TUN_V4 = "10.111.222.2"
        const val TUN_V6 = "fd00:6e63:7368::2"
        const val DNS_V4 = "10.111.222.53"
        private val DNS_V4_BYTES = byteArrayOf(10, 111, 222.toByte(), 53)

        @Volatile var instance: ShieldService? = null
            private set
    }
}

/** Package names and labels by uid, cached (bounded). */
private class AppNames {
    private val byUid = object : LinkedHashMap<Int, String>(64, 0.75f, true) {
        override fun removeEldestEntry(e: MutableMap.MutableEntry<Int, String>?) = size > 256
    }
    private val labels = object : LinkedHashMap<String, String>(64, 0.75f, true) {
        override fun removeEldestEntry(e: MutableMap.MutableEntry<String, String>?) = size > 256
    }

    @Synchronized fun clear() {
        byUid.clear()
        labels.clear()
    }

    @Synchronized fun packageOf(ctx: Context, uid: Int): String? =
        byUid[uid] ?: runCatching { ctx.packageManager.getPackagesForUid(uid)?.firstOrNull() }.getOrNull()?.also { byUid[uid] = it }

    @Synchronized fun label(ctx: Context, pkg: String): String =
        labels[pkg] ?: runCatching {
            val pm = ctx.packageManager
            pm.getApplicationLabel(pm.getApplicationInfo(pkg, 0)).toString()
        }.getOrDefault(pkg).also { labels[pkg] = it }
}

/** A token bucket: at most [rate] a second, bursts of [burst]. */
internal class Bucket(private val rate: Double, private val burst: Double, private val now: () -> Long = System::nanoTime) {
    private var tokens = burst
    private var last = now()

    @Synchronized fun take(): Boolean {
        val t = now()
        tokens = minOf(burst, tokens + (t - last) / 1e9 * rate)
        last = t
        if (tokens < 1) return false
        tokens -= 1
        return true
    }
}
