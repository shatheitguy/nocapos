package com.nocap.link.control

import com.nocap.link.data.Act
import com.nocap.link.data.Gesture
import com.nocap.link.data.Opt
import com.nocap.link.data.Prefs
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ControlTest {
    private fun n(key: String, pkg: String, time: Long) = Notifs.Item(key, pkg, "t$key", "", time, clearable = true, canReply = false)

    @Test
    fun notificationsGroupByAppNewestFirst() {
        val g = Notifs.group(listOf(n("a1", "chat", 10), n("b1", "mail", 30), n("a2", "chat", 50), n("b2", "mail", 20)))
        assertEquals(listOf("chat", "mail"), g.map { it.pkg })
        assertEquals(listOf("a2", "a1"), g[0].items.map { it.key })
        assertEquals(listOf("b1", "b2"), g[1].items.map { it.key })
        assertTrue(Notifs.group(emptyList()).isEmpty())
    }

    @Test
    fun lockScreenCountsReadInBothVoices() {
        assertEquals("1 notification", Notifs.countLine(1, genz = false))
        assertEquals("3 notifications", Notifs.countLine(3, genz = false))
        assertEquals("3 new notifs", Notifs.countLine(3, genz = true))
    }

    @Test
    fun brightnessLevelsRoundTripAndNeverGoBlack() {
        assertEquals(1, Switches.toLevel(0f))
        assertEquals(255, Switches.toLevel(1f))
        assertEquals(255, Switches.toLevel(7f))
        assertEquals(0.5f, Switches.fromLevel(Switches.toLevel(0.5f)), 0.01f)
    }

    @Test
    fun edgeStripOnlyFiresOnADownwardPull() {
        assertTrue(EdgeStrip.isPull(dx = 5f, dy = 60f, threshold = 40f))
        assertFalse(EdgeStrip.isPull(dx = 5f, dy = 20f, threshold = 40f))
        assertFalse(EdgeStrip.isPull(dx = 90f, dy = 60f, threshold = 40f)) // sideways
        assertFalse(EdgeStrip.isPull(dx = 0f, dy = -60f, threshold = 40f))
    }

    @Test
    fun serverGlanceSaysHowFreshItIs() {
        Prefs.setGenz(false)
        val now = 100_000_000L
        assertNull(ServerGlance.describe("Box", 0, now, 5, 0).ok)
        assertEquals(false, ServerGlance.describe("Box", now - 31 * 60_000L, now, 5, 0).ok)
        val on = ServerGlance.describe("Box", now - 60_000L, now, 12, 0)
        assertEquals(true, on.ok)
        assertEquals("Box · online · CPU 12%", on.text)
        assertEquals("Box · online · 2 updates", ServerGlance.describe("Box", now, now, 12, 2).text)
        Prefs.setGenz(true)
    }

    @Test
    fun swipeDownMovesToTheControlCenterOnlyFromTheOldDefault() {
        val old = mapOf(Gesture.SWIPE_DOWN.name to Act.NOTIFICATIONS.name, Gesture.SWIPE_UP.name to Act.DRAWER.name)
        val moved = Opt.controlCenterDefault(old)
        assertEquals(Act.CONTROL_CENTER.name, moved[Gesture.SWIPE_DOWN.name])
        assertEquals(Act.DRAWER.name, moved[Gesture.SWIPE_UP.name])
        val picked = mapOf(Gesture.SWIPE_DOWN.name to Act.SEARCH.name)
        assertEquals(picked, Opt.controlCenterDefault(picked))
        assertEquals(Act.CONTROL_CENTER.name, Opt.gestures.def[Gesture.SWIPE_DOWN.name])
    }

    @Test
    fun edgeSwipeStaysOnThisPhone() {
        assertFalse(Opt.export().has("edge_swipe"))
    }
}
