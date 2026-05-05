package com.screwy.igloo.player

import com.screwy.igloo.data.entity.SponsorBlockSegmentEntity
import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * Pure-function tests for [PlayerGestures]: 10-second skip math and
 * horizontal-drag seek mapping.
 */
class PlayerGesturesTest {

    @Test
    fun skip_backward_subtracts_ten_seconds() {
        assertEquals(5_000L, skipBackwardMs(15_000L))
    }

    @Test
    fun skip_backward_clamps_at_zero() {
        assertEquals(0L, skipBackwardMs(5_000L))
    }

    @Test
    fun skip_backward_from_zero_stays_zero() {
        assertEquals(0L, skipBackwardMs(0L))
    }

    @Test
    fun skip_forward_adds_ten_seconds() {
        assertEquals(20_000L, skipForwardMs(10_000L, 30_000L))
    }

    @Test
    fun skip_forward_clamps_at_duration() {
        assertEquals(30_000L, skipForwardMs(25_000L, 30_000L))
    }

    @Test
    fun skip_forward_already_at_end_stays_at_end() {
        assertEquals(30_000L, skipForwardMs(30_000L, 30_000L))
    }

    @Test
    fun skip_forward_overshoot_when_current_exceeds_duration_preserves_current() {
        // Defensive — racy updates could have current > duration briefly. Clamp
        // should not reel position backwards.
        assertEquals(40_000L, skipForwardMs(40_000L, 30_000L))
    }

    @Test
    fun seek_from_drag_full_width_sweeps_full_duration() {
        val target = seekFromHorizontalDrag(
            currentMs = 0L,
            dragPx = 1000f,
            widthPx = 1000f,
            durationMs = 60_000L,
        )
        assertEquals(60_000L, target)
    }

    @Test
    fun seek_from_drag_half_width_sweeps_half_duration() {
        val target = seekFromHorizontalDrag(
            currentMs = 10_000L,
            dragPx = 500f,
            widthPx = 1000f,
            durationMs = 60_000L,
        )
        assertEquals(40_000L, target)
    }

    @Test
    fun seek_from_drag_negative_drag_rewinds() {
        val target = seekFromHorizontalDrag(
            currentMs = 30_000L,
            dragPx = -500f,
            widthPx = 1000f,
            durationMs = 60_000L,
        )
        assertEquals(0L, target)
    }

    @Test
    fun seek_from_drag_clamps_at_duration() {
        val target = seekFromHorizontalDrag(
            currentMs = 50_000L,
            dragPx = 1000f,
            widthPx = 1000f,
            durationMs = 60_000L,
        )
        assertEquals(60_000L, target)
    }

    @Test
    fun seek_from_drag_zero_width_returns_current_unchanged() {
        val target = seekFromHorizontalDrag(
            currentMs = 10_000L,
            dragPx = 500f,
            widthPx = 0f,
            durationMs = 60_000L,
        )
        assertEquals(10_000L, target)
    }

    @Test
    fun seek_from_drag_zero_duration_returns_current_unchanged() {
        val target = seekFromHorizontalDrag(
            currentMs = 10_000L,
            dragPx = 500f,
            widthPx = 1000f,
            durationMs = 0L,
        )
        assertEquals(10_000L, target)
    }

    @Test
    fun vertical_level_drag_up_increases_from_start_level() {
        assertEquals(
            0.82f,
            adjustedLevelFromVerticalDrag(
                startLevel = 0.5f,
                dragPx = -200f,
                heightPx = 1000f,
            ),
            0.01f,
        )
    }

    @Test
    fun vertical_level_drag_down_decreases_from_start_level() {
        assertEquals(
            0.18f,
            adjustedLevelFromVerticalDrag(
                startLevel = 0.5f,
                dragPx = 200f,
                heightPx = 1000f,
            ),
            0.01f,
        )
    }

    @Test
    fun volume_index_rounds_small_upward_drags_to_next_step() {
        assertEquals(8, volumeIndexForFraction(0.5f, maxVolume = 15))
        assertEquals(9, volumeIndexForFraction(0.58f, maxVolume = 15))
    }

    @Test
    fun scrubber_thumb_offset_uses_pixel_space_and_clamps_inside_track() {
        assertEquals(0f, trackThumbOffsetPx(0f, barWidthPx = 1000, thumbWidthPx = 10f), 0.001f)
        assertEquals(495f, trackThumbOffsetPx(0.5f, barWidthPx = 1000, thumbWidthPx = 10f), 0.001f)
        assertEquals(990f, trackThumbOffsetPx(1f, barWidthPx = 1000, thumbWidthPx = 10f), 0.001f)
    }

    @Test
    fun scrubber_fraction_from_x_clamps_to_track_width() {
        assertEquals(0f, scrubberFractionForX(-10f, barWidthPx = 100), 0.001f)
        assertEquals(0.5f, scrubberFractionForX(50f, barWidthPx = 100), 0.001f)
        assertEquals(1f, scrubberFractionForX(150f, barWidthPx = 100), 0.001f)
    }

    @Test
    fun scrubber_segment_fractions_clamp_inside_duration() {
        val (start, width) = sponsorSegmentFractions(
            startTimeSec = 5.0,
            endTimeSec = 15.0,
            durationMs = 10_000L,
        )

        assertEquals(0.5f, start, 0.001f)
        assertEquals(0.5f, width, 0.001f)
    }

    @Test
    fun scrubber_tap_target_uses_segment_end_when_tap_hits_segment() {
        val segment = SponsorBlockSegmentEntity(
            videoId = "video",
            startTime = 2.0,
            endTime = 4.0,
            category = "sponsor",
        )
        val hit = scrubberSegmentAtFraction(
            segments = listOf(segment),
            fraction = 0.3f,
            durationMs = 10_000L,
        )

        assertEquals(segment, hit)
        assertEquals(4_000L, scrubberTapTargetMs(fraction = 0.3f, durationMs = 10_000L, downSegment = hit))
        assertEquals(7_500L, scrubberTapTargetMs(fraction = 0.75f, durationMs = 10_000L, downSegment = null))
    }
}
