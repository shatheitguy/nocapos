package com.nocap.link.settings

import org.junit.Assert.assertEquals
import org.junit.Test

class GuideTest {
    @Test
    fun progressCountsOnlyWhatAppliesOnThisPhone() {
        assertEquals(2 to 4, Guide.tally(listOf(true, false, null, true, false)))
        assertEquals(0 to 0, Guide.tally(listOf(null, null)))
        assertEquals(3 to 3, Guide.tally(listOf(true, true, true)))
    }
}
