package com.nocap.link.shield

import java.io.IOException
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetAddress
import java.net.InetSocketAddress
import java.util.concurrent.TimeUnit
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody

/** One answer and how it came. */
class Answer(val msg: ByteArray, val blocked: Boolean, val via: Via)

/** SERVER: your NoCap server's Shield. NORMAL: the phone's normal DNS (server unreachable). REFUSED: Shield said no itself. */
enum class Via { SERVER, NORMAL, REFUSED }

/** The server can't answer right now (down, unreachable, Shield off, unlinked). */
class ServerDown(msg: String, val code: Int = 0) : IOException(msg)

/** The server said "slow down" for this one question. */
class Busy : IOException("busy")

/** Something that answers DNS messages. */
fun interface Upstream {
    /** The answer to [query] (blocked = your server's Shield blocked it); throws when it can't answer. */
    fun ask(query: ByteArray): Pair<ByteArray, Boolean>
}

/**
 * Asks your server first. When it can't be reached, Shield uses the
 * fallback you chose (the phone's normal DNS, or block everything) and
 * tries the server again after a short pause that grows while it stays
 * down (5 s up to a minute), so a dead server doesn't slow every lookup.
 */
class ShieldResolver(
    @Volatile var server: Upstream?,
    @Volatile var normal: Upstream?,
    private val fallback: () -> Fallback,
    private val now: () -> Long = System::currentTimeMillis,
) {
    @Volatile var downUntil = 0L
        private set
    @Volatile private var backoff = 0L
    @Volatile var lastError: String? = null
        private set

    val serverUp: Boolean get() = server != null && now() >= downUntil

    /** Answers [query]; null only for messages that aren't a single question. */
    fun resolve(query: ByteArray): Answer? {
        val q = Dns.question(query) ?: return null
        val s = server
        if (s != null && now() >= downUntil) {
            try {
                val (msg, blocked) = s.ask(Dns.withId(query, 0))
                if (Dns.rcode(msg) >= 0 && msg.size >= q.end) {
                    backoff = 0
                    lastError = null
                    return Answer(Dns.withId(msg, q.id), blocked, Via.SERVER)
                }
                markDown("bad answer")
            } catch (e: Busy) {
                // Just this one: fall back without pausing the server.
            } catch (e: Exception) {
                markDown(e.message ?: e.javaClass.simpleName)
            }
        }
        return fallbackAnswer(query)
    }

    private fun markDown(why: String) {
        backoff = if (backoff == 0L) 5_000 else minOf(backoff * 2, 60_000)
        downUntil = now() + backoff
        lastError = why
    }

    /** Try the server again with the next question (network changed). */
    fun retryNow() {
        downUntil = 0
        backoff = 0
    }

    private fun fallbackAnswer(query: ByteArray): Answer? {
        if (fallback() == Fallback.OPEN) {
            val n = normal
            if (n != null) {
                try {
                    val (msg, _) = n.ask(query)
                    if (Dns.matches(query, msg)) return Answer(msg, false, Via.NORMAL)
                } catch (_: Exception) {
                }
            }
            return Dns.reply(query, Dns.RCODE_SERVFAIL)?.let { Answer(it, false, Via.NORMAL) }
        }
        return Dns.reply(query, Dns.RCODE_REFUSED)?.let { Answer(it, true, Via.REFUSED) }
    }
}

/** DNS-over-HTTPS to your NoCap server (/dns-query), with this phone's device token. */
class DohUpstream(private val client: OkHttpClient, private val url: () -> String?, private val token: String) : Upstream {
    private val fast = client.newBuilder().connectTimeout(4, TimeUnit.SECONDS).readTimeout(5, TimeUnit.SECONDS).writeTimeout(5, TimeUnit.SECONDS).build()

    override fun ask(query: ByteArray): Pair<ByteArray, Boolean> {
        val base = url() ?: throw ServerDown("no server address")
        val req = Request.Builder().url("$base/dns-query")
            .header("Authorization", "Device $token")
            .header("Accept", TYPE)
            .post(query.toRequestBody(TYPE.toMediaType()))
            .build()
        fast.newCall(req).execute().use { r ->
            if (r.code == 429) throw Busy()
            if (!r.isSuccessful) throw ServerDown("HTTP ${r.code}", r.code)
            val body = r.body ?: throw ServerDown("empty answer")
            if (body.contentLength() > LIMIT) throw ServerDown("answer too big")
            val src = body.source()
            src.request(LIMIT + 1)
            if (src.buffer.size > LIMIT) throw ServerDown("answer too big")
            return src.buffer.readByteArray() to (r.header("X-NoCap-Blocked") == "1")
        }
    }

    companion object {
        const val TYPE = "application/dns-message"
        private const val LIMIT = 65_535L
    }
}

/**
 * The phone's normal DNS servers (of the network under the VPN), plain UDP,
 * used only when "let everything through" is the fallback. [protect] keeps
 * the socket out of the VPN.
 */
class PlainUpstream(private val servers: () -> List<InetAddress>, private val protect: (DatagramSocket) -> Boolean) : Upstream {
    override fun ask(query: ByteArray): Pair<ByteArray, Boolean> {
        val list = servers().take(3)
        if (list.isEmpty()) throw IOException("no DNS servers")
        var last: Exception = IOException("no answer")
        for (addr in list) {
            try {
                DatagramSocket().use { s ->
                    protect(s)
                    s.soTimeout = 2500
                    s.send(DatagramPacket(query, query.size, InetSocketAddress(addr, 53)))
                    val buf = ByteArray(Dns.MAX)
                    val p = DatagramPacket(buf, buf.size)
                    s.receive(p)
                    return buf.copyOf(p.length) to false
                }
            } catch (e: Exception) {
                last = e
            }
        }
        throw last
    }
}
