package com.nocap.link.net

import com.nocap.link.data.Link
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

/** How this phone reaches its server right now. */
enum class Route { NEARBY, REMOTE }

/**
 * Nearby or Remote: which of the server's addresses to try, in order. Pure
 * logic (no Android), so it's unit-tested.
 */
object RoutePlan {
    /**
     * At home (Wi-Fi or Ethernet) the home-network addresses come first:
     * the ones found with mDNS, then the ones the server listed, then the
     * paired address if it's a home one. Then the remote address and the
     * paired one. Away (mobile data, or nothing better), only those last two.
     */
    fun order(atHome: Boolean, found: List<String>, lan: List<String>, remote: String?, paired: String): List<String> {
        val out = LinkedHashSet<String>()
        if (atHome) {
            found.forEach { out += it }
            lan.forEach { out += it }
            if (isHomeAddress(paired)) out += paired
        }
        remote?.takeIf { it.isNotBlank() }?.let { out += it }
        out += paired
        return out.toList()
    }

    /** Nearby when the address is on the home network, Remote otherwise. */
    fun routeOf(origin: String, lan: Collection<String>): Route =
        if (origin in lan || isHomeAddress(origin)) Route.NEARBY else Route.REMOTE

    /** A private (RFC 1918 / link-local / ULA) IP, or a *.local name. */
    fun isHomeAddress(origin: String): Boolean {
        val host = origin.toHttpUrlOrNull()?.host?.lowercase() ?: return false
        if (host.endsWith(".local")) return true
        if (host.contains(':')) return host.startsWith("fe80:") || host.startsWith("fc") || host.startsWith("fd")
        val p = host.split('.').mapNotNull { it.toIntOrNull() }
        if (p.size != 4 || p.any { it !in 0..255 }) return false
        return p[0] == 10 || (p[0] == 172 && p[1] in 16..31) || (p[0] == 192 && p[1] == 168) || (p[0] == 169 && p[1] == 254)
    }
}

/**
 * The address the whole app talks to now: chosen by the link service
 * (Nearby first, Remote otherwise); the paired address until then.
 */
object Endpoint {
    @Volatile var active: String? = null
        private set
    @Volatile var route: Route? = null
        private set
    /** Server addresses (for the Nearby/Remote label). */
    @Volatile var lan: Set<String> = emptySet()

    fun server(link: Link): String = active ?: link.server

    fun use(origin: String, r: Route) {
        active = origin
        route = r
    }

    fun reset() {
        active = null
        route = null
    }
}
