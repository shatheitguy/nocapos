package com.nocap.link.mirror

import com.nocap.link.mirror.Protocol.Control
import org.json.JSONObject
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ProtocolTest {
    @Test
    fun frameHeaderIsTypeThenBigEndianPts() {
        val f = Protocol.frame(Protocol.TYPE_KEY, 0x0102030405060708L, byteArrayOf(9, 8, 7))
        assertEquals(12, f.size)
        assertArrayEquals(byteArrayOf(2, 1, 2, 3, 4, 5, 6, 7, 8, 9, 8, 7), f)
        assertEquals(Protocol.TYPE_KEY to 0x0102030405060708L, Protocol.header(f))
        assertNull(Protocol.header(ByteArray(3)))
    }

    @Test
    fun fitKeepsAspectAndEvenSizes() {
        assertEquals(1080 to 2400, Protocol.fit(1080, 2400, 2400))
        assertEquals(486 to 1080, Protocol.fit(1080, 2400, 1080))
        val (w, h) = Protocol.fit(1440, 3120, 1080)
        assertEquals(1080, h)
        assertEquals(0, w % 2)
        assertEquals(498, w) // 1440 * 1080 / 3120 = 498.46
        assertEquals(1080 to 486, Protocol.fit(3200, 1440, 1080))
        assertEquals(720 to 406, Protocol.fit(1281, 723, 720))
        assertEquals(2 to 2, Protocol.fit(0, 100, 1080))
        // Already small: unchanged, but odd sizes become even.
        assertEquals(640 to 480, Protocol.fit(641, 481, 1080))
    }

    @Test
    fun normalizedPositionsMapIntoTheScreen() {
        assertEquals(0f to 0f, Protocol.toScreen(0.0, 0.0, 1080, 2400))
        assertEquals(1079f to 2399f, Protocol.toScreen(1.0, 1.0, 1080, 2400))
        assertEquals(539.5f to 1199.5f, Protocol.toScreen(0.5, 0.5, 1080, 2400))
    }

    @Test
    fun wheelDownSwipesUp() {
        val p = Protocol.scrollSwipe(Control.Scroll(0.5, 0.5, 0.0, 1.0), 1000, 2000)
        assertEquals(p[0], p[2])
        assertEquals(999.5f - 250f, p[3], 0.01f)
        val far = Protocol.scrollSwipe(Control.Scroll(0.5, 0.1, 0.0, 10.0), 1000, 2000)
        assertEquals(1f, far[3], 0.01f) // clamped inside the screen
        val up = Protocol.scrollSwipe(Control.Scroll(0.5, 0.5, 0.0, -1.0), 1000, 2000)
        assertTrue(up[3] > up[1])
    }

    @Test
    fun controlMessagesAreCheckedAndClamped() {
        assertEquals(Control.Tap(1.0, 0.0), Protocol.parse("""{"t":"tap","x":3,"y":-1}"""))
        assertNull(Protocol.parse("""{"t":"tap","x":"nope","y":0.5}"""))
        assertNull(Protocol.parse("""{"t":"tap","x":0.5}"""))
        assertEquals(Control.LongPress(0.25, 0.75), Protocol.parse("""{"t":"long","x":0.25,"y":0.75}"""))
        assertEquals(Control.Swipe(0.0, 0.1, 0.2, 0.3, 3000), Protocol.parse("""{"t":"swipe","x1":0,"y1":0.1,"x2":0.2,"y2":0.3,"ms":99999}"""))
        assertEquals(Control.Swipe(0.0, 0.1, 0.2, 0.3, 300), Protocol.parse("""{"t":"swipe","x1":0,"y1":0.1,"x2":0.2,"y2":0.3}"""))
        assertEquals(Control.Scroll(0.5, 0.5, 0.0, 10.0), Protocol.parse("""{"t":"scroll","x":0.5,"y":0.5,"dy":120}"""))
        assertNull(Protocol.parse("""{"t":"scroll","x":0.5,"y":0.5}"""))
        assertEquals(Control.Global("back"), Protocol.parse("""{"t":"global","a":"back"}"""))
        assertNull(Protocol.parse("""{"t":"global","a":"power"}"""))
        assertEquals(Control.Key("Enter"), Protocol.parse("""{"t":"key","k":"Enter"}"""))
        assertNull(Protocol.parse("""{"t":"key","k":"F13"}"""))
        assertEquals(Control.Volume(true), Protocol.parse("""{"t":"vol","d":1}"""))
        assertNull(Protocol.parse("""{"t":"vol","d":5}"""))
        assertEquals(Control.Viewers(2), Protocol.parse("""{"t":"viewers","n":2}"""))
        assertEquals(Control.Mode(true), Protocol.parse("""{"t":"mode","m":"jpeg"}"""))
        assertEquals(Control.KeyFrame, Protocol.parse("""{"t":"keyframe"}"""))
        assertNull(Protocol.parse("""{"t":"format-c"}"""))
        assertNull(Protocol.parse("not json"))
        val long = "x".repeat(Protocol.MAX_TEXT + 50)
        val text = Protocol.parse(JSONObject().put("t", "text").put("s", long).toString()) as Control.Text
        assertEquals(Protocol.MAX_TEXT, text.s.length)
        assertNull(Protocol.parse("""{"t":"text","s":""}"""))
    }

    @Test
    fun metaAndState() {
        val m = JSONObject(Protocol.meta(1080, 2400, 5, false, 6_000_000, 30, true))
        assertEquals("meta", m.getString("t"))
        assertEquals(1, m.getInt("rot"))
        assertEquals("h264", m.getString("codec"))
        assertTrue(m.getBoolean("control"))
        assertEquals("streaming", JSONObject(Protocol.state("streaming")).getString("state"))
    }

    @Test
    fun typingEditsAtTheCursor() {
        assertEquals(Protocol.Edit("heXllo", 3), Protocol.edit("hello", 2, 2, "X", null))
        assertEquals(Protocol.Edit("helloX", 6), Protocol.edit("hello", -1, -1, "X", null))
        assertEquals(Protocol.Edit("hXo", 2), Protocol.edit("hello", 1, 4, "X", null))
        assertEquals(Protocol.Edit("hllo", 1), Protocol.edit("hello", 2, 2, null, "Backspace"))
        assertNull(Protocol.edit("hello", 0, 0, null, "Backspace"))
        assertEquals(Protocol.Edit("helo", 2), Protocol.edit("hello", 2, 2, null, "Delete"))
        assertNull(Protocol.edit("hello", 5, 5, null, "Delete"))
        assertEquals(Protocol.Edit("ho", 1), Protocol.edit("hello", 4, 1, null, "Backspace"))
        assertEquals(Protocol.Edit("a", 1), Protocol.edit("a😀", 3, 3, null, "Backspace"))
        assertEquals(Protocol.Edit("hello", 1), Protocol.edit("hello", 2, 2, null, "ArrowLeft"))
        assertEquals(Protocol.Edit("hello", 5), Protocol.edit("hello", 5, 5, null, "ArrowRight"))
        assertEquals(Protocol.Edit("hello", 0), Protocol.edit("hello", 3, 3, null, "ArrowUp"))
        assertEquals(Protocol.Edit("hello", 5), Protocol.edit("hello", 3, 3, null, "ArrowDown"))
    }

    @Test
    fun findsSpsAndPpsInConfig() {
        val cfg = byteArrayOf(0, 0, 0, 1, 0x67, 0x42, 0, 0x1f, 0, 0, 0, 1, 0x68, 0x01)
        assertEquals(listOf(7, 8), Protocol.nalTypes(cfg))
        val f = Protocol.frame(Protocol.TYPE_CONFIG, 0, cfg)
        assertEquals(listOf(7, 8), Protocol.nalTypes(f, Protocol.HEADER))
    }
}
