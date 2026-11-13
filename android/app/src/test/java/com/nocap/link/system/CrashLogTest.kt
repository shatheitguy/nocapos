package com.nocap.link.system

import java.net.URLDecoder
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class CrashLogTest {
    private val report = """
        NoCap OS crash report
        Time: 2026-11-12 10:00:00 +0000
        App: com.nocap.link 3.1.0 (4, debug)
        Android: 14 (API 34)
        Phone: Google Pixel 8
        Crash on thread "main"

        java.lang.IllegalStateException: Unable to resolve host "home.example.net": no address
        	at com.nocap.link.net.NoCapApi.get(NoCapApi.kt:120)
        	at com.nocap.link.MainActivity.refresh(MainActivity.kt:470)
    """.trimIndent()

    @Test
    fun scrubsAddressesEmailsAndIps() {
        val s = CrashLog.sanitize("GET https://nocap.example.net/api/v1/device?x=1 from sam@example.com via 192.168.1.20:8443 and fe80:0:0:0:1:2:3:4")
        assertFalse(s.contains("nocap.example.net"))
        assertFalse(s.contains("sam@"))
        assertFalse(s.contains("192.168"))
        assertFalse(s.contains("fe80"))
        assertTrue(s.contains("<url>") && s.contains("<email>") && s.contains("<ip>"))
    }

    @Test
    fun scrubsFilePathsWithNamesAndSpaces() {
        val s = CrashLog.sanitize("open failed: /storage/emulated/0/Download/Sam Smith taxes.pdf: open failed: ENOENT")
        assertFalse(s.contains("Sam Smith"))
        assertTrue(s.contains("<path>"))
        assertTrue("keeps the error after the path", s.contains("ENOENT"))
        assertFalse(CrashLog.sanitize("at /data/user/0/com.nocap.link/files/sam/notes.txt").contains("sam"))
    }

    @Test
    fun scrubsTokensAndSecrets() {
        val s = CrashLog.sanitize("token=abc123secret Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload password: \"hunter2\" id 9f8e7d6c5b4a39281706f5e4d3c2b1a0ffee")
        assertFalse(s.contains("abc123secret"))
        assertFalse(s.contains("eyJhbGciOiJIUzI1NiJ9"))
        assertFalse(s.contains("hunter2"))
        assertFalse(s.contains("9f8e7d6c5b4a39281706f5e4d3c2b1a0ffee"))
    }

    @Test
    fun keepsStackFramesVersionsAndModel() {
        val s = CrashLog.sanitize(report)
        assertFalse(s.contains("home.example.net"))
        assertTrue(s.contains("at com.nocap.link.net.NoCapApi.get(NoCapApi.kt:120)"))
        assertTrue(s.contains("App: com.nocap.link 3.1.0 (4, debug)"))
        assertTrue(s.contains("Phone: Google Pixel 8"))
        assertTrue(s.contains("Android: 14 (API 34)"))
    }

    @Test
    fun headlineNamesTheErrorClass() {
        assertEquals("Android: IllegalStateException, Crash on thread \"main\"", CrashLog.headline(CrashLog.sanitize(report)))
    }

    @Test
    fun issueUrlIsPrefilledAndFitsTheLimit() {
        val url = CrashLog.issueUrl("Android: crash & burn", "short body")
        assertTrue(url.startsWith(CrashLog.ISSUES + "?title="))
        assertTrue(url.contains("crash%20%26%20burn"))
        assertTrue(URLDecoder.decode(url.substringAfter("&body="), "UTF-8").contains("short body"))
        assertFalse(url.contains("(cut"))

        val long = CrashLog.issueUrl("Android: long", "é stack frame line\n".repeat(2000))
        assertTrue(long.length <= CrashLog.MAX_URL)
        assertTrue(URLDecoder.decode(long.substringAfter("&body="), "UTF-8").contains("cut to fit"))
    }

    @Test
    fun bugTitleIsScrubbedToo() {
        val title = CrashLog.bugTitle("Can't reach https://my.server.lan from sam@example.com\nmore")
        assertFalse(title.contains("my.server.lan"))
        assertFalse(title.contains("sam@"))
        assertTrue(title.startsWith("Android: "))
    }
}
