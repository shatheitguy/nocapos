package com.nocap.link.sync

import android.content.Context
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import java.net.Inet6Address
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

/**
 * Finds NoCap servers on the home network: they announce themselves as
 * `_nocap._tcp` over mDNS, with their NoCap ID and scheme in the TXT
 * record. Returns origins of the server with [nocapId] only. Blocking: call
 * off the main thread.
 */
object Nearby {
    private const val TYPE = "_nocap._tcp"

    fun find(ctx: Context, nocapId: String, browseMs: Long = 2500): List<String> {
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
        val found = synchronized(seen) { seen.toList() }
        return found.take(8).mapNotNull { resolve(nsd, it, nocapId) }.distinct()
    }

    @Suppress("DEPRECATION") // resolveService: the callback form works on every version we support
    private fun resolve(nsd: NsdManager, info: NsdServiceInfo, nocapId: String): String? {
        val done = CountDownLatch(1)
        var out: String? = null
        try {
            nsd.resolveService(info, object : NsdManager.ResolveListener {
                override fun onResolveFailed(serviceInfo: NsdServiceInfo?, errorCode: Int) { done.countDown() }
                override fun onServiceResolved(r: NsdServiceInfo) {
                    val attrs = r.attributes ?: emptyMap()
                    val id = attrs["nocap_id"]?.let { String(it) }
                    val scheme = attrs["scheme"]?.let { String(it) }?.takeIf { it == "http" || it == "https" } ?: "https"
                    val host = r.host
                    if (id == nocapId && host != null && r.port in 1..65535) {
                        val h = host.hostAddress ?: ""
                        val name = if (host is Inet6Address) "[${h.substringBefore('%')}]" else h
                        if (name.isNotEmpty()) out = "$scheme://$name:${r.port}"
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
