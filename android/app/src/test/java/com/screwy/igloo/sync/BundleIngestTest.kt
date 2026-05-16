package com.screwy.igloo.sync

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.data.entity.BookmarkCategoryEntity
import com.screwy.igloo.data.entity.BookmarkEntity
import com.screwy.igloo.data.entity.BookmarkLabelEntity
import com.screwy.igloo.data.entity.ChannelFollowEntity
import com.screwy.igloo.data.entity.FeedRankEntity
import com.screwy.igloo.data.entity.FeedLikeEntity
import com.screwy.igloo.data.entity.OutboxEntity
import com.screwy.igloo.data.entity.WatchHistoryEntity
import com.screwy.igloo.net.BundleEnvelope
import com.screwy.igloo.outbox.OutboxKind
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.add
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
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
 * Bundle ingest — primary row upserts, attachment upserts, user-state booleans routed
 * to side tables with the preserve-local guard, and the watch_history LWW merge.
 * 02-sync.md §3–§8.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class BundleIngestTest {

    private lateinit var db: IglooDatabase
    private lateinit var ingest: BundleIngest
    private lateinit var guard: PreserveLocalGuard

    @Before fun setUp() {
        db = RoomTestSupport.freshDb()
        ingest = BundleIngest(db, nowMsProvider = { 1_000_000L })
        guard = PreserveLocalGuard(db.outboxDao())
    }

    @After fun tearDown() { db.close() }

    // ─── Primary row ingestion ────────────────────────────────────────────────

    @Test fun feedItem_primary_upserts() = runBlocking {
        val bundle = bundle(kind = "feed_items", primary = buildJsonObject {
            put("tweet_id", "t1")
            put("author_handle", "@alice")
            put("canonical_url", "https://x.com/alice/status/t1")
            put("quote_canonical_url", "https://x.com/bob/status/q1")
            put("body_translation", "hello")
            put("body_source_lang", "Korean")
            put("quote_translation", "quoted hello")
            put("quote_source_lang", "Japanese")
            put("published_at", 12345L)
            put("sync_seq", 10L)
        })
        val result = ingest.ingest(bundle, guard)
        assertEquals(IngestResult.Ok, result)
        val row = db.feedItemDao().getById("t1")
        assertNotNull(row)
        assertEquals("@alice", row!!.authorHandle)
        assertEquals("https://x.com/alice/status/t1", row.canonicalUrl)
        assertEquals("https://x.com/bob/status/q1", row.quoteCanonicalUrl)
        assertEquals("Korean", row.bodySourceLang)
        assertEquals("Japanese", row.quoteSourceLang)
        assertEquals(10L, row.syncSeq)
    }

    @Test fun feedItem_threadContextAttachment_replacesLeafContext() = runBlocking {
        val primary = buildJsonObject {
            put("tweet_id", "leaf")
            put("author_handle", "sample_leaf")
        }
        val withContext = bundle(
            kind = "feed_items",
            primary = primary,
            attachments = buildJsonObject {
                put("feed_thread_context", buildJsonArray {
                    add(buildJsonObject {
                        put("leaf_tweet_id", "leaf")
                        put("root_tweet_id", "root")
                        put("ancestor_tweet_id", "root")
                        put("ancestor_order", 0)
                    })
                })
            },
        )

        assertEquals(IngestResult.Ok, ingest.ingest(withContext, guard))
        val rows = db.feedReadDao().getThreadContexts(listOf("leaf"))
        assertEquals(1, rows.size)
        assertEquals("root", rows.single().rootTweetId)

        val cleared = bundle(
            kind = "feed_items",
            primary = primary,
            attachments = buildJsonObject {
                put("feed_thread_context", buildJsonArray {})
            },
        )
        assertEquals(IngestResult.Ok, ingest.ingest(cleared, guard))
        assertTrue(db.feedReadDao().getThreadContexts(listOf("leaf")).isEmpty())
    }

    @Test fun video_primary_upserts() = runBlocking {
        val bundle = bundle(kind = "videos", primary = buildJsonObject {
            put("video_id", "v1")
            put("channel_id", "c_youtube_1")
            put("media_mode", "slideshow")
            put("display_title", "Server Default")
            put("display_title_casual", "Server Casual")
            put("source_kind", "story")
            put("duration_label", "12:34")
            put("published_at", 42L)
        })
        assertEquals(IngestResult.Ok, ingest.ingest(bundle, guard))
        val row = db.videoDao().getById("v1")
        assertNotNull(row)
        assertEquals("slideshow", row!!.mediaMode)
        assertEquals("Server Default", row.displayTitle)
        assertEquals("Server Casual", row.displayTitleCasual)
        assertEquals("story", row.sourceKind)
        assertEquals("12:34", row.durationLabel)
    }

    @Test fun channel_primary_upserts() = runBlocking {
        val bundle = bundle(kind = "channels", primary = buildJsonObject {
            put("channel_id", "c1")
            put("source_id", "alice")
            put("name", "Alice")
            put("platform", "twitter")
        })
        assertEquals(IngestResult.Ok, ingest.ingest(bundle, guard))
        val row = db.channelDao().getById("c1")
        assertNotNull(row)
        assertEquals("alice", row!!.sourceId)
    }

    @Test fun channelProfile_primary_upserts() = runBlocking {
        val bundle = bundle(kind = "channel_profiles", primary = buildJsonObject {
            put("channel_id", "youtube_UCprofileonly")
            put("platform", "youtube")
            put("handle", "profileonly")
            put("display_name", "Profile Only")
            put("followers", 1200)
            put("followers_label", "1,200")
            put("profile_url", "https://www.youtube.com/@profileonly")
        })
        assertEquals(IngestResult.Ok, ingest.ingest(bundle, guard))
        val row = db.channelProfileDao().getById("youtube_UCprofileonly")
        assertNotNull(row)
        assertEquals("profileonly", row!!.handle)
        assertEquals("Profile Only", row.displayName)
        assertEquals("1,200", row.followersLabel)
        assertEquals("https://www.youtube.com/@profileonly", row.profileUrl)
    }

    // ─── Side-table booleans ──────────────────────────────────────────────────

    @Test fun feedItem_is_liked_true_writes_feed_like() = runBlocking {
        ingest.ingest(
            bundle("feed_items", buildJsonObject {
                put("tweet_id", "t1")
                put("author_handle", "@alice")
                put("published_at", 500L)
            }, userState("""{"version":1,"feed_likes":[{"tweet_id":"t1","liked":true,"liked_at":500}]}""")),
            guard,
        )
        assertTrue(db.feedLikeDao().exists("t1"))
    }

    @Test fun feedItem_is_liked_false_removes_feed_like() = runBlocking {
        db.feedLikeDao().upsert(FeedLikeEntity(tweetId = "t1", likedAt = 5L))
        ingest.ingest(
            bundle("feed_items", buildJsonObject {
                put("tweet_id", "t1")
                put("author_handle", "@alice")
            }, userState("""{"version":1,"feed_likes":[{"tweet_id":"t1","liked":false}]}""")),
            guard,
        )
        assertFalse(db.feedLikeDao().exists("t1"))
    }

    @Test fun channel_is_followed_true_writes_channel_follow() = runBlocking {
        ingest.ingest(
            bundle("channels", buildJsonObject {
                put("channel_id", "c1")
                put("name", "Alice")
                put("platform", "twitter")
            }, userState("""{"version":1,"channel_follows":[{"channel_id":"c1","followed":true,"followed_at":123}]}""")),
            guard,
        )
        assertTrue(db.channelFollowDao().exists("c1"))
    }

    @Test fun channel_channelStarState_writes_channel_star() = runBlocking {
        ingest.ingest(
            bundle("channels", buildJsonObject {
                put("channel_id", "c1")
                put("name", "Alice")
                put("platform", "twitter")
            }, userState("""{"version":1,"channel_stars":[{"channel_id":"c1","starred":true,"starred_at":123}]}""")),
            guard,
        )
        assertTrue(db.channelStarDao().exists("c1"))
    }

    @Test fun feedItem_authorMutedState_updates_mutedAccounts() = runBlocking {
        ingest.ingest(
            bundle("feed_items", buildJsonObject {
                put("tweet_id", "t1")
                put("author_handle", "@alice")
            }, userState("""{"version":1,"muted_accounts":[{"handle":"@alice","muted":true,"muted_at":123}]}""")),
            guard,
        )
        assertTrue(db.mutedAccountDao().exists("@alice"))

        ingest.ingest(
            bundle("feed_items", buildJsonObject {
                put("tweet_id", "t1")
                put("author_handle", "@alice")
            }, userState("""{"version":1,"muted_accounts":[{"handle":"@alice","muted":false}]}""")),
            guard,
        )
        assertFalse(db.mutedAccountDao().exists("@alice"))
    }

    @Test fun feedItem_reposterMutedState_updates_mutedAccounts() = runBlocking {
        ingest.ingest(
            bundle("feed_items", buildJsonObject {
                put("tweet_id", "t1")
                put("author_handle", "@alice")
                put("retweeted_by_handle", "@bob")
            }, userState("""{"version":1,"muted_accounts":[{"handle":"@bob","muted":true,"muted_at":123}]}""")),
            guard,
        )
        assertTrue(db.mutedAccountDao().exists("@bob"))

        ingest.ingest(
            bundle("feed_items", buildJsonObject {
                put("tweet_id", "t1")
                put("author_handle", "@alice")
                put("retweeted_by_handle", "@bob")
            }, userState("""{"version":1,"muted_accounts":[{"handle":"@bob","muted":false}]}""")),
            guard,
        )
        assertFalse(db.mutedAccountDao().exists("@bob"))
    }

    // ─── Preserve-local (02-sync.md §8) ───────────────────────────────────────

    @Test fun feedItem_preserveLocal_blocks_like_write() = runBlocking {
        // Pending `like` outbox row → delta's is_liked=false must NOT delete the side table.
        db.feedLikeDao().upsert(FeedLikeEntity("t1", likedAt = 5L))
        db.outboxDao().insert(pending(kind = OutboxKind.CODE_LIKE, itemId = "t1"))

        ingest.ingest(
            bundle("feed_items", buildJsonObject {
                put("tweet_id", "t1")
                put("author_handle", "@alice")
            }, userState("""{"version":1,"feed_likes":[{"tweet_id":"t1","liked":false}]}""")),
            guard,
        )
        // Preserve-local blocked the delete.
        assertTrue(db.feedLikeDao().exists("t1"))
    }

    @Test fun channel_preserveLocal_blocks_follow_write() = runBlocking {
        db.outboxDao().insert(pending(kind = OutboxKind.CODE_FOLLOW, itemId = "c1"))
        ingest.ingest(
            bundle("channels", buildJsonObject {
                put("channel_id", "c1")
                put("name", "Alice")
                put("platform", "twitter")
            }, userState("""{"version":1,"channel_follows":[{"channel_id":"c1","followed":true}]}""")),
            guard,
        )
        // Preserve-local blocked the write — no channel_follows row created.
        assertFalse(db.channelFollowDao().exists("c1"))
    }

    @Test fun bookmark_delta_preserves_existing_metadata_when_fields_are_omitted() = runBlocking {
        db.bookmarkDao().upsert(
            BookmarkEntity(
                videoId = "t1",
                categoryId = 7L,
                customTitle = "Saved title",
                accountHandles = "alice,bob",
                mediaIndices = "0,2",
                bookmarkedAt = 123L,
            ),
        )

        ingest.ingest(
            bundle("feed_items", buildJsonObject {
                put("tweet_id", "t1")
                put("author_handle", "@alice")
            }, userState("""{"version":1,"bookmarks":[{"video_id":"t1","bookmarked":true}]}""")),
            guard,
        )

        val row = db.bookmarkDao().getById("t1")!!
        assertEquals(7L, row.categoryId)
        assertEquals("Saved title", row.customTitle)
        assertEquals("alice,bob", row.accountHandles)
        assertEquals("0,2", row.mediaIndices)
        assertEquals(123L, row.bookmarkedAt)
    }

    @Test fun bookmark_delta_ignores_display_flag_without_bookmarkedAt() = runBlocking {
        ingest.ingest(
            bundle("feed_items", buildJsonObject {
                put("tweet_id", "t_sibling")
                put("author_handle", "@alice")
            }, userState("""{"version":1,"bookmarks":[{"video_id":"t_sibling","bookmarked":false}]}""")),
            guard,
        )

        assertNull(db.bookmarkDao().getById("t_sibling"))
    }

    // ─── LWW watch_history ─────────────────────────────────────────────────────

    @Test fun video_watchHistory_lww_serverWinsWhenNewer() = runBlocking {
        db.watchHistoryDao().upsert(
            WatchHistoryEntity(
                videoId = "v1",
                playbackPosition = 10.0,
                progressUpdatedAtMs = 100L,
                lastWatched = 100L,
            ),
        )
        ingest.ingest(
            bundle("videos", buildJsonObject {
                put("video_id", "v1")
                put("channel_id", "c1")
            }, userState("""{"version":1,"watch_history":[{"video_id":"v1","playback_position":55.0,"duration":120.0,"progress_updated_at_ms":500,"progress_source":"server","last_watched":500}]}""")),
            guard,
        )
        val row = db.watchHistoryDao().getById("v1")!!
        assertEquals(55.0, row.playbackPosition, 0.01)
        assertEquals(500L, row.progressUpdatedAtMs)
    }

    @Test fun video_watchHistory_lww_localWinsWhenOlder() = runBlocking {
        db.watchHistoryDao().upsert(
            WatchHistoryEntity(
                videoId = "v1",
                playbackPosition = 100.0,
                progressUpdatedAtMs = 5_000L,
                lastWatched = 5_000L,
            ),
        )
        ingest.ingest(
            bundle("videos", buildJsonObject {
                put("video_id", "v1")
                put("channel_id", "c1")
            }, userState("""{"version":1,"watch_history":[{"video_id":"v1","playback_position":20.0,"progress_updated_at_ms":1000,"last_watched":1000}]}""")),
            guard,
        )
        val row = db.watchHistoryDao().getById("v1")!!
        // Server was stale — local unchanged.
        assertEquals(100.0, row.playbackPosition, 0.01)
        assertEquals(5_000L, row.progressUpdatedAtMs)
    }

    @Test fun video_progress_preserveLocal_blocks_watchHistory() = runBlocking {
        db.outboxDao().insert(pending(kind = OutboxKind.CODE_PROGRESS, itemId = "v1"))
        ingest.ingest(
            bundle("videos", buildJsonObject {
                put("video_id", "v1")
                put("channel_id", "c1")
            }, userState("""{"version":1,"watch_history":[{"video_id":"v1","playback_position":55.0,"progress_updated_at_ms":999999,"last_watched":999999}]}""")),
            guard,
        )
        assertNull(db.watchHistoryDao().getById("v1"))
    }

    @Test fun video_momentViewState_writes_moment_view() = runBlocking {
        ingest.ingest(
            bundle("videos", buildJsonObject {
                put("video_id", "v1")
                put("channel_id", "c1")
            }, userState("""{"version":1,"moment_views":[{"video_id":"v1","viewed":true,"viewed_at":123}]}""")),
            guard,
        )
        assertTrue(db.momentViewDao().exists("v1"))
    }

    // ─── Attachment rows ──────────────────────────────────────────────────────

    @Test fun video_repostSources_preserve_server_author_label() = runBlocking {
        ingest.ingest(
            bundle("videos", buildJsonObject {
                put("video_id", "v1")
                put("channel_id", "tiktok_author")
            }, buildJsonObject {
                put(
                    "video_repost_sources",
                    Json.parseToJsonElement(
                        """
                        [
                          {
                            "video_id":"v1",
                            "reposter_channel_id":"tiktok_reposter",
                            "reposter_handle":"reposter",
                            "reposter_display_name":"Ignored Client-Side",
                            "repost_author_label":"Server Label",
                            "first_seen_at_ms":123,
                            "updated_at_ms":124
                          }
                        ]
                        """.trimIndent(),
                    ),
                )
            }),
            guard,
        )

        val rows = db.videoRepostSourceDao().forVideo("v1")
        assertEquals(1, rows.size)
        assertEquals("Server Label", rows.single().repostAuthorLabel)
    }

    @Test fun video_attachments_upsertVideoComments() = runBlocking {
        val attachments = buildJsonObject {
            put("video_comments", Json.parseToJsonElement("""
                [
                  {"video_id":"v1","id":"c1","author":"bob","text":"hi","published_at":1,"thread_order":1,"thread_depth":0,"parent_order":0,"reply_to_author":"","is_creator":true,"like_count_label":"12.3K"},
                  {"video_id":"v1","id":"c2","author":"eve","text":"hey","published_at":2,"thread_order":2,"thread_depth":1,"parent_order":1,"reply_to_author":"bob","is_creator":false,"like_count_label":"10"}
                ]
            """.trimIndent()))
        }
        val bundle = BundleEnvelope(
            primary_kind = "videos",
            primary = buildJsonObject {
                put("video_id", "v1")
                put("channel_id", "c1")
            },
            attachments = attachments,
        )
        assertEquals(IngestResult.Ok, ingest.ingest(bundle, guard))
        val comments = db.videoCommentDao().forVideoFlow("v1")
        // Ordering flow: we can't easily collect here; count by raw query instead.
        // Use DAO: read count via an explicit query.
        // Fallback — check forVideo via flow first collection
        val rows = comments.first()
        assertEquals(2, rows.size)
        assertEquals(listOf("c1", "c2"), rows.map { it.commentId })
        assertEquals(1, rows[0].threadOrder)
        assertEquals(0, rows[0].threadDepth)
        assertEquals(0, rows[0].parentOrder)
        assertEquals("", rows[0].replyToAuthor)
        assertEquals(true, rows[0].isCreator)
        assertEquals("12.3K", rows[0].likeCountLabel)
        assertEquals(2, rows[1].threadOrder)
        assertEquals(1, rows[1].threadDepth)
        assertEquals(1, rows[1].parentOrder)
        assertEquals("bob", rows[1].replyToAuthor)
        assertEquals(false, rows[1].isCreator)
        assertEquals("10", rows[1].likeCountLabel)
    }

    @Test fun video_attachments_upsertSponsorBlockPayloads_fromServerWireShape() = runBlocking {
        val attachments = buildJsonObject {
            put("sponsorblock_segments", Json.parseToJsonElement("""
                [
                  {"start":12.5,"end":25.0,"category":"sponsor"}
                ]
            """.trimIndent()))
            put("sponsorblock_checked", Json.parseToJsonElement("""
                {"video_id":"v1","checked_at_ms":123456,"video_age_at_check":"fresh"}
            """.trimIndent()))
        }
        val bundle = BundleEnvelope(
            primary_kind = "videos",
            primary = buildJsonObject {
                put("video_id", "v1")
                put("channel_id", "c1")
            },
            attachments = attachments,
        )

        assertEquals(IngestResult.Ok, ingest.ingest(bundle, guard))

        val segments = db.sponsorBlockSegmentDao().forVideo("v1")
        assertEquals(1, segments.size)
        assertEquals(12.5, segments.single().startTime, 0.001)
        assertEquals(25.0, segments.single().endTime, 0.001)
        assertEquals("sponsor", segments.single().category)

        val checked = db.sponsorBlockCheckedDao().forVideo("v1")
        assertNotNull(checked)
        assertEquals(123456L, checked!!.checkedAt)
        assertEquals("fresh", checked.videoAgeAtCheck)
    }

    @Test fun channel_attachments_upsertChannelProfile() = runBlocking {
        val bundle = BundleEnvelope(
            primary_kind = "channels",
            primary = buildJsonObject {
                put("channel_id", "twitter_alice")
                put("source_id", "alice")
                put("name", "Alice")
                put("platform", "twitter")
            },
            attachments = buildJsonObject {
                put("channel_profile", Json.parseToJsonElement("""
                    {
                      "channel_id":"twitter_alice",
                      "platform":"twitter",
                      "handle":"alice",
                      "display_name":"Alice Doe",
                      "bio":"bio text",
                      "website":"https://example.com",
                      "followers":123400,
                      "followers_label":"123.4K",
                      "following":42,
                      "following_label":"42",
                      "verified":true,
                      "verified_type":"business",
                      "protected":false,
                      "avatar_url":"https://example.com/avatar.jpg",
                      "banner_url":"https://example.com/banner.jpg"
                    }
                """.trimIndent()))
            },
        )

        assertEquals(IngestResult.Ok, ingest.ingest(bundle, guard))

        val profile = db.channelProfileDao().getById("twitter_alice")
        assertNotNull(profile)
        assertEquals("alice", profile!!.handle)
        assertEquals("Alice Doe", profile.displayName)
        assertEquals("bio text", profile.bio)
        assertEquals("https://example.com", profile.website)
        assertEquals(123400, profile.followers)
        assertEquals("123.4K", profile.followersLabel)
        assertEquals(42, profile.following)
        assertEquals("42", profile.followingLabel)
        assertTrue(profile.verified)
        assertEquals("business", profile.verifiedType)
    }

    @Test fun feedRank_replacesSnapshotAndSkipsStaleSnapshots() = runBlocking {
        db.feedRankDao().upsert(listOf(FeedRankEntity("old", rankPosition = 1, snapshotAt = 200L)))

        assertEquals(
            IngestResult.Ok,
            ingest.ingest(
                bundle("feed_rank", buildJsonObject {
                    put("snapshot_at", 100L)
                    put("row_count", 1)
                    put("rows", Json.parseToJsonElement("""
                        [{"tweet_id":"stale","rank_position":1}]
                    """.trimIndent()))
                }),
                guard,
            ),
        )
        assertEquals(1, db.feedRankDao().count())
        assertEquals(200L, db.feedRankDao().currentSnapshotAt())

        assertEquals(
            IngestResult.Ok,
            ingest.ingest(
                bundle("feed_rank", buildJsonObject {
                    put("snapshot_at", 300L)
                    put("row_count", 2)
                    put("rows", Json.parseToJsonElement("""
                        [
                          {"tweet_id":"new_one","rank_position":1},
                          {"tweet_id":"new_two","rank_position":2}
                        ]
                    """.trimIndent()))
                }),
                guard,
            ),
        )
        assertEquals(2, db.feedRankDao().count())
        assertEquals(300L, db.feedRankDao().currentSnapshotAt())
    }

    @Test fun feedRank_newerEmptySnapshotClearsRows() = runBlocking {
        db.feedRankDao().upsert(listOf(FeedRankEntity("old", rankPosition = 1, snapshotAt = 200L)))

        assertEquals(
            IngestResult.Ok,
            ingest.ingest(
                bundle("feed_rank", buildJsonObject {
                    put("snapshot_at", 300L)
                    put("row_count", 0)
                    put("rows", Json.parseToJsonElement("[]"))
                }),
                guard,
            ),
        )

        assertEquals(0, db.feedRankDao().count())
        assertEquals(0L, db.feedRankDao().currentSnapshotAt())
    }


    @Test fun feedRank_ignoresOversizedAndMismatchedPayloads() = runBlocking {
        db.feedRankDao().upsert(listOf(FeedRankEntity("old", rankPosition = 1, snapshotAt = 200L)))

        val tooManyRows = (1..5001).joinToString(prefix = "[", postfix = "]") { i ->
            "{\"tweet_id\":\"row_$i\",\"rank_position\":$i}"
        }
        assertEquals(
            IngestResult.Ok,
            ingest.ingest(
                bundle("feed_rank", buildJsonObject {
                    put("snapshot_at", 300L)
                    put("row_count", 5001)
                    put("rows", Json.parseToJsonElement(tooManyRows))
                }),
                guard,
            ),
        )
        assertEquals(1, db.feedRankDao().count())
        assertEquals(200L, db.feedRankDao().currentSnapshotAt())

        assertEquals(
            IngestResult.Ok,
            ingest.ingest(
                bundle("feed_rank", buildJsonObject {
                    put("snapshot_at", 400L)
                    put("row_count", 2)
                    put("rows", Json.parseToJsonElement("""
                        [{"tweet_id":"one","rank_position":1}]
                    """.trimIndent()))
                }),
                guard,
            ),
        )
        assertEquals(1, db.feedRankDao().count())
        assertEquals(200L, db.feedRankDao().currentSnapshotAt())
    }

    @Test fun bookmarkMetadata_replacesSyncedCategoriesAndLabels() = runBlocking {
        db.bookmarkCategoryDao().upsert(BookmarkCategoryEntity(categoryId = 3L, name = "old", createdAt = 1L))
        db.bookmarkCategoryDao().upsert(BookmarkCategoryEntity(categoryId = -1L, name = "pending", createdAt = 2L))
        db.bookmarkCategoryDao().upsert(BookmarkCategoryEntity(categoryId = -2L, name = "server", createdAt = 3L))
        db.bookmarkLabelDao().upsert(BookmarkLabelEntity(label = "stale", syncedAt = 1L))

        assertEquals(
            IngestResult.Ok,
            ingest.ingest(
                bundle("bookmark_metadata", buildJsonObject {
                    put("version", 1)
                    put("snapshot_at", 500L)
                    put(
                        "categories",
                        Json.parseToJsonElement(
                            """
                            [
                              {"category_id":7,"name":"server","archive_path":"/archive/server","created_at":100},
                              {"category_id":9,"name":"later","archive_path":"","created_at":200}
                            ]
                            """.trimIndent(),
                        ),
                    )
                    put(
                        "labels",
                        Json.parseToJsonElement(
                            """
                            [
                              {"label":" saved "},
                              {"label":"saved"},
                              {"label":"other"}
                            ]
                            """.trimIndent(),
                        ),
                    )
                }),
                guard,
            ),
        )

        val categories = db.bookmarkCategoryDao().all()
        assertEquals(listOf(-1L, 7L, 9L), categories.map { it.categoryId })
        assertEquals(listOf("pending", "server", "later"), categories.map { it.name })
        assertEquals("/archive/server", categories.first { it.categoryId == 7L }.archivePath)
        assertEquals(listOf("other", "saved"), db.bookmarkLabelDao().all().map { it.label })
        assertEquals(listOf(500L, 500L), db.bookmarkLabelDao().all().map { it.syncedAt })
    }

    // ─── Bad payloads ─────────────────────────────────────────────────────────

    @Test fun unknownPrimaryKind_returnsSkip() = runBlocking {
        val bundle = bundle("unknown_kind", buildJsonObject { put("x", "y") })
        val result = ingest.ingest(bundle, guard)
        assertTrue("got $result", result is IngestResult.UnknownKind)
    }

    @Test fun malformedPrimary_returnsParseFailure() = runBlocking {
        // Missing required `author_handle` field (not nullable in FeedItemEntity).
        val bundle = bundle("feed_items", buildJsonObject { put("tweet_id", "t1") })
        val result = ingest.ingest(bundle, guard)
        assertTrue("got $result", result is IngestResult.ParseFailure)
    }

    @Test fun batchIngest_keepsSuccessfulSiblingsAroundMalformedBundle() = runBlocking {
        val results = ingest.ingestBatch(
            listOf(
                bundle("feed_items", buildJsonObject {
                    put("tweet_id", "t1")
                    put("author_handle", "@alice")
                }),
                // Missing required `author_handle` field (not nullable in FeedItemEntity).
                bundle("feed_items", buildJsonObject { put("tweet_id", "bad") }),
                bundle("feed_items", buildJsonObject {
                    put("tweet_id", "t2")
                    put("author_handle", "@bob")
                }),
            ),
            guard,
        )

        assertEquals(3, results.size)
        assertEquals(IngestResult.Ok, results[0])
        assertTrue("got ${results[1]}", results[1] is IngestResult.ParseFailure)
        assertEquals(IngestResult.Ok, results[2])
        assertNotNull(db.feedItemDao().getById("t1"))
        assertNull(db.feedItemDao().getById("bad"))
        assertNotNull(db.feedItemDao().getById("t2"))
    }

    // ─── Helpers ───────────────────────────────────────────────────────────────

    private fun bundle(kind: String, primary: JsonObject, attachments: JsonObject? = null): BundleEnvelope =
        BundleEnvelope(primary_kind = kind, primary = primary, attachments = attachments)

    private fun userState(raw: String): JsonObject =
        buildJsonObject { put("user_state", Json.parseToJsonElement(raw)) }

    private fun pending(kind: String, itemId: String? = null, field: String? = null): OutboxEntity =
        OutboxEntity(
            kind = kind,
            itemId = itemId,
            field = field,
            payloadJson = "{}",
            state = "pending",
            createdAtMs = 1L,
        )
}
