package com.screwy.igloo.data

import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.FeedItemEntity
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class ThreadChainDaoTest {

    private lateinit var db: IglooDatabase

    @Before fun setUp() { db = RoomTestSupport.freshDb() }
    @After fun tearDown() { db.close() }

    @Test fun getThreadChain_returnsRootToLeafOrder() = runBlocking {
        db.channelDao().upsert(ChannelEntity(channelId = "twitter_user_alpha", name = "Alpha", platform = "twitter"))
        db.channelDao().upsert(ChannelEntity(channelId = "twitter_user_beta", name = "Beta", platform = "twitter"))
        db.feedItemDao().upsert(listOf(
            FeedItemEntity(tweetId = "t1", authorHandle = "user_alpha", channelId = "twitter_user_alpha"),
            FeedItemEntity(
                tweetId = "t2",
                authorHandle = "user_beta",
                channelId = "twitter_user_beta",
                isReply = true,
                replyToHandle = "user_alpha",
                replyToStatus = "t1",
            ),
            FeedItemEntity(
                tweetId = "t3",
                authorHandle = "user_alpha",
                channelId = "twitter_user_alpha",
                isReply = true,
                replyToHandle = "user_beta",
                replyToStatus = "t2",
            ),
        ))

        val chain = db.feedReadDao().getThreadChain("t3")

        assertEquals(listOf("t1", "t2", "t3"), chain.map { it.item.tweetId })
    }

    @Test fun getThreadChain_includesGhostParents() = runBlocking {
        db.channelDao().upsert(ChannelEntity(channelId = "twitter_user_alpha", name = "Alpha", platform = "twitter"))
        db.channelDao().upsert(ChannelEntity(channelId = "twitter_user_xenon", name = "Xenon", platform = "twitter"))
        db.feedItemDao().upsert(listOf(
            FeedItemEntity(
                tweetId = "g1",
                authorHandle = "user_xenon",
                channelId = "twitter_user_xenon",
                isGhost = true,
            ),
            FeedItemEntity(
                tweetId = "t1",
                authorHandle = "user_alpha",
                channelId = "twitter_user_alpha",
                isReply = true,
                replyToHandle = "user_xenon",
                replyToStatus = "g1",
            ),
        ))

        val chain = db.feedReadDao().getThreadChain("t1")

        assertEquals(listOf("g1", "t1"), chain.map { it.item.tweetId })
        assertEquals(true, chain.first().item.isGhost)
    }

    @Test fun getThreadChain_orphanLeafReturnsLeafOnly() = runBlocking {
        db.channelDao().upsert(ChannelEntity(channelId = "twitter_user_alpha", name = "Alpha", platform = "twitter"))
        db.feedItemDao().upsert(
            FeedItemEntity(
                tweetId = "t1",
                authorHandle = "user_alpha",
                channelId = "twitter_user_alpha",
                isReply = true,
                replyToHandle = "user_unknown",
                replyToStatus = "9999999",
            ),
        )

        val chain = db.feedReadDao().getThreadChain("t1")

        assertEquals(listOf("t1"), chain.map { it.item.tweetId })
    }

    @Test fun getThreadChain_unknownTweetReturnsEmpty() = runBlocking {
        val chain = db.feedReadDao().getThreadChain("does_not_exist")

        assertEquals(emptyList<String>(), chain.map { it.item.tweetId })
    }
}
