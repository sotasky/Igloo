package com.screwy.igloo.feed

import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.data.entity.FeedRow
import com.screwy.igloo.media.MediaUri
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test

class SocialTimelineModelsTest {
    @Test
    fun socialPostModelUsesStableActionAndMediaSnapshots() {
        val mediaJson = """[{"type":"photo","url":"https://example.test/a.jpg"}]"""
        val row = feedRow(
            FeedItemEntity(
                tweetId = "tweet-1",
                authorHandle = "alice",
                authorDisplayName = "Alice",
                mediaJson = mediaJson,
                channelId = "twitter_alice",
            ),
            isLiked = 1,
            isBookmarked = 1,
        )
        val mediaModel = fallbackFeedMediaGridModel("tweet-1", mediaJson)

        val model = buildSocialPostModel(
            row = row,
            mediaModels = mapOf("tweet-1" to mediaModel),
        )

        assertEquals("tweet-1", model.stableKey)
        assertEquals("social_post", model.contentType)
        assertEquals("twitter_alice", model.author.channelId)
        assertEquals("Alice", model.author.displayName)
        assertTrue(model.actions.isLiked)
        assertTrue(model.actions.isBookmarked)
        assertEquals(mediaModel, model.media.grid)
    }

    @Test
    fun socialPostModelBuildsFallbackMediaOutsideCells() {
        val mediaJson = """[{"type":"photo","url":"https://example.test/a.jpg"}]"""
        val model = buildSocialPostModel(
            row = feedRow(
                FeedItemEntity(
                    tweetId = "tweet-1",
                    authorHandle = "alice",
                    mediaJson = mediaJson,
                ),
            ),
            mediaModels = emptyMap(),
        )

        assertFalse(model.media.grid.inventoryLoaded)
        assertEquals(1, model.media.grid.mediaCount)
    }

    @Test
    fun profileOpenSnapshotUsesWarmRowIdentityAndServerBanner() {
        val post = buildSocialPostModel(
            row = feedRow(
                item = FeedItemEntity(
                    tweetId = "tweet-1",
                    authorHandle = "alice",
                    authorDisplayName = "Alice",
                    channelId = "twitter_alice",
                ),
                channelPlatform = "twitter",
                channelIsFollowed = 1,
                channelIsStarred = 1,
            ),
            mediaModels = emptyMap(),
        )

        val snapshot = buildProfileOpenSnapshot(post, "https://igloo.example/").also(::assertNotNull)!!

        assertEquals("twitter_alice", snapshot.channelId)
        assertEquals("Alice", snapshot.displayName)
        assertEquals("alice", snapshot.handle)
        assertTrue(snapshot.isFollowed)
        assertTrue(snapshot.isStarred)
        assertEquals(
            MediaUri.Remote("https://igloo.example/api/media/avatar/twitter_alice"),
            snapshot.avatarUri,
        )
        assertEquals(
            MediaUri.Remote("https://igloo.example/api/media/banner/twitter_alice"),
            snapshot.bannerUri,
        )
    }

    private fun feedRow(
        item: FeedItemEntity,
        isLiked: Int = 0,
        isBookmarked: Int = 0,
        channelPlatform: String? = "twitter",
        channelIsFollowed: Int = 0,
        channelIsStarred: Int = 0,
    ) = FeedRow(
        item = item,
        channelName = null,
        channelAvatarUrl = null,
        channelPlatform = channelPlatform,
        isLiked = isLiked,
        likedAt = null,
        isBookmarked = isBookmarked,
        bookmarkCategoryId = null,
        bookmarkCustomTitle = null,
        bookmarkedAt = null,
        channelIsFollowed = channelIsFollowed,
        channelIsStarred = channelIsStarred,
    )
}
