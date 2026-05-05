package com.screwy.igloo.settings

import androidx.compose.ui.unit.dp
import org.junit.Assert.assertEquals
import org.junit.Test

class ThemeRouteTest {

    @Test fun themeDropdownMenuYOffset_alignsSelectedItemToAnchor() {
        assertEquals(0.dp, themeDropdownMenuYOffset(selectedIndex = 0))
        assertEquals(ThemeDropdownItemHeight * -1f, themeDropdownMenuYOffset(selectedIndex = 1))
        assertEquals(ThemeDropdownItemHeight * -4f, themeDropdownMenuYOffset(selectedIndex = 4))
    }

    @Test fun themeDropdownMenuYOffset_clampsNegativeIndex() {
        assertEquals(0.dp, themeDropdownMenuYOffset(selectedIndex = -1))
    }
}
