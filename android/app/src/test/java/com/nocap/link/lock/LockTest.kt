package com.nocap.link.lock

import com.nocap.link.data.Opt
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class LockTest {
    @Test
    fun lockScreenShowsOnlyWhenOnLockedAndNotInACall() {
        assertTrue(LockWatch.shouldShow(enabled = true, keyguardLocked = true, inCall = false))
        assertFalse(LockWatch.shouldShow(enabled = false, keyguardLocked = true, inCall = false))
        assertFalse(LockWatch.shouldShow(enabled = true, keyguardLocked = false, inCall = false))
        assertFalse(LockWatch.shouldShow(enabled = true, keyguardLocked = true, inCall = true))
    }

    @Test
    fun hueMath() {
        assertEquals(0f, NoCapWalls.hueSat(0xFFFF0000.toInt()).first, 0.5f)
        assertEquals(120f, NoCapWalls.hueSat(0xFF00FF00.toInt()).first, 0.5f)
        assertEquals(240f, NoCapWalls.hueSat(0xFF0000FF.toInt()).first, 0.5f)
        assertEquals(0f, NoCapWalls.hueSat(0xFF808080.toInt()).second, 0.001f)
        assertTrue(NoCapWalls.isNoCapRed(NoCapWalls.RED))
        assertFalse(NoCapWalls.isNoCapRed(0xFF1AD1FF.toInt()))
    }

    @Test
    fun redWashTurnsTheNoCapWallpaperColorsRed() {
        // Typical colors of Tide (deep teal/blue), Nebula (purple) and Midnight (navy), plus grey.
        val bases = listOf(0xFF103040, 0xFF402060, 0xFF101830, 0xFF2A6080, 0xFF606060, 0xFF5030A0).map { it.toInt() }
        for (b in bases) {
            val c = NoCapWalls.nocapify(b)
            assertTrue("%08X → %08X".format(b, c), NoCapWalls.isNoCapRed(c))
        }
    }

    @Test
    fun blendEnds() {
        assertEquals(0xFF102030.toInt(), NoCapWalls.blend(0xFF102030.toInt(), NoCapWalls.RED, 0f))
        assertEquals(NoCapWalls.RED, NoCapWalls.blend(0xFF102030.toInt(), NoCapWalls.RED, 1f))
    }

    @Test
    fun lockScreenChoiceStaysOnThisPhone() {
        assertFalse(Opt.export().has("nocap_lock"))
    }
}
