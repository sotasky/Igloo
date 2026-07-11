package com.screwy.igloo.data

import org.junit.Assert.assertEquals
import org.junit.Test

/** Unit tests for [Dearrow] resolver — pure functions, no Room/Android needed. */
class DearrowTest {

    // ─── resolveTitle ────────────────────────────────────────────────────────

    @Test
    fun off_alwaysReturnsOriginal() {
        assertEquals("Original", Dearrow.resolveTitle("off", "Original", "Community", "Casual"))
    }

    @Test
    fun off_returnsEmptyWhenOriginalNull() {
        assertEquals("", Dearrow.resolveTitle("off", null, "Community", "Casual"))
    }

    @Test
    fun default_returnsCommunityWhenPresent() {
        assertEquals("Community", Dearrow.resolveTitle("default", "Original", "Community", null))
    }

    @Test
    fun default_fallsBackToOriginalWhenCommunityNull() {
        assertEquals("Original", Dearrow.resolveTitle("default", "Original", null, null))
    }

    @Test
    fun default_fallsBackToOriginalWhenCommunityBlank() {
        assertEquals("Original", Dearrow.resolveTitle("default", "Original", "  ", null))
    }

    @Test
    fun default_returnsEmptyWhenOriginalNullAndCommunityNull() {
        assertEquals("", Dearrow.resolveTitle("default", null, null, null))
    }

    @Test
    fun casual_prefersCasualOverCommunity() {
        assertEquals("Casual", Dearrow.resolveTitle("casual", "Original", "Community", "Casual"))
    }

    @Test
    fun casual_fallsBackToCommunityWhenCasualBlank() {
        assertEquals("Community", Dearrow.resolveTitle("casual", "Original", "Community", ""))
    }

    @Test
    fun casual_fallsBackToCommunityWhenCasualNull() {
        assertEquals("Community", Dearrow.resolveTitle("casual", "Original", "Community", null))
    }

    @Test
    fun casual_fallsBackToOriginalWhenBothAbsent() {
        assertEquals("Original", Dearrow.resolveTitle("casual", "Original", null, null))
    }

    @Test
    fun casual_fallsBackToEmptyWhenAllNull() {
        assertEquals("", Dearrow.resolveTitle("casual", null, null, null))
    }

    @Test
    fun unknownMode_treatedAsOff() {
        assertEquals(
            "Original",
            Dearrow.resolveTitle("unknown_mode", "Original", "Community", "Casual"),
        )
    }
}
