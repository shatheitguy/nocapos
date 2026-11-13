package com.nocap.link.data

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class LingoTest {
    @Test
    fun choiceDecidesTheWording() {
        assertTrue(Lingo.isGenz(Lingo.GENZ, Lingo.STANDARD))
        assertFalse(Lingo.isGenz(Lingo.STANDARD, Lingo.GENZ))
        assertTrue(Lingo.isGenz("something-old", ""))
    }

    @Test
    fun sameAsServerFollowsItAndFallsBackToGenz() {
        assertFalse(Lingo.isGenz(Lingo.SERVER, Lingo.STANDARD))
        assertTrue(Lingo.isGenz(Lingo.SERVER, Lingo.GENZ))
        assertTrue(Lingo.isGenz(Lingo.SERVER, "")) // never heard from the server / offline
    }

    @Test
    fun serverAnswerIsRememberedOnlyWhenFollowingIt() {
        Opt.lingo.set(Lingo.SERVER)
        Lingo.fromServer(Lingo.STANDARD)
        assertEquals(Lingo.SERVER, Opt.lingo.v)
        assertEquals("plain", t("fun", "plain"))
        Lingo.fromServer("pirate") // ignored
        assertEquals(Lingo.STANDARD, Opt.serverLingo.v)

        Opt.lingo.set(Lingo.GENZ)
        Lingo.fromServer(Lingo.STANDARD) // "sync from my server" applies it directly
        assertEquals(Lingo.STANDARD, Opt.lingo.v)
        Opt.lingo.reset()
        Opt.serverLingo.reset()
    }

    @Test
    fun serverLanguageIsNotBackedUp() {
        Opt.serverLingo.set(Lingo.STANDARD)
        assertFalse(Opt.export().has("server_lingo"))
        Opt.serverLingo.reset()
    }
}
