package com.nocap.link.sync

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class ScanCodeTest {
    @Test fun pairingCodesStopTheScan() {
        assertTrue(isPairCode("nocap://pair?server=https%3A%2F%2F192.168.0.10%3A8443&code=482913&id=abc"))
        assertTrue(isPairCode("  NOCAP://pair?code=1 "))
    }

    @Test fun otherCodesKeepScanning() {
        assertFalse(isPairCode(null))
        assertFalse(isPairCode(""))
        assertFalse(isPairCode("nocap://pair?"))
        assertFalse(isPairCode("https://example.com/pair?code=1"))
        assertFalse(isPairCode("WIFI:T:WPA;S:home;P:secret;;"))
        assertFalse(isPairCode("nocap://open?app=files"))
    }
}
