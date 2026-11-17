package com.nocap.link.shield

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ShieldTest {
    private val phone = byteArrayOf(10, 111, 222.toByte(), 2)
    private val dnsAddr = byteArrayOf(10, 111, 222.toByte(), 53)

    /** A DNS question for [name] (type A) with [id]. */
    private fun query(name: String, id: Int = 0x1234): ByteArray {
        val out = java.io.ByteArrayOutputStream()
        out.write(byteArrayOf((id ushr 8).toByte(), id.toByte(), 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0))
        name.split('.').forEach { l -> out.write(l.length); out.write(l.toByteArray()) }
        out.write(byteArrayOf(0, 0, 1, 0, 1))
        return out.toByteArray()
    }

    /** An answer to [q] with one A record 1.2.3.4. */
    private fun answer(q: ByteArray): ByteArray {
        val a = q.copyOf()
        a[2] = 0x81.toByte(); a[3] = 0x80.toByte(); a[7] = 1
        return a + byteArrayOf(0xC0.toByte(), 12, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 1, 2, 3, 4)
    }

    // ---------- packets ----------

    @Test fun udpRoundTrip() {
        val q = query("ads.example.com")
        val pkt = Packets.udp(phone, 40000, dnsAddr, 53, q)
        val p = IpPacket.parse(pkt, pkt.size)!!
        assertEquals(PROTO_UDP, p.proto)
        assertEquals(40000, p.srcPort)
        assertEquals(53, p.dstPort)
        assertArrayEquals(q, p.payload())
        assertTrue(Packets.checksumsOk(p))
    }

    @Test fun udpV6RoundTrip() {
        val a = ByteArray(16).also { it[0] = 0xfd.toByte(); it[15] = 2 }
        val b = ByteArray(16).also { it[0] = 0xfd.toByte(); it[15] = 0x53 }
        val pkt = Packets.udp(a, 5000, b, 53, byteArrayOf(1, 2, 3))
        val p = IpPacket.parse(pkt, pkt.size)!!
        assertTrue(p.v6)
        assertArrayEquals(byteArrayOf(1, 2, 3), p.payload())
        assertTrue(Packets.checksumsOk(p))
    }

    @Test fun tcpRoundTrip() {
        val pkt = Packets.tcp(phone, 41000, dnsAddr, 53, 100, 200, Packets.TCP_ACK or Packets.TCP_PSH, byteArrayOf(9, 8, 7))
        val p = IpPacket.parse(pkt, pkt.size)!!
        assertEquals(PROTO_TCP, p.proto)
        assertEquals(100L, p.tcpSeq)
        assertEquals(200L, p.tcpAck)
        assertEquals(Packets.TCP_ACK or Packets.TCP_PSH, p.tcpFlags)
        assertArrayEquals(byteArrayOf(9, 8, 7), p.payload())
        assertTrue(Packets.checksumsOk(p))
    }

    @Test fun rejectsGarbageAndFragments() {
        assertNull(IpPacket.parse(ByteArray(10), 10))
        assertNull(IpPacket.parse(ByteArray(60), 60)) // version 0
        val pkt = Packets.udp(phone, 1, dnsAddr, 53, byteArrayOf(1))
        pkt[6] = 0x20 // more fragments
        assertNull(IpPacket.parse(pkt, pkt.size))
        val short = Packets.udp(phone, 1, dnsAddr, 53, byteArrayOf(1, 2, 3, 4))
        assertNull(IpPacket.parse(short, short.size - 6)) // cut off
    }

    // ---------- DNS ----------

    @Test fun parsesQuestion() {
        val q = Dns.question(query("Ads.Example.COM", 7))!!
        assertEquals("ads.example.com", q.name)
        assertEquals(1, q.type)
        assertEquals(7, q.id)
    }

    @Test fun refusesBadQuestions() {
        assertNull(Dns.question(ByteArray(5)))
        assertNull(Dns.question(answer(query("a.example")))) // an answer
        val two = query("a.example").also { it[5] = 2 }
        assertNull(Dns.question(two))
        val ptr = query("a.example").also { it[12] = 0xC0.toByte() }
        assertNull(Dns.question(ptr))
        val cut = query("a.example").copyOf(15)
        assertNull(Dns.question(cut))
    }

    @Test fun repliesKeepQuestion() {
        val q = query("x.example", 99)
        val r = Dns.reply(q, Dns.RCODE_REFUSED)!!
        assertEquals(Dns.RCODE_REFUSED, Dns.rcode(r))
        assertTrue(Dns.matches(q, r))
        assertEquals(-1, Dns.rcode(q))
    }

    @Test fun truncatesBigAnswers() {
        val q = query("big.example")
        val a = answer(q) + ByteArray(2000)
        val t = Dns.truncated(q, a, 512)
        assertTrue(t.size < 512)
        assertTrue(t[2].toInt() and 0x02 != 0)
        assertArrayEquals(answer(q), Dns.truncated(q, answer(q), 512))
    }

    // ---------- resolver ----------

    @Test fun resolverUsesServerThenFallsBack() {
        var now = 0L
        var serverCalls = 0
        var fallback = Fallback.OPEN
        val r = ShieldResolver(
            server = { q -> serverCalls++; if (now < 1000) answer(q) to true else throw ServerDown("down") },
            normal = { q -> answer(q) to false },
            fallback = { fallback },
            now = { now },
        )
        val q = query("ads.example.com", 0x4242)
        val a1 = r.resolve(q)!!
        assertEquals(Via.SERVER, a1.via)
        assertTrue(a1.blocked)
        assertEquals(0x4242, u16(a1.msg, 0)) // the asker's id is put back
        now = 2000
        assertEquals(Via.NORMAL, r.resolve(q)!!.via) // server down: normal DNS
        val calls = serverCalls
        assertEquals(Via.NORMAL, r.resolve(q)!!.via)
        assertEquals("paused after a failure", calls, serverCalls)
        fallback = Fallback.BLOCK
        val b = r.resolve(q)!!
        assertEquals(Via.REFUSED, b.via)
        assertEquals(Dns.RCODE_REFUSED, Dns.rcode(b.msg))
        r.retryNow()
        r.resolve(q)
        assertEquals(calls + 1, serverCalls)
    }

    @Test fun busyServerIsNotMarkedDown() {
        val r = ShieldResolver({ throw Busy() }, { q -> answer(q) to false }, { Fallback.OPEN }, { 0L })
        assertEquals(Via.NORMAL, r.resolve(query("a.example"))!!.via)
        assertTrue(r.serverUp)
    }

    @Test fun noNormalDnsGivesServfail() {
        val r = ShieldResolver(null, null, { Fallback.OPEN })
        assertEquals(Dns.RCODE_SERVFAIL, Dns.rcode(r.resolve(query("a.example"))!!.msg))
        assertNull(r.resolve(ByteArray(3)))
    }

    // ---------- rules ----------

    private fun cfg(
        dns: Boolean = true, firewall: Boolean = true, hard: Boolean = false,
        rules: Map<String, AppRule> = emptyMap(), trusted: Set<String> = emptySet(), knows: Boolean = true, lockdown: Boolean = false,
    ) = ShieldConfig(dns, firewall, hard, trusted, rules, knows, lockdown)

    @Test fun ruleEvaluation() {
        assertTrue(ShieldRules.blocks(AppRule.BLOCK, NetKind.MOBILE))
        assertTrue(ShieldRules.blocks(AppRule.NO_WIFI, NetKind.WIFI))
        assertFalse(ShieldRules.blocks(AppRule.NO_WIFI, NetKind.MOBILE))
        assertTrue(ShieldRules.blocks(AppRule.NO_DATA, NetKind.MOBILE))
        assertFalse(ShieldRules.blocks(AppRule.NO_DATA, NetKind.WIFI))
        assertFalse(ShieldRules.blocks(AppRule.ALLOW, NetKind.WIFI))
    }

    @Test fun plans() {
        val wifi = Net(NetKind.WIFI, "Home")
        val mobile = Net(NetKind.MOBILE)
        val rules = mapOf("a.game" to AppRule.NO_DATA, "b.social" to AppRule.BLOCK)
        // DNS only.
        assertEquals(Plan(Mode.DNS, emptySet(), true), ShieldRules.plan(cfg(), wifi))
        // DNS + blocked apps (refused at DNS) without hard block.
        assertEquals(Plan(Mode.DNS, setOf("a.game", "b.social"), true), ShieldRules.plan(cfg(rules = rules), mobile))
        // Hard block: routing, DNS filter waits.
        val hard = cfg(hard = true, rules = rules)
        assertEquals(Plan(Mode.FIREWALL, setOf("b.social"), false), ShieldRules.plan(hard, wifi))
        assertTrue(ShieldRules.plan(hard, wifi).dnsPausedForFirewall(hard, wifi))
        // Older Android can't tell which app asked: routing.
        assertEquals(Mode.FIREWALL, ShieldRules.plan(cfg(rules = rules, knows = false), mobile).mode)
        // Firewall only.
        assertEquals(Plan(Mode.FIREWALL, setOf("b.social"), false), ShieldRules.plan(cfg(dns = false, rules = rules), wifi))
        // Nothing to block on this network and no DNS filter: no VPN.
        assertEquals(Mode.OFF, ShieldRules.plan(cfg(dns = false, rules = mapOf("a.game" to AppRule.NO_DATA)), wifi).mode)
        // Trusted Wi-Fi pauses the DNS filter; with lockdown it passes lookups through.
        assertEquals(Mode.OFF, ShieldRules.plan(cfg(trusted = setOf("Home")), wifi).mode)
        assertEquals(Mode.PASS, ShieldRules.plan(cfg(trusted = setOf("Home"), lockdown = true), wifi).mode)
        assertEquals(Mode.DNS, ShieldRules.plan(cfg(trusted = setOf("Home")), Net(NetKind.WIFI, null)).mode)
        // Firewall off: rules ignored.
        assertEquals(Plan(Mode.DNS, emptySet(), true), ShieldRules.plan(cfg(firewall = false, rules = rules), mobile))
    }

    @Test fun newApps() {
        assertEquals(mapOf("new.app" to AppRule.BLOCK), ShieldRules.newApps(setOf("old.app", "new.app"), setOf("old.app"), true))
        assertTrue(ShieldRules.newApps(setOf("new.app"), emptySet(), false).isEmpty())
    }

    @Test fun ssidAndDomains() {
        assertEquals("Home", ShieldRules.cleanSsid("\"Home\""))
        assertNull(ShieldRules.cleanSsid("<unknown ssid>"))
        assertEquals("ads.example.com", cleanDomain("https://Ads.Example.com/path?x=1"))
        assertEquals("example.com", cleanDomain("*.example.com"))
        assertNull(cleanDomain("not a domain"))
        assertNull(cleanDomain("localhost"))
    }

    @Test fun rulesEncode() {
        val r = mapOf("a" to AppRule.BLOCK, "b" to AppRule.NO_WIFI)
        assertEquals(r, ShieldStore.decodeRules(ShieldStore.encodeRules(r)))
        assertTrue(ShieldStore.decodeRules("garbage").isEmpty())
    }

    // ---------- DNS over TCP ----------

    @Test fun tcpDnsConversation() {
        val t = TcpDns(random = { 1000L })
        fun send(seq: Long, ack: Long, flags: Int, data: ByteArray = ByteArray(0)): TcpDns.Out {
            val pkt = Packets.tcp(phone, 42000, dnsAddr, 53, seq, ack, flags, data)
            return t.onPacket(IpPacket.parse(pkt, pkt.size)!!, 0)
        }
        val synAck = IpPacket.parse(send(500, 0, Packets.TCP_SYN).send.single().let { it }, 40)!!
        assertEquals(Packets.TCP_SYN or Packets.TCP_ACK, synAck.tcpFlags)
        assertEquals(501L, synAck.tcpAck)
        send(501, 1001, Packets.TCP_ACK)
        val q = query("tcp.example")
        val framed = byteArrayOf(0, q.size.toByte()) + q
        // In two pieces.
        val first = send(501, 1001, Packets.TCP_ACK, framed.copyOf(5))
        assertTrue(first.ask.isEmpty())
        val second = send(506, 1001, Packets.TCP_ACK or Packets.TCP_PSH, framed.copyOfRange(5, framed.size))
        val (key, asked) = second.ask.single()
        assertArrayEquals(q, asked)
        val segs = t.answer(key, answer(q), 0)
        val data = segs.map { IpPacket.parse(it, it.size)!! }
        assertEquals(1001L, data.first().tcpSeq)
        val body = data.flatMap { it.payload().toList() }.toByteArray()
        assertEquals(answer(q).size, u16(body, 0))
        // Out-of-order data is just re-acked.
        val ooo = send(9999, 1001, Packets.TCP_ACK, byteArrayOf(1))
        assertTrue(ooo.ask.isEmpty())
        // Close.
        val fin = send(501L + framed.size, 1001L + body.size, Packets.TCP_FIN or Packets.TCP_ACK)
        val finAck = IpPacket.parse(fin.send.single(), fin.send.single().size)!!
        assertTrue(finAck.tcpFlags and Packets.TCP_FIN != 0)
        send(502L + framed.size, finAck.tcpSeq + 1, Packets.TCP_ACK)
        assertEquals(0, t.size)
    }

    @Test fun tcpDnsBounds() {
        val t = TcpDns(maxConns = 1, idleMs = 100)
        fun syn(port: Int, now: Long): TcpDns.Out {
            val pkt = Packets.tcp(phone, port, dnsAddr, 53, 1, 0, Packets.TCP_SYN)
            return t.onPacket(IpPacket.parse(pkt, pkt.size)!!, now)
        }
        syn(1, 0)
        val refused = IpPacket.parse(syn(2, 0).send.single(), 40)!!
        assertTrue(refused.tcpFlags and Packets.TCP_RST != 0)
        syn(3, 500) // the idle one is gone by now
        assertEquals(1, t.size)
        // A huge length is refused.
        val t2 = TcpDns()
        val s = Packets.tcp(phone, 9, dnsAddr, 53, 1, 0, Packets.TCP_SYN)
        t2.onPacket(IpPacket.parse(s, s.size)!!, 0)
        val big = Packets.tcp(phone, 9, dnsAddr, 53, 2, 0, Packets.TCP_ACK, byteArrayOf(0xFF.toByte(), 0xFF.toByte()))
        val out = t2.onPacket(IpPacket.parse(big, big.size)!!, 0)
        assertNotNull(out.send.firstOrNull { IpPacket.parse(it, it.size)!!.tcpFlags and Packets.TCP_RST != 0 })
        assertEquals(0, t2.size)
    }

    @Test fun bucketLimits() {
        var now = 0L
        val b = Bucket(1.0, 2.0) { now }
        assertTrue(b.take()); assertTrue(b.take()); assertFalse(b.take())
        now = 1_000_000_000L
        assertTrue(b.take())
    }
}
