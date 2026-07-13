package com.screwy.igloo.sync

import androidx.test.core.app.ApplicationProvider
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.data.entity.AndroidSyncHeadEntity
import com.screwy.igloo.data.entity.AndroidSyncStateEntity
import com.screwy.igloo.data.entity.ChannelSettingEntity
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.data.entity.FeedLikeEntity
import com.screwy.igloo.data.entity.FeedSeenEntity
import com.screwy.igloo.data.entity.MomentsCursorEntity
import com.screwy.igloo.data.entity.OutboxEntity
import com.screwy.igloo.log.InMemoryLogSink
import com.screwy.igloo.log.Logger
import com.screwy.igloo.media.ForegroundPromoter
import com.screwy.igloo.net.AndroidSyncApi
import com.screwy.igloo.net.AndroidSyncAssetDto
import com.screwy.igloo.net.AndroidSyncChangeDto
import com.screwy.igloo.net.AndroidSyncPageResponse
import com.screwy.igloo.net.AndroidSyncRetentionRequest
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.net.iglooJson
import com.screwy.igloo.outbox.OutboxKind
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.MockRequestHandleScope
import io.ktor.client.engine.mock.respond
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.http.ContentType
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import io.ktor.serialization.kotlinx.json.json
import java.io.IOException
import java.util.Collections
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.encodeToJsonElement
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.put
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class AndroidSyncMirrorTest {
    @get:Rule val temporaryFolder = TemporaryFolder()

    private lateinit var db: IglooDatabase
    private lateinit var scope: CoroutineScope
    private lateinit var client: HttpClient
    private lateinit var logger: Logger
    private var nowMs = 1_000_000_000L

    @Before
    fun setUp() {
        db = RoomTestSupport.freshDb()
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        val prefs = PreferencesRepo(db.preferenceDao(), scope, nowMsProvider = { nowMs })
        logger = Logger(prefs, InMemoryLogSink(), scope, nowMsProvider = { nowMs })
    }

    @After
    fun tearDown() {
        if (::client.isInitialized) client.close()
        scope.cancel()
        db.close()
    }

    @Test
    fun flatBootstrapPageAppliesDependenciesBeforeOpaqueCursorCommit() = runBlocking {
        val requests = mutableListOf<String>()
        val changes =
            listOf(
                assetChange(missingAsset("sample_asset_post", "sample_post")),
                assetChange(
                    missingAsset(
                        "sample_asset_retweeter",
                        "sample_retweeter",
                        ownerKind = "channel",
                        assetKind = "avatar",
                    )
                ),
                assetChange(
                    missingAsset(
                        "sample_asset_reposter",
                        "sample_reposter",
                        ownerKind = "channel",
                        assetKind = "avatar",
                    )
                ),
                assetChange(
                    missingAsset(
                        "sample_asset_comment_raw",
                        "youtube_UCsample_raw",
                        ownerKind = "comment_author",
                        assetKind = "avatar",
                        bucket = "youtube",
                    )
                ),
                assetChange(
                    missingAsset(
                        "sample_asset_comment_prefixed",
                        "youtube_UCsample_prefixed",
                        ownerKind = "comment_author",
                        assetKind = "avatar",
                        bucket = "youtube",
                    )
                ),
                profileOnlyChannelChange("sample_retweeter"),
                profileOnlyChannelChange("sample_reposter"),
                feedChange("sample_post"),
                retweetSourcesChange("hash-sample_post", "sample_retweeter"),
                videoChange(
                    "sample_video",
                    nowMs,
                    nowMs,
                    reposterChannelId = "sample_reposter",
                    commentAuthorIds = listOf("UCsample_raw", "youtube_UCsample_prefixed"),
                ),
                feedLikeChange("sample_post"),
            )
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/bootstrap" -> {
                    requests += "bootstrap:${request.url.parameters["after"]}"
                    respondJson(page(changes, "cursor-1"))
                }
                "/api/android/sync/changes" -> {
                    requests += "changes:${request.url.parameters["after"]}"
                    assertRetentionQuery(request.url.parameters.entries().associate { it.key to it.value.single() })
                    respondJson(page(emptyList(), "cursor-1"))
                }
                "/api/android/sync/health" -> respondOk()
                else -> error("Unexpected request ${request.url}")
            }
        }

        buildMirror(engine).syncOnce()

        assertNotNull(db.feedItemDao().getById("sample_post"))
        assertNotNull(db.videoDao().getById("sample_video"))
        assertEquals(1, db.retweetSourceDao().countForContentHash("hash-sample_post"))
        assertEquals(
            listOf("sample_reposter"),
            db.videoRepostSourceDao().forVideo("sample_video").map { it.reposterChannelId },
        )
        assertEquals(
            setOf("youtube_UCsample_raw", "youtube_UCsample_prefixed"),
            db.videoCommentDao().forVideoFlow("sample_video").first().mapNotNull { it.authorId }.toSet(),
        )
        assertNotNull(db.channelProfileDao().getById("sample_retweeter"))
        assertNotNull(db.channelProfileDao().getById("sample_reposter"))
        assertTrue(db.feedLikeDao().exists("sample_post"))
        listOf(
            "sample_asset_post",
            "sample_asset_retweeter",
            "sample_asset_reposter",
            "sample_asset_comment_raw",
            "sample_asset_comment_prefixed",
        ).forEach { assertNotNull(db.androidSyncDao().asset(it)) }
        assertEquals(11, db.androidSyncDao().headCount())
        assertEquals("cursor-1", db.androidSyncDao().syncState()?.cursor)
        assertEquals(listOf("bootstrap:null", "changes:cursor-1"), requests)
    }

    @Test
    fun assetDrainBoundsRetriesAndReturnsChangedMetadataPromptly() = runBlocking {
        db.androidSyncDao().upsertAsset(readyAsset("sample_retry_asset"))
        var attempts = 0
        val retryEngine = MockEngine {
            attempts++
            assertEquals(1, attempts)
            respond("", HttpStatusCode.ServiceUnavailable)
        }
        buildAssetDrainer(retryEngine) {
            nowMs += 31_000
            nowMs
        }.drain()
        assertEquals(1, attempts)

        db.androidSyncDao().deleteAsset("sample_retry_asset")
        repeat(65) { index ->
            db.androidSyncDao().upsertAsset(readyAsset("sample_changed_asset_%03d".format(index)))
        }
        val requested = Collections.synchronizedList(mutableListOf<String>())
        val changedEngine = MockEngine { request ->
            val assetId = request.url.encodedPath.substringAfter("/assets/").substringBefore('/')
            requested += assetId
            val status =
                if (assetId == "sample_changed_asset_000") HttpStatusCode.NotFound
                else HttpStatusCode.ServiceUnavailable
            respond("", status)
        }

        val failure = runCatching { buildAssetDrainer(changedEngine) { nowMs }.drain() }.exceptionOrNull()

        assertTrue(failure is AndroidSyncAssetChangedException)
        assertEquals(64, requested.size)
        assertTrue("sample_changed_asset_064" !in requested)
    }

    @Test
    fun dependencyReplacingUpsertCollectsDepartedIdentitiesAndAssets() = runBlocking {
        val initial =
            listOf(
                assetChange(
                    missingAsset(
                        "sample_asset_reposter",
                        "sample_reposter",
                        ownerKind = "channel",
                        assetKind = "avatar",
                    )
                ),
                assetChange(
                    missingAsset(
                        "sample_asset_comment",
                        "youtube_UCsample_commenter",
                        ownerKind = "comment_author",
                        assetKind = "avatar",
                        bucket = "youtube",
                    )
                ),
                profileOnlyChannelChange("sample_reposter"),
                videoChange(
                    "sample_video",
                    nowMs,
                    nowMs,
                    reposterChannelId = "sample_reposter",
                    commentAuthorIds = listOf("UCsample_commenter"),
                ),
            )
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/bootstrap" -> respondJson(page(initial, "cursor-initial"))
                "/api/android/sync/changes" ->
                    respondJson(
                        page(
                            listOf(
                                videoChange(
                                    "sample_video",
                                    nowMs,
                                    nowMs,
                                    reposterChannelId = null,
                                )
                            ),
                            "cursor-updated",
                        )
                    )
                "/api/android/sync/health" -> respondOk()
                else -> error("Unexpected request ${request.url}")
            }
        }

        buildMirror(engine).syncOnce()

        assertTrue(db.videoRepostSourceDao().forVideo("sample_video").isEmpty())
        assertNull(db.channelProfileDao().getById("sample_reposter"))
        assertNull(db.androidSyncDao().asset("sample_asset_reposter"))
        assertNull(db.androidSyncDao().asset("sample_asset_comment"))
    }

    @Test
    fun interruptedBootstrapResumesWithoutHidingCanonicalRows() = runBlocking {
        db.feedItemDao().upsert(FeedItemEntity(tweetId = "sample_old_post"))
        db.androidSyncDao().upsertHead(AndroidSyncHeadEntity("feed", "sample_old_post", "feed", nowMs))
        db.androidSyncDao().upsertAsset(
            readyAsset("sample_old_asset").copy(ownerId = "sample_old_owner")
        )
        var bootstrapCalls = 0
        val firstEngine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/bootstrap" -> {
                    bootstrapCalls++
                    if (request.url.parameters["after"] == null) {
                        respondJson(page(listOf(feedChange("sample_new_post")), "bootstrap-token", end = false))
                    } else {
                        throw IOException("interrupted")
                    }
                }
                else -> error("Unexpected request ${request.url}")
            }
        }

        assertNotNull(runCatching { buildMirror(firstEngine).syncOnce() }.exceptionOrNull())
        assertEquals("bootstrap", db.androidSyncDao().syncState()?.mode)
        assertEquals("bootstrap-token", db.androidSyncDao().syncState()?.cursor)
        assertNotNull(db.feedItemDao().getById("sample_old_post"))
        assertNotNull(db.feedItemDao().getById("sample_new_post"))
        assertNotNull(db.androidSyncDao().asset("sample_old_asset"))

        var resumedAfter: String? = null
        val secondEngine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/bootstrap" -> {
                    resumedAfter = request.url.parameters["after"]
                    respondJson(page(emptyList(), "cursor-2"))
                }
                "/api/android/sync/changes" -> respondJson(page(emptyList(), "cursor-2"))
                "/api/android/sync/health" -> respondOk()
                else -> error("Unexpected request ${request.url}")
            }
        }

        buildMirror(secondEngine).syncOnce()

        assertEquals(2, bootstrapCalls)
        assertEquals("bootstrap-token", resumedAfter)
        assertNull(db.feedItemDao().getById("sample_old_post"))
        assertNotNull(db.feedItemDao().getById("sample_new_post"))
        assertEquals("cursor-2", db.androidSyncDao().syncState()?.cursor)
    }

    @Test
    fun resetBootstrapSweepsAbsentOwnersAndRestoresPendingState() = runBlocking {
        db.androidSyncDao().upsertSyncState(changesState("old-cursor"))
        db.feedItemDao().upsert(FeedItemEntity(tweetId = "sample_deleted_post"))
        db.feedItemDao().upsert(FeedItemEntity(tweetId = "sample_existing_post"))
        db.androidSyncDao().upsertHead(AndroidSyncHeadEntity("feed", "sample_deleted_post", "feed", nowMs))
        db.androidSyncDao().upsertHead(AndroidSyncHeadEntity("feed", "sample_existing_post", "feed", nowMs))
        db.feedLikeDao().upsert(FeedLikeEntity("sample_existing_post", nowMs))
        db.feedLikeDao().upsert(FeedLikeEntity("sample_rejected_post", nowMs))
        db.momentsCursorDao()
            .upsert(MomentsCursorEntity("stories", "sample_story", updatedAtMs = nowMs))
        db.channelSettingDao()
            .upsert(
                ChannelSettingEntity(
                    channelId = "sample_channel",
                    mediaOnly = 1,
                    maxVideos = 7,
                    updatedAt = nowMs,
                )
            )
        db.outboxDao().insert(
            OutboxEntity(
                kind = OutboxKind.CODE_LIKE,
                itemId = "sample_existing_post",
                payloadJson = """{"tweet_id":"sample_existing_post","action":"set","updated_at_ms":$nowMs}""",
                createdAtMs = nowMs,
            )
        )
        db.outboxDao().insert(
            OutboxEntity(
                kind = OutboxKind.CODE_CHANNEL_SETTING,
                itemId = "sample_channel",
                field = "max_videos",
                payloadJson =
                    """{"channel_id":"sample_channel","field":"max_videos","value":7,"updated_at_ms":$nowMs}""",
                createdAtMs = nowMs,
            )
        )
        var resetSent = false
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/changes" -> {
                    if (!resetSent) {
                        resetSent = true
                        respond(
                            """{"ok":false,"error_code":"sync_reset_required","error_message":"reset"}""",
                            HttpStatusCode.Conflict,
                            jsonHeaders(),
                        )
                    } else {
                        respondJson(page(emptyList(), "cursor-reset"))
                    }
                }
                "/api/android/sync/bootstrap" ->
                    respondJson(
                        page(
                            listOf(
                                feedChange("sample_existing_post"),
                                channelSettingChange("sample_channel", mediaOnly = 0, maxVideos = 5),
                            ),
                            "cursor-reset",
                        )
                    )
                "/api/android/sync/health" -> respondOk()
                else -> error("Unexpected request ${request.url}")
            }
        }

        buildMirror(engine).syncOnce()

        assertNull(db.feedItemDao().getById("sample_deleted_post"))
        assertNotNull(db.feedItemDao().getById("sample_existing_post"))
        assertTrue(db.feedLikeDao().exists("sample_existing_post"))
        assertFalse(db.feedLikeDao().exists("sample_rejected_post"))
        assertEquals("sample_story", db.momentsCursorDao().get("stories")?.videoId)
        assertEquals(0, db.channelSettingDao().getById("sample_channel")?.mediaOnly)
        assertEquals(7, db.channelSettingDao().getById("sample_channel")?.maxVideos)
        assertEquals(2, db.outboxDao().countByState("pending"))
    }

    @Test
    fun protectedTombstoneBecomesCollectableAfterProtectionIsRemoved() = runBlocking {
        db.androidSyncDao().upsertSyncState(changesState("cursor-a"))
        db.feedItemDao().upsert(FeedItemEntity(tweetId = "sample_bookmark_post"))
        db.feedLikeDao().upsert(FeedLikeEntity("sample_bookmark_post", nowMs))
        db.androidSyncDao().upsertHead(AndroidSyncHeadEntity("feed", "sample_bookmark_post", "feed", nowMs))
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/changes" ->
                    respondJson(page(listOf(deleteChange("feed", "sample_bookmark_post")), "cursor-b"))
                "/api/android/sync/health" -> respondOk()
                else -> error("Unexpected request ${request.url}")
            }
        }
        val mirror = buildMirror(engine)

        mirror.syncOnce()

        assertNotNull(db.feedItemDao().getById("sample_bookmark_post"))
        assertTrue(db.androidSyncDao().headIds("feed").isEmpty())

        db.feedLikeDao().delete("sample_bookmark_post")
        mirror.prune()

        assertNull(db.feedItemDao().getById("sample_bookmark_post"))
    }

    @Test
    fun rootEffectiveRecencyKeepsOldReplyAncestorAndRepostedVideo() = runBlocking {
        val recentRoot = nowMs
        val oldPublished = nowMs - 100L * DAY_MS
        val changes =
            listOf(
                feedChange("sample_ancestor", publishedAt = oldPublished, retainAt = recentRoot),
                feedChange(
                    "sample_reply",
                    replyTo = "sample_ancestor",
                    publishedAt = recentRoot,
                    retainAt = recentRoot,
                ),
                videoChange("sample_reposted_video", oldPublished, recentRoot),
            )
        val engine = bootstrapEngine(changes, "cursor-retained")
        val mirror = buildMirror(engine)

        mirror.syncOnce()

        assertNotNull(db.feedItemDao().getById("sample_ancestor"))
        assertNotNull(db.videoDao().getById("sample_reposted_video"))
        val contexts = db.feedReadDao().getThreadContexts(listOf("sample_reply"))
        assertEquals(listOf("sample_ancestor"), contexts.map { it.ancestorTweetId })

        nowMs += 8L * DAY_MS
        mirror.prune()

        assertNull(db.feedItemDao().getById("sample_ancestor"))
        assertNull(db.feedItemDao().getById("sample_reply"))
        assertNull(db.videoDao().getById("sample_reposted_video"))
    }

    @Test
    fun selectionChangeFinishesChangeStreamBeforeBootstrap() = runBlocking {
        db.androidSyncDao().upsertSyncState(changesState("cursor-a"))
        db.feedSeenDao().upsert(FeedSeenEntity("sample_post", nowMs))
        val order = mutableListOf<String>()
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/changes" -> {
                    val after = request.url.parameters["after"]
                    order += "changes:$after"
                    when (after) {
                        "cursor-a" ->
                            respondJson(
                                page(
                                    listOf(channelFollowChange("sample_channel"), channelChange("sample_channel")),
                                    "cursor-b",
                                    end = false,
                                )
                            )
                        "cursor-b" ->
                            respondJson(
                                page(
                                    listOf(deleteChange("feed_seen", "sample_post")),
                                    "cursor-c",
                                )
                            )
                        "cursor-d" -> respondJson(page(emptyList(), "cursor-d"))
                        else -> error("Unexpected changes cursor $after")
                    }
                }
                "/api/android/sync/bootstrap" -> {
                    order += "bootstrap:${request.url.parameters["after"]}"
                    respondJson(
                        page(
                            listOf(channelFollowChange("sample_channel"), channelChange("sample_channel")),
                            "cursor-d",
                        )
                    )
                }
                "/api/android/sync/health" -> respondOk()
                else -> error("Unexpected request ${request.url}")
            }
        }

        buildMirror(engine).syncOnce()

        assertEquals(
            listOf("changes:cursor-a", "changes:cursor-b", "bootstrap:null", "changes:cursor-d"),
            order,
        )
        assertNull(db.feedSeenDao().getById("sample_post"))
        assertTrue(db.channelFollowDao().exists("sample_channel"))
        assertNotNull(db.channelDao().getById("sample_channel"))
    }

    @Test
    fun narrowingRepostSettingDoesNotBootstrap() = runBlocking {
        db.androidSyncDao().upsertSyncState(changesState("cursor-a"))
        val requests = mutableListOf<String>()
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/changes" -> {
                    requests += "changes:${request.url.parameters["after"]}"
                    respondJson(
                        page(
                            listOf(
                                channelSettingChange(
                                    "sample_channel",
                                    mediaOnly = 0,
                                    maxVideos = 5,
                                    includeReposts = 0,
                                )
                            ),
                            "cursor-b",
                        )
                    )
                }
                "/api/android/sync/health" -> respondOk()
                else -> error("Unexpected request ${request.url}")
            }
        }

        buildMirror(engine).syncOnce()

        assertEquals(listOf("changes:cursor-a"), requests)
        assertEquals(0, db.channelSettingDao().getById("sample_channel")?.includeReposts)
        assertFalse(requireNotNull(db.androidSyncDao().syncState()).bootstrapRequired)
    }

    private fun buildMirror(engine: MockEngine): AndroidSyncMirror {
        if (::client.isInitialized) client.close()
        client =
            HttpClient(engine) {
                expectSuccess = false
                install(ContentNegotiation) { json(iglooJson) }
            }
        val reachability =
            Reachability(
                scope = scope,
                probe = { true },
                foregroundFlow = MutableSharedFlow(extraBufferCapacity = 1),
            )
        val baseUrlProvider = ServerBaseUrlProvider { BASE_URL }
        return AndroidSyncMirror(
            db = db,
            dao = db.androidSyncDao(),
            api = AndroidSyncApi(client, baseUrlProvider::baseUrl),
            client = client,
            baseUrlProvider = baseUrlProvider,
            reachability = reachability,
            foregroundPromoter =
                ForegroundPromoter(
                    context = ApplicationProvider.getApplicationContext(),
                    logger = logger,
                    startForegroundService = {},
                    stopForegroundService = {},
                ),
            mediaRoot = temporaryFolder.root,
            logger = logger,
            retentionProvider = { RETENTION },
            serverNowMsProvider = { nowMs },
            metadataRetryDelaysMs = emptyList(),
        )
    }

    private fun buildAssetDrainer(
        engine: MockEngine,
        nowMsProvider: () -> Long,
    ): AndroidSyncAssetDrainer {
        if (::client.isInitialized) client.close()
        client = HttpClient(engine) { expectSuccess = false }
        val reachability =
            Reachability(
                scope = scope,
                probe = { true },
                foregroundFlow = MutableSharedFlow(extraBufferCapacity = 1),
            )
        return AndroidSyncAssetDrainer(
            dao = db.androidSyncDao(),
            client = client,
            baseUrlProvider = ServerBaseUrlProvider { BASE_URL },
            reachability = reachability,
            foregroundPromoter =
                ForegroundPromoter(
                    context = ApplicationProvider.getApplicationContext(),
                    logger = logger,
                    startForegroundService = {},
                    stopForegroundService = {},
                ),
            mediaRoot = temporaryFolder.root,
            logger = logger,
            nowMsProvider = nowMsProvider,
        )
    }

    private fun readyAsset(id: String) =
        AndroidSyncAssetEntity(
            assetId = id,
            assetKind = "post_media",
            ownerId = "sample_post",
            ownerKind = "tweet",
            bucket = "feed",
            contentType = "image/jpeg",
            sizeBytes = 1,
            sha256 = "0".repeat(64),
            revision = 1,
        )

    private fun bootstrapEngine(changes: List<AndroidSyncChangeDto>, cursor: String) =
        MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/bootstrap" -> respondJson(page(changes, cursor))
                "/api/android/sync/changes" -> respondJson(page(emptyList(), cursor))
                "/api/android/sync/health" -> respondOk()
                else -> error("Unexpected request ${request.url}")
            }
        }

    private fun changesState(cursor: String) =
        AndroidSyncStateEntity(
            mode = "changes",
            cursor = cursor,
            feedDays = RETENTION.feedDays,
            youtubeDays = RETENTION.youtubeDays,
            momentsDays = RETENTION.momentsDays,
            storyHours = RETENTION.storyHours,
        )

    private fun page(
        changes: List<AndroidSyncChangeDto>,
        cursor: String,
        end: Boolean = true,
    ) = AndroidSyncPageResponse(changes, cursor, end)

    private fun feedChange(
        id: String,
        channelId: String = "",
        replyTo: String = "",
        publishedAt: Long = nowMs,
        retainAt: Long = nowMs,
    ) =
        upsertChange(
            ownerKind = "feed",
            ownerId = id,
            retentionBucket = "feed",
            retainAt = retainAt,
            payload = buildJsonObject { put("item", feedItemPayload(id, channelId, replyTo, publishedAt)) },
        )

    private fun feedItemPayload(
        id: String,
        channelId: String,
        replyTo: String,
        publishedAt: Long,
    ) = buildJsonObject {
        put("tweet_id", id)
        put("source_channel_id", channelId)
        put("body_text", "body")
        put("lang", "en")
        put("is_retweet", false)
        put("reposter_channel_id", "")
        put("quote_tweet_id", "")
        put("quote_channel_id", "")
        put("quote_body_text", "")
        put("quote_lang", "")
        put("quote_media_json", "")
        put("quote_published_at", 0)
        put("quote_canonical_url", "")
        put("media_json", "")
        put("views", 0)
        put("likes", 0)
        put("retweets", 0)
        put("canonical_url", "")
        put("canonical_tweet_id", "")
        put("reply_channel_id", "")
        put("reply_to_status", replyTo)
        put("is_reply", replyTo.isNotEmpty())
        put("is_ghost", false)
        put("content_hash", "hash-$id")
        put("body_translation", "")
        put("body_source_lang", "")
        put("quote_translation", "")
        put("quote_source_lang", "")
        put("published_at", publishedAt)
        put("channel_id", channelId)
    }

    private fun videoChange(
        id: String,
        publishedAt: Long,
        retainAt: Long,
        reposterChannelId: String? = "sample_reposter",
        commentAuthorIds: List<String> = emptyList(),
    ) =
        upsertChange(
            ownerKind = "video",
            ownerId = id,
            retentionBucket = "youtube",
            retainAt = retainAt,
            payload = buildJsonObject {
                put(
                    "item",
                    buildJsonObject {
                        put("video_id", id)
                        put("channel_id", "youtube_UCsample_channel")
                        put("owner_kind", "youtube_video")
                        put("title", "title")
                        put("description", "")
                        put("duration", 60)
                        put("published_at", publishedAt)
                        put("media_kind", "video")
                        put("slide_count", 0)
                        put("source_kind", "")
                        put("metadata_json", "")
                        put("canonical_url", "")
                        put("dearrow_title", JsonNull)
                        put("dearrow_title_casual", JsonNull)
                    },
                )
                put(
                    "comments",
                    kotlinx.serialization.json.JsonArray(
                        commentAuthorIds.mapIndexed { index, authorId ->
                            buildJsonObject {
                                put("id", "sample_comment_$index")
                                put("parent", "")
                                put("author", "Sample Author")
                                put("author_id", authorId)
                                put("text", "Sample comment")
                                put("like_count", 0)
                                put("published_at", publishedAt)
                            }
                        }
                    ),
                )
                put("sponsorblock_segments", kotlinx.serialization.json.JsonArray(emptyList()))
                put("sponsorblock_checked", JsonNull)
                put(
                    "repost_sources",
                    kotlinx.serialization.json.JsonArray(
                        reposterChannelId?.let { channelId ->
                            listOf(
                                buildJsonObject {
                                    put("reposter_channel_id", channelId)
                                    put("reposted_at_ms", retainAt)
                                    put("first_seen_at_ms", retainAt)
                                    put("updated_at_ms", retainAt)
                                }
                            )
                        } ?: emptyList()
                    ),
                )
            },
        )

    private fun channelChange(id: String) =
        upsertChange(
            ownerKind = "channel",
            ownerId = id,
            payload = buildJsonObject {
                put(
                    "channel",
                    buildJsonObject {
                        put("channel_id", id)
                        put("source_id", id)
                        put("name", id)
                        put("url", "")
                        put("platform", "twitter")
                    },
                )
                put("profile", JsonNull)
            },
        )

    private fun profileOnlyChannelChange(id: String) =
        upsertChange(
            ownerKind = "channel",
            ownerId = id,
            payload = buildJsonObject {
                put("channel", JsonNull)
                put(
                    "profile",
                    buildJsonObject {
                        put("channel_id", id)
                        put("platform", "twitter")
                        put("handle", id)
                        put("display_name", "Sample Profile")
                        put("bio", "")
                        put("website", "")
                        put("followers", 0)
                        put("following", 0)
                        put("verified", false)
                        put("verified_type", "")
                        put("protected", false)
                    },
                )
            },
        )

    private fun retweetSourcesChange(contentHash: String, channelId: String) =
        upsertChange(
            ownerKind = "retweet_sources",
            ownerId = contentHash,
            payload = buildJsonObject {
                put(
                    "rows",
                    kotlinx.serialization.json.JsonArray(
                        listOf(
                            buildJsonObject {
                                put("content_hash", contentHash)
                                put("retweeter_channel_id", channelId)
                                put("tweet_id", "sample_retweet")
                                put("published_at", nowMs)
                            }
                        )
                    ),
                )
            },
        )

    private fun assetChange(asset: AndroidSyncAssetDto) =
        upsertChange(
            ownerKind = "asset",
            ownerId = asset.asset_id,
            retentionBucket = "feed",
            retainAt = nowMs,
            payload = iglooJson.encodeToJsonElement(asset).jsonObject,
        )

    private fun missingAsset(
        id: String,
        ownerId: String,
        ownerKind: String = "tweet",
        assetKind: String = "post_media",
        bucket: String = "feed",
    ) =
        AndroidSyncAssetDto(
            asset_id = id,
            asset_kind = assetKind,
            media_index = 0,
            owner_id = ownerId,
            owner_kind = ownerKind,
            bucket = bucket,
            content_type = "",
            size_bytes = 0,
            sha256 = "",
            revision = 1,
            state = "server_missing",
            is_auto = null,
        )

    private fun feedLikeChange(id: String) =
        upsertChange(
            ownerKind = "feed_like",
            ownerId = id,
            payload = buildJsonObject {
                put("tweet_id", id)
                put("liked_at", nowMs)
            },
        )

    private fun channelFollowChange(id: String) =
        upsertChange(
            ownerKind = "channel_follow",
            ownerId = id,
            payload = buildJsonObject {
                put("channel_id", id)
                put("followed_at", nowMs)
            },
        )

    private fun channelSettingChange(
        id: String,
        mediaOnly: Int,
        maxVideos: Int,
        includeReposts: Int? = null,
    ) =
        upsertChange(
            ownerKind = "channel_setting",
            ownerId = id,
            payload = buildJsonObject {
                put("channel_id", id)
                put("media_only", mediaOnly)
                put("max_videos", maxVideos)
                includeReposts?.let { put("include_reposts", it) }
                put("updated_at", nowMs)
            },
        )

    private fun upsertChange(
        ownerKind: String,
        ownerId: String,
        retentionBucket: String = "",
        retainAt: Long = 0,
        payload: JsonObject,
    ) = AndroidSyncChangeDto(ownerKind, ownerId, "upsert", retentionBucket, retainAt, payload)

    private fun deleteChange(ownerKind: String, ownerId: String) =
        AndroidSyncChangeDto(ownerKind, ownerId, "delete", "", 0, null)

    private fun assertRetentionQuery(parameters: Map<String, String>) {
        assertEquals(RETENTION.feedDays.toString(), parameters["feed_days"])
        assertEquals(RETENTION.youtubeDays.toString(), parameters["youtube_days"])
        assertEquals(RETENTION.momentsDays.toString(), parameters["moments_days"])
        assertEquals(RETENTION.storyHours.toString(), parameters["story_hours"])
    }

    private inline fun <reified T> MockRequestHandleScope.respondJson(body: T) =
        respond(iglooJson.encodeToString(body), HttpStatusCode.OK, jsonHeaders())

    private fun MockRequestHandleScope.respondOk() = respond("{}", HttpStatusCode.OK, jsonHeaders())

    private fun jsonHeaders() = headersOf("Content-Type", ContentType.Application.Json.toString())

    private companion object {
        const val BASE_URL = "http://example.local"
        const val DAY_MS = 86_400_000L
        val RETENTION = AndroidSyncRetentionRequest(7, 7, 7, 48)
    }
}
