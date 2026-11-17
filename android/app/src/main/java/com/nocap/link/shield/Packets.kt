package com.nocap.link.shield

/*
 * Just enough IP for Shield: reading the packets Android hands the VPN
 * (IPv4/IPv6, UDP/TCP) and writing answers back. Pure Kotlin, no Android,
 * so it's unit-tested. Nothing here allocates more than one packet.
 */

const val PROTO_TCP = 6
const val PROTO_UDP = 17

/** One parsed IP packet: where it's from and to, and where its payload is. */
class IpPacket(
    val raw: ByteArray,
    val len: Int,
    val v6: Boolean,
    val proto: Int,
    val src: ByteArray,
    val dst: ByteArray,
    /** Where the UDP/TCP header starts. */
    val l4: Int,
) {
    val srcPort: Int get() = u16(raw, l4)
    val dstPort: Int get() = u16(raw, l4 + 2)

    /** UDP payload start / length (only for UDP). */
    val udpData: Int get() = l4 + 8
    val udpLen: Int get() = (u16(raw, l4 + 4) - 8).coerceIn(0, len - udpData)

    // TCP
    val tcpSeq: Long get() = u32(raw, l4 + 4)
    val tcpAck: Long get() = u32(raw, l4 + 8)
    val tcpHeaderLen: Int get() = ((raw[l4 + 12].toInt() ushr 4) and 0xF) * 4
    val tcpFlags: Int get() = raw[l4 + 13].toInt() and 0x3F
    val tcpData: Int get() = l4 + tcpHeaderLen
    val tcpLen: Int get() = (len - tcpData).coerceAtLeast(0)

    fun payload(): ByteArray = when (proto) {
        PROTO_UDP -> raw.copyOfRange(udpData, udpData + udpLen)
        PROTO_TCP -> raw.copyOfRange(tcpData, tcpData + tcpLen)
        else -> ByteArray(0)
    }

    companion object {
        /**
         * Parses [len] bytes of [raw]; null for anything Shield doesn't handle
         * (fragments, IPv6 extension headers, other protocols, garbage).
         */
        fun parse(raw: ByteArray, len: Int): IpPacket? {
            if (len < 20 || len > raw.size) return null
            return when ((raw[0].toInt() ushr 4) and 0xF) {
                4 -> {
                    val ihl = (raw[0].toInt() and 0xF) * 4
                    val total = u16(raw, 2)
                    if (ihl < 20 || total < ihl || total > len) return null
                    val frag = u16(raw, 6)
                    if (frag and 0x3FFF != 0) return null // a fragment: not for us
                    val proto = raw[9].toInt() and 0xFF
                    if (!fits(proto, ihl, total, raw)) return null
                    IpPacket(raw, total, false, proto, raw.copyOfRange(12, 16), raw.copyOfRange(16, 20), ihl)
                }
                6 -> {
                    if (len < 40) return null
                    val total = 40 + u16(raw, 4)
                    if (total > len) return null
                    val proto = raw[6].toInt() and 0xFF
                    if (!fits(proto, 40, total, raw)) return null
                    IpPacket(raw, total, true, proto, raw.copyOfRange(8, 24), raw.copyOfRange(24, 40), 40)
                }
                else -> null
            }
        }

        private fun fits(proto: Int, l4: Int, total: Int, raw: ByteArray): Boolean = when (proto) {
            PROTO_UDP -> total - l4 >= 8 && u16(raw, l4 + 4) in 8..(total - l4)
            PROTO_TCP -> total - l4 >= 20 && ((raw[l4 + 12].toInt() ushr 4) and 0xF) * 4 in 20..(total - l4)
            else -> false
        }
    }
}

internal fun u16(b: ByteArray, i: Int): Int = ((b[i].toInt() and 0xFF) shl 8) or (b[i + 1].toInt() and 0xFF)

internal fun u32(b: ByteArray, i: Int): Long =
    ((b[i].toLong() and 0xFF) shl 24) or ((b[i + 1].toLong() and 0xFF) shl 16) or ((b[i + 2].toLong() and 0xFF) shl 8) or (b[i + 3].toLong() and 0xFF)

internal fun put16(b: ByteArray, i: Int, v: Int) {
    b[i] = (v ushr 8).toByte(); b[i + 1] = v.toByte()
}

internal fun put32(b: ByteArray, i: Int, v: Long) {
    b[i] = (v ushr 24).toByte(); b[i + 1] = (v ushr 16).toByte(); b[i + 2] = (v ushr 8).toByte(); b[i + 3] = v.toByte()
}

