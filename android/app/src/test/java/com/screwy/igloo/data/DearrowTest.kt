package com.screwy.igloo.data

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * Unit tests for [Dearrow] resolver — pure functions, no Room/Android needed.
 */
class DearrowTest {

    // ─── resolveTitle ────────────────────────────────────────────────────────

    @Test fun off_alwaysReturnsOriginal() {
        assertEquals("Original", Dearrow.resolveTitle("off", "Original", "Community", "Casual"))
    }

    @Test fun off_returnsEmptyWhenOriginalNull() {
        assertEquals("", Dearrow.resolveTitle("off", null, "Community", "Casual"))
    }

    @Test fun default_returnsCommunityWhenPresent() {
        assertEquals("Community", Dearrow.resolveTitle("default", "Original", "Community", null))
    }

    @Test fun default_fallsBackToOriginalWhenCommunityNull() {
        assertEquals("Original", Dearrow.resolveTitle("default", "Original", null, null))
    }

    @Test fun default_fallsBackToOriginalWhenCommunityBlank() {
        assertEquals("Original", Dearrow.resolveTitle("default", "Original", "  ", null))
    }

    @Test fun default_returnsEmptyWhenOriginalNullAndCommunityNull() {
        assertEquals("", Dearrow.resolveTitle("default", null, null, null))
    }

    @Test fun casual_prefersCasualOverCommunity() {
        assertEquals("Casual", Dearrow.resolveTitle("casual", "Original", "Community", "Casual"))
    }

    @Test fun default_prefersSyncedDisplayTitleWhenPresent() {
        assertEquals(
            "Server Default",
            Dearrow.resolveTitle(
                mode = "default",
                original = "Original",
                community = "Community",
                casual = "Casual",
                displayTitle = "Server Default",
                displayTitleCasual = "Server Casual",
            ),
        )
    }

    @Test fun casual_prefersSyncedCasualDisplayTitleWhenPresent() {
        assertEquals(
            "Server Casual",
            Dearrow.resolveTitle(
                mode = "casual",
                original = "Original",
                community = "Community",
                casual = "Casual",
                displayTitle = "Server Default",
                displayTitleCasual = "Server Casual",
            ),
        )
    }

    @Test fun off_ignoresSyncedDisplayTitle() {
        assertEquals(
            "Original",
            Dearrow.resolveTitle(
                mode = "off",
                original = "Original",
                community = "Community",
                casual = "Casual",
                displayTitle = "Server Default",
                displayTitleCasual = "Server Casual",
            ),
        )
    }

    @Test fun casual_fallsBackToCommunityWhenCasualBlank() {
        assertEquals("Community", Dearrow.resolveTitle("casual", "Original", "Community", ""))
    }

    @Test fun casual_fallsBackToCommunityWhenCasualNull() {
        assertEquals("Community", Dearrow.resolveTitle("casual", "Original", "Community", null))
    }

    @Test fun casual_fallsBackToOriginalWhenBothAbsent() {
        assertEquals("Original", Dearrow.resolveTitle("casual", "Original", null, null))
    }

    @Test fun casual_fallsBackToEmptyWhenAllNull() {
        assertEquals("", Dearrow.resolveTitle("casual", null, null, null))
    }

    @Test fun unknownMode_treatedAsOff() {
        assertEquals("Original", Dearrow.resolveTitle("unknown_mode", "Original", "Community", "Casual"))
    }

    // ─── thumbnailUrlSuffix ──────────────────────────────────────────────────

    @Test fun thumbnailSuffix_emptyWhenModeOff() {
        assertEquals("", Dearrow.thumbnailUrlSuffix("off", "/path/to/thumb"))
    }

    @Test fun thumbnailSuffix_emptyWhenPathNull() {
        assertEquals("", Dearrow.thumbnailUrlSuffix("default", null))
    }

    @Test fun thumbnailSuffix_emptyWhenPathBlank() {
        assertEquals("", Dearrow.thumbnailUrlSuffix("default", "  "))
    }

    @Test fun thumbnailSuffix_da1WhenDefaultAndPathPresent() {
        assertEquals("?da=1", Dearrow.thumbnailUrlSuffix("default", "/dearrow/thumb.jpg"))
    }

    @Test fun thumbnailSuffix_da1WhenCasualAndPathPresent() {
        assertEquals("?da=1", Dearrow.thumbnailUrlSuffix("casual", "/dearrow/thumb.jpg"))
    }
}
