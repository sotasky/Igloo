package com.screwy.igloo.player

import com.screwy.igloo.R
import com.screwy.igloo.data.entity.SponsorBlockSegmentEntity
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class SponsorBlockControllerTest {

    @Test
    fun build_sponsorblock_segments_filters_disabled_unknown_and_invalid_segments() {
        val modes = sponsorBlockModeMap(
            sponsor = SponsorBlockModeSilent,
            selfPromo = "off",
            interaction = SponsorBlockModeAsk,
            intro = SponsorBlockModeAsk,
            outro = SponsorBlockModeAsk,
            preview = SponsorBlockModeAsk,
            filler = SponsorBlockModeAsk,
            music = SponsorBlockModeAsk,
        )

        val segments = buildSponsorBlockUiSegments(
            segments = listOf(
                segment(category = "Sponsor", start = 1.25, end = 2.5),
                segment(category = "selfpromo", start = 2.5, end = 3.0),
                segment(category = "unknown", start = 3.0, end = 4.0),
                segment(category = "intro", start = 5.0, end = 4.5),
            ),
            modes = modes,
        )

        assertEquals(1, segments.size)
        assertEquals("sponsor:1250:2500", segments.single().key)
        assertEquals(SponsorBlockModeSilent, segments.single().mode)
    }

    @Test
    fun sponsorblock_label_res_normalizes_known_categories() {
        assertEquals(R.string.sponsorblock_category_sponsor, sponsorBlockLabelRes("SPONSOR"))
        assertEquals(R.string.sponsorblock_category_music_offtopic, sponsorBlockLabelRes("music_offtopic"))
        assertEquals(R.string.sponsorblock_segment_fallback, sponsorBlockLabelRes("unknown"))
    }

    @Test
    fun playback_controller_auto_skips_silent_segment_when_playing_through() {
        var now = 10_000L
        val seeks = mutableListOf<Long>()
        val controller = playbackController(
            seekTo = seeks::add,
            nowMs = { now },
        )
        val segments = uiSegments(mode = SponsorBlockModeSilent)

        controller.onTick(isPlaying = true, positionMs = 1_500L, segments = segments)

        assertEquals(listOf(2_500L), seeks)
        assertNull(controller.skipSegment)
        assertEquals("Skipped sponsor", controller.autoSkipMessage)
    }

    @Test
    fun playback_controller_prompts_silent_segment_after_recent_manual_seek() {
        var now = 10_000L
        val seeks = mutableListOf<Long>()
        val controller = playbackController(
            seekTo = seeks::add,
            nowMs = { now },
        )
        val segments = uiSegments(mode = SponsorBlockModeSilent)

        controller.onSeek(positionMs = 1_500L)
        now = 10_100L
        controller.onTick(isPlaying = true, positionMs = 1_500L, segments = segments)

        assertEquals(emptyList<Long>(), seeks)
        assertEquals(segments.single(), controller.skipSegment)
        assertNull(controller.autoSkipMessage)
    }

    @Test
    fun playback_controller_prompts_ask_segment_without_seeking() {
        val seeks = mutableListOf<Long>()
        val controller = playbackController(seekTo = seeks::add)
        val segments = uiSegments(mode = SponsorBlockModeAsk)

        controller.onTick(isPlaying = true, positionMs = 1_500L, segments = segments)

        assertEquals(emptyList<Long>(), seeks)
        assertEquals(segments.single(), controller.skipSegment)
        assertNull(controller.autoSkipMessage)
    }

    @Test
    fun playback_controller_manual_skip_seeks_and_sets_notification() {
        val seeks = mutableListOf<Long>()
        val controller = playbackController(seekTo = seeks::add)
        val segment = uiSegments(mode = SponsorBlockModeAsk).single()

        controller.skip(segment)

        assertEquals(listOf(2_500L), seeks)
        assertNull(controller.skipSegment)
        assertEquals("Skipped sponsor", controller.autoSkipMessage)
    }

    private fun segment(
        category: String,
        start: Double,
        end: Double,
    ): SponsorBlockSegmentEntity = SponsorBlockSegmentEntity(
        videoId = "video",
        startTime = start,
        endTime = end,
        category = category,
    )

    private fun uiSegments(mode: String): List<SponsorBlockUiSegment> = buildSponsorBlockUiSegments(
        segments = listOf(segment(category = "sponsor", start = 1.0, end = 2.5)),
        modes = mapOf("sponsor" to mode),
    )

    private fun playbackController(
        seekTo: (Long) -> Unit = {},
        nowMs: () -> Long = { 10_000L },
    ): SponsorBlockPlaybackController = SponsorBlockPlaybackController(
        seekTo = seekTo,
        skippedMessage = { category -> "Skipped $category" },
        nowMs = nowMs,
    )
}
