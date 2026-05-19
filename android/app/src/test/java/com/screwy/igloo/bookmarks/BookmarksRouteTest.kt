package com.screwy.igloo.bookmarks

import com.screwy.igloo.data.entity.BookmarkEntity
import com.screwy.igloo.data.entity.BookmarkItem
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.data.entity.VideoEntity
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.media.OwnerKind
import com.screwy.igloo.ui.component.MediaType
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class BookmarksRouteTest {

    @Test
    fun video_rows_open_in_moments_overlay() {
        assertTrue(opensBookmarkInMomentsOverlay(videoBookmark()))
    }

    @Test
    fun tweet_rows_with_parent_media_open_in_moments_overlay() {
        assertTrue(
            opensBookmarkInMomentsOverlay(
                tweetBookmark(mediaJson = """[{"type":"video"}]"""),
            ),
        )
    }

    @Test
    fun tweet_rows_with_quote_media_open_in_moments_overlay() {
        assertTrue(
            opensBookmarkInMomentsOverlay(
                tweetBookmark(quoteMediaJson = """[{"type":"image"}]"""),
            ),
        )
    }

    @Test
    fun text_only_tweets_stay_out_of_moments_overlay() {
        assertFalse(opensBookmarkInMomentsOverlay(tweetBookmark()))
    }

    @Test
    fun tweet_mapping_keeps_author_and_builds_synthetic_channel_id_when_missing() {
        val playerItem = toBookmarkMomentItem(
            tweetBookmark(
                tweetId = "tweet_42",
                authorHandle = "author42",
                channelId = null,
                mediaJson = """[{"type":"image"}]""",
                bodyText = "first line\nsecond line",
                likes = 123L,
            ),
        )

        assertEquals("tweet_42", playerItem.videoId)
        assertEquals("twitter_author42", playerItem.channelId)
        assertEquals("@author42", playerItem.authorHandle)
        assertEquals("Author 42", playerItem.authorDisplayName)
        assertEquals("first line\nsecond line", playerItem.description)
        assertEquals(123, playerItem.likeCount)
        assertEquals("image", playerItem.mediaKind)
        assertEquals(1, playerItem.slideCount)
        assertEquals(OwnerKind.Tweet, playerItem.ownerKind)
        assertTrue(playerItem.isBookmarked)
    }

    @Test
    fun tweet_mapping_uses_quote_media_when_parent_has_no_media() {
        val playerItem = toBookmarkMomentItem(
            tweetBookmark(
                tweetId = "parent_tweet",
                mediaJson = null,
                quoteTweetId = "quoted_tweet",
                quoteMediaJson = """[{"type":"image"}]""",
            ),
        )

        assertEquals("image", playerItem.mediaKind)
        assertEquals(1, playerItem.slideCount)
        assertEquals("parent_tweet", playerItem.videoId)
        assertEquals("quoted_tweet", playerItem.mediaOwnerId)
    }

    @Test
    fun bookmark_timestamp_prefers_saved_date_and_falls_back_to_content_date() {
        val savedAt = 1710000000000L
        assertEquals(
            savedAt,
            bookmarkPublishedAt(tweetBookmark(bookmarkedAt = savedAt, publishedAt = 123L)),
        )
        assertEquals(
            456L,
            bookmarkPublishedAt(tweetBookmark(bookmarkedAt = 0L, publishedAt = 456L)),
        )
    }

    @Test
    fun video_mapping_uses_platform_prefix_for_author_fallback() {
        val playerItem = toBookmarkMomentItem(
            videoBookmark(
                videoId = "youtube_abc123",
                channelId = "youtube_demo_channel",
                description = "",
                title = "Demo title",
            ).copy(
                video = VideoEntity(
                    videoId = "youtube_abc123",
                    channelId = "youtube_demo_channel",
                    title = "Demo title",
                    description = "",
                    mediaKind = "slideshow",
                    slideCount = 3,
                ),
            ),
        )

        assertEquals("@demo_channel", playerItem.authorHandle)
        assertEquals("Demo Channel", playerItem.authorDisplayName)
        assertEquals("Demo title", playerItem.description)
        assertEquals("slideshow", playerItem.mediaKind)
        assertEquals(3, playerItem.slideCount)
    }

    @Test
    fun tiktok_video_mapping_keeps_tiktok_channel_for_avatar_and_profile_navigation() {
        val playerItem = toBookmarkMomentItem(
            videoBookmark(
                videoId = "759123456789",
                channelId = "tiktok_alice",
                channelName = "Alice",
                channelSourceId = "alice",
            ),
        )

        assertEquals("tiktok_alice", playerItem.channelId)
        assertEquals("@alice", playerItem.authorHandle)
        assertEquals(OwnerKind.TikTokVideo, playerItem.ownerKind)
    }

    @Test
    fun bookmark_mapping_uses_resolved_follow_state_for_shared_player_badge() {
        val playerItem = toBookmarkMomentItem(
            tweetBookmark(
                authorHandle = "alice",
                channelId = "twitter_alice",
                mediaJson = """[{"type":"video"}]""",
                isFollowed = true,
            ),
        )

        assertEquals("twitter_alice", playerItem.channelId)
        assertTrue(playerItem.isAuthorFollowed)
    }

    @Test
    fun video_thumbnail_fallback_uses_thumbnail_endpoint_when_local_path_missing() {
        val uri = videoBookmark(videoId = "tiktok_42")
            .initialThumbnailUri("https://igloo.example")

        assertEquals(MediaUri.Remote("https://igloo.example/api/media/thumbnail/tiktok_42"), uri)
    }

    @Test
    fun slideshow_thumbnail_fallback_uses_first_slide_endpoint() {
        val uri = videoBookmark(
            videoId = "tiktok_slide",
            channelId = "tiktok_demo_channel",
        ).copy(
            video = VideoEntity(
                videoId = "tiktok_slide",
                channelId = "tiktok_demo_channel",
                mediaKind = "slideshow",
                slideCount = 4,
            ),
        ).initialThumbnailUri("https://igloo.example")

        assertEquals(MediaUri.Remote("https://igloo.example/api/media/slide/tiktok_slide/0"), uri)
    }

    @Test
    fun mixed_tweet_bookmark_uses_sync_asset_shape_when_feed_json_only_has_first_photo() {
        val item = tweetBookmark(
            tweetId = "sample_tweet_mixed",
            mediaJson = """[{"type":"photo"}]""",
        ).copy(
            video = VideoEntity(
                videoId = "sample_tweet_mixed",
                channelId = "twitter_sample_author",
                mediaKind = "",
                slideCount = 0,
            ),
            assetMediaCount = 3,
            assetImageCount = 1,
            assetVideoCount = 2,
        )

        val playerItem = toBookmarkMomentItem(item, baseUrl = "https://igloo.example")

        assertEquals(MediaType.Slideshow, bookmarkMediaType(item))
        assertEquals("slideshow", playerItem.mediaKind)
        assertEquals(3, playerItem.slideCount)
        assertEquals("sample_tweet_mixed", playerItem.mediaOwnerId)
        assertEquals(OwnerKind.Tweet, playerItem.ownerKind)
        assertEquals(
            MediaUri.Remote("https://igloo.example/api/media/slide/sample_tweet_mixed/0"),
            playerItem.fallbackThumbnailUri,
        )
    }

    @Test
    fun tweet_mapping_keeps_tile_thumbnail_as_overlay_fallback() {
        val playerItem = toBookmarkMomentItem(
            tweetBookmark(mediaJson = """[{\"type\":\"image\"}]"""),
            baseUrl = "https://igloo.example",
        )

        assertEquals(MediaUri.Remote("https://igloo.example/api/media/thumbnail/tweet_1"), playerItem.fallbackThumbnailUri)
    }

    @Test
    fun label_counts_trim_group_and_sort_by_frequency() {
        val rows = bookmarkLabelCounts(
            listOf(
                videoBookmark(videoId = "a", customTitle = " cinema "),
                videoBookmark(videoId = "b", customTitle = "cinema"),
                videoBookmark(videoId = "c", customTitle = "japan"),
                videoBookmark(videoId = "d", customTitle = null),
                videoBookmark(videoId = "e", customTitle = ""),
                videoBookmark(videoId = "f", customTitle = "   "),
            ),
        )

        assertEquals(
            listOf(
                BookmarkLabelCount(label = null, count = 3),
                BookmarkLabelCount(label = "cinema", count = 2),
                BookmarkLabelCount(label = "japan", count = 1),
            ),
            rows,
        )
    }

    @Test
    fun bookmark_filters_are_single_active_modes() {
        val rows = listOf(
            videoBookmark(videoId = "cat_one", categoryId = 1L, customTitle = "cinema"),
            videoBookmark(videoId = "cat_two", categoryId = 2L, customTitle = "cinema"),
            videoBookmark(videoId = "unlabelled", categoryId = 1L, customTitle = " "),
        )

        assertEquals(
            listOf("cat_one", "cat_two", "unlabelled"),
            filterBookmarkItems(rows, BookmarkFilter.All).map { it.bookmark.videoId },
        )
        assertEquals(
            listOf("cat_one", "unlabelled"),
            filterBookmarkItems(rows, BookmarkFilter.Category(1L)).map { it.bookmark.videoId },
        )
        assertEquals(
            listOf("cat_one", "cat_two"),
            filterBookmarkItems(rows, BookmarkFilter.Label("cinema")).map { it.bookmark.videoId },
        )
        assertEquals(
            listOf("unlabelled"),
            filterBookmarkItems(rows, BookmarkFilter.NoLabel).map { it.bookmark.videoId },
        )
    }

    @Test
    fun label_search_matches_displayed_no_label_row() {
        val rows = listOf(
            BookmarkLabelCount(label = null, count = 3),
            BookmarkLabelCount(label = "cinema", count = 2),
            BookmarkLabelCount(label = "japan", count = 1),
        )

        assertEquals(listOf("cinema"), filterBookmarkLabelCounts(rows, "cine").map { it.label })
        assertEquals(listOf(null), filterBookmarkLabelCounts(rows, "no", "No label").map { it.label })
    }

    private fun videoBookmark(
        videoId: String = "youtube_video_1",
        channelId: String = "youtube_channel_1",
        description: String = "Video description",
        title: String = "Video title",
        channelName: String? = "Demo Channel",
        channelSourceId: String? = "demo_channel",
        isFollowed: Boolean = false,
        categoryId: Long = 1L,
        customTitle: String? = null,
    ): BookmarkItem = BookmarkItem(
        bookmark = BookmarkEntity(
            videoId = videoId,
            categoryId = categoryId,
            customTitle = customTitle,
            bookmarkedAt = 10L,
        ),
        feedItem = null,
        video = VideoEntity(
            videoId = videoId,
            channelId = channelId,
            title = title,
            description = description,
            publishedAt = 123L,
        ),
        resolvedChannelId = channelId,
        resolvedChannelName = channelName,
        resolvedChannelSourceId = channelSourceId,
        resolvedChannelIsFollowed = if (isFollowed) 1 else 0,
    )

    private fun tweetBookmark(
        tweetId: String = "tweet_1",
        authorHandle: String = "author1",
        channelId: String? = "twitter_author1",
        mediaJson: String? = null,
        quoteTweetId: String? = null,
        quoteMediaJson: String? = null,
        bodyText: String = "Tweet body",
        likes: Long? = null,
        authorDisplayName: String? = "Author 42",
        isFollowed: Boolean = false,
        bookmarkedAt: Long = 20L,
        publishedAt: Long = 0L,
    ): BookmarkItem = BookmarkItem(
        bookmark = BookmarkEntity(videoId = tweetId, categoryId = 2L, bookmarkedAt = bookmarkedAt),
        feedItem = FeedItemEntity(
            tweetId = tweetId,
            authorHandle = authorHandle,
            authorDisplayName = authorDisplayName,
            bodyText = bodyText,
            mediaJson = mediaJson,
            quoteTweetId = quoteTweetId,
            quoteMediaJson = quoteMediaJson,
            likes = likes,
            publishedAt = publishedAt,
            channelId = channelId,
        ),
        video = null,
        resolvedChannelId = channelId,
        resolvedChannelName = authorDisplayName,
        resolvedChannelSourceId = authorHandle,
        resolvedChannelIsFollowed = if (isFollowed) 1 else 0,
    )
}
