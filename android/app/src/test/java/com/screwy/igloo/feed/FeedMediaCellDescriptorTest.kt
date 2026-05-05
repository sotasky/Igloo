package com.screwy.igloo.feed

import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.data.entity.FeedRow
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.ui.component.MediaItem
import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Pure tests for [describeFeedMediaCells]. Covers the production shapes observed
 * in `feed_items.media_json` / `feed_items.quote_media_json` (the two keys the
 * parser handles):
 *   - `"thumbnail_url": "…"` (modern main-post shape)
 *   - `"thumbnail": ""` (legacy quote-post shape, blank)
 *   - video/gif descriptors whose `url` is an mp4 stream that Coil can't decode.
 */
class FeedMediaCellDescriptorTest {

    @Test
    fun empty_or_null_input_returns_empty() {
        assertEquals(emptyList<FeedMediaCellDescriptor>(), describeFeedMediaCells(null))
        assertEquals(emptyList<FeedMediaCellDescriptor>(), describeFeedMediaCells(""))
        assertEquals(emptyList<FeedMediaCellDescriptor>(), describeFeedMediaCells("not json"))
    }

    @Test
    fun photo_with_thumbnail_url_uses_thumbnail() {
        val json = """[{"type":"photo","url":"https://pbs.twimg.com/media/a.jpg","thumbnail_url":"https://pbs.twimg.com/media/a-thumb.jpg"}]"""
        val cells = describeFeedMediaCells(json)
        assertEquals(1, cells.size)
        assertEquals("https://pbs.twimg.com/media/a-thumb.jpg", cells[0].displayUrl)
        assertEquals(false, cells[0].isVideo)
    }

    @Test
    fun photo_with_blank_thumbnail_falls_through_to_url() {
        // Quote-post shape from production: `thumbnail: ""`
        val json = """[{"type":"photo","url":"https://pbs.twimg.com/media/a.jpg","thumbnail":""}]"""
        val cells = describeFeedMediaCells(json)
        assertEquals(1, cells.size)
        assertEquals("https://pbs.twimg.com/media/a.jpg", cells[0].displayUrl)
    }

    @Test
    fun video_descriptor_without_thumbnail_returns_blank_display_url() {
        // A video descriptor whose `url` is an mp4 stream must not be rendered as
        // an image; callers render a play badge over the empty cell instead.
        val json = """[{"type":"video","url":"https://video.twimg.com/a.mp4","thumbnail_url":""}]"""
        val cells = describeFeedMediaCells(json)
        assertEquals(1, cells.size)
        assertTrue(cells[0].isVideo)
        assertEquals("", cells[0].displayUrl)
    }

    @Test
    fun video_descriptor_uses_thumbnail_when_present() {
        val json = """[{"type":"video","url":"https://video.twimg.com/a.mp4","thumbnail_url":"https://pbs.twimg.com/amplify/a.jpg"}]"""
        val cells = describeFeedMediaCells(json)
        assertEquals(1, cells.size)
        assertTrue(cells[0].isVideo)
        assertEquals("https://pbs.twimg.com/amplify/a.jpg", cells[0].displayUrl)
        assertEquals("https://video.twimg.com/a.mp4", cells[0].streamUrl)
        assertEquals("https://pbs.twimg.com/amplify/a.jpg", cells[0].posterUrl)
    }

    @Test
    fun multi_image_returns_one_descriptor_per_slide() {
        val json = """[
            {"type":"photo","url":"https://pbs.twimg.com/a.jpg","thumbnail_url":""},
            {"type":"photo","url":"https://pbs.twimg.com/b.jpg","thumbnail_url":""},
            {"type":"photo","url":"https://pbs.twimg.com/c.jpg","thumbnail_url":""}
        ]"""
        val cells = describeFeedMediaCells(json)
        assertEquals(3, cells.size)
        assertEquals("https://pbs.twimg.com/a.jpg", cells[0].displayUrl)
        assertEquals("https://pbs.twimg.com/c.jpg", cells[2].displayUrl)
    }

