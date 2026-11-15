package com.nocap.link.shield

/**
 * DNS over TCP to Shield's DNS address, the tiny bit of TCP that needs:
 * accept, read length-prefixed questions, write the answers, close. The
 * VPN interface never loses packets, so there's no retransmission; anything
 * odd (out of order, unknown connection) gets a reset and the phone retries.
 * Connections and buffers are bounded. Not thread-safe: the caller locks.
 */
class TcpDns(private val maxConns: Int = 32, private val idleMs: Long = 15_000, private val random: () -> Long = { kotlin.random.Random.nextLong() }) {
    data class Key(val addr: List<Byte>, val port: Int)

    /** What to do after a packet: segments to write now, questions to resolve. */
    class Out(val send: List<ByteArray>, val ask: List<Pair<Key, ByteArray>>)

    private class Conn(
        val client: ByteArray, val clientPort: Int, val server: ByteArray, val serverPort: Int,
        var rcvNext: Long, var sndNext: Long, var seen: Long,
    ) {
        var established = false
        var closing = false
        val buf = java.io.ByteArrayOutputStream()
        var pending = 0
        var finPending = false
    }

    private val conns = LinkedHashMap<Key, Conn>()

    val size: Int get() = conns.size

    fun onPacket(p: IpPacket, now: Long): Out {
        sweep(now)
        val key = Key(p.src.toList(), p.srcPort)
        val flags = p.tcpFlags
        val send = ArrayList<ByteArray>(2)
        val ask = ArrayList<Pair<Key, ByteArray>>(1)
        var c = conns[key]
        if (flags and Packets.TCP_RST != 0) {
            conns.remove(key)
            return Out(send, ask)
        }
        if (flags and Packets.TCP_SYN != 0 && flags and Packets.TCP_ACK == 0) {
            if (c != null) conns.remove(key)
            if (conns.size >= maxConns) {
                send += Packets.tcp(p.dst, p.dstPort, p.src, p.srcPort, 0, (p.tcpSeq + 1) and MASK, Packets.TCP_RST or Packets.TCP_ACK)
                return Out(send, ask)
            }
            val isn = random() and MASK
            c = Conn(p.src, p.srcPort, p.dst, p.dstPort, (p.tcpSeq + 1) and MASK, (isn + 1) and MASK, now)
            conns[key] = c
            send += Packets.tcp(p.dst, p.dstPort, p.src, p.srcPort, isn, c.rcvNext, Packets.TCP_SYN or Packets.TCP_ACK)
            return Out(send, ask)
        }
        if (c == null) {
            // Not ours (we restarted, or it's long gone): reset it.
            if (flags and Packets.TCP_ACK != 0) send += Packets.tcp(p.dst, p.dstPort, p.src, p.srcPort, p.tcpAck, 0, Packets.TCP_RST)
            return Out(send, ask)
        }
        c.seen = now
        if (flags and Packets.TCP_ACK != 0) {
            if (!c.established && p.tcpAck == c.sndNext) c.established = true
            if (c.closing && p.tcpAck == c.sndNext) {
                conns.remove(key)
                return Out(send, ask)
            }
        }
        val len = p.tcpLen
        if (len > 0 || flags and Packets.TCP_FIN != 0) {
            if (p.tcpSeq != c.rcvNext) {
                // Out of order or a repeat: say what we expect.
                send += ack(c)
                return Out(send, ask)
            }
            if (len > 0) {
                if (c.buf.size() + len > Dns.MAX + 2) {
                    conns.remove(key)
                    send += Packets.tcp(c.server, c.serverPort, c.client, c.clientPort, c.sndNext, 0, Packets.TCP_RST)
                    return Out(send, ask)
                }
                c.buf.write(p.raw, p.tcpData, len)
                c.rcvNext = (c.rcvNext + len) and MASK
                // Whole questions (2-byte length, then the message).
                var b = c.buf.toByteArray()
                while (b.size >= 2) {
                    val n = u16(b, 0)
                    if (n == 0 || n > Dns.MAX) {
                        conns.remove(key)
                        send += Packets.tcp(c.server, c.serverPort, c.client, c.clientPort, c.sndNext, 0, Packets.TCP_RST)
                        return Out(send, ask)
                    }
                    if (b.size < 2 + n) break
                    ask += key to b.copyOfRange(2, 2 + n)
                    c.pending++
                    b = b.copyOfRange(2 + n, b.size)
                }
                c.buf.reset()
                c.buf.write(b)
            }
            if (flags and Packets.TCP_FIN != 0) {
                c.rcvNext = (c.rcvNext + 1) and MASK
                if (c.pending == 0) {
                    send += Packets.tcp(c.server, c.serverPort, c.client, c.clientPort, c.sndNext, c.rcvNext, Packets.TCP_FIN or Packets.TCP_ACK)
                    c.sndNext = (c.sndNext + 1) and MASK
                    c.closing = true
                } else {
                    send += ack(c)
                    c.closing = false
                    c.finPending = true
                }
            } else {
                send += ack(c)
            }
        }
        return Out(send, ask)
    }

    /** Segments carrying [answer] to the connection [key] (empty if it's gone). */
    fun answer(key: Key, answer: ByteArray?, now: Long): List<ByteArray> {
        val c = conns[key] ?: return emptyList()
        c.seen = now
        c.pending = (c.pending - 1).coerceAtLeast(0)
        val out = ArrayList<ByteArray>()
        if (answer != null) {
            val framed = ByteArray(2 + answer.size)
            put16(framed, 0, answer.size)
            answer.copyInto(framed, 2)
            var off = 0
            while (off < framed.size) {
                val n = minOf(SEGMENT, framed.size - off)
                out += Packets.tcp(c.server, c.serverPort, c.client, c.clientPort, c.sndNext, c.rcvNext, Packets.TCP_ACK or Packets.TCP_PSH, framed.copyOfRange(off, off + n))
                c.sndNext = (c.sndNext + n) and MASK
                off += n
            }
        }
        if (c.pending == 0 && c.finPending) {
            out += Packets.tcp(c.server, c.serverPort, c.client, c.clientPort, c.sndNext, c.rcvNext, Packets.TCP_FIN or Packets.TCP_ACK)
            c.sndNext = (c.sndNext + 1) and MASK
            c.closing = true
            c.finPending = false
        }
        return out
    }

    private fun ack(c: Conn) = Packets.tcp(c.server, c.serverPort, c.client, c.clientPort, c.sndNext, c.rcvNext, Packets.TCP_ACK)

    /** Drops idle connections. */
    fun sweep(now: Long) {
        val it = conns.entries.iterator()
        while (it.hasNext()) {
            val c = it.next().value
            if (now - c.seen > idleMs) it.remove()
        }
    }

    fun clear() = conns.clear()

    companion object {
        const val MASK = 0xFFFFFFFFL
        const val SEGMENT = 1200
    }
}
