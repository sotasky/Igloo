package com.screwy.igloo.data

import com.screwy.igloo.data.entity.BookmarkCategoryEntity
import com.screwy.igloo.data.entity.BookmarkEntity
import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.ChannelFollowEntity
import com.screwy.igloo.data.entity.ChannelSettingEntity
import com.screwy.igloo.data.entity.ChannelStarEntity
import com.screwy.igloo.data.entity.CursorEntity
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.data.entity.FeedLikeEntity
import com.screwy.igloo.data.entity.FeedSeenEntity
import com.screwy.igloo.data.entity.MomentViewEntity
import com.screwy.igloo.data.entity.MutedAccountEntity
import com.screwy.igloo.data.entity.RetweetSourceEntity
import com.screwy.igloo.data.entity.SponsorBlockCheckedEntity
import com.screwy.igloo.data.entity.SponsorBlockSegmentEntity
import com.screwy.igloo.data.entity.VideoCommentEntity
import com.screwy.igloo.data.entity.VideoEntity
import com.screwy.igloo.data.entity.WatchHistoryEntity
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * Round-trip CRUD coverage per per-entity DAO. One test per entity — upsert writes a row,
 * read-back returns the same row, delete returns zero-count. Intentionally exhaustive so
 * schema drift surfaces immediately at test-time instead of first run-on-device.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class DaoCrudTest {

    private lateinit var db: IglooDatabase

    @Before fun setUp() { db = RoomTestSupport.freshDb() }
    @After fun tearDown() { db.close() }

    @Test fun feedItems_roundTrip() = runBlocking {
        val dao = db.feedItemDao()
        val row = FeedItemEntity(
            tweetId = "t_1", authorHandle = "alice", publishedAt = 100, syncSeq = 10,
            channelId = "twitter_alice",
        )
        dao.upsert(row)
        assertEquals(row, dao.getById("t_1"))
        assertEquals(row, dao.getByIdFlow("t_1").first())
        assertEquals(1, dao.count())
        dao.deleteByIds(listOf("t_1"))
        assertNull(dao.getById("t_1"))
    }

    @Test fun videos_roundTrip() = runBlocking {
        val dao = db.videoDao()
        val row = VideoEntity(
            videoId = "v_1", channelId = "youtube_abc", title = "Hello",
            duration = 600, publishedAt = 200,
        )
        dao.upsert(row)
        assertEquals(row, dao.getById("v_1"))
        assertEquals(1, dao.count())
        dao.deleteByIds(listOf("v_1"))
        assertEquals(0, dao.count())
    }

    @Test fun videos_roundTripsDearrowFields() = runBlocking {
        val dao = db.videoDao()
        val row = VideoEntity(
            videoId = "v_da", channelId = "youtube_abc", title = "Original",
            dearrowTitle = "Community Title",
            dearrowTitleCasual = "Casual Title",
            dearrowThumbPath = "thumbnails/dearrow/v_da.jpg",
            dearrowCheckedAtMs = 1_700_000_000_000L,
        )
        dao.upsert(row)
        val got = dao.getById("v_da")!!
        assertEquals("Community Title", got.dearrowTitle)
        assertEquals("Casual Title", got.dearrowTitleCasual)
        assertEquals("thumbnails/dearrow/v_da.jpg", got.dearrowThumbPath)
        assertEquals(1_700_000_000_000L, got.dearrowCheckedAtMs)
    }

    @Test fun channels_roundTrip() = runBlocking {
        val dao = db.channelDao()
        val row = ChannelEntity(channelId = "twitter_alice", name = "Alice", platform = "twitter")
        dao.upsert(row)
        assertEquals(row, dao.getById("twitter_alice"))
    }

    @Test fun videoComments_forVideoFlow() = runBlocking {
        val dao = db.videoCommentDao()
        val a = VideoCommentEntity(videoId = "v_1", commentId = "c_1", text = "hi", publishedAt = 10, threadOrder = 2, threadDepth = 1, parentOrder = 1, replyToAuthor = "Root", isCreator = false)
        val b = VideoCommentEntity(videoId = "v_1", commentId = "c_2", text = "hey", publishedAt = 30, threadOrder = 1, threadDepth = 0, parentOrder = 0, replyToAuthor = "", isCreator = true)
        val c = VideoCommentEntity(videoId = "v_2", commentId = "c_3", text = "bye", publishedAt = 20, threadOrder = 1)
        val legacy = VideoCommentEntity(videoId = "v_1", commentId = "legacy", text = "old", publishedAt = 40)
        dao.upsert(listOf(a, b, c, legacy))
        val v1 = dao.forVideoFlow("v_1").first()
        assertEquals(listOf("c_2", "c_1", "legacy"), v1.map { it.commentId })
        assertEquals(0, v1[0].threadDepth)
        assertEquals(1, v1[1].threadDepth)
        assertEquals("Root", v1[1].replyToAuthor)
        assertTrue(v1[0].isCreator)
        dao.deleteForVideo("v_1")
        assertTrue(dao.forVideoFlow("v_1").first().isEmpty())
    }

    @Test fun retweetSources_orderedDesc() = runBlocking {
        val dao = db.retweetSourceDao()
        val rows = listOf(
            RetweetSourceEntity(contentHash = "h", retweeterHandle = "a", tweetId = "t", publishedAt = 10),
            RetweetSourceEntity(contentHash = "h", retweeterHandle = "b", tweetId = "t", publishedAt = 30),
            RetweetSourceEntity(contentHash = "h", retweeterHandle = "c", tweetId = "t", publishedAt = 20),
        )
        dao.upsert(rows)
        val byDate = dao.forContentHash("h", 10)
        assertEquals(listOf("b", "c", "a"), byDate.map { it.retweeterHandle })
    }

    @Test fun sponsorBlock_roundTrip() = runBlocking {
        val segDao = db.sponsorBlockSegmentDao()
        val chkDao = db.sponsorBlockCheckedDao()
        val seg = SponsorBlockSegmentEntity("v", startTime = 0.0, endTime = 5.0, category = "sponsor")
        segDao.upsert(listOf(seg))
        chkDao.upsert(SponsorBlockCheckedEntity(videoId = "v", checkedAt = 1))
        assertEquals(listOf(seg), segDao.forVideo("v"))
        assertNotNull(chkDao.forVideo("v"))
    }

    @Test fun feedLikes_existence() = runBlocking {
        val dao = db.feedLikeDao()
        assertFalse(dao.exists("t"))
        dao.upsert(FeedLikeEntity("t", likedAt = 42))
        assertTrue(dao.exists("t"))
        dao.delete("t")
        assertFalse(dao.exists("t"))
    }

    @Test fun bookmarks_andCategoryRemap() = runBlocking {
        val catDao = db.bookmarkCategoryDao()
        val bmDao  = db.bookmarkDao()
        catDao.upsert(BookmarkCategoryEntity(-1, "provisional")) // provisional negative ID
        bmDao.upsert(BookmarkEntity(videoId = "t_1", categoryId = -1, bookmarkedAt = 5))
        assertEquals(-1L, bmDao.getById("t_1")!!.categoryId)
        bmDao.remapCategory(oldId = -1, newId = 42)
        assertEquals(42L, bmDao.getById("t_1")!!.categoryId)
    }

    @Test fun bookmarkCategories_doNotSynthesizeMissingCategoryMetadata() = runBlocking {
        val catDao = db.bookmarkCategoryDao()
        db.bookmarkDao().upsert(BookmarkEntity(videoId = "t_1", categoryId = 4, bookmarkedAt = 20))
        db.bookmarkDao().upsert(BookmarkEntity(videoId = "t_2", categoryId = 4, bookmarkedAt = 30))
        db.bookmarkDao().upsert(BookmarkEntity(videoId = "t_3", categoryId = 8, bookmarkedAt = 10))

        val rows = catDao.allFlow().first()
        assertTrue(rows.isEmpty())
    }

    @Test fun bookmarkCategories_allFlowReturnsSyncedRowsOnly() = runBlocking {
        val catDao = db.bookmarkCategoryDao()
        db.bookmarkDao().upsert(BookmarkEntity(videoId = "t_1", categoryId = 7, bookmarkedAt = 20))
        catDao.upsert(BookmarkCategoryEntity(categoryId = 7, name = "art", createdAt = 5))

        val rows = catDao.allFlow().first()
        assertEquals(1, rows.size)
        assertEquals("art", rows.single().name)
    }

    @Test fun feedSeen_mutedAccounts_existence() = runBlocking {
        db.feedSeenDao().upsert(FeedSeenEntity("t", seenAt = 10))
        assertTrue(db.feedSeenDao().exists("t"))

        db.mutedAccountDao().upsert(MutedAccountEntity("alice", mutedAt = 10))
        assertTrue(db.mutedAccountDao().exists("alice"))
    }

    @Test fun momentViews_watchHistory() = runBlocking {
        db.momentViewDao().upsert(MomentViewEntity("v", viewedAt = 10))
        assertTrue(db.momentViewDao().exists("v"))

        val wh = WatchHistoryEntity(videoId = "v", playbackPosition = 12.5, duration = 60.0, lastWatched = 5)
        db.watchHistoryDao().upsert(wh)
        assertEquals(wh, db.watchHistoryDao().getById("v"))
    }

    @Test fun channelFollowStar_existence() = runBlocking {
        db.channelFollowDao().upsert(ChannelFollowEntity("twitter_alice", 10))
        db.channelStarDao().upsert(ChannelStarEntity("twitter_alice", 10))
        assertTrue(db.channelFollowDao().exists("twitter_alice"))
        assertTrue(db.channelStarDao().exists("twitter_alice"))
        db.channelFollowDao().delete("twitter_alice")
        assertFalse(db.channelFollowDao().exists("twitter_alice"))
    }

    @Test fun channelSettings_getById() = runBlocking {
        val dao = db.channelSettingDao()
        val row = ChannelSettingEntity(
            channelId = "twitter_alice",
            mediaOnly = 1, includeReposts = 0, updatedAt = 10,
        )
        dao.upsert(row)
        assertEquals(row, dao.getById("twitter_alice"))
        dao.delete("twitter_alice")
        assertNull(dao.getById("twitter_alice"))
    }

    @Test fun cursors_upsertAndClear() = runBlocking {
        val dao = db.cursorDao()
        dao.upsert(CursorEntity(stream = "feed", cursor = "abc", updatedAt = 10))
        assertEquals("abc", dao.get("feed")?.cursor)
        dao.upsert(CursorEntity(stream = "feed", cursor = "def", updatedAt = 20))
        assertEquals("def", dao.get("feed")?.cursor)
        dao.deleteAll()
        assertNull(dao.get("feed"))
    }
}
