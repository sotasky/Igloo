package com.screwy.igloo.ui.component

import androidx.media3.ui.AspectRatioFrameLayout
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.unit.IntSize
import org.junit.Assert.assertEquals
import org.junit.Test

class MediaStateTest {

    @Test
    fun videoPosterStaysVisibleUntilFirstFrameRenders() {
        assertEquals(true, shouldRenderVideoPosterOverlay(hasPlayer = false, firstFrameRendered = false))
        assertEquals(true, shouldRenderVideoPosterOverlay(hasPlayer = true, firstFrameRendered = false))
        assertEquals(false, shouldRenderVideoPosterOverlay(hasPlayer = true, firstFrameRendered = true))
    }

    @Test
    fun videoResizeModeFitsWideFramesAndZoomsTallFrames() {
        assertEquals(
            AspectRatioFrameLayout.RESIZE_MODE_FIT,
            videoResizeModeFor(width = 1920, height = 1080),
        )
        assertEquals(
            AspectRatioFrameLayout.RESIZE_MODE_ZOOM,
            videoResizeModeFor(width = 720, height = 1280),
        )
        assertEquals(
            AspectRatioFrameLayout.RESIZE_MODE_ZOOM,
            videoResizeModeFor(width = 0, height = 0),
        )
    }

    @Test
    fun zoomedImagePanStaysInsideTheScaledViewport() {
        assertEquals(
            Offset(500f, -500f),
            boundedZoomPanOffset(
                current = Offset(480f, -480f),
                pan = Offset(80f, -80f),
                scale = 2f,
                size = IntSize(width = 1000, height = 1000),
            ),
        )
        assertEquals(
            Offset.Zero,
            boundedZoomPanOffset(
                current = Offset(120f, -80f),
                pan = Offset(20f, -20f),
                scale = 1f,
                size = IntSize(width = 1000, height = 1000),
            ),
        )
    }

}
