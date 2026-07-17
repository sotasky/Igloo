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

class FeedMediaCellDescriptorTest {

    @Test
    fun empty_or_null_input_returns_empty() {
        assertEquals(emptyList<FeedMediaCellDescriptor>(), describeFeedMediaCells(null))
        assertEquals(emptyList<FeedMediaCellDescriptor>(), describeFeedMediaCells(""))
        assertEquals(emptyList<FeedMediaCellDescriptor>(), describeFeedMediaCells("not json"))
    }

    @Test
    fun video_descriptor_is_marked_as_video() {
        val json = """[{"type":"video","url":"https://video.twimg.com/a.mp4","thumbnail_url":""}]"""
        val cells = describeFeedMediaCells(json)
        assertEquals(1, cells.size)
        assertTrue(cells[0].isVideo)
    }

    @Test
    fun multi_image_returns_one_descriptor_per_slide() {
        val json =
            """[
            {"type":"photo","url":"https://pbs.twimg.com/a.jpg","thumbnail_url":""},
            {"type":"photo","url":"https://pbs.twimg.com/b.jpg","thumbnail_url":""},
            {"type":"photo","url":"https://pbs.twimg.com/c.jpg","thumbnail_url":""}
        ]"""
        val cells = describeFeedMediaCells(json)
        assertEquals(3, cells.size)
        assertTrue(cells.none { it.isVideo })
    }

