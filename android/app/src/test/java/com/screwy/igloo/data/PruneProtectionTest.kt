package com.screwy.igloo.data

import com.screwy.igloo.data.entity.BookmarkEntity
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.data.entity.FeedLikeEntity
import com.screwy.igloo.data.entity.MomentViewEntity
import com.screwy.igloo.data.entity.VideoEntity
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * Prune-protection invariants.
 *
 * 1. YouTube `videos` rows never prune (retention clause excludes them by channel_id).
 * 2. Shorts `videos` rows prune when past retention AND no side-table saves them.
 * 3. Shorts `videos` rows survive if protected by bookmarks.
 * 4. Twitter `feed_items` rows respect feed_likes + bookmarks protection.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class PruneProtectionTest {

    private lateinit var db: IglooDatabase

    @Before fun setUp() { db = RoomTestSupport.freshDb() }
    @After fun tearDown() { db.close() }

    // ─── Videos: YouTube never prunes ─────────────────────────────────────────

    @Test fun videos_youtubeNeverPruned_evenPastRetention() = runBlocking {
        db.videoDao().upsert(VideoEntity(
            videoId = "v_youtube", channelId = "youtube_abc",
            title = "Old video", publishedAt = 0,
        ))
        val deleted = db.videoDao().pruneShorts(cutoffMs = 100_000)
        assertEquals(0, deleted)
        assertNotNull(db.videoDao().getById("v_youtube"))
    }

    @Test fun videos_shortsUnprotected_getPruned() = runBlocking {
        db.videoDao().upsert(VideoEntity("v_tt", "tiktok_alice", publishedAt = 10))
        db.videoDao().upsert(VideoEntity("v_ig", "instagram_alice", publishedAt = 999))
        val deleted = db.videoDao().pruneShorts(cutoffMs = 100)
        assertEquals(1, deleted)
        assertNull(db.videoDao().getById("v_tt"))
        assertNotNull(db.videoDao().getById("v_ig")) // published_at past cutoff
    }

    @Test fun videos_shortsSurvive_whenBookmarked() = runBlocking {
        db.videoDao().upsert(VideoEntity("v_tt", "tiktok_alice", publishedAt = 10))
        db.bookmarkDao().upsert(BookmarkEntity(videoId = "v_tt", bookmarkedAt = 50, categoryId = 0))
        val deleted = db.videoDao().pruneShorts(cutoffMs = 100)
        assertEquals(0, deleted)
        assertNotNull(db.videoDao().getById("v_tt"))
    }

    @Test fun videos_shortsPrune_whenOnlyMomentViewed() = runBlocking {
        db.videoDao().upsert(VideoEntity("v_tt", "tiktok_alice", publishedAt = 10))
        db.momentViewDao().upsert(MomentViewEntity("v_tt", viewedAt = 50))
        val deleted = db.videoDao().pruneShorts(cutoffMs = 100)
        assertEquals(1, deleted)
        assertNull(db.videoDao().getById("v_tt"))
    }

    // ─── Feed items: side-table protection ────────────────────────────────────

    @Test fun feedItems_unprotectedPastCutoff_pruned() = runBlocking {
        db.feedItemDao().upsert(FeedItemEntity(
            tweetId = "t_old", authorHandle = "alice",
            publishedAt = 10, syncSeq = 1,
        ))
        val deleted = db.feedItemDao().pruneExpired(cutoffMs = 100)
        assertEquals(1, deleted)
        assertNull(db.feedItemDao().getById("t_old"))
    }

    @Test fun feedItems_survive_whenLiked() = runBlocking {
        db.feedItemDao().upsert(FeedItemEntity(
            tweetId = "t_liked", authorHandle = "alice",
            publishedAt = 10, syncSeq = 1,
        ))
        db.feedLikeDao().upsert(FeedLikeEntity("t_liked", likedAt = 100))
        val deleted = db.feedItemDao().pruneExpired(cutoffMs = 100)
        assertEquals(0, deleted)
        assertNotNull(db.feedItemDao().getById("t_liked"))
    }

    @Test fun feedItems_survive_whenBookmarked() = runBlocking {
        db.feedItemDao().upsert(FeedItemEntity(
            tweetId = "t_bm", authorHandle = "alice",
            publishedAt = 10, syncSeq = 1,
        ))
        db.bookmarkDao().upsert(BookmarkEntity(videoId = "t_bm", bookmarkedAt = 50, categoryId = 0))
        val deleted = db.feedItemDao().pruneExpired(cutoffMs = 100)
        assertEquals(0, deleted)
        assertNotNull(db.feedItemDao().getById("t_bm"))
    }

    @Test fun feedItems_freshPosts_notPruned() = runBlocking {
        db.feedItemDao().upsert(FeedItemEntity(
            tweetId = "t_fresh", authorHandle = "alice",
            publishedAt = 200, syncSeq = 1,
        ))
        val deleted = db.feedItemDao().pruneExpired(cutoffMs = 100)
        assertEquals(0, deleted)
        assertNotNull(db.feedItemDao().getById("t_fresh"))
    }
}
