package com.screwy.igloo.data

import com.screwy.igloo.data.entity.BookmarkEntity
import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.ChannelFollowEntity
import com.screwy.igloo.data.entity.ChannelProfileEntity
import com.screwy.igloo.data.entity.ChannelStarEntity
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.data.entity.FeedLikeEntity
import com.screwy.igloo.data.entity.FeedRankEntity
import com.screwy.igloo.data.entity.FeedSeenEntity
import com.screwy.igloo.data.entity.MutedAccountEntity
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

/**
 * Feed read DAO — the load-bearing join. Covers the feed surface variants, per-row
 * boolean flag correctness, main-feed anti-joins (feed_seen + muted_accounts for both
 * author and retweeter), and muted-retweeter rescue for rows where `retweeted_by_handle`
 * is null.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class FeedReadDaoTest {

    private lateinit var db: IglooDatabase

    @Before fun setUp() { db = RoomTestSupport.freshDb() }
    @After fun tearDown() { db.close() }

    private fun item(
        tweetId: String,
        authorHandle: String = "alice",
        channelId: String? = "twitter_alice",
        syncSeq: Long = 0,
        publishedAt: Long = 0,
        retweetedByHandle: String? = null,
        contentHash: String? = null,
    ) = FeedItemEntity(
        tweetId = tweetId,
        authorHandle = authorHandle,
        channelId = channelId,
        syncSeq = syncSeq,
        publishedAt = publishedAt,
        retweetedByHandle = retweetedByHandle,
        contentHash = contentHash,
    )

    private fun channel(id: String, name: String = "Alice", platform: String = "twitter") =
        ChannelEntity(channelId = id, name = name, platform = platform, avatarUrl = "https://ex/$id.jpg")

    // ─── Filter variants ─────────────────────────────────────────────────────

    @Test fun feedFlow_unfilteredOrdersBySyncSeqDesc() = runBlocking {
        db.channelDao().upsert(channel("twitter_alice"))
        db.feedItemDao().upsert(listOf(
            item("t1", syncSeq = 1),
            item("t2", syncSeq = 3),
            item("t3", syncSeq = 2),
        ))
        val rows = db.feedReadDao().feedFlow(limit = 10).first()
        assertEquals(listOf("t2", "t3", "t1"), rows.map { it.item.tweetId })
    }

    @Test fun feedFlow_prefersExplicitRankPositionOverSyncSeq() = runBlocking {
        db.channelDao().upsert(channel("twitter_alice"))
        db.feedItemDao().upsert(listOf(
            item("t1", syncSeq = 1),
            item("t2", syncSeq = 3),
            item("t3", syncSeq = 2),
        ))
        db.feedRankDao().upsert(
            listOf(
                FeedRankEntity(tweetId = "t3", rankPosition = 1, snapshotAt = 1),
                FeedRankEntity(tweetId = "t1", rankPosition = 2, snapshotAt = 1),
                FeedRankEntity(tweetId = "t2", rankPosition = 3, snapshotAt = 1),
            )
        )

        val rows = db.feedReadDao().feedFlow(limit = 10).first()
        assertEquals(listOf("t3", "t1", "t2"), rows.map { it.item.tweetId })
    }

    @Test fun feedFlow_keepsUnrankedRowsAfterRankedSnapshotRows() = runBlocking {
        db.channelDao().upsert(channel("twitter_alice"))
        db.feedItemDao().upsert(listOf(
            item("ranked_low_seq", syncSeq = 1),
            item("unranked_high_seq", syncSeq = 99),
            item("ranked_mid_seq", syncSeq = 50),
        ))
        db.feedRankDao().upsert(
            listOf(
                FeedRankEntity(tweetId = "ranked_mid_seq", rankPosition = 1, snapshotAt = 1),
                FeedRankEntity(tweetId = "ranked_low_seq", rankPosition = 2, snapshotAt = 1),
            )
        )

        val rows = db.feedReadDao().feedFlow(limit = 10).first()
        assertEquals(
            listOf("ranked_mid_seq", "ranked_low_seq", "unranked_high_seq"),
            rows.map { it.item.tweetId },
        )
    }

    @Test fun likedFlow_onlyReturnsLiked_orderedByLikedAtDesc() = runBlocking {
        db.channelDao().upsert(channel("twitter_alice"))
        db.feedItemDao().upsert(listOf(item("t1", syncSeq = 1), item("t2", syncSeq = 2), item("t3", syncSeq = 3)))
        db.feedLikeDao().upsert(FeedLikeEntity("t1", likedAt = 500))
        db.feedLikeDao().upsert(FeedLikeEntity("t3", likedAt = 100))

        val rows = db.feedReadDao().likedFlow().first()
        assertEquals(listOf("t1", "t3"), rows.map { it.item.tweetId })
        assertTrue(rows.all { it.isLiked == 1 })
    }

    @Test fun likedFlow_ignoresFeedRankOrdering() = runBlocking {
        db.channelDao().upsert(channel("twitter_alice"))
        db.feedItemDao().upsert(listOf(item("t1", syncSeq = 1), item("t2", syncSeq = 2)))
        db.feedLikeDao().upsert(FeedLikeEntity("t1", likedAt = 100))
        db.feedLikeDao().upsert(FeedLikeEntity("t2", likedAt = 500))
        db.feedRankDao().upsert(listOf(
            FeedRankEntity(tweetId = "t1", rankPosition = 1, snapshotAt = 1),
            FeedRankEntity(tweetId = "t2", rankPosition = 2, snapshotAt = 1),
        ))

        val rows = db.feedReadDao().likedFlow(limit = 10).first()
        assertEquals(listOf("t2", "t1"), rows.map { it.item.tweetId })
    }

    @Test fun likedFlow_includesSeenAndMutedLikedRows() = runBlocking {
        db.channelDao().upsert(channel("twitter_alice"))
        db.feedItemDao().upsert(listOf(
            item("seen", authorHandle = "alice", syncSeq = 1),
            item("muted", authorHandle = "muted_account", syncSeq = 2),
        ))
        db.feedLikeDao().upsert(FeedLikeEntity("seen", likedAt = 100))
        db.feedLikeDao().upsert(FeedLikeEntity("muted", likedAt = 500))
        db.feedSeenDao().upsert(FeedSeenEntity("seen", seenAt = 10))
        db.mutedAccountDao().upsert(MutedAccountEntity("muted_account", mutedAt = 10))

        val rows = db.feedReadDao().likedFlow(limit = 10).first()
        assertEquals(listOf("muted", "seen"), rows.map { it.item.tweetId })
    }

    @Test fun channelFeedFlow_scopedByChannelId() = runBlocking {
        db.channelDao().upsert(listOf(channel("twitter_alice"), channel("twitter_bob", "Bob")))
        db.feedItemDao().upsert(listOf(
            item("t1", channelId = "twitter_alice", syncSeq = 2),
            item("t2", channelId = "twitter_bob",   syncSeq = 3),
            item("t3", channelId = "twitter_alice", syncSeq = 1),
        ))
        val rows = db.feedReadDao().channelFeedFlow("twitter_alice").first()
        assertEquals(listOf("t1", "t3"), rows.map { it.item.tweetId })
    }

    @Test fun channelFeedFlow_ignoresFeedRankOrdering() = runBlocking {
        db.channelDao().upsert(channel("twitter_alice"))
        db.feedItemDao().upsert(listOf(
            item("t1", channelId = "twitter_alice", syncSeq = 1),
            item("t2", channelId = "twitter_alice", syncSeq = 2),
        ))
        db.feedRankDao().upsert(listOf(
            FeedRankEntity(tweetId = "t1", rankPosition = 1, snapshotAt = 1),
            FeedRankEntity(tweetId = "t2", rankPosition = 2, snapshotAt = 1),
        ))

        val rows = db.feedReadDao().channelFeedFlow("twitter_alice", limit = 10).first()
        assertEquals(listOf("t2", "t1"), rows.map { it.item.tweetId })
    }

    @Test fun channelFeedFlow_ordersByPublishedAtDesc() = runBlocking {
        db.channelDao().upsert(channel("twitter_alice"))
        db.feedItemDao().upsert(listOf(
            item("ingested_newer", channelId = "twitter_alice", syncSeq = 3, publishedAt = 100),
            item("published_newer", channelId = "twitter_alice", syncSeq = 1, publishedAt = 500),
            item("middle", channelId = "twitter_alice", syncSeq = 2, publishedAt = 300),
        ))

        val rows = db.feedReadDao().channelFeedFlow("twitter_alice", limit = 10).first()
        assertEquals(listOf("published_newer", "middle", "ingested_newer"), rows.map { it.item.tweetId })
    }

    @Test fun channelFeedFlow_includesSeenAndMutedChannelRows() = runBlocking {
        db.channelDao().upsert(channel("twitter_alice"))
        db.feedItemDao().upsert(listOf(
            item("seen", channelId = "twitter_alice", authorHandle = "alice", publishedAt = 100),
            item("muted", channelId = "twitter_alice", authorHandle = "muted_account", publishedAt = 500),
        ))
        db.feedSeenDao().upsert(FeedSeenEntity("seen", seenAt = 10))
        db.mutedAccountDao().upsert(MutedAccountEntity("muted_account", mutedAt = 10))

        val rows = db.feedReadDao().channelFeedFlow("twitter_alice", limit = 10).first()
        assertEquals(listOf("muted", "seen"), rows.map { it.item.tweetId })
    }

    @Test fun listFlows_excludeGhostRows() = runBlocking {
        db.channelDao().upsert(channel("twitter_alice"))
        db.channelDao().upsert(channel("twitter_user_beta", name = "Beta"))
        db.feedItemDao().upsert(listOf(
            item("t_real", channelId = "twitter_alice", syncSeq = 1, publishedAt = 100),
            FeedItemEntity(
                tweetId = "g_ghost",
                authorHandle = "user_beta",
                channelId = "twitter_user_beta",
                syncSeq = 2,
                publishedAt = 200,
                isGhost = true,
            ),
        ))
        db.feedLikeDao().upsert(FeedLikeEntity("t_real", likedAt = 20))
        db.feedLikeDao().upsert(FeedLikeEntity("g_ghost", likedAt = 30))

        assertEquals(listOf("t_real"), db.feedReadDao().feedFlow(limit = 10).first().map { it.item.tweetId })
        assertEquals(listOf("t_real"), db.feedReadDao().likedFlow(limit = 10).first().map { it.item.tweetId })
        assertEquals(
            emptyList<String>(),
            db.feedReadDao().channelFeedFlow("twitter_user_beta", limit = 10).first().map { it.item.tweetId },
        )
    }

    // ─── Per-row flag correctness ────────────────────────────────────────────

    @Test fun feedFlow_flagsReflectSideTables() = runBlocking {
        db.channelDao().upsert(channel("twitter_alice"))
        db.feedItemDao().upsert(item("t1", syncSeq = 1))
        val before = db.feedReadDao().feedFlow().first().single()
        assertEquals(0, before.isLiked)
        assertEquals(0, before.isBookmarked)
        assertEquals(0, before.channelIsFollowed)
        assertEquals(0, before.channelIsStarred)

        db.feedLikeDao().upsert(FeedLikeEntity("t1", likedAt = 42))
        db.bookmarkDao().upsert(BookmarkEntity(videoId = "t1", bookmarkedAt = 42, categoryId = 1))
        db.channelFollowDao().upsert(ChannelFollowEntity("twitter_alice", 10))
        db.channelStarDao().upsert(ChannelStarEntity("twitter_alice", 10))

        val after = db.feedReadDao().feedFlow().first().single()
        assertEquals(1, after.isLiked)
        assertEquals(42L, after.likedAt)
        assertEquals(1, after.isBookmarked)
        assertEquals(1L, after.bookmarkCategoryId)
        assertEquals(1, after.channelIsFollowed)
        assertEquals(1, after.channelIsStarred)
        assertEquals("Alice", after.channelName)
    }

    @Test fun actionStateFlow_reflectsSideTablesForRowsExcludedFromMainFeed() = runBlocking {
        db.channelDao().upsert(channel("twitter_alice"))
        db.feedItemDao().upsert(
            FeedItemEntity(
                tweetId = "t1",
                authorHandle = "alice",
                channelId = "twitter_alice",
                syncSeq = 1,
                contentHash = "same_media",
            ),
        )
        db.feedItemDao().upsert(
            FeedItemEntity(
                tweetId = "t_sibling",
                authorHandle = "alice",
                channelId = "twitter_alice",
                syncSeq = 2,
                contentHash = "same_media",
            ),
        )
        db.feedSeenDao().upsert(FeedSeenEntity("t1", seenAt = 10))
        db.feedSeenDao().upsert(FeedSeenEntity("t_sibling", seenAt = 11))

        assertEquals(emptyList<String>(), db.feedReadDao().feedFlow(limit = 10).first().map { it.item.tweetId })

        db.feedLikeDao().upsert(FeedLikeEntity("t1", likedAt = 42))
        db.bookmarkDao().upsert(
            BookmarkEntity(
                videoId = "t_sibling",
                bookmarkedAt = 50,
                categoryId = 7,
                customTitle = "saved",
            ),
        )

        val state = db.feedReadDao().actionStateFlow(listOf("t1")).first().single()
        assertEquals("t1", state.tweetId)
        assertEquals(1, state.isLiked)
        assertEquals(42L, state.likedAt)
        assertEquals(1, state.isBookmarked)
        assertEquals(7L, state.bookmarkCategoryId)
        assertEquals("saved", state.bookmarkCustomTitle)
        assertEquals(50L, state.bookmarkedAt)
    }

    @Test fun feedFlow_flagsQuoteAuthorFollowState() = runBlocking {
        db.channelDao().upsert(channel("twitter_alice"))
        db.feedItemDao().upsert(
            FeedItemEntity(
                tweetId = "quote_wrapper",
                authorHandle = "alice",
                channelId = "twitter_alice",
                quoteTweetId = "quoted_tweet",
                quoteAuthorHandle = "@Quote_Author",
                syncSeq = 1,
            )
        )

        val before = db.feedReadDao().feedFlow(limit = 10).first().single()
        assertEquals("twitter_quote_author", before.quoteChannelId)
        assertEquals(0, before.quoteChannelIsFollowed)

        db.channelFollowDao().upsert(ChannelFollowEntity("twitter_quote_author", 10))

        val after = db.feedReadDao().feedFlow(limit = 10).first().single()
        assertEquals("twitter_quote_author", after.quoteChannelId)
        assertEquals(1, after.quoteChannelIsFollowed)
    }

    @Test fun feedFlow_propagatesBookmarkAcrossRetweetContentHashSiblings() = runBlocking {
        db.channelDao().upsert(channel("twitter_alice"))
        db.feedItemDao().upsert(
            listOf(
                FeedItemEntity(
                    tweetId = "tweet_original",
                    sourceHandle = "alice",
                    authorHandle = "alice",
                    channelId = "twitter_alice",
                    contentHash = "shared_content",
                    publishedAt = 10,
                    syncSeq = 1,
                ),
                FeedItemEntity(
                    tweetId = "tweet_repost",
                    sourceHandle = "bob",
                    authorHandle = "alice",
                    channelId = "twitter_alice",
                    isRetweet = true,
                    retweetedByHandle = "bob",
                    contentHash = "shared_content",
                    publishedAt = 20,
                    syncSeq = 2,
                ),
            ),
        )
        db.bookmarkDao().upsert(
            BookmarkEntity(
                videoId = "tweet_repost",
                bookmarkedAt = 42,
                categoryId = 7,
                customTitle = "saved",
            ),
        )

        val rows = db.feedReadDao().feedFlow(limit = 10).first().associateBy { it.item.tweetId }

        assertEquals(1, rows["tweet_repost"]!!.isBookmarked)
        assertEquals(1, rows["tweet_original"]!!.isBookmarked)
        assertEquals(7L, rows["tweet_original"]!!.bookmarkCategoryId)
        assertEquals("saved", rows["tweet_original"]!!.bookmarkCustomTitle)
        assertEquals(42L, rows["tweet_original"]!!.bookmarkedAt)
    }

    @Test fun feedFlow_usesChannelProfileDisplayNameWhenChannelRowMissing() = runBlocking {
        db.channelProfileDao().upsert(
            ChannelProfileEntity(
                channelId = "twitter_author_alpha",
                platform = "twitter",
                handle = "author_alpha",
                displayName = "Display From Profile",
            )
        )
        db.feedItemDao().upsert(
            item(
                tweetId = "t_profile_only",
                authorHandle = "author_alpha",
                channelId = "twitter_author_alpha",
                syncSeq = 1,
            )
        )

        val row = db.feedReadDao().feedFlow().first().single()
        assertEquals("Display From Profile", row.channelName)
    }

    // ─── Main feed head candidates ─────────────────────────────────────────

    @Test fun mainFeedHeadCandidates_orderByRankExcludeMutedAndGhostRowsButIgnoreSeen() = runBlocking {
        db.feedItemDao().upsert(
            listOf(
                item("ranked_two", authorHandle = "alice", syncSeq = 10),
                item("ranked_one", authorHandle = "bob", syncSeq = 20),
                item("unranked", authorHandle = "carol", syncSeq = 99),
                item("seen", authorHandle = "dave", syncSeq = 100),
                item("muted_author", authorHandle = "muted", syncSeq = 101),
                item("muted_retweeter", authorHandle = "erin", syncSeq = 102, retweetedByHandle = "muted_rt"),
                FeedItemEntity(
                    tweetId = "ghost",
                    authorHandle = "ghosted",
                    channelId = "twitter_ghosted",
                    syncSeq = 103,
                    isGhost = true,
                ),
            ),
        )
        db.feedRankDao().upsert(
            listOf(
                FeedRankEntity(tweetId = "ranked_one", rankPosition = 1, snapshotAt = 1),
                FeedRankEntity(tweetId = "ranked_two", rankPosition = 2, snapshotAt = 1),
            ),
        )
        db.feedSeenDao().upsert(FeedSeenEntity("seen", seenAt = 10))
        db.mutedAccountDao().upsert(MutedAccountEntity("muted", mutedAt = 10))
        db.mutedAccountDao().upsert(MutedAccountEntity("muted_rt", mutedAt = 11))

        val rows = db.feedReadDao().mainFeedHeadCandidatesFlow(limit = 10).first()

        assertEquals(listOf("ranked_one", "ranked_two", "seen", "unranked"), rows.map { it.tweetId })
    }

    // ─── Anti-joins ──────────────────────────────────────────────────────────

    @Test fun feedFlow_excludesSeen() = runBlocking {
        db.channelDao().upsert(channel("twitter_alice"))
        db.feedItemDao().upsert(listOf(item("t1", syncSeq = 1), item("t2", syncSeq = 2)))
        db.feedSeenDao().upsert(FeedSeenEntity("t1", seenAt = 10))
        val rows = db.feedReadDao().feedFlow().first()
        assertEquals(listOf("t2"), rows.map { it.item.tweetId })
    }

    @Test fun mainFeed_excludesSeenContentHashSiblings() = runBlocking {
        db.channelDao().upsert(channel("twitter_alice"))
        db.feedItemDao().upsert(
            listOf(
                item("seen_original", syncSeq = 1, contentHash = "same_content"),
                item("unseen_repost", syncSeq = 2, contentHash = "same_content"),
                item("fresh", syncSeq = 3, contentHash = "fresh_content"),
            ),
        )
        db.feedRankDao().upsert(
            listOf(
                FeedRankEntity(tweetId = "unseen_repost", rankPosition = 1, snapshotAt = 1),
                FeedRankEntity(tweetId = "fresh", rankPosition = 2, snapshotAt = 1),
            ),
        )
        db.feedSeenDao().upsert(FeedSeenEntity("seen_original", seenAt = 10))

        val rows = db.feedReadDao().feedFlow(limit = 10).first()
        assertEquals(listOf("fresh"), rows.map { it.item.tweetId })
        val candidates = db.feedReadDao().mainFeedHeadCandidatesFlow(limit = 10).first()
        assertEquals(listOf("unseen_repost", "fresh", "seen_original"), candidates.map { it.tweetId })
    }

    @Test fun feedFlow_excludesMutedAuthor() = runBlocking {
        db.channelDao().upsert(channel("twitter_alice"))
        db.feedItemDao().upsert(listOf(
            item("t1", authorHandle = "alice", syncSeq = 1),
            item("t2", authorHandle = "bob",   syncSeq = 2),
        ))
        db.mutedAccountDao().upsert(MutedAccountEntity("alice", mutedAt = 10))
        val rows = db.feedReadDao().feedFlow().first()
        assertEquals(listOf("t2"), rows.map { it.item.tweetId })
    }

    @Test fun feedFlow_excludesMutedRetweeter() = runBlocking {
        db.channelDao().upsert(channel("twitter_alice"))
        db.feedItemDao().upsert(listOf(
            item("t1", syncSeq = 1, retweetedByHandle = null),
            item("t2", syncSeq = 2, retweetedByHandle = "spammer"),
        ))
        db.mutedAccountDao().upsert(MutedAccountEntity("spammer", mutedAt = 10))
        val rows = db.feedReadDao().feedFlow().first()
        assertEquals(listOf("t1"), rows.map { it.item.tweetId }) // t2 filtered
    }

    @Test fun feedFlow_nullRetweeterNotFilteredWhenMuteListHasOther() = runBlocking {
        db.channelDao().upsert(channel("twitter_alice"))
        db.feedItemDao().upsert(item("t1", syncSeq = 1, retweetedByHandle = null))
        db.mutedAccountDao().upsert(MutedAccountEntity("otheruser", mutedAt = 10))
        val rows = db.feedReadDao().feedFlow().first()
        assertEquals(listOf("t1"), rows.map { it.item.tweetId })
    }
}
