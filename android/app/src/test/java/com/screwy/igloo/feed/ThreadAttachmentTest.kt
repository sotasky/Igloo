package com.screwy.igloo.feed

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.data.entity.FeedRankEntity
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
class ThreadAttachmentTest {

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
    fun attachThreadChains_setsChainOnReplyLeafs() = runBlocking {
        db.channelDao()
            .upsert(
                ChannelEntity(
                    channelId = "twitter_sample_alpha",
                    name = "Alpha",
                    platform = "twitter",
                )
            )
        db.feedItemDao()
            .upsert(
                listOf(
                    FeedItemEntity(tweetId = "t1", channelId = "twitter_sample_alpha"),
                    FeedItemEntity(
                        tweetId = "t2",
                        channelId = "twitter_sample_alpha",
                        isReply = true,
                        replyToStatus = "t1",
                    ),
                )
            )

        val rows = db.feedReadDao().feedFlow(limit = 10).first()
        val threaded = attachThreadChains(db.feedReadDao(), rows)

        assertEquals(1, threaded.size)
        assertEquals("t2", threaded[0].row.item.tweetId)
        assertEquals(listOf("t1"), threaded[0].chain.map { it.item.tweetId })
    }

    @Test
    fun attachThreadChains_collapsesSiblingReplyBranchesToFirstRankedLeaf() = runBlocking {
        db.channelDao()
            .upsert(
                listOf(
                    ChannelEntity(
                        channelId = "twitter_sample_alpha",
                        name = "Alpha",
                        platform = "twitter",
                    ),
                    ChannelEntity(
                        channelId = "twitter_sample_beta",
                        name = "Beta",
                        platform = "twitter",
                    ),
                    ChannelEntity(
                        channelId = "twitter_sample_gamma",
                        name = "Gamma",
                        platform = "twitter",
                    ),
                )
            )
        db.feedItemDao()
            .upsert(
                listOf(
                    FeedItemEntity(
                        tweetId = "root",
                        channelId = "twitter_sample_alpha",
                        isReply = true,
                        replyToStatus = "missing_ancestor",
                    ),
                    FeedItemEntity(
                        tweetId = "parent_a",
                        channelId = "twitter_sample_beta",
                        isReply = true,
                        replyToStatus = "root",
                    ),
                    FeedItemEntity(
                        tweetId = "leaf_a",
                        channelId = "twitter_sample_gamma",
                        isReply = true,
                        replyToStatus = "parent_a",
                    ),
                    FeedItemEntity(
                        tweetId = "parent_b",
                        channelId = "twitter_sample_beta",
                        isReply = true,
                        replyToStatus = "root",
                    ),
                    FeedItemEntity(
                        tweetId = "leaf_b",
                        channelId = "twitter_sample_alpha",
                        isReply = true,
                        replyToStatus = "parent_b",
                    ),
                )
            )
        db.feedRankDao()
            .upsert(
                listOf(
                    FeedRankEntity(tweetId = "leaf_b", rankPosition = 1, snapshotAt = 1),
                    FeedRankEntity(tweetId = "leaf_a", rankPosition = 2, snapshotAt = 1),
                    FeedRankEntity(tweetId = "parent_b", rankPosition = 3, snapshotAt = 1),
                    FeedRankEntity(tweetId = "parent_a", rankPosition = 4, snapshotAt = 1),
                    FeedRankEntity(tweetId = "root", rankPosition = 5, snapshotAt = 1),
                )
            )

        val rows = db.feedReadDao().feedFlow(limit = 10).first()
        val threaded = attachThreadChains(db.feedReadDao(), rows)

        assertEquals(listOf("leaf_b"), threaded.map { it.row.item.tweetId })
        assertEquals(listOf("root", "parent_b"), threaded[0].chain.map { it.item.tweetId })
    }

    @Test
    fun attachThreadChains_keepsNonReplyRows() = runBlocking {
        db.channelDao()
            .upsert(
                ChannelEntity(
                    channelId = "twitter_sample_alpha",
                    name = "Alpha",
                    platform = "twitter",
                )
            )
        db.feedItemDao().upsert(FeedItemEntity(tweetId = "t1", channelId = "twitter_sample_alpha"))

        val rows = db.feedReadDao().feedFlow(limit = 10).first()
        val threaded = attachThreadChains(db.feedReadDao(), rows)

        assertEquals(1, threaded.size)
        assertEquals("t1", threaded[0].row.item.tweetId)
        assertTrue(threaded[0].chain.isEmpty())
    }

    @Test
    fun attachThreadChains_matchesServerFiftyParentDepthLimit() = runBlocking {
        db.channelDao()
            .upsert(
                ChannelEntity(
                    channelId = "twitter_sample_alpha",
                    name = "Alpha",
                    platform = "twitter",
                )
            )
        db.feedItemDao()
            .upsert(
                (0..50).map { depth ->
                    FeedItemEntity(
                        tweetId = "sample_thread_$depth",
                        channelId = "twitter_sample_alpha",
                        isReply = depth > 0,
                        replyToStatus = if (depth > 0) "sample_thread_${depth - 1}" else "",
                    )
                }
            )

        val leaf = db.feedReadDao().getFeedRowsByTweetIds(listOf("sample_thread_50"))
        val threaded = attachThreadChains(db.feedReadDao(), leaf)

        assertEquals(1, threaded.size)
        assertEquals(50, threaded[0].chain.size)
        assertEquals("sample_thread_0", threaded[0].chain.first().item.tweetId)
        assertEquals("sample_thread_49", threaded[0].chain.last().item.tweetId)
    }

    @Test
    fun attachThreadChains_replyWithMissingParentHasEmptyChain() = runBlocking {
        db.channelDao()
            .upsert(
                ChannelEntity(
                    channelId = "twitter_sample_alpha",
                    name = "Alpha",
                    platform = "twitter",
                )
            )
        db.feedItemDao()
            .upsert(
                FeedItemEntity(
                    tweetId = "t1",
                    channelId = "twitter_sample_alpha",
                    isReply = true,
                    replyToStatus = "9999",
                )
            )

        val rows = db.feedReadDao().feedFlow(limit = 10).first()
        val threaded = attachThreadChains(db.feedReadDao(), rows)

        assertEquals(1, threaded.size)
        assertEquals("t1", threaded[0].row.item.tweetId)
        assertTrue(threaded[0].chain.isEmpty())
    }
}
