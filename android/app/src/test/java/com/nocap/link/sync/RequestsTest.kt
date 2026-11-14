package com.nocap.link.sync

import com.nocap.link.net.parseEvents
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class RequestsTest {
    private val fresh = """{"cursor":42,"notices":[],"clipboard":[],"dismissed":[],"signins":[],"actions":[],"mirror":{"id":"ab12cd34","seq":42,"at":"2026-11-14T10:00:00Z"}}"""

    @Test
    fun mirrorAskIsParsedFromTheLongPoll() {
        val (ev, actions) = parseEvents(fresh)
        assertEquals("ab12cd34", ev.mirror)
        assertTrue(actions.isEmpty())
    }

    @Test
    fun noMirrorFieldMeansNoAsk() {
        val (ev, _) = parseEvents("""{"cursor":7,"actions":[]}""")
        assertNull(ev.mirror)
        val (blank, _) = parseEvents("""{"cursor":7,"mirror":{"id":""}}""")
        assertNull(blank.mirror)
    }

    @Test
    fun mirrorAskIsHandledOnTheFirstAnswerToo() {
        val (ev, actions) = parseEvents(fresh)
        val steps = Requests.plan(ev, actions, hadCursor = false, seen = emptySet())
        assertEquals(listOf<Requests.Step>(Requests.Step.Mirror("ab12cd34")), steps)
    }

    @Test
    fun actionsRunOnceAndBeforeTheFeed() {
        val text = """{"cursor":50,"actions":[
            {"id":48,"kind":"ring"},
            {"id":49,"kind":"open","url":"https://example.test"},
            {"id":50,"kind":""}
        ]}"""
        val (ev, actions) = parseEvents(text)
        val steps = Requests.plan(ev, actions, hadCursor = true, seen = setOf(48L))
        assertEquals(2, steps.size)
        val act = steps[0] as Requests.Step.Act
        assertEquals("open", act.action.kind)
        assertEquals("https://example.test", act.action.url)
        assertEquals(Requests.Step.Feed, steps[1])
    }

    @Test
    fun ringOnAFreshConnectionIsNotDropped() {
        val (ev, actions) = parseEvents("""{"cursor":9,"actions":[{"id":9,"kind":"ring"}]}""")
        val steps = Requests.plan(ev, actions, hadCursor = false, seen = emptySet())
        assertEquals(1, steps.size)
        assertEquals("ring", (steps[0] as Requests.Step.Act).action.kind)
    }
}
