package com.screwy.igloo.data

import com.screwy.igloo.data.entity.BookmarkEntity
import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.ChannelFollowEntity
import com.screwy.igloo.data.entity.ChannelStarEntity
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.data.entity.MomentViewEntity
import com.screwy.igloo.data.entity.VideoEntity
import com.screwy.igloo.data.entity.WatchHistoryEntity
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.collect
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeoutOrNull
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * Moments / Videos / Bookmarks / Channel read DAO tests.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class ReadDaoTest {

    private lateinit var db: IglooDatabase

    @Before fun setUp() { db = RoomTestSupport.freshDb() }
    @After fun tearDown() { db.close() }

    // ─── MomentReadDao ────────────────────────────────────────────────────────

    @Test fun moments_scopesToTikTokAndInstagram() = runBlocking {
        db.videoDao().upsert(listOf(
            VideoEntity("v_tt", "tiktok_alice",    title = "TT",  publishedAt = 10),
            VideoEntity("v_ig", "instagram_alice", title = "IG",  publishedAt = 20),
            VideoEntity("v_youtube", "youtube_alice",   title = "YouTube",  publishedAt = 30),
        ))
        db.channelFollowDao().upsert(ChannelFollowEntity(channelId = "tiktok_alice"))
        db.channelFollowDao().upsert(ChannelFollowEntity(channelId = "instagram_alice"))
        val rows = db.momentReadDao().momentsFollowingFlow().first()
        val ids  = rows.map { it.video.videoId }.toSet()
        assertEquals(setOf("v_tt", "v_ig"), ids)
    }

    @Test fun moments_flagsViewedWhenSideTablePresent() = runBlocking {
        db.channelDao().upsert(
            ChannelEntity("tiktok_alice", name = "Alice Doe", platform = "tiktok", sourceId = "alice"),
        )
        db.channelFollowDao().upsert(ChannelFollowEntity(channelId = "tiktok_alice"))
        db.channelFollowDao().upsert(ChannelFollowEntity(channelId = "instagram_alice"))
        db.videoDao().upsert(listOf(
            VideoEntity("v_tt", "tiktok_alice", publishedAt = 10),
            VideoEntity("v_ig", "instagram_alice", publishedAt = 20),
        ))
        db.momentViewDao().upsert(MomentViewEntity("v_tt", viewedAt = 50))
        val rows = db.momentReadDao().momentsFollowingFlow().first().associateBy { it.video.videoId }
        assertEquals(1, rows["v_tt"]!!.isViewed)
        assertEquals(50L, rows["v_tt"]!!.viewedAt)
        assertEquals("Alice Doe", rows["v_tt"]!!.channelName)
        assertEquals("alice", rows["v_tt"]!!.channelSourceId)
        assertEquals(0, rows["v_ig"]!!.isViewed)
        assertNull(rows["v_ig"]!!.viewedAt)
    }

    @Test fun player_moments_doNotReemitWhenViewSideTableChanges() = runBlocking {
        db.channelDao().upsert(
            ChannelEntity("tiktok_alice", name = "Alice Doe", platform = "tiktok", sourceId = "alice"),
        )
        db.channelFollowDao().upsert(ChannelFollowEntity(channelId = "tiktok_alice"))
        db.videoDao().upsert(VideoEntity("v_tt", "tiktok_alice", publishedAt = 10))

        val emissions = mutableListOf<List<String>>()
        val collector = launch {
            db.momentReadDao().playerMomentsFollowingFlow().collect { rows ->
                emissions += rows.map { it.video.videoId }
            }
        }

        val initial = withTimeoutOrNull(1_000L) {
            while (emissions.isEmpty()) delay(10)
            true
        }
        assertEquals(true, initial)

        db.momentViewDao().upsert(MomentViewEntity("v_tt", viewedAt = 50))
        delay(250L)
        collector.cancel()

        assertEquals(listOf(listOf("v_tt")), emissions)
    }

    @Test fun channel_moments_oldestFirst() = runBlocking {
        db.channelDao().upsert(
            ChannelEntity("tiktok_alice", name = "Alice Doe", platform = "tiktok", sourceId = "alice"),
        )
        db.videoDao().upsert(
            listOf(
                VideoEntity("v_old", "tiktok_alice", publishedAt = 10),
                VideoEntity("v_new", "tiktok_alice", publishedAt = 20),
            ),
        )

        val rows = db.momentReadDao().channelMomentsFlow("tiktok_alice").first()

        assertEquals(listOf("v_old", "v_new"), rows.map { it.video.videoId })
    }

    @Test fun stories_rankUnseenThenStarredThenRecent() = runBlocking {
        db.channelDao().upsert(
            listOf(
                ChannelEntity("tiktok_seen_star", name = "Seen Star", platform = "tiktok", sourceId = "seen_star"),
                ChannelEntity("tiktok_unseen", name = "Unseen", platform = "tiktok", sourceId = "unseen"),
                ChannelEntity("tiktok_unseen_star", name = "Unseen Star", platform = "tiktok", sourceId = "unseen_star"),
            ),
        )
        db.channelFollowDao().upsert(ChannelFollowEntity(channelId = "tiktok_seen_star"))
        db.channelFollowDao().upsert(ChannelFollowEntity(channelId = "tiktok_unseen"))
        db.channelFollowDao().upsert(ChannelFollowEntity(channelId = "tiktok_unseen_star"))
        db.channelStarDao().upsert(ChannelStarEntity("tiktok_seen_star", starredAt = 20))
        db.channelStarDao().upsert(ChannelStarEntity("tiktok_unseen_star", starredAt = 10))
        db.videoDao().upsert(
            listOf(
                VideoEntity("v_seen_star", "tiktok_seen_star", publishedAt = 300, sourceKind = "story"),
                VideoEntity("v_unseen", "tiktok_unseen", publishedAt = 200, sourceKind = "story"),
                VideoEntity("v_unseen_new", "tiktok_unseen", publishedAt = 250, sourceKind = "story"),
                VideoEntity("v_unseen_star", "tiktok_unseen_star", publishedAt = 100, sourceKind = "story"),
                VideoEntity("v_regular", "tiktok_unseen", publishedAt = 400),
            ),
        )
        db.momentViewDao().upsert(MomentViewEntity("v_seen_star", viewedAt = 400))

        val channels = db.momentReadDao().storyChannelsFlow(cutoffMs = 0).first()
        assertEquals(
            listOf("tiktok_unseen_star", "tiktok_unseen", "tiktok_seen_star"),
            channels.map { it.channelId },
        )

        val playlist = db.momentReadDao().storyPlaylistFlow("tiktok_unseen", cutoffMs = 0).first()
        assertEquals(
            listOf("v_unseen", "v_unseen_new"),
            playlist.map { it.video.videoId },
        )

        val trayPlaylist = db.momentReadDao().storyTrayPlaylistFlow(cutoffMs = 0).first()
        assertEquals(
            listOf("v_unseen_star", "v_unseen", "v_unseen_new", "v_seen_star"),
            trayPlaylist.map { it.video.videoId },
        )
    }

    // ─── VideoReadDao ─────────────────────────────────────────────────────────

    @Test fun videos_onlyYoutube_withResumeProgress() = runBlocking {
        db.channelDao().upsert(
            ChannelEntity("youtube_alice", name = "Alice Channel", platform = "youtube", sourceId = "alice"),
        )
        db.videoDao().upsert(listOf(
            VideoEntity("v_youtube", "youtube_alice", title = "YouTube", duration = 600, publishedAt = 10),
            VideoEntity("v_tt", "tiktok_alice", publishedAt = 20),
        ))
        db.watchHistoryDao().upsert(
            WatchHistoryEntity(videoId = "v_youtube", playbackPosition = 120.0, duration = 600.0, lastWatched = 50),
        )
        val rows = db.videoReadDao().videosFlow().first()
        assertEquals(listOf("v_youtube"), rows.map { it.video.videoId })
        assertEquals(120.0, rows.single().playbackPosition!!, 0.0)
        assertEquals(600.0, rows.single().watchDuration!!, 0.0)
        assertEquals("Alice Channel", rows.single().channelName)
        assertEquals("alice", rows.single().channelSourceId)
    }

    // ─── BookmarkReadDao ──────────────────────────────────────────────────────

    @Test fun bookmarks_mixedPlatform_joinsBothSides() = runBlocking {
        // Twitter bookmark: video_id == tweet_id
        db.channelDao().upsert(ChannelEntity("twitter_alice", name = "Alice", platform = "twitter"))
        db.channelFollowDao().upsert(ChannelFollowEntity("twitter_alice", followedAt = 50))
        db.feedItemDao().upsert(FeedItemEntity(
            tweetId = "t_1",
            authorHandle = "alice",
            channelId = "twitter_alice",
            mediaJson = """[{"type":"image"}]""",
            publishedAt = 10,
            syncSeq = 1,
        ))
        // TikTok bookmark: video_id == videos.video_id
        db.videoDao().upsert(VideoEntity(videoId = "v_1", channelId = "tiktok_alice",
            title = "TT", publishedAt = 20))
        db.bookmarkDao().upsert(BookmarkEntity(videoId = "t_1", bookmarkedAt = 100, categoryId = 0))
        db.bookmarkDao().upsert(BookmarkEntity(videoId = "v_1", bookmarkedAt = 200, categoryId = 0))

        val rows = db.bookmarkReadDao().bookmarksFlow().first()
        assertEquals(listOf("v_1", "t_1"), rows.map { it.bookmark.videoId }) // bookmarked_at DESC

        val twitter = rows.single { it.bookmark.videoId == "t_1" }
        assertNotNull(twitter.feedItem)
        assertEquals("alice", twitter.feedItem!!.authorHandle)
        assertEquals("twitter_alice", twitter.resolvedChannelId)
        assertEquals("Alice", twitter.resolvedChannelName)
        assertEquals(1, twitter.resolvedChannelIsFollowed)
        // On a pure Twitter bookmark the video-side LEFT JOIN returns NULLs; the @Embedded
        // nullable entity becomes null if every column is NULL.
        assertNull(twitter.video)

        val tiktok = rows.single { it.bookmark.videoId == "v_1" }
        assertNull(tiktok.feedItem)
        assertNotNull(tiktok.video)
        assertEquals("TT", tiktok.video!!.title)
        assertEquals("tiktok_alice", tiktok.resolvedChannelId)
    }

    @Test fun bookmarks_dedupesTwitterRetweetClusterByCanonicalTweet() = runBlocking {
        db.channelDao().upsert(ChannelEntity("twitter_alice", name = "Alice", platform = "twitter"))
        db.feedItemDao().upsert(
            listOf(
                FeedItemEntity(
                    tweetId = "tweet_original",
                    authorHandle = "alice",
                    channelId = "twitter_alice",
                    mediaJson = """[{"type":"image"}]""",
                    contentHash = "same_content",
                    canonicalTweetId = "tweet_original",
                    publishedAt = 10,
                    syncSeq = 1,
                ),
                FeedItemEntity(
                    tweetId = "tweet_retweet",
                    sourceHandle = "bob",
                    authorHandle = "alice",
                    channelId = "twitter_alice",
                    mediaJson = """[{"type":"image"}]""",
                    isRetweet = true,
                    retweetedByHandle = "bob",
                    contentHash = "same_content",
                    canonicalTweetId = "tweet_original",
                    publishedAt = 20,
                    syncSeq = 2,
                ),
            ),
        )
        db.bookmarkDao().upsert(BookmarkEntity(videoId = "tweet_original", bookmarkedAt = 100, categoryId = 0))
        db.bookmarkDao().upsert(BookmarkEntity(videoId = "tweet_retweet", bookmarkedAt = 200, categoryId = 0))

        val rows = db.bookmarkReadDao().bookmarksFlow().first()

        assertEquals(listOf("tweet_original"), rows.map { it.bookmark.videoId })
        assertEquals("tweet_original", rows.single().feedItem!!.tweetId)
    }

    @Test fun bookmarks_dedupesRetweetOnlyClusterByContentHash() = runBlocking {
        db.channelDao().upsert(ChannelEntity("twitter_alice", name = "Alice", platform = "twitter"))
        db.feedItemDao().upsert(
            listOf(
                FeedItemEntity(
                    tweetId = "tweet_rt_old",
                    sourceHandle = "bob",
                    authorHandle = "alice",
                    channelId = "twitter_alice",
                    mediaJson = """[{"type":"image"}]""",
                    isRetweet = true,
                    retweetedByHandle = "bob",
                    contentHash = "retweet_only",
                    publishedAt = 10,
                    syncSeq = 1,
                ),
                FeedItemEntity(
                    tweetId = "tweet_rt_new",
                    sourceHandle = "carol",
                    authorHandle = "alice",
                    channelId = "twitter_alice",
                    mediaJson = """[{"type":"image"}]""",
                    isRetweet = true,
                    retweetedByHandle = "carol",
                    contentHash = "retweet_only",
                    publishedAt = 20,
                    syncSeq = 2,
                ),
            ),
        )
        db.bookmarkDao().upsert(BookmarkEntity(videoId = "tweet_rt_old", bookmarkedAt = 100, categoryId = 0))
        db.bookmarkDao().upsert(BookmarkEntity(videoId = "tweet_rt_new", bookmarkedAt = 200, categoryId = 0))

        val rows = db.bookmarkReadDao().bookmarksFlow().first()

        assertEquals(listOf("tweet_rt_new"), rows.map { it.bookmark.videoId })
        assertEquals("carol", rows.single().feedItem!!.retweetedByHandle)
    }

    @Test fun bookmarks_excludes_textOnlyTwitterStubs_butKeepsQuoteMediaTweets() = runBlocking {
        db.channelDao().upsert(
            listOf(
                ChannelEntity("twitter_alice", name = "Alice", platform = "twitter"),
                ChannelEntity("twitter_bob", name = "Bob", platform = "twitter"),
            ),
        )
        db.feedItemDao().upsert(
            listOf(
                FeedItemEntity(
                    tweetId = "tweet_text_only",
                    authorHandle = "alice",
                    channelId = "twitter_alice",
                    bodyText = "plain text",
                    publishedAt = 10,
                    syncSeq = 1,
                ),
                FeedItemEntity(
                    tweetId = "tweet_quote_media",
                    authorHandle = "bob",
                    channelId = "twitter_bob",
                    quoteMediaJson = """[{"type":"video"}]""",
                    publishedAt = 20,
                    syncSeq = 2,
                ),
            ),
        )
        db.videoDao().upsert(
            listOf(
                VideoEntity(videoId = "tweet_text_only", channelId = "twitter_alice", publishedAt = 10),
                VideoEntity(videoId = "tweet_quote_media", channelId = "twitter_bob", publishedAt = 20),
            ),
        )
        db.bookmarkDao().upsert(BookmarkEntity(videoId = "tweet_text_only", bookmarkedAt = 100, categoryId = 0))
        db.bookmarkDao().upsert(BookmarkEntity(videoId = "tweet_quote_media", bookmarkedAt = 200, categoryId = 0))

        val rows = db.bookmarkReadDao().bookmarksFlow().first()

        assertEquals(listOf("tweet_quote_media"), rows.map { it.bookmark.videoId })
        assertEquals("bob", rows.single().feedItem!!.authorHandle)
    }

    // ─── ChannelReadDao ───────────────────────────────────────────────────────

    @Test fun channels_starredFirst_thenAlphabetical() = runBlocking {
        db.channelDao().upsert(listOf(
            ChannelEntity("twitter_alice",  name = "Alice",   platform = "twitter"),
            ChannelEntity("twitter_bob",    name = "Bob",     platform = "twitter"),
            ChannelEntity("twitter_carol",  name = "Carol",   platform = "twitter"),
            ChannelEntity("twitter_dave",   name = "dave",    platform = "twitter"),
        ))
        db.channelStarDao().upsert(ChannelStarEntity("twitter_carol", starredAt = 10))
        db.channelStarDao().upsert(ChannelStarEntity("twitter_alice", starredAt = 20))
        db.channelFollowDao().upsert(ChannelFollowEntity("twitter_bob", followedAt = 10))

        val rows = db.channelReadDao().allFlow().first()
        assertEquals(
            // Starred block (alice, carol — alpha), then unstarred (Bob, dave — alpha case-insensitive).
            listOf("twitter_alice", "twitter_carol", "twitter_bob", "twitter_dave"),
            rows.map { it.channel.channelId },
        )
        // Followed flag correct on Bob.
        val bob = rows.single { it.channel.channelId == "twitter_bob" }
        assertEquals(1, bob.isFollowed)
        assertEquals(0, bob.isStarred)
    }
}
