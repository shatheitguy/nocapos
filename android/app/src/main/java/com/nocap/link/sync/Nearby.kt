package com.nocap.link.sync

import android.content.Context
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import java.net.Inet6Address
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

/**
 * Finds NoCap servers on the home network: they announce themselves as
 * `_nocap._tcp` over mDNS, with their NoCap ID, scheme, version and setup
 * state in the TXT record. Blocking: call off the main thread.
 */
object Nearby {
    private const val TYPE = "_nocap._tcp"

    /** One server as mDNS describes it. */
    data class Resolved(val serviceName: String, val origin: String, val attrs: Map<String, String>)

    /** Origins of the server with [nocapId] only. */
    fun find(ctx: Context, nocapId: String, browseMs: Long = 2500): List<String> =
        browse(ctx, browseMs).filter { it.attrs["nocap_id"] == nocapId }.map { it.origin }.distinct()

    /** Every NoCap server that answered within [browseMs] (at most [max]). */
    fun browse(ctx: Context, browseMs: Long = 2500, max: Int = 8): List<Resolved> {
        val nsd = ctx.getSystemService(NsdManager::class.java) ?: return emptyList()
        val seen = mutableListOf<NsdServiceInfo>()
        val started = CountDownLatch(1)
        val listener = object : NsdManager.DiscoveryListener {
            override fun onDiscoveryStarted(serviceType: String?) { started.countDown() }
            override fun onDiscoveryStopped(serviceType: String?) {}
            override fun onStartDiscoveryFailed(serviceType: String?, errorCode: Int) { started.countDown() }
            override fun onStopDiscoveryFailed(serviceType: String?, errorCode: Int) {}
            override fun onServiceFound(info: NsdServiceInfo) { synchronized(seen) { seen += info } }
            override fun onServiceLost(info: NsdServiceInfo) {}
        }
        try {
            nsd.discoverServices(TYPE, NsdManager.PROTOCOL_DNS_SD, listener)
        } catch (_: Exception) {
            return emptyList()
        }
        try {
            started.await(1, TimeUnit.SECONDS)
            Thread.sleep(browseMs)
        } finally {
            runCatching { nsd.stopServiceDiscovery(listener) }
        }
        val found = synchronized(seen) { seen.distinctBy { it.serviceName }.toList() }
        // One at a time: older Android resolves only one service at once.
        return found.take(max).mapNotNull { resolve(nsd, it) }
    }

    @Suppress("DEPRECATION") // resolveService: the callback form works on every version we support
    private fun resolve(nsd: NsdManager, info: NsdServiceInfo): Resolved? {
        val done = CountDownLatch(1)
        var out: Resolved? = null
        try {
            nsd.resolveService(info, object : NsdManager.ResolveListener {
                override fun onResolveFailed(serviceInfo: NsdServiceInfo?, errorCode: Int) { done.countDown() }
                override fun onServiceResolved(r: NsdServiceInfo) {
                    val attrs = (r.attributes ?: emptyMap()).mapValues { (_, v) -> v?.let { String(it) } ?: "" }
                    val scheme = attrs["scheme"]?.takeIf { it == "http" || it == "https" } ?: "https"
                    val host = r.host
                    if (host != null && r.port in 1..65535) {
                        val h = host.hostAddress ?: ""
                        val name = if (host is Inet6Address) "[${h.substringBefore('%')}]" else h
                        if (name.isNotEmpty()) out = Resolved(r.serviceName.orEmpty(), "$scheme://$name:${r.port}", attrs)
                    }
                    done.countDown()
                }
            })
            done.await(3, TimeUnit.SECONDS)
        } catch (_: Exception) {
            return null
        }
        return out
    }
}
