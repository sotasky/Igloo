package com.screwy.igloo.feed

import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.data.entity.FeedRow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test

class SocialTimelineModelsTest {
    @Test
    fun socialPostModelUsesStableActionAndMediaSnapshots() {
        val mediaJson = """[{"type":"photo","url":"https://example.test/a.jpg"}]"""
        val row =
            feedRow(
                FeedItemEntity(
                    tweetId = "tweet-1",
                    mediaJson = mediaJson,
                    channelId = "twitter_alice",
                ),
                authorHandle = "alice",
                authorDisplayName = "Alice",
                isLiked = 1,
                isBookmarked = 1,
            )
        val mediaModel = fallbackFeedMediaGridModel("tweet-1", mediaJson)

        val model = buildSocialPostModel(row = row, mediaModels = mapOf("tweet-1" to mediaModel))

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
        val model =
            buildSocialPostModel(
                row =
                    feedRow(
                        FeedItemEntity(tweetId = "tweet-1", mediaJson = mediaJson),
                        authorHandle = "alice",
                    ),
                mediaModels = emptyMap(),
            )

        assertFalse(model.media.grid.inventoryLoaded)
        assertEquals(1, model.media.grid.mediaCount)
    }

    @Test
    fun profileOpenSnapshotUsesRowIdentityAndState() {
        val post =
            buildSocialPostModel(
                row =
                    feedRow(
                        item = FeedItemEntity(tweetId = "tweet-1", channelId = "twitter_alice"),
                        authorHandle = "alice",
                        authorDisplayName = "Alice",
                        channelPlatform = "twitter",
                        channelIsFollowed = 1,
                        channelIsStarred = 1,
                    ),
                mediaModels = emptyMap(),
            )

        val snapshot = buildProfileOpenSnapshot(post).also(::assertNotNull)!!

        assertEquals("twitter_alice", snapshot.channelId)
        assertEquals("Alice", snapshot.displayName)
        assertEquals("alice", snapshot.handle)
        assertTrue(snapshot.isFollowed)
        assertTrue(snapshot.isStarred)
    }

    private fun feedRow(
        item: FeedItemEntity,
        authorHandle: String? = null,
        authorDisplayName: String? = null,
        isLiked: Int = 0,
        isBookmarked: Int = 0,
        channelPlatform: String? = "twitter",
        channelIsFollowed: Int = 0,
        channelIsStarred: Int = 0,
    ) =
        FeedRow(
            item = item,
            channelName = null,
            channelPlatform = channelPlatform,
            authorHandle = authorHandle,
            authorDisplayName = authorDisplayName,
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
