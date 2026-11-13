package com.nocap.link.update

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ReleasesTest {
    private val json = """
        {"tag_name":"v3.2.0","name":"NoCapOS 3.2.0","html_url":"https://github.com/shatheitguy/nocapos/releases/tag/v3.2.0",
         "body":"## What's new\n- **Faster** sync\n- See [docs](https://x)\nversionCode: 5",
         "assets":[
           {"name":"nocap-os-debug.apk","browser_download_url":"https://github.com/a/debug.apk","size":1},
           {"name":"nocap-os.apk","browser_download_url":"https://github.com/a/nocap-os.apk","size":42},
           {"name":"evil.apk","browser_download_url":"http://insecure/evil.apk","size":1}
         ]}
    """.trimIndent()

    @Test
    fun parsesAndPicksTheApp() {
        val r = Releases.parse(json)
        assertEquals("v3.2.0", r.tag)
        assertEquals(2, r.assets.size) // the plain-http one is dropped
        assertEquals("nocap-os.apk", Releases.pickApk(r.assets)?.name)
        assertEquals(AppVersion(5, null), Releases.versionFromBody(r.body))
    }

    @Test
    fun neverPicksDebugBuilds() {
        val list = listOf(Asset("nocap-os-debug.apk", "https://a", 1), Asset("nocap-os-3.1.0.apk", "https://b", 1), Asset("nocap-os-3.10.0.apk", "https://c", 1))
        assertEquals("nocap-os-3.10.0.apk", Releases.pickApk(list)?.name)
        assertNull(Releases.pickApk(listOf(Asset("nocap-os-debug.apk", "https://a", 1))))
    }

    @Test
    fun comparesVersions() {
        assertTrue(Releases.compareNames("3.10.0", "3.9.2") > 0)
        assertTrue(Releases.compareNames("v3.2.0", "3.2.0-beta") > 0)
        assertEquals(0, Releases.compareNames("3.2", "3.2.0"))
        assertEquals(true, Releases.isNewer(AppVersion(5, "3.0.0"), 4, "3.1.0")) // the code decides
        assertEquals(false, Releases.isNewer(AppVersion(null, "3.1.0"), 4, "3.1.0"))
        assertNull(Releases.isNewer(null, 4, "3.1.0"))
    }

    @Test
    fun readsVersionsFromFileNames() {
        assertEquals(AppVersion(7, "3.2.0"), Releases.versionFromAssetName("nocap-os-3.2.0-7.apk"))
        assertEquals(AppVersion(null, "3.2.0"), Releases.versionFromAssetName("nocap-os-v3.2.0.apk"))
        assertEquals(AppVersion(9, null), Releases.versionFromAssetName("nocap-os-9.apk"))
        assertNull(Releases.versionFromAssetName("nocap-os.apk"))
    }

    @Test
    fun whatsNewIsPlainText() {
        val notes = Releases.whatsNew(Releases.parse(json).body)
        assertEquals("• Faster sync\n• See docs", notes)
    }

    @Test
    fun signersMustMatch() {
        assertTrue(Releases.signersMatch(setOf("aa"), setOf("aa")))
        assertFalse(Releases.signersMatch(setOf("aa"), setOf("bb")))
        assertTrue(Releases.signersMatch(setOf("aa"), setOf("bb"), setOf("aa", "bb"))) // key rotation
        assertFalse(Releases.signersMatch(emptySet(), setOf("aa")))
    }
}
