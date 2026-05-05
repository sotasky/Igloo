package com.screwy.igloo.data

import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.ChannelFollowEntity
import com.screwy.igloo.data.entity.FeedItemEntity
import kotlinx.coroutines.flow.first
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
class ChannelFeedReadDaoTest {

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
    fun channelFeedFlow_fallsBackToAuthorHandleWhenChannelIdMissing() = runBlocking {
        db.channelDao().upsert(
            ChannelEntity(
                channelId = "twitter_account",
                sourceId = "account",
                name = "Account",
                platform = "twitter",
            )
        )
        db.channelFollowDao().upsert(ChannelFollowEntity("twitter_account", followedAt = 1))
        db.feedItemDao().upsert(
            listOf(
                FeedItemEntity(
                    tweetId = "direct",
                    authorHandle = "Account",
                    channelId = "twitter_account",
                    syncSeq = 2,
                ),
                FeedItemEntity(
                    tweetId = "legacy",
                    authorHandle = "@account",
                    channelId = null,
                    syncSeq = 3,
                ),
                FeedItemEntity(
                    tweetId = "other",
                    authorHandle = "other",
                    channelId = null,
                    syncSeq = 4,
                ),
            )
        )

        val rows = db.feedReadDao()
            .channelFeedFlow(channelId = "twitter_account", channelHandle = "account")
            .first()

        assertEquals(listOf("legacy", "direct"), rows.map { it.item.tweetId })
        assertEquals(listOf("twitter_account", "twitter_account"), rows.map { it.item.channelId })
        assertEquals(listOf(1, 1), rows.map { it.channelIsFollowed })
    }
}
