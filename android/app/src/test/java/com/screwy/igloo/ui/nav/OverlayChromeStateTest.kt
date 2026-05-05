package com.screwy.igloo.ui.nav

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class OverlayChromeStateTest {

    @Test
    fun fullscreenMediaOverlayOwnsBothChromeEdges() {
        assertTrue(OverlayChromeState.FullscreenMedia.hidesScaffoldTopBar)
        assertTrue(OverlayChromeState.FullscreenMedia.hidesBottomNav)
    }

    @Test
    fun topBarOnlyOverlayKeepsBottomNavigationVisible() {
        assertTrue(OverlayChromeState.HideTopBar.hidesScaffoldTopBar)
        assertFalse(OverlayChromeState.HideTopBar.hidesBottomNav)
    }
}