    @Test
    fun modern_stream_url_and_preview_url_shape_is_supported() {
        val json = """[
            {
                "kind":"video",
                "stream_url":"https://video.twimg.com/ext_tw_video/1/pu/vid/avc1/720x1280/a.mp4",
                "preview_url":"https://pbs.twimg.com/ext_tw_video_thumb/1/pu/img/a.jpg"
            }
        ]"""
        val cells = describeFeedMediaCells(json)
        assertEquals(1, cells.size)
        assertTrue(cells[0].isVideo)
        assertEquals("https://pbs.twimg.com/ext_tw_video_thumb/1/pu/img/a.jpg", cells[0].displayUrl)
        assertEquals("https://video.twimg.com/ext_tw_video/1/pu/vid/avc1/720x1280/a.mp4", cells[0].streamUrl)
        assertEquals("https://pbs.twimg.com/ext_tw_video_thumb/1/pu/img/a.jpg", cells[0].posterUrl)
    }

    @Test
    fun single_media_aspect_ratio_uses_url_dimensions_when_row_dimensions_are_zero() {
        val json = """[
            {
                "kind":"video",
                "stream_url":"https://video.twimg.com/ext_tw_video/1/pu/vid/avc1/720x1280/a.mp4",
                "preview_url":"https://pbs.twimg.com/ext_tw_video_thumb/1/pu/img/a.jpg",
                "width":0,
                "height":0
            }
        ]"""
        val cells = describeFeedMediaCells(json)
        assertEquals(1, cells.size)
        assertEquals(720f / 1280f, cells[0].aspectRatio, 0.0001f)
    }

    @Test
    fun image_without_dimensions_uses_square_full_width_default() {
        val json = """[
            {
                "type":"photo",
                "url":"https://pbs.twimg.com/media/example?format=jpg&name=orig",
                "thumbnail_url":"",
                "width":0,
                "height":0
            }
        ]"""
        val cells = describeFeedMediaCells(json)
        assertEquals(1, cells.size)
        assertEquals(FeedUnknownMediaAspectRatio, cells[0].aspectRatio, 0.0001f)
        assertEquals(false, cells[0].aspectRatioKnown)
    }

    @Test
    fun preview_items_prefer_verified_sync_media_for_each_slide() {
        val first = File.createTempFile("igloo-sync-first", ".jpg").also { it.deleteOnExit() }
        val second = File.createTempFile("igloo-sync-second", ".jpg").also { it.deleteOnExit() }
        val json = """[
            {"type":"photo","url":"https://pbs.twimg.com/media/a.jpg"},
            {"type":"photo","url":"https://pbs.twimg.com/media/b.jpg"}
        ]"""

        val items = buildFeedPreviewItems(
            ownerId = "tweet-1",
            rawJson = json,
            inventoryRows = emptyList(),
            baseUrl = "https://igloo.example",
            syncAssetRows = listOf(
                syncPostMedia(assetId = "twitter_tweet_tweet-1_post_media_1", localPath = second.absolutePath),
                syncPostMedia(assetId = "twitter_tweet_tweet-1_post_media_0", localPath = first.absolutePath),
            ),
        )

        assertEquals(2, items.size)
        assertEquals(first, ((items[0] as MediaItem.Image).uri as MediaUri.Local).file)
        assertEquals(second, ((items[1] as MediaItem.Image).uri as MediaUri.Local).file)
    }

    @Test
    fun preview_items_use_sync_server_url_before_public_descriptor_url_when_file_is_missing() {
        val json = """[{"type":"photo","url":"https://pbs.twimg.com/media/a.jpg"}]"""

        val items = buildFeedPreviewItems(
            ownerId = "tweet-1",
            rawJson = json,
            inventoryRows = emptyList(),
            baseUrl = "https://igloo.example",
            syncAssetRows = listOf(
                syncPostMedia(
                    assetId = "twitter_tweet_tweet-1_post_media_0",
                    localPath = "/missing/sync.jpg",
                    serverUrl = "/api/android/sync/assets/twitter_tweet_tweet-1_post_media_0",
                ),
            ),
        )

        assertEquals(
            "https://igloo.example/api/android/sync/assets/twitter_tweet_tweet-1_post_media_0",
            (((items.single() as MediaItem.Image).uri) as MediaUri.Remote).url,
        )
    }

