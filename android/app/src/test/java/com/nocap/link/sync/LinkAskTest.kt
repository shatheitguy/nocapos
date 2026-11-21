package com.nocap.link.sync

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class LinkAskTest {
    private fun server(id: String, origin: String? = "https://192.168.1.20:8443", setup: String = "done") =
        FoundServer(id, "box", origin, "", setup, lan = origin != null, bluetooth = origin == null)

    @Test
    fun readsTheRequestTheServerTook() {
        val o = JSONObject().put("request_id", "abc123").put("secret", "s".repeat(48)).put("check", "0427").put("nocap_id", "NC-AB12").put("server_name", "media-box")
        val a = LinkAsk.asked(o)
        assertEquals("abc123", a.id)
        assertEquals("0427", a.check)
        assertEquals("NC-AB12", a.nocapId)
        // No secret, no request.
        assertTrue(runCatching { LinkAsk.asked(JSONObject().put("request_id", "x")) }.isFailure)
    }

    @Test
    fun pendingThenAcceptedWithTheToken() {
        val pending = LinkAsk.state(JSONObject().put("state", "pending").put("check", "0427"))
        assertTrue(pending.pending)
        assertFalse(pending.accepted)
        assertNull(pending.claimed)

        val device = JSONObject().put("device_id", "d1").put("token", "tok").put("nocap_id", "NC-AB12").put("server_name", "media-box")
            .put("user", JSONObject().put("username", "boss"))
        val ok = LinkAsk.state(JSONObject().put("state", "accepted").put("device", device))
        assertTrue(ok.accepted)
        assertEquals("tok", ok.claimed!!.token)
        assertEquals("boss", ok.claimed!!.username)

        // "Accepted" without a token isn't usable.
        assertFalse(LinkAsk.state(JSONObject().put("state", "accepted")).accepted)
        assertTrue(LinkAsk.state(JSONObject().put("state", "declined")).declined)
        // Anything odd reads as still waiting.
        assertTrue(LinkAsk.state(JSONObject().put("state", "lol")).pending)
    }

    @Test
    fun statusOfAFoundServer() {
        assertEquals(LinkAsk.Status.LINKED, LinkAsk.status(server("NC-AB12"), "nc-ab12"))
        assertEquals(LinkAsk.Status.READY, LinkAsk.status(server("NC-AB12"), "NC-OTHER"))
        assertEquals(LinkAsk.Status.READY, LinkAsk.status(server("NC-AB12"), null))
        assertEquals(LinkAsk.Status.NEW, LinkAsk.status(server("NC-AB12", setup = "new"), null))
        assertEquals(LinkAsk.Status.BLUETOOTH_ONLY, LinkAsk.status(server("", origin = null), null))
        assertTrue(LinkAsk.canAsk(server("NC-AB12"), null))
        assertFalse(LinkAsk.canAsk(server("NC-AB12"), "NC-AB12"))
        assertFalse(LinkAsk.canAsk(server("NC-AB12", origin = "http://192.168.1.20:8080"), null))
        assertFalse(LinkAsk.canAsk(server("NC-AB12", setup = "new"), null))
    }

    @Test
    fun blipsMatchTheWebRadar() {
        // Same hash as the web (FNV-1a, 32-bit).
        assertEquals(0x811c9dc5L, LinkAsk.hashOf(""))
        assertEquals(0xe40c292cL, LinkAsk.hashOf("a"))
        for (id in listOf("NC-AAAA", "NC-BBBB", "NC-123", "NC-x")) {
            for (i in 0 until 6) {
                val (x, y) = LinkAsk.blipSpot(id, i)
                assertEquals(LinkAsk.blipSpot(id, i), x to y)
                val d = Math.hypot((x - 0.5).toDouble(), (y - 0.5).toDouble())
                assertTrue("$id/$i: $d", d in 0.19..0.47)
            }
        }
        assertNotEquals(LinkAsk.blipSpot("NC-AAAA", 0), LinkAsk.blipSpot("NC-BBBB", 0))
    }
}
