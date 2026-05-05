package com.screwy.igloo.moments

import org.junit.Assert.assertEquals
import org.junit.Test

class MomentTextTest {
    @Test
    fun momentDisplayTextFallsBackFromBlankDescriptionToTitle() {
        assertEquals(
            "Feliz 1ero de Mayo",
            momentDisplayText(description = "", title = "Feliz 1ero de Mayo"),
        )
    }

    @Test
    fun momentDisplayTextPrefersNonBlankDescription() {
        assertEquals(
            "Photo by @creator",
            momentDisplayText(description = "Photo by @creator", title = "Fallback title"),
        )
    }
}
