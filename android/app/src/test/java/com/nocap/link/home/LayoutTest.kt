package com.nocap.link.home

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

class LayoutTest {
    @Before
    fun reset() {
        Home.set(Layout(listOf(Page()), emptyList()))
    }

    @Test
    fun cellsOverlap() {
        assertTrue(Cell(0, 0, 2, 2).overlaps(Cell(1, 1)))
        assertFalse(Cell(0, 0, 2, 2).overlaps(Cell(2, 0)))
        assertFalse(Cell(0, 0).overlaps(Cell(0, 1)))
    }

    @Test
    fun jsonRoundTrip() {
        val l = Layout(
            listOf(
                Page(listOf(AppItem("a", Cell(0, 0), "pkg/Act"), NoCapItem("w", Cell(0, 1, 4, 2), NoCapKinds.CLOCK, mapOf("face" to "glow")))),
                Page(listOf(FolderItem("f", Cell(1, 1), "Stuff", listOf("x/X", "y/Y")))),
            ),
            listOf("pkg/Act", "x/X"),
        )
        val back = Home.fromJson(Home.toJson(l))
        assertEquals(l.dock, back.dock)
        assertEquals(2, back.pages.size)
        assertEquals(l.pages[0].items[0], back.pages[0].items[0])
        assertEquals("glow", (back.pages[0].items[1] as NoCapItem).opt("face"))
        assertEquals(listOf("x/X", "y/Y"), (back.pages[1].items[0] as FolderItem).keys)
    }

    @Test
    fun brokenEntriesAreSkippedNotFatal() {
        val o = JSONObject(
            """{"pages":[{"items":[{"type":"app"},{"type":"app","id":"ok","key":"p/A","x":0,"y":0},"junk",{"type":"nope"}]},"bad"],"dock":["p/A","p/A"]}""",
        )
        val l = Home.fromJson(o)
        assertTrue(l.pages.isNotEmpty())
        assertEquals(1, l.pages[0].items.count { it is AppItem })
        assertEquals(listOf("p/A"), l.dock)
    }

    @Test
    fun emptyJsonGivesAUsableLayout() {
        val l = Home.fromJson(JSONObject())
        assertTrue(l.dock.isEmpty())
    }

    @Test
    fun freeSpotSkipsTakenCells() {
        Home.set(Layout(listOf(Page(listOf(AppItem("a", Cell(0, 0), "p/A")))), emptyList()))
        val (page, cell) = Home.freeSpot(1, 1)
        assertEquals(0, page)
        assertFalse(cell.overlaps(Cell(0, 0)))
    }

    @Test
    fun widgetSizesStayInsideTheGrid() {
        for (kind in listOf(NoCapKinds.CLOCK, NoCapKinds.STATUS, NoCapKinds.AI, NoCapKinds.WEATHER)) {
            for ((w, h) in NoCapKinds.sizes(kind, cols = 3, rows = 4)) {
                assertTrue("$kind $w x $h", w in 1..3 && h in 1..4)
            }
        }
    }
}
