package com.nocap.link.data

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class RosterFoldersTest {
    private val base = RosterFolders.create(emptyList(), "Socials", listOf("a", "b"), id = "f1")

    @Test
    fun createTakesAppsOutOfOtherFolders() {
        val l = RosterFolders.create(base, "Games", listOf("b", "c"), id = "f2")
        assertEquals(listOf("a"), l.first { it.id == "f1" }.keys)
        assertEquals(listOf("b", "c"), l.first { it.id == "f2" }.keys)
    }

    @Test
    fun addMovesAndKeepsTarget() {
        val two = RosterFolders.create(base, "Games", listOf("c"), id = "f2")
        val l = RosterFolders.add(two, "f2", "a")
        assertEquals(listOf("b"), l.first { it.id == "f1" }.keys)
        assertEquals(listOf("c", "a"), l.first { it.id == "f2" }.keys)
        // Adding what's already there changes nothing.
        assertEquals(l, RosterFolders.add(l, "f2", "a"))
        // An unknown folder changes nothing.
        assertEquals(l, RosterFolders.add(l, "nope", "z"))
    }

    @Test
    fun emptyFoldersGoAway() {
        val l = RosterFolders.remove(RosterFolders.remove(base, "a"), "b")
        assertTrue(l.isEmpty())
        assertTrue(RosterFolders.prune(base, setOf("x")).isEmpty())
        assertEquals(listOf("a"), RosterFolders.prune(base, setOf("a")).single().keys)
    }

    @Test
    fun renameAndDelete() {
        assertEquals("Fam", RosterFolders.rename(base, "f1", "  Fam ").single().name)
        assertEquals("Socials", RosterFolders.rename(base, "f1", "  ").single().name)
        assertTrue(RosterFolders.delete(base, "f1").isEmpty())
    }

    @Test
    fun jsonRoundTripAndBadInput() {
        assertEquals(base, RosterFolders.decode(RosterFolders.encode(base)))
        assertTrue(RosterFolders.decode("not json").isEmpty())
        assertTrue(RosterFolders.decode("[{\"name\":\"no id\"}]").isEmpty())
        assertEquals(setOf("a", "b"), RosterFolders.inFolders(base))
    }
}
