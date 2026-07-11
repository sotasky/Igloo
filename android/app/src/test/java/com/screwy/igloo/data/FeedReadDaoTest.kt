package com.screwy.igloo.data

import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.ChannelProfileEntity
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.data.entity.FeedLikeEntity
import com.screwy.igloo.data.entity.MutedChannelEntity
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class FeedReadDaoTest {
    private lateinit var db: IglooDatabase

    @Before
    fun setUp() {
        db = RoomTestSupport.freshDb()
    }

    @After
    fun tearDown() {
        db.close()
    }

    @Test
    fun displayIdentityComesOnlyFromChannelProfiles() = runBlocking {
        seedIdentity("sample_author", "sample_author_handle", "Sample Author")
        seedIdentity("sample_source", "sample_source_handle", "Sample Source")
        seedIdentity("sample_quote", "sample_quote_handle", "Sample Quote")
        seedIdentity("sample_reply", "sample_reply_handle", "Sample Reply")
        seedIdentity("sample_reposter", "sample_reposter_handle", "Sample Reposter")
        db.feedItemDao()
            .upsert(
                FeedItemEntity(
                    tweetId = "item-1",
                    sourceChannelId = "sample_source",
                    reposterChannelId = "sample_reposter",
                    quoteChannelId = "sample_quote",
                    replyChannelId = "sample_reply",
                    channelId = "sample_author",
                    publishedAt = 1,
                )
            )

        val row = db.feedReadDao().feedFlow().first().single()

        assertEquals("sample_author_handle", row.authorHandle)
        assertEquals("Sample Author", row.authorDisplayName)
        assertEquals("sample_source_handle", row.sourceHandle)
        assertEquals("Sample Source", row.sourceDisplayName)
        assertEquals("sample_quote_handle", row.quoteAuthorHandle)
        assertEquals("Sample Quote", row.quoteAuthorDisplayName)
        assertEquals("sample_reply_handle", row.replyHandle)
        assertEquals("sample_reposter_handle", row.reposterHandle)
        assertEquals("Sample Reposter", row.reposterDisplayName)
    }

    @Test
    fun mainFeedMutesByAuthorOrReposterChannelId() = runBlocking {
        seedIdentity("sample_author_a", "sample_a", "Sample A")
        seedIdentity("sample_author_b", "sample_b", "Sample B")
        seedIdentity("sample_reposter", "sample_r", "Sample R")
        db.feedItemDao()
            .upsert(
                listOf(
                    FeedItemEntity(
                        tweetId = "sample_author_muted",
                        channelId = "sample_author_a",
                        publishedAt = 3,
                    ),
                    FeedItemEntity(
                        tweetId = "sample_reposter_muted",
                        channelId = "sample_author_b",
                        reposterChannelId = "sample_reposter",
                        isRetweet = true,
                        publishedAt = 2,
                    ),
                    FeedItemEntity(
                        tweetId = "sample_tweet_visible",
                        channelId = "sample_author_b",
                        publishedAt = 1,
                    ),
                )
            )
        db.mutedChannelDao()
            .upsert(
                listOf(MutedChannelEntity("sample_author_a"), MutedChannelEntity("sample_reposter"))
            )

        val rows = db.feedReadDao().feedFlow().first()

        assertEquals(listOf("sample_tweet_visible"), rows.map { it.item.tweetId })
    }

    @Test
    fun explicitSurfacesKeepMutedRowsAndJoinCurrentProfileValues() = runBlocking {
        seedIdentity("sample_author", "sample_old_handle", "Sample Old")
        db.feedItemDao()
            .upsert(
                FeedItemEntity(tweetId = "item-1", channelId = "sample_author", publishedAt = 1)
            )
        db.feedLikeDao().upsert(FeedLikeEntity("item-1", 1))
        db.mutedChannelDao().upsert(MutedChannelEntity("sample_author"))
        db.channelProfileDao()
            .upsert(
                ChannelProfileEntity("sample_author", "twitter", "sample_new_handle", "Sample New")
            )

        val liked = db.feedReadDao().likedFlow().first().single()
        val channel =
            db.feedReadDao().channelFeedFlow("sample_author", "sample_new_handle").first().single()

        assertEquals("sample_new_handle", liked.authorHandle)
        assertEquals("Sample New", liked.authorDisplayName)
        assertEquals("item-1", channel.item.tweetId)
        assertTrue(db.feedReadDao().feedFlow().first().isEmpty())
    }

    private suspend fun seedIdentity(channelId: String, handle: String, displayName: String) {
        db.channelDao().upsert(ChannelEntity(channelId, handle, handle, null, "twitter"))
        db.channelProfileDao()
            .upsert(ChannelProfileEntity(channelId, "twitter", handle, displayName))
    }
}
