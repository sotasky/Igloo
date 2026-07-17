package com.screwy.igloo.ui.component

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.media3.common.Player
import androidx.media3.ui.AspectRatioFrameLayout
import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.media.OwnerKind
import java.io.File
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
        assertEquals(
            MomentMediaMode.Slideshow,
            momentMediaMode(mediaKind = "slideshow", slideCount = 4),
        )
        assertEquals(
            MomentMediaMode.Slideshow,
            momentMediaMode(mediaKind = "image", slideCount = 3),
        )
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
        assertTrue(
            momentCaptionExpandedAfterPlainTextClick(expanded = false, descriptionCanExpand = true)
        )
        assertFalse(
            momentCaptionExpandedAfterPlainTextClick(expanded = true, descriptionCanExpand = true)
        )
        assertFalse(
            momentCaptionExpandedAfterPlainTextClick(expanded = false, descriptionCanExpand = false)
        )
    }

    @Test
    fun caption_background_only_appears_while_expanded() {
        assertEquals(Color.Transparent, momentCaptionBackgroundColor(expanded = false))
        assertEquals(Color.Black.copy(alpha = 0.28f), momentCaptionBackgroundColor(expanded = true))
    }

    @Test
    fun slideshow_pages_advance_every_two_seconds() {
        assertEquals(2_000L, momentSlideshowAdvanceDelayMs())
    }

    @Test
    fun moment_pager_start_uses_video_identity_before_fallback_index() {
        val initial = listOf(storyItem("older", "tiktok_a"), storyItem("active", "tiktok_b"))
        val expanded = listOf(storyItem("backfill", "tiktok_c")) + initial

        assertEquals(1, momentPagerStartIndex(initial, "active", fallbackIndex = 1))
        assertEquals(2, momentPagerStartIndex(expanded, "active", fallbackIndex = 1))
    }

    @Test
    fun consumed_start_request_does_not_reposition_a_reordered_active_video() {
        val initial = listOf(storyItem("older", "tiktok_a"), storyItem("active", "tiktok_b"))
        val reordered = listOf(storyItem("backfill", "tiktok_c")) + initial
        val request = momentPagerStartRequest(scope = "all", startVideoId = "active")!!

        assertNull(
            pendingMomentPagerStartIndex(
                items = reordered,
                startRequest = request,
                lastAppliedStartRequest = request,
            )
        )
    }

    @Test
    fun new_start_request_selects_the_requested_video_once() {
        val items = listOf(storyItem("older", "tiktok_a"), storyItem("active", "tiktok_b"))
        val request = momentPagerStartRequest(scope = "all", startVideoId = "active")!!

        assertEquals(
            1,
            pendingMomentPagerStartIndex(
                items = items,
                startRequest = request,
                lastAppliedStartRequest =
                    momentPagerStartRequest(scope = "all", startVideoId = "older"),
            )
        )
    }

    @Test
    fun start_request_reapplies_after_an_absent_request_or_playlist_scope_change() {
        val items = listOf(storyItem("older", "tiktok_a"), storyItem("active", "tiktok_b"))
        val allRequest = momentPagerStartRequest(scope = "all", startVideoId = "active")!!
        val followingRequest =
            momentPagerStartRequest(scope = "following", startVideoId = "active")!!

        assertNull(momentPagerStartRequest(scope = "following", startVideoId = null))
        assertEquals(
            1,
            pendingMomentPagerStartIndex(
                items = items,
                startRequest = allRequest,
                lastAppliedStartRequest = null,
            )
        )
        assertEquals(
            1,
            pendingMomentPagerStartIndex(
                items = items,
                startRequest = allRequest,
                lastAppliedStartRequest = followingRequest,
            )
        )
    }

    @Test
    fun repost_actions_are_available_only_for_a_repost_and_mute_only_for_an_unfollowed_author() {
        val direct = storyItem("direct", "tiktok_author")
        val repostFromUnfollowedAuthor =
            direct.copy(
                repostIntroduced = true,
                reposterChannelId = "tiktok_sample_reposter",
                isAuthorFollowed = false,
            )
        val repostFromFollowedAuthor = repostFromUnfollowedAuthor.copy(isAuthorFollowed = true)

        assertEquals(
            MomentActionAvailability(canToggleReposts = false, canToggleMute = false),
            momentActionAvailability(direct),
        )
        assertEquals(
            MomentActionAvailability(canToggleReposts = true, canToggleMute = true),
            momentActionAvailability(repostFromUnfollowedAuthor),
        )
        assertEquals(
            MomentActionAvailability(canToggleReposts = true, canToggleMute = false),
            momentActionAvailability(repostFromFollowedAuthor),
        )
    }

    @Test
    fun moment_action_labels_name_the_reposter_and_original_author() {
        val item =
            storyItem("sample_moment", "tiktok_sample_author").copy(
                authorDisplayName = "Account B",
                authorHandle = "@sample_author",
                repostIntroduced = true,
                reposterChannelId = "tiktok_sample_reposter",
                repostAuthorLabel = "Account A",
            )

        assertEquals(
            MomentActionAccountLabels(reposter = "Account A", author = "@sample_author"),
            momentActionAccountLabels(item),
        )
    }

    @Test
    fun moment_action_labels_keep_platform_handles_and_fall_back_to_display_names() {
        val tiktok =
            storyItem("sample_tiktok_moment", "tiktok_sample_author").copy(
                authorDisplayName = "Account B",
                authorHandle = "",
                reposterChannelId = "tiktok_sample_reposter",
            )
        val instagram =
            storyItem("sample_instagram_moment", "instagram_sample_author").copy(
                authorHandle = "@sample_author",
                reposterChannelId = "instagram_sample_reposter",
            )
        val x =
            storyItem("sample_x_moment", "twitter_sample_author").copy(
                authorHandle = "sample_author",
                reposterChannelId = "twitter_sample_reposter",
            )

        assertEquals(
            MomentActionAccountLabels(reposter = "@sample_reposter", author = "Account B"),
            momentActionAccountLabels(tiktok),
        )
        assertEquals(
            MomentActionAccountLabels(reposter = "@sample_reposter", author = "@sample_author"),
            momentActionAccountLabels(instagram),
        )
        assertEquals(
            MomentActionAccountLabels(reposter = "@sample_reposter", author = "@sample_author"),
            momentActionAccountLabels(x),
        )
    }

    @Test
    fun repost_author_click_range_covers_only_the_reposter_name() {
        assertEquals(0..7, repostAuthorRange("Sample A reposted", "Sample A"))
        assertNull(repostAuthorRange("Sample A reposted", "Sample B"))
    }

    @Test
    fun story_progress_window_counts_only_current_profile_group() {
        val items =
            listOf(
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
        val items =
            listOf(
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
        val items =
            listOf(
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
    fun moment_stream_load_key_changes_when_uri_changes() {
        assertEquals(
            "remote:demo:https://igloo.example/api/media/stream/demo",
            momentStreamLoadKey(
                "demo",
                MediaUri.Remote("https://igloo.example/api/media/stream/demo"),
            ),
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
        assertEquals(
            "remote",
            MediaUri.Remote("https://igloo.example/video.mp4").momentsDebugKind(),
        )
        assertEquals("missing", MediaUri.Missing.momentsDebugKind())
        assertEquals("ready", Player.STATE_READY.momentPlayerStateDebugName())
        assertEquals("ended", Player.STATE_ENDED.momentPlayerStateDebugName())
    }

    @Test
    fun moment_debug_hash_is_stable_without_logging_raw_ids() {
        assertEquals("", momentDebugHash(null))
        assertEquals("", momentDebugHash(""))
        assertEquals(momentDebugHash("sample-video-id"), momentDebugHash("sample-video-id"))
        assertFalse(momentDebugHash("sample-video-id").contains("sample-video-id"))
    }

    @Test
    fun moment_player_media_item_carries_video_id_as_media_id() {
        val mediaItem =
            momentPlayerMediaItem(
                videoId = "demo",
                streamUri = MediaUri.Remote("https://igloo.example/api/media/stream/demo"),
            )

        assertEquals("demo", mediaItem?.mediaId)
        assertNull(momentPlayerMediaItem("demo", MediaUri.Missing))
    }

    @Test
    fun moments_video_surface_fills_only_vertical_videos() {
        assertEquals(
            AspectRatioFrameLayout.RESIZE_MODE_ZOOM,
            momentsVideoResizeMode(width = 720, height = 1280),
        )
        assertEquals(
            AspectRatioFrameLayout.RESIZE_MODE_FIT,
            momentsVideoResizeMode(width = 1920, height = 1080),
        )
        assertEquals(
            AspectRatioFrameLayout.RESIZE_MODE_FIT,
            momentsVideoResizeMode(width = 1000, height = 1000),
        )
        assertEquals(
            AspectRatioFrameLayout.RESIZE_MODE_ZOOM,
            momentsVideoResizeMode(width = 0, height = 0),
        )
    }

    @Test
    fun moment_video_surface_state_rejects_recycled_player_media() {
        val state =
            momentVideoSurfaceStateFor(
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
    fun resolve_moment_slide_uris_prefers_verified_sync_slide_rows() {
        val first =
            File.createTempFile("igloo-moment-sync-first", ".jpg").also { it.deleteOnExit() }
        val second =
            File.createTempFile("igloo-moment-sync-second", ".jpg").also { it.deleteOnExit() }
        val third =
            File.createTempFile("igloo-moment-sync-third", ".jpg").also { it.deleteOnExit() }
        val uris =
            resolveMomentSlideUris(
                baseUrl = "https://igloo.example",
                syncRows =
                    listOf(
                        syncAsset(
                            assetId = "tiktok_tiktok_video_demo_post_media_2",
                            assetKind = "post_media",
                            mediaIndex = 2,
                            localPath = third.absolutePath,
                        ),
                        syncAsset(
                            assetId = "tiktok_tiktok_video_demo_post_media",
                            assetKind = "post_media",
                            mediaIndex = 0,
                            localPath = first.absolutePath,
                        ),
                        syncAsset(
                            assetId = "tiktok_tiktok_video_demo_post_media_1",
                            assetKind = "post_media",
                            mediaIndex = 1,
                            localPath = second.absolutePath,
                        ),
                    ),
            )

        assertEquals(
            listOf(MediaUri.Local(first), MediaUri.Local(second), MediaUri.Local(third)),
            uris,
        )
    }

    @Test
    fun resolve_moment_slide_media_keeps_verified_sync_video_rows() {
        val first =
            File.createTempFile("igloo-moment-sync-first", ".jpg").also { it.deleteOnExit() }
        val second =
            File.createTempFile("igloo-moment-sync-second", ".mp4").also { it.deleteOnExit() }
        val third =
            File.createTempFile("igloo-moment-sync-third", ".mp4").also { it.deleteOnExit() }
        val slides =
            resolveMomentSlideMedia(
                baseUrl = "https://igloo.example",
                syncRows =
                    listOf(
                        syncAsset(
                            assetId = "tweet_demo_post_media_2",
                            assetKind = "post_media",
                            mediaIndex = 2,
                            localPath = third.absolutePath,
                            contentType = "video/mp4",
                        ),
                        syncAsset(
                            assetId = "tweet_demo_post_media",
                            assetKind = "post_media",
                            mediaIndex = 0,
                            localPath = first.absolutePath,
                            contentType = "image/jpeg",
                        ),
                        syncAsset(
                            assetId = "tweet_demo_post_media_1",
                            assetKind = "post_media",
                            mediaIndex = 1,
                            localPath = second.absolutePath,
                            contentType = "video/mp4",
                        ),
                    ),
            )

        assertEquals(
            listOf(MediaUri.Local(first), MediaUri.Local(second), MediaUri.Local(third)),
            slides.map { it.uri },
        )
        assertEquals(
            listOf(MomentSlideKind.Image, MomentSlideKind.Video, MomentSlideKind.Video),
            slides.map { it.kind },
        )
    }

    @Test
    fun resolve_moment_slide_media_uses_ready_sync_rows_as_remote_slides() {
        val slides =
            resolveMomentSlideMedia(
                baseUrl = "https://igloo.example",
                syncRows =
                    listOf(
                        syncAsset(
                            assetId = "tweet_demo_post_media_2",
                            assetKind = "post_media",
                            mediaIndex = 2,
                            localPath = null,
                            contentType = "video/mp4",
                            state = "desired",
                        ),
                        syncAsset(
                            assetId = "tweet_demo_post_media",
                            assetKind = "post_media",
                            mediaIndex = 0,
                            localPath = null,
                            contentType = "image/jpeg",
                            state = "desired",
                        ),
                        syncAsset(
                            assetId = "tweet_demo_post_media_1",
                            assetKind = "post_media",
                            mediaIndex = 1,
                            localPath = null,
                            contentType = "video/mp4",
                            state = "desired",
                        ),
                    ),
            )

        assertEquals(
            listOf(
                MediaUri.Remote(
                    "https://igloo.example/api/android/sync/assets/tweet_demo_post_media/file?revision=1"
                ),
                MediaUri.Remote(
                    "https://igloo.example/api/android/sync/assets/tweet_demo_post_media_1/file?revision=1"
                ),
                MediaUri.Remote(
                    "https://igloo.example/api/android/sync/assets/tweet_demo_post_media_2/file?revision=1"
                ),
            ),
            slides.map { it.uri },
        )
        assertEquals(
            listOf(MomentSlideKind.Image, MomentSlideKind.Video, MomentSlideKind.Video),
            slides.map { it.kind },
        )
    }

    @Test
    fun resolve_moment_slide_media_uses_exact_kind_and_content_type() {
        val slides =
            resolveMomentSlideMedia(
                baseUrl = "https://igloo.example",
                syncRows =
                    listOf(
                        syncAsset(
                            assetId = "demo_unknown_post_media",
                            assetKind = "post_media",
                            contentType = "application/octet-stream",
                            state = "desired",
                        ),
                        syncAsset(
                            assetId = "demo_wrong_kind",
                            assetKind = "post_audio",
                            contentType = "video/mp4",
                            state = "desired",
                        ),
                        syncAsset(
                            assetId = "demo_image_post_media",
                            assetKind = "post_media",
                            contentType = "image/jpeg",
                            state = "desired",
                        ),
                    ),
            )

        assertEquals(1, slides.size)
        assertEquals(MomentSlideKind.Image, slides.single().kind)
        assertEquals(
            MediaUri.Remote(
                "https://igloo.example/api/android/sync/assets/demo_image_post_media/file?revision=1"
            ),
            slides.single().uri,
        )
    }

    @Test
    fun resolve_moment_audio_uri_prefers_verified_sync_audio_row() {
        val audio =
            File.createTempFile("igloo-moment-sync-audio", ".mp3").also { it.deleteOnExit() }
        val uri =
            resolveMomentAudioUri(
                baseUrl = "https://igloo.example",
                syncRows =
                    listOf(
                        syncAsset(
                            assetId = "demo_unsupported_audio_kind",
                            assetKind = "audio",
                            localPath = audio.absolutePath,
                            contentType = "audio/mpeg",
                        ),
                        syncAsset(
                            assetId = "demo_inferred_audio_endpoint",
                            assetKind = "post_media",
                            localPath = audio.absolutePath,
                            contentType = "audio/mpeg",
                        ),
                        syncAsset(
                            assetId = "demo_wrong_audio_content_type",
                            assetKind = "post_audio",
                            localPath = audio.absolutePath,
                            contentType = "application/octet-stream",
                        ),
                        syncAsset(
                            assetId = "demo_post_audio",
                            assetKind = "post_audio",
                            localPath = audio.absolutePath,
                        ),
                    ),
            )

        assertEquals(MediaUri.Local(audio), uri)
    }

    @Test
    fun should_play_current_page_even_while_pager_is_moving() {
        assertTrue(shouldPlayMomentPage(isCurrentPage = true, isScrollInProgress = false))
        assertTrue(shouldPlayMomentPage(isCurrentPage = true, isScrollInProgress = true))
        assertFalse(shouldPlayMomentPage(isCurrentPage = false, isScrollInProgress = false))
    }

    @Test
    fun auto_swipe_end_advance_wraps_to_start() {
        assertEquals(
            3,
            nextMomentPageForAutoSwipe(currentPage = 2, lastIndex = 5, autoSwipeEnabled = true),
        )
        assertEquals(
            0,
            nextMomentPageForAutoSwipe(currentPage = 5, lastIndex = 5, autoSwipeEnabled = true),
        )
        assertNull(
            nextMomentPageForAutoSwipe(currentPage = 2, lastIndex = 5, autoSwipeEnabled = false)
        )
    }

    @Test
    fun first_frame_stops_showing_thumbnail_fallback_for_any_orientation() {
        assertTrue(isWideMomentVideo(width = 1920, height = 1080))
        assertTrue(isVerticalMomentVideo(width = 720, height = 1280))
        assertFalse(isWideMomentVideo(width = 720, height = 1280))

        assertFalse(
            shouldShowMomentThumbnailFallback(
                remoteOffline = false,
                surfaceState =
                    MomentVideoSurfaceState(
                        playerReady = true,
                        isWide = false,
                        hasExpectedMedia = true,
                        renderedFirstFrame = true,
                    ),
            )
        )
        assertTrue(
            shouldShowMomentVideoSurface(
                MomentVideoSurfaceState(hasExpectedMedia = true, renderedFirstFrame = true)
            )
        )
        assertEquals(
            1f,
            momentVideoSurfaceAlpha(
                MomentVideoSurfaceState(hasExpectedMedia = true, renderedFirstFrame = true)
            ),
        )
        assertTrue(
            shouldShowMomentThumbnailFallback(
                remoteOffline = false,
                surfaceState =
                    MomentVideoSurfaceState(
                        playerReady = false,
                        isWide = false,
                        hasExpectedMedia = true,
                        renderedFirstFrame = false,
                    ),
            )
        )
        assertFalse(
            shouldShowMomentVideoSurface(
                MomentVideoSurfaceState(hasExpectedMedia = true, renderedFirstFrame = false)
            )
        )
        assertEquals(
            0f,
            momentVideoSurfaceAlpha(
                MomentVideoSurfaceState(hasExpectedMedia = true, renderedFirstFrame = false)
            ),
        )
        assertTrue(
            shouldShowMomentThumbnailFallback(
                remoteOffline = true,
                surfaceState =
                    MomentVideoSurfaceState(
                        playerReady = true,
                        isWide = false,
                        hasExpectedMedia = true,
                        renderedFirstFrame = true,
                    ),
            )
        )
    }

    @Test
    fun video_fallback_poster_uses_crop_to_match_short_video_surface() {
        assertEquals(ContentScale.Fit, momentFitWidthContentScale())
        assertEquals(ContentScale.Crop, momentVideoFallbackContentScale())
    }

    @Test
    fun video_fallback_poster_draws_above_the_player_surface_until_first_frame() {
        assertTrue(momentVideoFallbackZIndex() > momentVideoSurfaceZIndex())
    }

    @Test
    fun loaded_inactive_shared_page_suppresses_fallback_while_pager_is_moving() {
        assertFalse(
            shouldShowMomentVideoFallbackLayer(
                fallback = true,
                sharedPlayer = true,
                isActive = false,
                pagerScrolling = true,
                hasLoadedMedia = true,
            )
        )
        assertTrue(
            shouldShowMomentVideoFallbackLayer(
                fallback = true,
                sharedPlayer = true,
                isActive = true,
                pagerScrolling = true,
                hasLoadedMedia = true,
            )
        )
        assertTrue(
            shouldShowMomentVideoFallbackLayer(
                fallback = true,
                sharedPlayer = false,
                isActive = false,
                pagerScrolling = true,
                hasLoadedMedia = true,
            )
        )
        assertTrue(
            shouldShowMomentVideoFallbackLayer(
                fallback = true,
                sharedPlayer = true,
                isActive = false,
                pagerScrolling = false,
                hasLoadedMedia = true,
            )
        )
        assertTrue(
            shouldShowMomentVideoFallbackLayer(
                fallback = true,
                sharedPlayer = true,
                isActive = false,
                pagerScrolling = true,
                hasLoadedMedia = false,
            )
        )
    }

    @Test
    fun playback_stream_uri_does_not_swap_for_visible_page_during_swipe() {
        assertFalse(
            shouldAdoptMomentPlaybackStreamUri(
                currentStreamUri = MediaUri.Remote("https://igloo.example/api/media/stream/v1"),
                isActive = false,
                pagerScrolling = true,
            )
        )
        assertFalse(
            shouldAdoptMomentPlaybackStreamUri(
                currentStreamUri = MediaUri.Local(File("/tmp/v1.mp4")),
                isActive = false,
                pagerScrolling = true,
            )
        )
        assertFalse(
            shouldAdoptMomentPlaybackStreamUri(
                currentStreamUri = MediaUri.Remote("https://igloo.example/api/media/stream/v1"),
                isActive = true,
                pagerScrolling = false,
            )
        )
        assertFalse(
            shouldAdoptMomentPlaybackStreamUri(
                currentStreamUri = MediaUri.Remote("https://igloo.example/api/media/stream/v1"),
                isActive = true,
                pagerScrolling = true,
            )
        )
        assertTrue(
            shouldAdoptMomentPlaybackStreamUri(
                currentStreamUri = MediaUri.Remote("https://igloo.example/api/media/stream/v1"),
                isActive = false,
                pagerScrolling = false,
            )
        )
        assertTrue(
            shouldAdoptMomentPlaybackStreamUri(
                currentStreamUri = MediaUri.Missing,
                isActive = true,
                pagerScrolling = true,
            )
        )
    }

    @Test
    fun ready_state_requires_a_rendered_frame_before_hiding_thumbnail() {
        val noFrame =
            momentVideoSurfaceStateFor(
                expectedMediaId = "demo",
                currentMediaId = "demo",
                playbackState = Player.STATE_READY,
                videoWidth = 1920,
                videoHeight = 1080,
                renderedFrameCount = 0,
            )
        assertFalse(noFrame.playerReady)
        assertTrue(shouldShowMomentThumbnailFallback(remoteOffline = false, surfaceState = noFrame))

        val withFrame =
            momentVideoSurfaceStateFor(
                expectedMediaId = "demo",
                currentMediaId = "demo",
                playbackState = Player.STATE_READY,
                videoWidth = 1920,
                videoHeight = 1080,
                renderedFrameCount = 1,
            )
        assertTrue(withFrame.playerReady)
        assertFalse(
            shouldShowMomentThumbnailFallback(remoteOffline = false, surfaceState = withFrame)
        )
    }

    @Test
    fun surface_state_discards_stale_frame_metadata_when_player_media_changes() {
        val staleFrame =
            momentVideoSurfaceStateFor(
                expectedMediaId = "visible",
                currentMediaId = "previous",
                playbackState = Player.STATE_READY,
                videoWidth = 720,
                videoHeight = 1280,
                renderedFrameCount = 1,
                playerIsPlaying = true,
                playerPositionMs = 3_000L,
            )

        assertFalse(staleFrame.hasExpectedMedia)
        assertFalse(staleFrame.renderedFirstFrame)
        assertEquals(0, staleFrame.renderedFrameCount)
        assertEquals(0, staleFrame.videoWidth)
        assertEquals(0, staleFrame.videoHeight)
        assertEquals(0L, staleFrame.playerPositionMs)
        assertFalse(shouldShowMomentVideoSurface(staleFrame))
    }

    @Test
    fun moments_video_progress_bar_only_mounts_for_active_ready_page() {
        val ready = MomentVideoSurfaceState(hasExpectedMedia = true, renderedFirstFrame = true)
        val streamUri = MediaUri.Remote("https://igloo.example/api/media/stream/demo")

        assertTrue(
            shouldShowMomentsVideoProgressBar(
                isActive = true,
                shouldPrepare = true,
                streamUri = streamUri,
                remoteOffline = false,
                surfaceState = ready,
            )
        )
        assertFalse(
            shouldShowMomentsVideoProgressBar(
                isActive = false,
                shouldPrepare = true,
                streamUri = streamUri,
                remoteOffline = false,
                surfaceState = ready,
            )
        )
        assertFalse(
            shouldShowMomentsVideoProgressBar(
                isActive = true,
                shouldPrepare = true,
                streamUri = MediaUri.Missing,
                remoteOffline = false,
                surfaceState = ready,
            )
        )
    }

    @Test
    fun shared_moments_player_is_owned_only_by_active_page() {
        val streamUri = MediaUri.Remote("https://igloo.example/api/media/stream/demo")

        assertTrue(
            shouldPrepareMomentVideoPlayer(
                isActive = true,
                shouldPrepare = true,
                sharedPlayer = true,
            )
        )
        assertFalse(
            shouldPrepareMomentVideoPlayer(
                isActive = false,
                shouldPrepare = true,
                sharedPlayer = true,
            )
        )
        assertTrue(
            shouldPrepareMomentVideoPlayer(
                isActive = false,
                shouldPrepare = true,
                sharedPlayer = false,
            )
        )
        assertTrue(
            shouldMountMomentVideoSurface(
                isActive = true,
                shouldPrepare = true,
                sharedPlayer = true,
                streamUri = streamUri,
                remoteOffline = false,
            )
        )
        assertFalse(
            shouldMountMomentVideoSurface(
                isActive = false,
                shouldPrepare = true,
                sharedPlayer = true,
                streamUri = streamUri,
                remoteOffline = false,
            )
        )
    }

    @Test
    fun inactive_prepared_moment_rewinds_only_after_a_different_video_settles() {
        assertFalse(
            shouldRewindInactiveMomentPlayback(
                currentMediaId = "demo",
                expectedVideoId = "demo",
                settledVideoId = "demo",
                loadedVideoId = "demo",
                mediaItemCount = 1,
                currentPositionMs = 1_250L,
            )
        )
        assertTrue(
            shouldRewindInactiveMomentPlayback(
                currentMediaId = "demo",
                expectedVideoId = "demo",
                settledVideoId = "next",
                loadedVideoId = "demo",
                mediaItemCount = 1,
                currentPositionMs = 1_250L,
            )
        )
        assertFalse(
            shouldRewindInactiveMomentPlayback(
                currentMediaId = "demo",
                expectedVideoId = "demo",
                settledVideoId = "next",
                loadedVideoId = "demo",
                mediaItemCount = 1,
                currentPositionMs = 0L,
            )
        )
        assertFalse(
            shouldRewindInactiveMomentPlayback(
                currentMediaId = "other",
                expectedVideoId = "demo",
                settledVideoId = "next",
                loadedVideoId = "demo",
                mediaItemCount = 1,
                currentPositionMs = 1_250L,
            )
        )
        assertFalse(
            shouldRewindInactiveMomentPlayback(
                currentMediaId = "demo",
                expectedVideoId = "demo",
                settledVideoId = "next",
                loadedVideoId = "other",
                mediaItemCount = 1,
                currentPositionMs = 1_250L,
            )
        )
    }

    @Test
    fun ended_loop_keeps_rendered_frame_until_restart() {
        assertFalse(shouldClearMomentRenderedFrame(Player.STATE_ENDED))
        assertTrue(shouldClearMomentRenderedFrame(Player.STATE_IDLE))

        val ended =
            momentVideoSurfaceStateFor(
                expectedMediaId = "demo",
                currentMediaId = "demo",
                playbackState = Player.STATE_ENDED,
                videoWidth = 720,
                videoHeight = 1280,
                renderedFrameCount = 1,
            )

        assertFalse(ended.playerReady)
        assertTrue(ended.renderedFirstFrame)
        assertFalse(shouldShowMomentThumbnailFallback(remoteOffline = false, surfaceState = ended))
        assertEquals(
            AspectRatioFrameLayout.RESIZE_MODE_ZOOM,
            momentsVideoResizeMode(ended.videoWidth, ended.videoHeight),
        )
    }

    private fun syncAsset(
        assetId: String,
        assetKind: String,
        mediaIndex: Int = 0,
        localPath: String? = null,
        contentType: String = if (assetKind == "post_audio") "audio/mpeg" else "image/jpeg",
        state: String = "verified",
    ): AndroidSyncAssetEntity =
        AndroidSyncAssetEntity(
            assetId = assetId,
            assetKind = assetKind,
            mediaIndex = mediaIndex,
            ownerId = "demo",
            ownerKind = "tiktok_video",
            bucket = "shorts_videos",
            contentType = contentType,
            sizeBytes = 10L,
            revision = 1,
            state = if (state == "verified" || state == "desired") "ready" else state,
            localPath = localPath,
            verifiedAtMs = localPath?.let { 1_000L },
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
            ownerKind = OwnerKind.TikTokVideo,
            mediaKind = "image",
        )
}
