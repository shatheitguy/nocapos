package com.nocap.link.net

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class NoCapApiTest {
    @Test
    fun typedAddressesTryHttpsFirst() {
        assertEquals(listOf("https://192.168.1.20:8443", "http://192.168.1.20:8443"), NoCapApi.candidates("192.168.1.20:8443"))
        assertEquals(listOf("https://nocap.local", "http://nocap.local"), NoCapApi.candidates(" nocap.local/ "))
    }

    @Test
    fun fullUrlsAreKeptAsTyped() {
        assertEquals(listOf("http://nocap.local:8080"), NoCapApi.candidates("http://nocap.local:8080/some/page"))
        assertTrue(NoCapApi.candidates("").isEmpty())
    }

    @Test
    fun originDropsDefaultPortsAndPaths() {
        assertEquals("https://example.test", NoCapApi.origin("https://example.test:443/x?y=1"))
        assertEquals("http://example.test:8080", NoCapApi.origin("http://example.test:8080/"))
        assertEquals("https://[fe80::1]:8443", NoCapApi.origin("https://[fe80::1]:8443/"))
        assertNull(NoCapApi.origin("not a url"))
    }

    @Test
    fun onlyTheLinkedServerIsTrusted() {
        val api = NoCapApi("http://nocap.local:8080", null)
        assertTrue(api.sameServer("http://nocap.local:8080/api/v1/x"))
        assertTrue(!api.sameServer("http://nocap.local:8080.evil.test/x"))
        assertTrue(!api.sameServer("https://nocap.local:8080/x"))
    }

    @Test(expected = IllegalArgumentException::class)
    fun httpsNeedsAPinnedCertificate() {
        NoCapApi("https://nocap.local", null)
    }

    @Test
    fun pinsArePrintedInPairs() {
        assertEquals("AB:CD:EF", Pins.pretty("abcdef"))
    }

    @Test
    fun clientsAreSharedPerPin() {
        assertTrue(Pins.client("aa") === Pins.client("aa"))
        assertTrue(Pins.client("aa") !== Pins.client("bb"))
    }
}
