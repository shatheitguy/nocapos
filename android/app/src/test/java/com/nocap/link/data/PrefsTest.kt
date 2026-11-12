package com.nocap.link.data

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test

class PrefsTest {
    @Test
    fun valuesAreClampedToTheirRange() {
        Opt.columns.set(99)
        assertEquals(7, Opt.columns.v)
        Opt.columns.set(-3)
        assertEquals(3, Opt.columns.v)
        Opt.columns.reset()
    }

    @Test
    fun backupSkipsThisPhoneOnlySettings() {
        val o = Opt.export()
        assertFalse(o.has("custom_icons"))
        assertFalse(o.has("server_notices"))
        assertFalse(o.has("clip_sync"))
    }

    @Test
    fun importIgnoresUnknownAndBadValues() {
        Opt.import(JSONObject("""{"drawer":"NOT_A_STYLE","rows":"lots","wallpaper":"gone","what":1}"""))
        assertEquals(DrawerStyle.PAGED, Opt.drawer.v)
        assertEquals("tide", Opt.wallpaper.v)
    }

    @Test
    fun wordingFollowsTheLingo() {
        Prefs.setGenz(false)
        assertEquals("plain", t("fun", "plain"))
        Prefs.setGenz(true)
        assertEquals("fun", t("fun", "plain"))
    }
}