/** The Internet checksum (RFC 1071) over [len] bytes from [off], starting from [sum]. */
internal fun csum(b: ByteArray, off: Int, len: Int, sum0: Long = 0): Long {
    var sum = sum0
    var i = off
    val end = off + len
    while (i + 1 < end) {
        sum += u16(b, i)
        i += 2
    }
    if (i < end) sum += (b[i].toInt() and 0xFF) shl 8
    return sum
}

internal fun fold(sum0: Long): Int {
    var sum = sum0
    while (sum ushr 16 != 0L) sum = (sum and 0xFFFF) + (sum ushr 16)
    return (sum.inv() and 0xFFFF).toInt()
}

object Packets {
    const val TCP_FIN = 0x01
    const val TCP_SYN = 0x02
    const val TCP_RST = 0x04
    const val TCP_PSH = 0x08
    const val TCP_ACK = 0x10

    /** A UDP packet from [src]:[srcPort] to [dst]:[dstPort] carrying [data]. */
    fun udp(src: ByteArray, srcPort: Int, dst: ByteArray, dstPort: Int, data: ByteArray): ByteArray {
        val v6 = src.size == 16
        val ipLen = if (v6) 40 else 20
        val out = ByteArray(ipLen + 8 + data.size)
        ipHeader(out, v6, PROTO_UDP, src, dst, 8 + data.size)
        put16(out, ipLen, srcPort)
        put16(out, ipLen + 2, dstPort)
        put16(out, ipLen + 4, 8 + data.size)
        data.copyInto(out, ipLen + 8)
        var c = fold(pseudo(src, dst, PROTO_UDP, 8 + data.size) + csum(out, ipLen, 8 + data.size))
        if (c == 0) c = 0xFFFF
        put16(out, ipLen + 6, c)
        return out
    }

    /** A TCP segment (no options) with [flags], [seq], [ack] and [data]. */
    fun tcp(src: ByteArray, srcPort: Int, dst: ByteArray, dstPort: Int, seq: Long, ack: Long, flags: Int, data: ByteArray = ByteArray(0), window: Int = 65535): ByteArray {
        val v6 = src.size == 16
        val ipLen = if (v6) 40 else 20
        val segLen = 20 + data.size
        val out = ByteArray(ipLen + segLen)
        ipHeader(out, v6, PROTO_TCP, src, dst, segLen)
        put16(out, ipLen, srcPort)
        put16(out, ipLen + 2, dstPort)
        put32(out, ipLen + 4, seq and 0xFFFFFFFFL)
        put32(out, ipLen + 8, ack and 0xFFFFFFFFL)
        out[ipLen + 12] = (5 shl 4).toByte()
        out[ipLen + 13] = flags.toByte()
        put16(out, ipLen + 14, window)
        data.copyInto(out, ipLen + 20)
        put16(out, ipLen + 16, fold(pseudo(src, dst, PROTO_TCP, segLen) + csum(out, ipLen, segLen)))
        return out
    }

    private fun ipHeader(out: ByteArray, v6: Boolean, proto: Int, src: ByteArray, dst: ByteArray, payload: Int) {
        if (v6) {
            out[0] = 0x60
            put16(out, 4, payload)
            out[6] = proto.toByte()
            out[7] = 64
            src.copyInto(out, 8)
            dst.copyInto(out, 24)
        } else {
            out[0] = 0x45
            put16(out, 2, 20 + payload)
            put16(out, 6, 0x4000) // don't fragment
            out[8] = 64
            out[9] = proto.toByte()
            src.copyInto(out, 12)
            dst.copyInto(out, 16)
            put16(out, 10, fold(csum(out, 0, 20)))
        }
    }

    private fun pseudo(src: ByteArray, dst: ByteArray, proto: Int, len: Int): Long {
        var s = csum(src, 0, src.size) + csum(dst, 0, dst.size)
        s += proto
        s += len.toLong()
        return s
    }

    /** Checks an IPv4 header's and a UDP/TCP segment's checksums (tests). */
    fun checksumsOk(p: IpPacket): Boolean {
        if (!p.v6 && fold(csum(p.raw, 0, p.l4)) != 0) return false
        val segLen = p.len - p.l4
        return fold(pseudo(p.src, p.dst, p.proto, segLen) + csum(p.raw, p.l4, segLen)) == 0
    }
}
