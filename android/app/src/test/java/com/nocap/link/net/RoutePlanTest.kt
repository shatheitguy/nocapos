package com.nocap.link.net

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class RoutePlanTest {
    private val paired = "https://192.168.1.20:8443"
    private val remote = "https://nocap.example.com"

    @Test fun atHomeTriesNearbyFirst() {
        val order = RoutePlan.order(true, listOf("https://192.168.1.20:8443"), listOf("https://10.0.0.5:8443", paired), remote, paired)
        assertEquals(listOf("https://192.168.1.20:8443", "https://10.0.0.5:8443", remote), order)
    }

    @Test fun awaySkipsHomeAddresses() {
        val order = RoutePlan.order(false, listOf("https://192.168.1.30:8443"), listOf("https://10.0.0.5:8443"), remote, paired)
        assertEquals(listOf(remote, paired), order)
    }

    @Test fun noRemoteFallsBackToPaired() {
        assertEquals(listOf(paired), RoutePlan.order(false, emptyList(), emptyList(), null, paired))
        assertEquals(listOf(paired), RoutePlan.order(true, emptyList(), emptyList(), "", paired))
    }

    @Test fun pairedRemoteComesAfterLan() {
        val order = RoutePlan.order(true, emptyList(), listOf("http://192.168.1.20:8080"), null, remote)
        assertEquals(listOf("http://192.168.1.20:8080", remote), order)
    }

    @Test fun nearbyOrRemote() {
        assertEquals(Route.NEARBY, RoutePlan.routeOf("https://192.168.1.20:8443", emptySet()))
        assertEquals(Route.NEARBY, RoutePlan.routeOf("https://nocap.local", emptySet()))
        assertEquals(Route.REMOTE, RoutePlan.routeOf(remote, emptySet()))
        assertEquals(Route.NEARBY, RoutePlan.routeOf("https://home.lan:8443", setOf("https://home.lan:8443")))
    }

    @Test fun homeAddresses() {
        for (a in listOf("https://10.1.2.3", "http://172.16.0.1:80", "https://172.31.255.255", "https://192.168.0.1", "https://169.254.1.1", "https://[fd00::1]:8443", "https://box.local")) {
            assertTrue(a, RoutePlan.isHomeAddress(a))
        }
        for (a in listOf("https://172.32.0.1", "https://8.8.8.8", "https://nocap.example.com", "not a url", "https://[2001:db8::1]")) {
            assertFalse(a, RoutePlan.isHomeAddress(a))
        }
    }

    @Test fun parsesActions() {
        val (ev, actions) = parseEvents(
            """{"cursor":42,"notices":[],"clipboard":[{"id":7,"text":"hi","from":"NoCap OS"}],"dismissed":["k"],"signins":[],
               "actions":[{"id":40,"kind":"reply","key":"m1","text":"yes"},{"id":41,"kind":"file","root":"home","path":"/a.txt","name":"a.txt","size":3,"open":true}]}""",
        )
        assertEquals(42L, ev.cursor)
        assertEquals("hi", ev.clips.single().text)
        assertEquals(listOf("k"), ev.dismissed)
        assertEquals(2, actions.size)
        assertEquals("yes", actions[0].text)
        assertTrue(actions[1].open)
        assertEquals(3L, actions[1].size)
        // An older server: no actions at all.
        assertTrue(parseEvents("""{"cursor":1}""").second.isEmpty())
    }
}