    @Test
    fun warm_media_set_uses_ready_grid_model_without_waiting_for_route_db_load() {
        val cached = File.createTempFile("igloo-warm-feed", ".jpg").also { it.deleteOnExit() }
        val json = """[{"type":"photo","url":"https://pbs.twimg.com/media/a.jpg"}]"""
        val model = FeedMediaGridModel(
            ownerId = "tweet-1",
            inventoryLoaded = true,
            cells = listOf(
                FeedMediaCellModel(
                    descriptor = describeFeedMediaCells(json).single(),
                    previewItem = MediaItem.Image(MediaUri.Local(cached), aspectRatio = 1f),
                ),
            ),
        )
        val set = buildWarmFeedMediaSet(
            row = feedRow(
                FeedItemEntity(
                    tweetId = "tweet-1",
                    authorHandle = "alice",
                    authorDisplayName = "Alice",
                    bodyText = "hello",
                    mediaJson = json,
                    channelId = "twitter_alice",
                ),
            ),
            mediaModels = mapOf("tweet-1" to model),
        )

        assertEquals("Alice", set?.authorDisplayName)
        assertEquals("hello", set?.bodyText)
        assertEquals(1, set?.parentMediaCount)
        assertEquals(cached, ((set?.items?.single() as MediaItem.Image).uri as MediaUri.Local).file)
    }

    @Test
    fun feed_media_open_snapshot_prefers_visible_cell_model_over_route_map() {
        val cached = File.createTempFile("igloo-visible-feed", ".jpg").also { it.deleteOnExit() }
        val json = """[{"type":"photo","url":"https://pbs.twimg.com/media/a.jpg"}]"""
        val staleModel = FeedMediaGridModel(
            ownerId = "tweet-1",
            inventoryLoaded = false,
            cells = listOf(
                FeedMediaCellModel(
                    descriptor = describeFeedMediaCells(json).single(),
                    previewItem = MediaItem.Image(MediaUri.Remote("https://stale.example/a.jpg"), aspectRatio = 1f),
                ),
            ),
        )
        val visibleModel = FeedMediaGridModel(
            ownerId = "tweet-1",
            inventoryLoaded = true,
            cells = listOf(
                FeedMediaCellModel(
                    descriptor = describeFeedMediaCells(json).single(),
                    previewItem = MediaItem.Image(MediaUri.Local(cached), aspectRatio = 1f),
                ),
            ),
        )

        val snapshot = buildFeedMediaOpenSnapshot(
            row = feedRow(
                FeedItemEntity(
                    tweetId = "tweet-1",
                    authorHandle = "alice",
                    mediaJson = json,
                ),
            ),
            mediaIndex = 0,
            mediaModels = mapOf("tweet-1" to staleModel),
            visibleMediaModel = visibleModel,
        )

        assertEquals(MediaUri.Local(cached), snapshot.posterUri)
        assertEquals(cached, ((snapshot.mediaSet?.items?.single() as MediaItem.Image).uri as MediaUri.Local).file)
    }

    private fun syncPostMedia(
        assetId: String,
        localPath: String,
        serverUrl: String = "/api/android/sync/assets/$assetId",
    ) = AndroidSyncAssetEntity(
        generationId = "android-sync-test",
        seq = 1,
        assetId = assetId,
        assetKind = "post_media",
        ownerId = "tweet-1",
        ownerKind = "tweet",
        bucket = "twitter_media",
        serverUrl = serverUrl,
        contentType = "image/jpeg",
        sizeBytes = 1,
        serverState = "ready",
        state = "verified",
        localPath = localPath,
        fileSize = 1,
        verifiedAtMs = 1,
    )

    private fun feedRow(item: FeedItemEntity) = FeedRow(
        item = item,
        channelName = null,
        channelAvatarUrl = null,
        channelPlatform = "twitter",
        isLiked = 0,
        likedAt = null,
        isBookmarked = 0,
        bookmarkCategoryId = null,
        bookmarkCustomTitle = null,
        bookmarkedAt = null,
        channelIsFollowed = 0,
        channelIsStarred = 0,
    )
}
