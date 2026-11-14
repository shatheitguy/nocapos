package com.nocap.link.sync

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class FinderTest {
    @Test
    fun readsSetupStateFromTheServiceUuid() {
        assertEquals("new", Finder.bleSetup(listOf("6E6F6361-7001-4E0C-A9B1-0D15C0F3B201")))
        assertEquals("done", Finder.bleSetup(listOf("0000180f-0000-1000-8000-00805f9b34fb", "6e6f6361-7001-4e0c-a9b1-0d15c0f3b200")))
        assertNull(Finder.bleSetup(listOf("6e6f6361-7001-4e0c-a9b1-0d15c0f3b2ff")))
        assertNull(Finder.bleSetup(emptyList()))
    }

    @Test
    fun shortNameMatchesTheServer() {
        assertEquals("media-box", Finder.shortName("media-box"))
        assertEquals("media-box", Finder.shortName("media-box.local"))
        assertEquals("a-very-lon", Finder.shortName("a-very-long-hostname"))
        assertEquals("NoCap", Finder.shortName(""))
        assertEquals("wohnzimmer", Finder.shortName("wohnzimmer_pc"))
    }

    @Test
    fun mdnsAnswersNeedANocapId() {
        val s = Finder.fromMdns("NoCap media-box", "https://192.168.1.20:8443", mapOf("nocap_id" to "NC-AB12", "setup" to "new", "version" to "0.13.0"))!!
        assertEquals("media-box", s.name)
        assertEquals(true, s.isNew)
        assertEquals("0.13.0", s.version)
        assertNull(Finder.fromMdns("x", "https://1.2.3.4:1", mapOf("nocap_id" to "<script>")))
        assertEquals("", Finder.fromMdns("NoCap old", "https://1.2.3.4:1", mapOf("nocap_id" to "NC-OLD", "setup" to "weird"))!!.setup)
    }

    @Test
    fun bluetoothHitsJoinTheNetworkEntry() {
        val lan = listOf(Finder.fromMdns("NoCap media-box.lan", "https://192.168.1.20:8443", mapOf("nocap_id" to "NC-AB12"))!!)
        val out = Finder.merge(lan, listOf(BleHit("media-box", "done"), BleHit("fresh", "new"), BleHit("fresh", "new")))
        assertEquals(2, out.size)
        assertEquals("media-box.lan", out[0].name)
        assertEquals(true, out[0].bluetooth)
        assertEquals("done", out[0].setup)
        assertEquals("fresh", out[1].name)
        assertNull(out[1].origin)
        assertEquals(true, out[1].isNew)
    }
}
