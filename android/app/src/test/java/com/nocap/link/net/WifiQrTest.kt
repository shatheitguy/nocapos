package com.nocap.link.net

import org.junit.Assert.assertEquals
import org.junit.Test

class WifiQrTest {
    @Test
    fun plainNetwork() {
        assertEquals("WIFI:T:WPA;S:Home;P:hunter22;H:false;;", WifiQr.payload("Home", "hunter22", "WPA", false))
    }

    @Test
    fun specialCharactersAreEscaped() {
        assertEquals("a\\\\b\\;c\\,d\\:e\\\"f", WifiQr.escape("a\\b;c,d:e\"f"))
        assertEquals(
            "WIFI:T:WPA;S:Joe's \\\"Fast\\\"\\; Wi\\:Fi;P:p\\,a\\;s\\\\s;H:true;;",
            WifiQr.payload("Joe's \"Fast\"; Wi:Fi", "p,a;s\\s", "WPA", true),
        )
    }

    @Test
    fun openNetworkHasNoPassword() {
        assertEquals("WIFI:T:nopass;S:Cafe;H:false;;", WifiQr.payload("Cafe", "", "nopass", false))
    }
}
