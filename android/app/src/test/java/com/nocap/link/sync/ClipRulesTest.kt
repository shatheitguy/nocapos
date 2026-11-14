package com.nocap.link.sync

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ClipRulesTest {
    @Test fun copyButtons() {
        assertTrue(ClipRules.isCopyLabel("Copy"))
        assertTrue(ClipRules.isCopyLabel(" Copy link "))
        assertTrue(ClipRules.isCopyLabel("Kopieren", own = "Kopieren"))
        assertFalse(ClipRules.isCopyLabel("Copyright 2026"))
        assertFalse(ClipRules.isCopyLabel("Paste"))
        assertFalse(ClipRules.isCopyLabel(null))
        assertFalse(ClipRules.isCopyLabel(""))
    }

    @Test fun copiedToasts() {
        assertTrue(ClipRules.isCopiedToast("Copied to clipboard"))
        assertTrue(ClipRules.isCopiedToast("Link copied"))
        assertFalse(ClipRules.isCopiedToast("Saved"))
        assertFalse(ClipRules.isCopiedToast("x".repeat(100) + " copied"))
    }

    @Test fun pictureTypes() {
        assertEquals("image/png", ClipRules.imageMime(byteArrayOf(0x89.toByte(), 'P'.code.toByte(), 'N'.code.toByte(), 'G'.code.toByte(), 13, 10, 26, 10)))
        assertEquals("image/jpeg", ClipRules.imageMime(byteArrayOf(0xFF.toByte(), 0xD8.toByte(), 0xFF.toByte(), 0xE0.toByte())))
        assertNull(ClipRules.imageMime("GIF89a".toByteArray()))
        assertNull(ClipRules.imageMime(ByteArray(0)))
    }

    @Test fun limits() {
        assertTrue(ClipRules.textFits("hi"))
        assertFalse(ClipRules.textFits(""))
        assertTrue(ClipRules.textFits("x".repeat(ClipRules.MAX_TEXT)))
        assertFalse(ClipRules.textFits("é".repeat(ClipRules.MAX_TEXT / 2 + 1)))
    }

    @Test fun throttle() {
        assertTrue(ClipRules.due(5_000, 0))
        assertFalse(ClipRules.due(5_000, 4_000))
        assertTrue(ClipRules.due(5_500, 4_000))
    }
}