    @Test
    fun single_media_aspect_ratio_uses_url_dimensions_when_row_dimensions_are_zero() {
        val json =
            """[
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
        assertTrue(cells[0].isVideo)
        assertEquals(720f / 1280f, cells[0].aspectRatio, 0.0001f)
    }

    @Test
    fun image_without_dimensions_uses_square_full_width_default() {
        val json =
            """[
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
        val json =
            """[
            {"type":"photo","url":"https://pbs.twimg.com/media/a.jpg"},
            {"type":"photo","url":"https://pbs.twimg.com/media/b.jpg"}
        ]"""

        val items =
            buildFeedPreviewItemsByIndex(
                ownerId = "tweet-1",
                rawJson = json,
                assetRows =
                    listOf(
                        syncPostMedia(
                            assetId = "twitter_tweet_tweet-1_post_media_1",
                            mediaIndex = 1,
                            localPath = second.absolutePath,
                        ),
                        syncPostMedia(
                            assetId = "twitter_tweet_tweet-1_post_media_0",
                            mediaIndex = 0,
                            localPath = first.absolutePath,
                        ),
                    ),
                baseUrl = "https://igloo.example",
            )

        assertEquals(2, items.size)
        assertEquals(first, ((items.getValue(0) as MediaItem.Image).uri as MediaUri.Local).file)
        assertEquals(second, ((items.getValue(1) as MediaItem.Image).uri as MediaUri.Local).file)
    }

    @Test
    fun preview_items_trust_verified_current_asset_without_render_time_stat() {
        val json = """[{"type":"photo","url":"https://pbs.twimg.com/media/a.jpg"}]"""

        val items =
            buildFeedPreviewItemsByIndex(
                ownerId = "tweet-1",
                rawJson = json,
                assetRows =
                    listOf(
                        syncPostMedia(
                            assetId = "twitter_tweet_tweet-1_post_media_0",
                            localPath = "/missing/sync.jpg",
                        )
                    ),
                baseUrl = "https://igloo.example",
            )

        assertEquals(
            File("/missing/sync.jpg"),
            (((items.getValue(0) as MediaItem.Image).uri) as MediaUri.Local).file,
        )
    }

    @Test
    fun ready_asset_content_type_owns_media_classification() {
        val ownerId = "sample_tweet"
        val json = """[{"type":"photo","url":"https://pbs.twimg.com/media/a.jpg"}]"""

        val items =
            buildFeedPreviewItemsByIndex(
                ownerId = ownerId,
                rawJson = json,
                assetRows =
                    listOf(
                        syncPostMedia(
                            assetId = "twitter_tweet_${ownerId}_post_media_0",
                            localPath = "/verified/video.mp4",
                            contentType = "video/mp4",
                            ownerId = ownerId,
                        )
                    ),
                baseUrl = "https://igloo.example",
            )

        assertTrue(items.getValue(0) is MediaItem.Video)
    }

    @Test
    fun sparse_parent_and_quote_cells_open_matching_compacted_viewer_items() {
        val parentOwnerId = "sample_parent"
        val quoteOwnerId = "sample_quote"
        val json =
            """[
            {"type":"photo","url":"https://pbs.twimg.com/media/a.jpg"},
            {"type":"photo","url":"https://pbs.twimg.com/media/b.jpg"},
            {"type":"photo","url":"https://pbs.twimg.com/media/c.jpg"}
        ]"""
        val assetRows =
            listOf(
                syncPostMedia("parent-1", 1, "/verified/parent-1.jpg", ownerId = parentOwnerId),
                syncPostMedia("parent-2", 2, "/verified/parent-2.jpg", ownerId = parentOwnerId),
                syncPostMedia("quote-2", 2, "/verified/quote-2.jpg", ownerId = quoteOwnerId),
            )
        val parent =
            buildFeedMediaGridModel(
                ownerId = parentOwnerId,
                rawJson = json,
                assetRows = assetRows.filter { it.ownerId == parentOwnerId },
                baseUrl = "https://igloo.example",
            )
        val quote =
            buildFeedMediaGridModel(
                ownerId = quoteOwnerId,
                rawJson = json,
                assetRows = assetRows.filter { it.ownerId == quoteOwnerId },
                baseUrl = "https://igloo.example",
            )
        val mediaSet =
            buildFeedMediaSet(
                row =
                    feedRow(
                        FeedItemEntity(
                            tweetId = parentOwnerId,
                            mediaJson = json,
                            quoteTweetId = quoteOwnerId,
                            quoteMediaJson = json,
                        )
                    ),
                assetRows = assetRows,
                baseUrl = "https://igloo.example",
            ) ?: error("expected media set")

        assertEquals(null, feedMediaViewerIndex(parent, cellIndex = 0, mediaIndexOffset = 0))
        assertEquals(2, parent.mediaSetItemCount)
        val parentSecondIndex = feedMediaViewerIndex(parent, cellIndex = 1, mediaIndexOffset = 0)
        val parentThirdIndex = feedMediaViewerIndex(parent, cellIndex = 2, mediaIndexOffset = 0)
        val quoteThirdIndex = feedMediaViewerIndex(quote, 2, parent.mediaSetItemCount)
        assertEquals(0, parentSecondIndex)
        assertEquals(1, parentThirdIndex)
        assertEquals(2, quoteThirdIndex)
        assertEquals(
            listOf(
                File("/verified/parent-1.jpg"),
                File("/verified/parent-2.jpg"),
                File("/verified/quote-2.jpg"),
            ),
            listOf(parentSecondIndex, parentThirdIndex, quoteThirdIndex).map { index ->
                (((mediaSet.items[index ?: error("missing viewer index")] as MediaItem.Image).uri)
                        as MediaUri.Local)
                    .file
            },
        )
    }

    @Test
    fun video_media_item_uses_the_exact_owner_thumbnail_asset() {
        val ownerId = "sample_tweet"
        val thumbnail = File.createTempFile("igloo-sync-thumb", ".jpg").also { it.deleteOnExit() }
        val json = """[{"type":"video","url":"https://video.twimg.com/a.mp4"}]"""

        val items =
            buildFeedPreviewItemsByIndex(
                ownerId = ownerId,
                rawJson = json,
                assetRows =
                    listOf(
                        syncPostMedia(
                            assetId = "twitter_tweet_${ownerId}_post_media_0",
                            localPath = "/verified/video.mp4",
                            contentType = "video/mp4",
                            ownerId = ownerId,
                        ),
                        syncAsset(
                            assetId = "twitter_tweet_${ownerId}_post_thumbnail_0",
                            assetKind = "post_thumbnail",
                            mediaIndex = 0,
                            localPath = thumbnail.absolutePath,
                            ownerId = ownerId,
                            contentType = "image/jpeg",
                        ),
                    ),
                baseUrl = "https://igloo.example",
            )

        val video = items.getValue(0) as MediaItem.Video
        assertEquals(thumbnail, (video.thumbnailUri as MediaUri.Local).file)
    }

    @Test
    fun route_media_set_does_not_extend_json_media_with_stale_asset_rows() {
        val tweetId = "sample_tweet"
        val json =
            """[
            {"type":"photo","url":"https://pbs.twimg.com/media/a.jpg"},
            {"type":"photo","url":"https://pbs.twimg.com/media/b.jpg"}
        ]"""

        val set =
            buildFeedMediaSet(
                row = feedRow(FeedItemEntity(tweetId = tweetId, mediaJson = json)),
                assetRows =
                    listOf(
                        syncPostMedia("asset-0", 0, "/missing/0.jpg", ownerId = tweetId),
                        syncPostMedia("asset-1", 1, "/missing/1.jpg", ownerId = tweetId),
                        syncPostMedia("asset-2", 2, "/missing/2.jpg", ownerId = tweetId),
                        syncPostMedia("asset-3", 3, "/missing/3.jpg", ownerId = tweetId),
                    ),
                baseUrl = "https://igloo.example",
            )

        assertEquals(2, set?.items?.size)
    }

    @Test
    fun route_media_set_keeps_mixed_parent_and_quote_slides_in_order() {
        val parentId = "sample_parent"
        val quoteId = "sample_quote"
        val parentJson =
            """[
            {"type":"photo","url":"https://pbs.twimg.com/media/a.jpg"},
            {"type":"animated_gif","url":"https://video.twimg.com/media/b.gif"},
            {"type":"video","url":"https://video.twimg.com/media/c.mp4"}
        ]"""
        val quoteJson = """[{"type":"photo","url":"https://pbs.twimg.com/media/d.jpg"}]"""
        val set =
            buildFeedMediaSet(
                row =
                    feedRow(
                        FeedItemEntity(
                            tweetId = parentId,
                            mediaJson = parentJson,
                            quoteTweetId = quoteId,
                            quoteMediaJson = quoteJson,
                        )
                    ),
                assetRows =
                    listOf(
                        syncPostMedia("parent-image", 0, "/verified/a.jpg", parentId),
                        syncPostMedia(
                            "parent-gif",
                            1,
                            "/verified/b.gif",
                            parentId,
                            contentType = "image/gif",
                        ),
                        syncPostMedia(
                            "parent-video",
                            2,
                            "/verified/c.mp4",
                            parentId,
                            contentType = "video/mp4",
                        ),
                        syncPostMedia("quote-image", 0, "/verified/d.jpg", quoteId),
                    ),
                baseUrl = "https://igloo.example",
            ) ?: error("expected media set")

        assertEquals(4, set.items.size)
        assertTrue(set.items[0] is MediaItem.Image)
        assertTrue(set.items[1] is MediaItem.Gif)
        assertTrue(set.items[2] is MediaItem.Video)
        assertTrue(set.items[3] is MediaItem.Image)
    }

    private fun syncPostMedia(
        assetId: String,
        mediaIndex: Int = 0,
        localPath: String,
        ownerId: String = "tweet-1",
        contentType: String = "image/jpeg",
    ) =
        syncAsset(
            assetId = assetId,
            assetKind = "post_media",
            mediaIndex = mediaIndex,
            localPath = localPath,
            ownerId = ownerId,
            contentType = contentType,
        )

    private fun syncAsset(
        assetId: String,
        assetKind: String,
        mediaIndex: Int,
        localPath: String,
        ownerId: String = "tweet-1",
        contentType: String,
    ) =
        AndroidSyncAssetEntity(
            assetId = assetId,
            assetKind = assetKind,
            mediaIndex = mediaIndex,
            ownerId = ownerId,
            ownerKind = "tweet",
            bucket = "twitter_media",
            contentType = contentType,
            sizeBytes = 1,
            revision = 1,
            state = "ready",
            localPath = localPath,
            verifiedAtMs = 1,
        )

    private fun feedRow(item: FeedItemEntity) =
        FeedRow(
            item = item,
            channelName = null,
            channelPlatform = "twitter",
            authorHandle = "sample_author",
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
