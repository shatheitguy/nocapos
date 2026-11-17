package com.nocap.link.shield

/** The question of a DNS query: who asked (id), which name and type. */
data class DnsQuestion(val id: Int, val name: String, val type: Int, val end: Int)

/**
 * Reads DNS questions and writes the few answers Shield makes itself
 * ("refused", "server failure"). Everything else comes from the server.
 */
object Dns {
    const val MAX = 4096
    const val RCODE_SERVFAIL = 2
    const val RCODE_NXDOMAIN = 3
    const val RCODE_REFUSED = 5

    /** The single question of a query; null for answers, garbage or several questions. */
    fun question(msg: ByteArray): DnsQuestion? {
        if (msg.size < 12 || msg.size > MAX) return null
        val flags = u16(msg, 2)
        if (flags and 0x8000 != 0) return null // an answer
        if ((flags ushr 11) and 0xF != 0) return null // not a standard query
        if (u16(msg, 4) != 1) return null
        var i = 12
        val name = StringBuilder()
        var labels = 0
        while (true) {
            if (i >= msg.size) return null
            val n = msg[i].toInt() and 0xFF
            if (n == 0) { i++; break }
            if (n and 0xC0 != 0) return null // no compression in a question
            if (++labels > 127 || i + 1 + n > msg.size) return null
            if (name.isNotEmpty()) name.append('.')
            for (k in i + 1..i + n) {
                val c = (msg[k].toInt() and 0xFF).toChar()
                name.append(if (c in 'A'..'Z') c + 32 else c)
            }
            if (name.length > 253) return null
            i += 1 + n
        }
        if (i + 4 > msg.size) return null
        return DnsQuestion(u16(msg, 0), name.toString(), u16(msg, i), i + 4)
    }

    /** An answer with no records and [rcode], for [query] (its question kept). */
    fun reply(query: ByteArray, rcode: Int): ByteArray? {
        val q = question(query) ?: return null
        val out = query.copyOf(q.end)
        val rd = out[2].toInt() and 0x01
        out[2] = (0x80 or rd).toByte() // QR, opcode 0, RD as asked
        out[3] = (0x80 or (rcode and 0xF)).toByte() // RA
        put16(out, 6, 0); put16(out, 8, 0); put16(out, 10, 0)
        return out
    }

    /** The answer's response code (-1 when it isn't an answer). */
    fun rcode(msg: ByteArray): Int = if (msg.size < 12 || msg[2].toInt() and 0x80 == 0) -1 else msg[3].toInt() and 0xF

    /** Whether [answer] answers [query] (same id and question). */
    fun matches(query: ByteArray, answer: ByteArray): Boolean {
        if (answer.size < 12 || query.size < 12 || answer[2].toInt() and 0x80 == 0) return false
        if (u16(query, 0) != u16(answer, 0)) return false
        val q = question(query) ?: return false
        // The question section of the answer is the same bytes (names compared case-insensitively).
        if (answer.size < q.end || u16(answer, 4) != 1) return false
        for (k in 12 until q.end) {
            val a = query[k].toInt() and 0xFF
            val b = answer[k].toInt() and 0xFF
            if (a != b && (a or 0x20) != (b or 0x20)) return false
        }
        return true
    }

    /** [msg] with its id set to [id] (a copy). */
    fun withId(msg: ByteArray, id: Int): ByteArray = msg.copyOf().also { put16(it, 0, id) }

    /**
     * Cuts an answer down to fit UDP: the question only, with the
     * truncated bit, so the asker retries over TCP.
     */
    fun truncated(query: ByteArray, answer: ByteArray, max: Int): ByteArray {
        if (answer.size <= max) return answer
        val q = question(query) ?: return answer.copyOf(12)
        val out = answer.copyOf(q.end)
        out[2] = (out[2].toInt() or 0x02).toByte() // TC
        put16(out, 6, 0); put16(out, 8, 0); put16(out, 10, 0)
        return out
    }
}
