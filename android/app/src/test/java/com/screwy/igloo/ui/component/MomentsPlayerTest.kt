package com.screwy.igloo.ui.component

import androidx.compose.ui.graphics.Color
import androidx.media3.common.Player
import androidx.media3.ui.AspectRatioFrameLayout
import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.data.entity.MediaInventoryEntity
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.media.OwnerKind
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class MomentsPlayerTest {

    @Test
    fun moment_media_mode_routes_images_and_slideshows_into_still_surfaces() {
        assertEquals(MomentMediaMode.Video, momentMediaMode(mediaKind = null, slideCount = 0))
        assertEquals(MomentMediaMode.Image, momentMediaMode(mediaKind = "image", slideCount = 0))
        assertEquals(MomentMediaMode.Slideshow, momentMediaMode(mediaKind = "slideshow", slideCount = 4))
        assertEquals(MomentMediaMode.Slideshow, momentMediaMode(mediaKind = "image", slideCount = 3))
    }

    @Test
    fun video_caption_base_padding_keeps_text_close_to_progress_bar() {
        assertEquals(16, momentCaptionBaseBottomPaddingDp(MomentMediaMode.Video))
        assertEquals(12, momentCaptionBaseBottomPaddingDp(MomentMediaMode.Image))
        assertEquals(12, momentCaptionBaseBottomPaddingDp(MomentMediaMode.Slideshow))
    }

    @Test
    fun collapsed_caption_start_padding_sits_near_left_edge() {
        assertEquals(8, momentCollapsedCaptionStartPaddingDp())
    }

    @Test
    fun caption_expansion_only_changes_description_line_limit() {
        assertEquals(2, momentCaptionDescriptionMaxLines(expanded = false))
        assertEquals(Int.MAX_VALUE, momentCaptionDescriptionMaxLines(expanded = true))
    }

    @Test
    fun caption_plain_text_tap_toggles_expandable_description() {
        assertTrue(momentCaptionExpandedAfterPlainTextClick(expanded = false, descriptionCanExpand = true))
        assertFalse(momentCaptionExpandedAfterPlainTextClick(expanded = true, descriptionCanExpand = true))
        assertFalse(momentCaptionExpandedAfterPlainTextClick(expanded = false, descriptionCanExpand = false))
    }

    @Test
    fun caption_background_only_appears_while_expanded() {
        assertEquals(Color.Transparent, momentCaptionBackgroundColor(expanded = false))
        assertEquals(Color.Black.copy(alpha = 0.28f), momentCaptionBackgroundColor(expanded = true))
    }

    @Test
    fun moment_slide_count_treats_single_images_as_one_page() {
        assertEquals(0, momentSlideCount(mediaKind = null, slideCount = 0))
        assertEquals(1, momentSlideCount(mediaKind = "image", slideCount = 0))
        assertEquals(5, momentSlideCount(mediaKind = "slideshow", slideCount = 5))
    }

    @Test
    fun story_progress_window_counts_only_current_profile_group() {
        val items = listOf(
            storyItem("a1", "tiktok_a"),
            storyItem("a2", "tiktok_a"),
            storyItem("b1", "tiktok_b"),
            storyItem("b2", "tiktok_b"),
            storyItem("b3", "tiktok_b"),
        )

        assertEquals(StoryProgressWindow(index = 0, count = 3), storyProgressWindow(items, 2))
        assertEquals(StoryProgressWindow(index = 2, count = 3), storyProgressWindow(items, 4))
    }

    @Test
    fun story_tap_advance_stops_at_profile_boundary_when_scoped_from_avatar() {
        val items = listOf(
            storyItem("a1", "tiktok_a"),
            storyItem("a2", "tiktok_a"),
            storyItem("b1", "tiktok_b"),
        )

        assertEquals(
            StoryAdvanceTarget(nextIndex = 1, shouldExit = false, animate = false),
            storyAdvanceTarget(items, currentIndex = 0, crossProfile = false),
        )
        assertEquals(
            StoryAdvanceTarget(nextIndex = null, shouldExit = true, animate = false),
            storyAdvanceTarget(items, currentIndex = 1, crossProfile = false),
        )
    }

    @Test
    fun story_tap_advance_crosses_profile_boundary_from_stories_tab() {
        val items = listOf(
            storyItem("a1", "tiktok_a"),
            storyItem("a2", "tiktok_a"),
            storyItem("b1", "tiktok_b"),
        )

        assertEquals(
            StoryAdvanceTarget(nextIndex = 2, shouldExit = false, animate = true),
            storyAdvanceTarget(items, currentIndex = 1, crossProfile = true),
        )
        assertEquals(
            StoryAdvanceTarget(nextIndex = null, shouldExit = true, animate = false),
            storyAdvanceTarget(items, currentIndex = 2, crossProfile = true),
        )
    }

    @Test
    fun resolve_initial_moment_thumbnail_uri_reuses_relative_thumbnail_path_before_resolver_catches_up() {
        val uri = resolveInitialMomentThumbnailUri(
            videoId = "demo",
            thumbnailPath = "/thumb/demo.jpg",
            mediaKind = "video",
            slideCount = 0,
            ownerKind = OwnerKind.TikTokVideo,
            baseUrl = "https://igloo.example",
        )

        assertEquals(
            MediaUri.Remote("https://igloo.example/thumb/demo.jpg"),
            uri,
        )
    }

    @Test
    fun resolve_initial_moment_thumbnail_uri_falls_back_to_slide_endpoint_for_slideshows() {
        val uri = resolveInitialMomentThumbnailUri(
            videoId = "demo",
            thumbnailPath = null,
            mediaKind = "slideshow",
            slideCount = 4,
            ownerKind = OwnerKind.TikTokVideo,
            baseUrl = "https://igloo.example",
        )

        assertEquals(
            MediaUri.Remote("https://igloo.example/api/media/slide/demo/0"),
            uri,
        )
    }

    @Test
    fun resolve_initial_moment_stream_uri_prefers_cached_inventory_rows() {
        val uri = resolveInitialMomentStreamUri(
            rows = listOf(
                streamRow(state = "pending"),
                streamRow(state = "cached"),
            ),
            baseUrl = "https://igloo.example",
            videoId = "demo",
        )

        assertEquals(
            MediaUri.Remote("https://igloo.example/api/media/stream/demo"),
            uri,
        )
    }

    @Test
    fun resolve_initial_moment_stream_uri_falls_back_to_server_stream_endpoint_when_inventory_lags() {
        val uri = resolveInitialMomentStreamUri(
            rows = emptyList(),
            baseUrl = "https://igloo.example",
            videoId = "demo",
        )

        assertEquals(
            MediaUri.Remote("https://igloo.example/api/media/stream/demo"),
            uri,
        )
    }

    @Test
    fun moment_stream_load_key_changes_when_uri_changes() {
        assertEquals(
            "remote:demo:https://igloo.example/api/media/stream/demo",
            momentStreamLoadKey("demo", MediaUri.Remote("https://igloo.example/api/media/stream/demo")),
        )
        assertEquals(
            "remote:demo:https://cdn.example/demo.mp4",
            momentStreamLoadKey("demo", MediaUri.Remote("https://cdn.example/demo.mp4")),
        )
        assertNull(momentStreamLoadKey("demo", MediaUri.Missing))
    }

    @Test
    fun moment_stream_load_key_exposes_video_id_without_parsing_remote_url() {
        assertEquals(
            "demo",
            momentStreamLoadKeyVideoId("remote:demo:https://igloo.example/api/media/stream/demo"),
        )
        assertEquals(
            "demo",
            momentStreamLoadKeyVideoId("local:demo:/data/user/0/com.screwy.igloo/files/demo.mp4"),
        )
        assertNull(momentStreamLoadKeyVideoId(null))
    }

    @Test
    fun moment_video_debug_helpers_label_stream_and_player_state() {
        assertEquals("local", MediaUri.Local(java.io.File("/tmp/video.mp4")).momentsDebugKind())
        assertEquals("remote", MediaUri.Remote("https://igloo.example/video.mp4").momentsDebugKind())
        assertEquals("missing", MediaUri.Missing.momentsDebugKind())
        assertEquals("ready", Player.STATE_READY.momentPlayerStateDebugName())
        assertEquals("ended", Player.STATE_ENDED.momentPlayerStateDebugName())
    }

    @Test
    fun moment_player_media_item_carries_video_id_as_media_id() {
        val mediaItem = momentPlayerMediaItem(
            videoId = "demo",
            streamUri = MediaUri.Remote("https://igloo.example/api/media/stream/demo"),
        )

        assertEquals("demo", mediaItem?.mediaId)
        assertNull(momentPlayerMediaItem("demo", MediaUri.Missing))
    }

    @Test
    fun moments_video_surface_fills_only_vertical_videos() {
        assertEquals(AspectRatioFrameLayout.RESIZE_MODE_ZOOM, momentsVideoResizeMode(width = 720, height = 1280))
        assertEquals(AspectRatioFrameLayout.RESIZE_MODE_FIT, momentsVideoResizeMode(width = 1920, height = 1080))
        assertEquals(AspectRatioFrameLayout.RESIZE_MODE_FIT, momentsVideoResizeMode(width = 1000, height = 1000))
        assertEquals(AspectRatioFrameLayout.RESIZE_MODE_FIT, momentsVideoResizeMode(width = 0, height = 0))
    }

    @Test
    fun moment_video_surface_state_rejects_recycled_player_media() {
        val state = momentVideoSurfaceStateFor(
            expectedMediaId = "visible",
            currentMediaId = "old-slot-video",
            playbackState = Player.STATE_READY,
            videoWidth = 1080,
            videoHeight = 1920,
        )

        assertFalse(state.playerReady)
        assertFalse(state.hasExpectedMedia)
    }

    @Test
    fun resolve_moment_slide_uris_prefers_inventory_rows_and_sorts_by_slide_index() {
        val uris = resolveMomentSlideUris(
            rows = listOf(
                slideRow(index = 2),
                slideRow(index = 0),
                slideRow(index = 1),
            ),
            baseUrl = "https://igloo.example",
            videoId = "demo",
            fallbackSlideCount = 1,
        )

        assertEquals(
            listOf(
                MediaUri.Remote("https://igloo.example/api/media/slide/demo/0"),
                MediaUri.Remote("https://igloo.example/api/media/slide/demo/1"),
                MediaUri.Remote("https://igloo.example/api/media/slide/demo/2"),
            ),
            uris,
        )
    }

    @Test
    fun resolve_moment_slide_uris_falls_back_to_remote_slide_endpoints_when_inventory_lags() {
        val uris = resolveMomentSlideUris(
            rows = emptyList(),
            baseUrl = "https://igloo.example",
            videoId = "demo",
            fallbackSlideCount = 3,
        )

        assertEquals(
            listOf(
                MediaUri.Remote("https://igloo.example/api/media/slide/demo/0"),
                MediaUri.Remote("https://igloo.example/api/media/slide/demo/1"),
                MediaUri.Remote("https://igloo.example/api/media/slide/demo/2"),
            ),
            uris,
        )
    }

    @Test
    fun resolve_moment_slide_uris_prefers_verified_sync_slide_rows() {
        val uris = resolveMomentSlideUris(
            rows = listOf(slideRow(index = 0)),
            baseUrl = "https://igloo.example",
            videoId = "demo",
            fallbackSlideCount = 1,
            syncRows = listOf(
                syncAsset(
                    assetId = "tiktok_tiktok_video_demo_post_media_2",
                    assetKind = "post_media",
                    serverUrl = "/api/android/sync/assets/tiktok_tiktok_video_demo_post_media_2",
                ),
                syncAsset(
                    assetId = "tiktok_tiktok_video_demo_post_media",
                    assetKind = "post_media",
                    serverUrl = "/api/android/sync/assets/tiktok_tiktok_video_demo_post_media",
                ),
                syncAsset(
                    assetId = "tiktok_tiktok_video_demo_post_media_1",
                    assetKind = "post_media",
                    serverUrl = "/api/android/sync/assets/tiktok_tiktok_video_demo_post_media_1",
                ),
            ),
        )

        assertEquals(
            listOf(
                MediaUri.Remote("https://igloo.example/api/android/sync/assets/tiktok_tiktok_video_demo_post_media"),
                MediaUri.Remote("https://igloo.example/api/android/sync/assets/tiktok_tiktok_video_demo_post_media_1"),
                MediaUri.Remote("https://igloo.example/api/android/sync/assets/tiktok_tiktok_video_demo_post_media_2"),
            ),
            uris,
        )
    }

    @Test
    fun resolve_moment_audio_uri_prefers_inventory_audio_rows_when_present() {
        val uri = resolveMomentAudioUri(
            rows = listOf(audioRow()),
            baseUrl = "https://igloo.example",
            videoId = "demo",
        )

        assertEquals(
            MediaUri.Remote("https://igloo.example/api/media/audio/demo"),
            uri,
        )
    }

    @Test
    fun resolve_moment_audio_uri_falls_back_to_server_audio_endpoint_when_inventory_lags() {
        val uri = resolveMomentAudioUri(
            rows = emptyList(),
            baseUrl = "https://igloo.example",
            videoId = "demo",
        )

        assertEquals(
            MediaUri.Remote("https://igloo.example/api/media/audio/demo"),
            uri,
        )
    }

    @Test
    fun resolve_moment_audio_uri_prefers_verified_sync_audio_row() {
        val uri = resolveMomentAudioUri(
            rows = listOf(audioRow()),
            baseUrl = "https://igloo.example",
            videoId = "demo",
            syncRows = listOf(syncAsset(assetId = "demo_post_audio", assetKind = "post_audio", serverUrl = "/api/media/audio/demo")),
        )

        assertEquals(
            MediaUri.Remote("https://igloo.example/api/media/audio/demo"),
            uri,
        )
    }

    @Test
    fun should_play_current_page_even_while_pager_is_moving() {
        assertTrue(
            shouldPlayMomentPage(
                isCurrentPage = true,
                isScrollInProgress = false,
            ),
        )
        assertTrue(
            shouldPlayMomentPage(
                isCurrentPage = true,
                isScrollInProgress = true,
            ),
        )
        assertFalse(
            shouldPlayMomentPage(
                isCurrentPage = false,
                isScrollInProgress = false,
            ),
        )
    }

    @Test
    fun auto_swipe_end_advance_wraps_to_start() {
        assertEquals(3, nextMomentPageForAutoSwipe(currentPage = 2, lastIndex = 5, autoSwipeEnabled = true))
        assertEquals(0, nextMomentPageForAutoSwipe(currentPage = 5, lastIndex = 5, autoSwipeEnabled = true))
        assertNull(nextMomentPageForAutoSwipe(currentPage = 2, lastIndex = 5, autoSwipeEnabled = false))
    }

    @Test
    fun first_frame_stops_showing_thumbnail_fallback_for_any_orientation() {
        assertTrue(isWideMomentVideo(width = 1920, height = 1080))
        assertTrue(isVerticalMomentVideo(width = 720, height = 1280))
        assertFalse(isWideMomentVideo(width = 720, height = 1280))

        assertFalse(
            shouldShowMomentThumbnailFallback(
                remoteOffline = false,
                surfaceState = MomentVideoSurfaceState(
                    playerReady = true,
                    isWide = false,
                    hasExpectedMedia = true,
                    renderedFirstFrame = true,
                ),
            ),
        )
        assertTrue(
            shouldShowMomentThumbnailFallback(
                remoteOffline = false,
                surfaceState = MomentVideoSurfaceState(
                    playerReady = false,
                    isWide = false,
                    hasExpectedMedia = true,
                    renderedFirstFrame = false,
                ),
            ),
        )
        assertTrue(
            shouldShowMomentThumbnailFallback(
                remoteOffline = true,
                surfaceState = MomentVideoSurfaceState(
                    playerReady = true,
                    isWide = false,
                    hasExpectedMedia = true,
                    renderedFirstFrame = true,
                ),
            ),
        )
    }

    @Test
    fun ready_state_requires_a_rendered_frame_before_hiding_thumbnail() {
        val noFrame = momentVideoSurfaceStateFor(
            expectedMediaId = "demo",
            currentMediaId = "demo",
            playbackState = Player.STATE_READY,
            videoWidth = 1920,
            videoHeight = 1080,
            renderedFrameCount = 0,
        )
        assertFalse(noFrame.playerReady)
        assertTrue(shouldShowMomentThumbnailFallback(remoteOffline = false, surfaceState = noFrame))

        val withFrame = momentVideoSurfaceStateFor(
            expectedMediaId = "demo",
            currentMediaId = "demo",
            playbackState = Player.STATE_READY,
            videoWidth = 1920,
            videoHeight = 1080,
            renderedFrameCount = 1,
        )
        assertTrue(withFrame.playerReady)
        assertFalse(shouldShowMomentThumbnailFallback(remoteOffline = false, surfaceState = withFrame))
    }

    private fun slideRow(index: Int): MediaInventoryEntity = MediaInventoryEntity(
        assetId = "demo_post_media_$index",
        assetKind = "post_media",
        scope = "moments",
        ownerId = "demo",
        bucket = "shorts_videos",
        serverUrl = "/api/media/slide/demo/$index",
        localPath = null,
        state = "pending",
    )

    private fun audioRow(): MediaInventoryEntity = MediaInventoryEntity(
        assetId = "demo_audio",
        assetKind = "post_audio",
        scope = "moments",
        ownerId = "demo",
        bucket = "shorts_videos",
        serverUrl = "/api/media/audio/demo",
        localPath = null,
        state = "pending",
    )

    private fun streamRow(state: String): MediaInventoryEntity = MediaInventoryEntity(
        assetId = "demo_stream",
        assetKind = "video_stream",
        scope = "moments",
        ownerId = "demo",
        bucket = "shorts_videos",
        serverUrl = "/api/media/stream/demo",
        localPath = null,
        state = state,
    )

    private fun syncAsset(
        assetId: String,
        assetKind: String,
        serverUrl: String,
    ): AndroidSyncAssetEntity = AndroidSyncAssetEntity(
        generationId = "android-sync-test",
        seq = 1L,
        assetId = assetId,
        assetKind = assetKind,
        ownerId = "demo",
        ownerKind = "tiktok_video",
        bucket = "shorts_videos",
        serverUrl = serverUrl,
        contentType = if (assetKind == "post_audio") "audio/mpeg" else "image/jpeg",
        sizeBytes = 10L,
        sha256 = "sha-$assetId",
        serverState = "ready",
        requiredReason = "retention",
        effectiveRecencyMs = 1_000L,
        state = "verified",
    )

    private fun storyItem(videoId: String, channelId: String): MomentItem =
        MomentItem(
            videoId = videoId,
            channelId = channelId,
            authorHandle = "@${channelId.removePrefix("tiktok_")}",
            description = "",
            likeCount = null,
            isLiked = false,
            isBookmarked = false,
            mediaKind = "image",
        )
}
