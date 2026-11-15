package com.nocap.link.shield

/** What an app may do: everything, not on Wi-Fi, not on mobile data, nothing. */
enum class AppRule { ALLOW, NO_WIFI, NO_DATA, BLOCK }

/** The network the phone is on right now (under Shield). */
enum class NetKind { WIFI, MOBILE, OTHER, NONE }

/** When your server can't be reached: let DNS through the normal way, or block everything. */
enum class Fallback { OPEN, BLOCK }

/**
 * How the VPN runs:
 * - DNS: only Shield's DNS address goes into the VPN; every other packet
 *   of every app goes straight out as usual. Blocked apps (firewall) get
 *   no DNS answers.
 * - FIREWALL: only the blocked apps are in the VPN, with all their traffic
 *   (dropped there). Every other app bypasses the VPN completely.
 * - PASS: DNS address only, answered the normal way without filtering
 *   (Android's "block connections without VPN" is on, so Shield can't just
 *   step aside while it's paused on a trusted Wi-Fi).
 * - OFF: no VPN interface at all.
 */
enum class Mode { OFF, DNS, FIREWALL, PASS }

data class Net(val kind: NetKind, val ssid: String? = null)

data class Plan(val mode: Mode, val blocked: Set<String> = emptySet(), val filter: Boolean = false) {
    /** The DNS filter would run but the firewall's hard block took the VPN. */
    fun dnsPausedForFirewall(c: ShieldConfig, net: Net): Boolean = mode == Mode.FIREWALL && ShieldRules.dnsActive(c, net)
}

data class ShieldConfig(
    val dns: Boolean,
    val firewall: Boolean,
    /** Firewall by routing even while the DNS filter is on (the DNS filter pauses while apps are blocked). */
    val hardBlock: Boolean,
    val trustedWifi: Set<String>,
    val rules: Map<String, AppRule>,
    /** The phone can tell which app asked (Android 10+); else blocked apps always need routing. */
    val knowsApps: Boolean,
    /** Android's "Block connections without VPN" is on. */
    val lockdown: Boolean,
)

object ShieldRules {
    fun blocks(rule: AppRule, kind: NetKind): Boolean = when (rule) {
        AppRule.ALLOW -> false
        AppRule.BLOCK -> true
        AppRule.NO_WIFI -> kind == NetKind.WIFI
        AppRule.NO_DATA -> kind == NetKind.MOBILE
    }

    /** "MyWifi" from Android's "\"MyWifi\"" (null when Android hides it). */
    fun cleanSsid(raw: String?): String? {
        val s = raw?.trim()?.removeSurrounding("\"") ?: return null
        return s.takeIf { it.isNotEmpty() && it != "<unknown ssid>" && it != "0x" }
    }

    fun trusted(c: ShieldConfig, net: Net): Boolean = net.kind == NetKind.WIFI && net.ssid != null && net.ssid in c.trustedWifi

    fun dnsActive(c: ShieldConfig, net: Net): Boolean = c.dns && !trusted(c, net)

    fun blockedApps(c: ShieldConfig, net: Net): Set<String> =
        if (!c.firewall || net.kind == NetKind.NONE) emptySet() else c.rules.filterValues { blocks(it, net.kind) }.keys.toSortedSet()

    fun plan(c: ShieldConfig, net: Net): Plan {
        val blocked = blockedApps(c, net)
        val dns = dnsActive(c, net)
        return when {
            blocked.isNotEmpty() && (!dns || c.hardBlock || !c.knowsApps) -> Plan(Mode.FIREWALL, blocked, false)
            dns -> Plan(Mode.DNS, blocked, true)
            c.lockdown && (c.dns || c.firewall) -> Plan(Mode.PASS)
            else -> Plan(Mode.OFF)
        }
    }

    /**
     * Rules for apps installed since Shield last looked: blocked when "block
     * new apps" is on (only apps that weren't known before; known ones keep
     * whatever they had).
     */
    fun newApps(installed: Set<String>, known: Set<String>, blockNew: Boolean): Map<String, AppRule> =
        if (!blockNew) emptyMap() else (installed - known).associateWith { AppRule.BLOCK }
}
