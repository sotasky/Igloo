package com.screwy.igloo.ui.component

import com.screwy.igloo.media.MediaUri
import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Pure-function tests for the [MediaViewer] helpers.
 *
 * Covers the zoom clamp (1f..5f, pinch-in floor) and the swipe-down dismiss
 * predicate — extracted so no Android Context is needed.
 */
class MediaViewerTest {

    @Test
    fun clamp_zoom_scales_within_range() {
        assertEquals(1.5f, clampZoom(1f, 1.5f), 1e-6f)
    }

    @Test
    fun clamp_zoom_caps_at_max_five() {
        assertEquals(5f, clampZoom(4f, 2f), 1e-6f)
    }

    @Test
    fun clamp_zoom_floors_at_min_one() {
        assertEquals(1f, clampZoom(1f, 0.5f), 1e-6f)
    }

    @Test
    fun swipe_down_past_threshold_dismisses() {
        assertTrue(isSwipeDownDismiss(150f, 100f))
    }

    @Test
    fun swipe_down_under_threshold_does_not_dismiss() {
        assertFalse(isSwipeDownDismiss(50f, 100f))
    }

    @Test
    fun swipe_up_never_dismisses() {
        assertFalse(isSwipeDownDismiss(-150f, 100f))
    }

    @Test
    fun single_finger_drag_at_fit_scale_leaves_pager_swipe_enabled() {
        assertFalse(shouldHandleImageTransform(scale = 1f, pointerCount = 1))
    }

    @Test
    fun zoomed_image_or_multitouch_claims_transform_gestures() {
        assertTrue(shouldHandleImageTransform(scale = 1.5f, pointerCount = 1))
        assertTrue(shouldHandleImageTransform(scale = 1f, pointerCount = 2))
    }

    @Test
    fun media_image_memory_key_is_shared_between_grid_and_overlay_placeholder() {
        assertEquals(
            "igloo-media-thumb:local:/tmp/full.jpg",
            mediaImageMemoryCacheKey(MediaUri.Local(File("/tmp/full.jpg"))),
        )
        assertEquals(
            "igloo-media-thumb:remote:https://example.test/image.jpg",
            mediaImageMemoryCacheKey(MediaUri.Remote("https://example.test/image.jpg")),
        )
    }
}
