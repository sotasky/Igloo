package com.screwy.igloo.ui.component

import com.screwy.igloo.data.entity.VideoEntity
import com.screwy.igloo.data.entity.VideoGridItem
import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * Pure-function tests for [progressFraction] — the watch-progress bar width calculation used by
 * [VideoGrid] cells.
 */
class VideoGridTest {

    @Test
    fun null_position_returns_zero() {
        assertEquals(0f, progressFraction(null, 100.0), 0f)
    }

    @Test
    fun null_duration_returns_zero() {
        assertEquals(0f, progressFraction(50.0, null), 0f)
    }

    @Test
    fun zero_duration_returns_zero() {
        assertEquals(0f, progressFraction(50.0, 0.0), 0f)
    }

    @Test
    fun negative_duration_returns_zero() {
        assertEquals(0f, progressFraction(50.0, -10.0), 0f)
    }

    @Test
    fun half_way() {
        assertEquals(0.5f, progressFraction(50.0, 100.0), 0.0001f)
    }

    @Test
    fun clamps_above_one() {
        assertEquals(1f, progressFraction(500.0, 100.0), 0f)
    }

    @Test
    fun clamps_below_zero() {
        assertEquals(0f, progressFraction(-10.0, 100.0), 0f)
    }

    @Test
    fun youtube_channel_label_does_not_append_source_id_as_handle() {
        val item =
            gridItem(
                channelId = "youtube_UCAbCdEfGhIjKlMnOpQrStUv",
                channelName = "Readable Channel",
                channelSourceId = "UCAbCdEfGhIjKlMnOpQrStUv",
            )

        assertEquals("Readable Channel", videoGridChannelLabel(item))
    }

    @Test
    fun non_youtube_channel_label_can_still_include_handle() {
        val item =
            gridItem(
                channelId = "tiktok_creator",
                channelName = "Readable Creator",
                channelSourceId = "creator",
            )

        assertEquals("Readable Creator (@creator)", videoGridChannelLabel(item))
    }

    @Test
    fun non_youtube_matching_label_still_includes_handle_marker() {
        val item =
            gridItem(
                channelId = "tiktok_creator",
                channelName = "creator",
                channelSourceId = "creator",
            )

        assertEquals("creator (@creator)", videoGridChannelLabel(item))
    }

    @Test
    fun tiktok_channel_label_rejects_internal_source_id() {
        val item =
            gridItem(
                channelId = "tiktok_7000000000000000001",
                channelName = "Readable Creator",
                channelSourceId = "7000000000000000001",
            )

        assertEquals("Readable Creator", videoGridChannelLabel(item))
    }

    @Test
    fun duration_badge_formats_synced_duration() {
        assertEquals(
            "12:34",
            videoDurationBadgeLabel(
                VideoEntity(
                    videoId = "video-1",
                    channelId = "youtube_chan",
                    ownerKind = "youtube_video",
                    duration = 754L,
                )
            ),
        )
        assertEquals(
            "",
            videoDurationBadgeLabel(
                VideoEntity(
                    videoId = "video-2",
                    channelId = "youtube_chan",
                    ownerKind = "youtube_video",
                    duration = null,
                )
            ),
        )
    }

    @Test
    fun scroll_arrows_at_top_show_down_only() {
        assertEquals(
            ScrollArrowVisibility(showTop = false, showBottom = true),
            scrollArrowVisibility(
                showScrollFabs = true,
                itemCount = 30,
                visibleItemCount = 9,
                firstVisibleItemIndex = 0,
                firstVisibleItemScrollOffset = 0,
            ),
        )
    }

    @Test
    fun scroll_arrows_before_one_page_hide_both() {
        assertEquals(
            ScrollArrowVisibility(showTop = false, showBottom = false),
            scrollArrowVisibility(
                showScrollFabs = true,
                itemCount = 30,
                visibleItemCount = 9,
                firstVisibleItemIndex = 4,
                firstVisibleItemScrollOffset = 0,
            ),
        )
    }

    @Test
    fun scroll_arrows_after_one_page_show_up_only() {
        assertEquals(
            ScrollArrowVisibility(showTop = true, showBottom = false),
            scrollArrowVisibility(
                showScrollFabs = true,
                itemCount = 30,
                visibleItemCount = 9,
                firstVisibleItemIndex = 9,
                firstVisibleItemScrollOffset = 0,
            ),
        )
    }

    private fun gridItem(channelId: String, channelName: String?, channelSourceId: String?) =
        VideoGridItem(
            video =
                VideoEntity(
                    videoId = "video-1",
                    channelId = channelId,
                    ownerKind = "youtube_video",
                    title = "Video",
                ),
            playbackPosition = null,
            watchDuration = null,
            channelName = channelName,
            channelSourceId = channelSourceId,
        )
}
