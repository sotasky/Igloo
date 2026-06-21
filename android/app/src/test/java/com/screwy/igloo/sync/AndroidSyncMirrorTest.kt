package com.screwy.igloo.sync

import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.data.entity.AndroidSyncGenerationEntity
import com.screwy.igloo.data.entity.AndroidSyncItemEntity
import com.screwy.igloo.data.entity.BookmarkCategoryEntity
import com.screwy.igloo.data.entity.BookmarkEntity
import com.screwy.igloo.data.entity.BookmarkLabelEntity
import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.ChannelFollowEntity
import com.screwy.igloo.data.entity.ChannelProfileEntity
import com.screwy.igloo.data.entity.ChannelSettingEntity
import com.screwy.igloo.data.entity.ChannelStarEntity
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.data.entity.FeedLikeEntity
import com.screwy.igloo.data.entity.FeedThreadContextEntity
import com.screwy.igloo.data.entity.MediaInventoryEntity
import com.screwy.igloo.data.entity.OutboxEntity
import com.screwy.igloo.data.entity.RetweetSourceEntity
import com.screwy.igloo.data.entity.VideoEntity
import com.screwy.igloo.log.InMemoryLogSink
import com.screwy.igloo.log.LogEntry
import com.screwy.igloo.log.Logger
import com.screwy.igloo.media.ForegroundPromoter
import com.screwy.igloo.net.AndroidSyncApi
import com.screwy.igloo.net.AndroidSyncAssetDto
import com.screwy.igloo.net.AndroidSyncAssetsResponse
import com.screwy.igloo.net.AndroidSyncGenerationDto
import com.screwy.igloo.net.AndroidSyncItemsResponse
import com.screwy.igloo.net.AndroidSyncItemDto
import com.screwy.igloo.net.AndroidSyncLatestResponse
import com.screwy.igloo.net.BundleEnvelope
import com.screwy.igloo.net.AndroidSyncRetentionRequest
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.net.iglooJson
import com.screwy.igloo.outbox.OutboxKind
import androidx.test.core.app.ApplicationProvider
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.MockRequestHandleScope
import io.ktor.client.engine.mock.respond
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.http.ContentType
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import io.ktor.serialization.kotlinx.json.json
import io.ktor.utils.io.ByteReadChannel
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.io.File
import java.io.IOException
import java.security.MessageDigest
import java.util.Collections
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class AndroidSyncMirrorTest {

    @get:Rule val tmpFolder = TemporaryFolder()

    private lateinit var db: IglooDatabase
    private lateinit var scope: CoroutineScope
    private lateinit var client: HttpClient
    private lateinit var logSink: InMemoryLogSink
    private lateinit var logger: Logger
    private var nowMs: Long = 10_000L

    @Before fun setUp() {
        db = RoomTestSupport.freshDb()
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        logSink = InMemoryLogSink()
        logger = Logger(
            prefs = PreferencesRepo(db.preferenceDao(), scope, nowMsProvider = { nowMs }),
            sink = logSink,
            scope = scope,
            nowMsProvider = { nowMs },
        )
    }

    @After fun tearDown() {
        if (::client.isInitialized) client.close()
        scope.cancel()
        db.close()
    }

    private suspend fun waitForLog(event: String): LogEntry =
        withTimeout(3_000L) {
            while (true) {
                logSink.snapshot().firstOrNull { it.event == event }?.let { return@withTimeout it }
                delay(10)
            }
            error("unreachable")
        }

    @Test fun triggerMarksMirrorPendingBeforeRunnerCreatesGeneration() {
        val mirror = buildMirror(MockEngine { respondOk("{}") })

        assertFalse(mirror.hasPendingOrActiveWork())

        mirror.trigger()

        assertTrue(mirror.hasPendingOrActiveWork())
    }

    @Test fun syncOnceSchedulesDelayedRetryWhenServerGenerationIsRefreshing() = runBlocking {
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> respondJson(
                    AndroidSyncLatestResponse(
                        generation = AndroidSyncGenerationDto(
                            generation_id = GENERATION_ID,
                            created_at_ms = nowMs,
                            status = "published",
                            source_version = "test",
                        ),
                        refreshing = true,
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/items" -> respondJson(
                    AndroidSyncItemsResponse(
                        generation_id = GENERATION_ID,
                        items = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = GENERATION_ID,
                        assets = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                else -> error("Unexpected request ${request.url}")
            }
        }
        val mirror = buildMirror(engine, refreshRetryDelayMs = 1L)

        mirror.syncOnce()

        withTimeout(1_000L) {
            while (!mirror.hasPendingOrActiveWork()) delay(10L)
        }
    }

    @Test fun syncOnceDoesNotScheduleRefreshRetryWhenRetryIsDisabled() = runBlocking {
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> respondJson(
                    AndroidSyncLatestResponse(
                        generation = AndroidSyncGenerationDto(
                            generation_id = GENERATION_ID,
                            created_at_ms = nowMs,
                            status = "published",
                            source_version = "test",
                        ),
                        refreshing = true,
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/items" -> respondJson(
                    AndroidSyncItemsResponse(
                        generation_id = GENERATION_ID,
                        items = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = GENERATION_ID,
                        assets = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                else -> error("Unexpected request ${request.url}")
            }
        }
        val mirror = buildMirror(
            engine,
            refreshRetryDelayMs = 1L,
            refreshRetryEnabledProvider = { false },
        )

        mirror.syncOnce()
        delay(25L)

        assertFalse(mirror.hasPendingOrActiveWork())
        assertEquals("false", waitForLog("android_sync_generation_refresh_pending").fields["retry_scheduled"])
    }

    @Test fun sameGenerationSyncSkipsRepeatedOrphanFileWalkWhenNothingWasPruned() = runBlocking {
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> respondJson(
                    AndroidSyncLatestResponse(
                        generation = AndroidSyncGenerationDto(
                            generation_id = GENERATION_ID,
                            created_at_ms = nowMs,
                            status = "published",
                            source_version = "test",
                        ),
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/items" -> respondJson(
                    AndroidSyncItemsResponse(
                        generation_id = GENERATION_ID,
                        items = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = GENERATION_ID,
                        assets = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                else -> error("Unexpected request ${request.url}")
            }
        }
        val mirror = buildMirror(engine)
        val syncBucket = File(tmpFolder.root, "sync/manual").apply { mkdirs() }
        val staleBeforeFirstWalk = File(syncBucket, "stale-before-first-walk.bin").apply { writeText("stale") }

        mirror.syncOnce()

        assertFalse(staleBeforeFirstWalk.exists())
        val staleAfterCleanPass = File(syncBucket, "stale-after-clean-pass.bin").apply { writeText("stale") }

        mirror.syncOnce()

        assertTrue(staleAfterCleanPass.exists())
    }

    @Test fun healthUploadFailureDoesNotStopAssetDrain() = runBlocking {
        val healthAttempts = AtomicInteger(0)
        val assetBody = "asset-body"
        val assets = (1..13).map { index ->
            AndroidSyncAssetDto(
                seq = index.toLong(),
                asset_id = "asset-$index",
                asset_kind = "post_media",
                owner_id = "owner-$index",
                owner_kind = "feed_item",
                bucket = "feed",
                server_url = "/api/android/sync/assets/asset-$index",
                content_type = "application/octet-stream",
                size_bytes = assetBody.length.toLong(),
                state = "ready",
                effective_recency_ms = nowMs - index,
            )
        }
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> respondJson(
                    AndroidSyncLatestResponse(
                        generation = AndroidSyncGenerationDto(
                            generation_id = GENERATION_ID,
                            created_at_ms = nowMs,
                            status = "published",
                            source_version = "test",
                            asset_count = assets.size,
                            ready_asset_count = assets.size,
                        ),
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/items" -> respondJson(
                    AndroidSyncItemsResponse(
                        generation_id = GENERATION_ID,
                        items = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = GENERATION_ID,
                        assets = assets,
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/health" -> {
                    healthAttempts.incrementAndGet()
                    respond("""{"ok":false}""", HttpStatusCode.InternalServerError, jsonHeaders())
                }
                else -> {
                    if (request.url.encodedPath.startsWith("/api/android/sync/assets/")) {
                        respond(
                            ByteReadChannel(assetBody),
                            HttpStatusCode.OK,
                            headersOf("Content-Type" to listOf("application/octet-stream")),
                        )
                    } else {
                        error("Unexpected request ${request.url}")
                    }
                }
            }
        }
        val mirror = buildMirror(engine)

        mirror.syncOnce()

        val counts = db.androidSyncDao().healthCounts(GENERATION_ID)
        assertEquals(assets.size, counts.verified)
        assertEquals(0, counts.pending)
        val localPath = db.androidSyncDao().latestVerifiedLocalPath("owner-1", "post_media")
        assertNotNull(localPath)
        assertTrue(localPath!!.contains("${java.io.File.separator}sync${java.io.File.separator}feed${java.io.File.separator}"))
        assertTrue("health should be attempted after each batch and final report", healthAttempts.get() >= 2)
    }

    @Test fun assetDrainDoneLogCapturesTelemetryCountsAndTiming() = runBlocking {
        val existingBody = "existing-body"
        val downloadedBody = "downloaded-body"
        val existingFile = java.io.File(tmpFolder.root, "sync/feed/existing.bin").apply {
            parentFile?.mkdirs()
            writeText(existingBody)
        }
        val assets = listOf(
            AndroidSyncAssetDto(
                seq = 1,
                asset_id = "existing",
                asset_kind = "post_media",
                owner_id = "sample_existing",
                owner_kind = "feed_item",
                bucket = "feed",
                server_url = "/api/android/sync/assets/existing",
                content_type = "application/octet-stream",
                size_bytes = existingBody.length.toLong(),
                sha256 = sha256Hex(existingBody),
                state = "ready",
                effective_recency_ms = nowMs,
            ),
            AndroidSyncAssetDto(
                seq = 2,
                asset_id = "downloaded",
                asset_kind = "post_media",
                owner_id = "sample_new",
                owner_kind = "feed_item",
                bucket = "feed",
                server_url = "/api/android/sync/assets/downloaded",
                content_type = "application/octet-stream",
                size_bytes = downloadedBody.length.toLong(),
                sha256 = sha256Hex(downloadedBody),
                state = "ready",
                effective_recency_ms = nowMs - 1,
            ),
            AndroidSyncAssetDto(
                seq = 3,
                asset_id = "limited",
                asset_kind = "post_media",
                owner_id = "sample_media",
                owner_kind = "feed_item",
                bucket = "feed",
                server_url = "/api/android/sync/assets/limited",
                content_type = "application/octet-stream",
                size_bytes = 1,
                state = "ready",
                effective_recency_ms = nowMs - 2,
            ),
            AndroidSyncAssetDto(
                seq = 4,
                asset_id = "missing",
                asset_kind = "post_media",
                owner_id = "sample_missing",
                owner_kind = "feed_item",
                bucket = "feed",
                server_url = "/api/android/sync/assets/missing",
                content_type = "application/octet-stream",
                size_bytes = 1,
                state = "ready",
                effective_recency_ms = nowMs - 3,
            ),
        )
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> respondJson(
                    AndroidSyncLatestResponse(
                        generation = AndroidSyncGenerationDto(
                            generation_id = GENERATION_ID,
                            created_at_ms = nowMs,
                            status = "published",
                            source_version = "test",
                            asset_count = assets.size,
                            ready_asset_count = assets.size,
                        ),
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/items" -> respondJson(
                    AndroidSyncItemsResponse(
                        generation_id = GENERATION_ID,
                        items = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = GENERATION_ID,
                        assets = assets,
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                "/api/android/sync/assets/downloaded" -> respond(
                    ByteReadChannel(downloadedBody),
                    HttpStatusCode.OK,
                    headersOf("Content-Type" to listOf("application/octet-stream")),
                )
                "/api/android/sync/assets/limited" -> respond(
                    ByteReadChannel(""),
                    HttpStatusCode.TooManyRequests,
                    headersOf("Content-Type" to listOf("text/plain")),
                )
                "/api/android/sync/assets/missing" -> respond(
                    ByteReadChannel(""),
                    HttpStatusCode.NotFound,
                    headersOf("Content-Type" to listOf("text/plain")),
                )
                else -> error("Unexpected request ${request.url}")
            }
        }

        buildMirror(engine).syncOnce()

        assertTrue(existingFile.exists())
        val log = waitForLog("android_sync_asset_drain_done")
        assertEquals(32, log.fields["worker_cap"])
        assertEquals(1, log.fields["verified_existing"])
        assertEquals(1, log.fields["downloaded"])
        assertEquals(1, log.fields["deferred"])
        assertEquals(1, log.fields["server_missing"])
        assertEquals(0, log.fields["failed"])
        assertEquals(1, log.fields["pending"])
        assertEquals(1, log.fields["claim_batches"])
        assertEquals(4, log.fields["claimed_assets"])
        assertEquals(1, log.fields["empty_claims"])
        assertEquals(1, log.fields["http_429"])
        assertTrue((log.fields["hash_bytes"] as Long) >= (existingBody.length + downloadedBody.length).toLong())
        assertTrue((log.fields["hash_time_us"] as Long) >= 0L)
        assertEquals(1, log.fields["health_uploads"])
        assertTrue((log.fields["health_upload_time_ms"] as Long) >= 0L)
    }

    @Test fun transientGatewayMetadataFailureRetriesAsHttpStatusNotDecodeFailure() = runBlocking {
        val latestAttempts = AtomicInteger(0)
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> {
                    if (latestAttempts.incrementAndGet() == 1) {
                        respond(
                            "<html><body><h1>502 Bad Gateway</h1><p>proxy response</p></body></html>",
                            HttpStatusCode.BadGateway,
                            headersOf("Content-Type", ContentType.Text.Html.toString()),
                        )
                    } else {
                        respondJson(
                            AndroidSyncLatestResponse(
                                generation = AndroidSyncGenerationDto(
                                    generation_id = GENERATION_ID,
                                    created_at_ms = nowMs,
                                    status = "published",
                                    source_version = "test",
                                ),
                            ),
                        )
                    }
                }
                "/api/android/sync/generation/$GENERATION_ID/items" -> respondJson(
                    AndroidSyncItemsResponse(
                        generation_id = GENERATION_ID,
                        items = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = GENERATION_ID,
                        assets = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                else -> error("Unexpected request ${request.url}")
            }
        }
        val mirror = buildMirror(engine)

        mirror.syncOnce()

        assertEquals(2, latestAttempts.get())
        val retry = waitForLog("android_sync_metadata_retry")
        assertEquals("latest_generation", retry.fields["label"])
        assertEquals(1, retry.fields["attempt"])
        assertEquals("Sync HTTP 502 for latest_generation", retry.fields["error"])
    }

    @Test fun malformedSuccessfulMetadataFailsWithoutInfiniteRetry() = runBlocking {
        val latestAttempts = AtomicInteger(0)
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> {
                    latestAttempts.incrementAndGet()
                    respondOk("""{"generation": "not an object"}""")
                }
                else -> error("Unexpected request ${request.url}")
            }
        }
        val mirror = buildMirror(engine)

        try {
            withTimeout(500) {
                mirror.syncOnce()
            }
            fail("malformed metadata should fail")
        } catch (e: IllegalStateException) {
            assertTrue(e.message.orEmpty().contains("latest_generation"))
            assertTrue(e.message.orEmpty().contains("Sync decode failed"))
        }
        assertEquals(1, latestAttempts.get())
    }

    @Test fun transportMetadataFailureStopsAfterBoundedRetries() = runBlocking {
        val latestAttempts = AtomicInteger(0)
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> {
                    latestAttempts.incrementAndGet()
                    throw IOException("socket failed: EPERM (Operation not permitted)")
                }
                else -> error("Unexpected request ${request.url}")
            }
        }
        val mirror = buildMirror(engine, metadataRetryDelaysMs = listOf(1L, 1L))

        val failure = runCatching { mirror.syncOnce() }.exceptionOrNull()

        assertTrue(failure is IOException)
        assertEquals(3, latestAttempts.get())
        val exhausted = waitForLog("android_sync_metadata_retry_exhausted")
        assertEquals("latest_generation", exhausted.fields["label"])
        assertEquals(3, exhausted.fields["attempts"])
    }

    @Test fun runnerTreatsTransportFailureAsCompletedTrigger() = runBlocking {
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" ->
                    throw IOException("socket failed: EPERM (Operation not permitted)")
                else -> error("Unexpected request ${request.url}")
            }
        }
        val mirror = buildMirror(engine, metadataRetryDelaysMs = listOf(1L))
        val job = launch { mirror.run() }
        try {
            mirror.trigger()
            waitForLog("android_sync_unhandled")

            withTimeout(1_000L) {
                while (mirror.hasPendingOrActiveWork()) delay(10L)
            }
        } finally {
            job.cancel()
        }
    }

    @Test fun staleGenerationAssetConflictTriggersResyncWithoutPermanentAssetFailure() = runBlocking {
        val assetRequests = AtomicInteger(0)
        val asset = AndroidSyncAssetDto(
            seq = 1,
            asset_id = "asset-1",
            asset_kind = "post_thumbnail",
            owner_id = "sample_video",
            owner_kind = "video",
            bucket = "thumbnails",
            server_url = "/api/android/sync/generation/$GENERATION_ID/assets/asset-1",
            content_type = "image/jpeg",
            size_bytes = 10L,
            sha256 = "expected-hash",
            state = "ready",
            effective_recency_ms = nowMs,
        )
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> respondJson(
                    AndroidSyncLatestResponse(
                        generation = AndroidSyncGenerationDto(
                            generation_id = GENERATION_ID,
                            created_at_ms = nowMs,
                            status = "published",
                            source_version = "test",
                            asset_count = 1,
                            ready_asset_count = 1,
                        ),
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/items" -> respondJson(
                    AndroidSyncItemsResponse(
                        generation_id = GENERATION_ID,
                        items = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = GENERATION_ID,
                        assets = listOf(asset),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                "/api/android/sync/generation/$GENERATION_ID/assets/asset-1" -> {
                    assetRequests.incrementAndGet()
                    respond(
                        "asset changed; request latest generation",
                        HttpStatusCode.Conflict,
                        headersOf("Content-Type", ContentType.Text.Plain.toString()),
                    )
                }
                else -> error("Unexpected request ${request.url}")
            }
        }

        val failure = runCatching { buildMirror(engine).syncOnce() }.exceptionOrNull()

        assertTrue(failure is AndroidSyncStaleGenerationException)
        assertEquals(1, assetRequests.get())
        val counts = db.androidSyncDao().healthCounts(GENERATION_ID)
        assertEquals(0, counts.failed)
        assertEquals(1, counts.pending)
    }

    @Test fun generationScopedVerifyFailureTriggersResyncWithoutPermanentAssetFailure() = runBlocking {
        val asset = AndroidSyncAssetDto(
            seq = 1,
            asset_id = "asset-1",
            asset_kind = "post_thumbnail",
            owner_id = "sample_video",
            owner_kind = "video",
            bucket = "thumbnails",
            server_url = "/api/android/sync/generation/$GENERATION_ID/assets/asset-1",
            content_type = "image/jpeg",
            size_bytes = 4L,
            sha256 = "expected-hash",
            state = "ready",
            effective_recency_ms = nowMs,
        )
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> respondJson(
                    AndroidSyncLatestResponse(
                        generation = AndroidSyncGenerationDto(
                            generation_id = GENERATION_ID,
                            created_at_ms = nowMs,
                            status = "published",
                            source_version = "test",
                            asset_count = 1,
                            ready_asset_count = 1,
                        ),
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/items" -> respondJson(
                    AndroidSyncItemsResponse(
                        generation_id = GENERATION_ID,
                        items = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = GENERATION_ID,
                        assets = listOf(asset),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                "/api/android/sync/generation/$GENERATION_ID/assets/asset-1" -> respond(
                    ByteReadChannel("bad!"),
                    HttpStatusCode.OK,
                    headersOf("Content-Type" to listOf("image/jpeg")),
                )
                else -> error("Unexpected request ${request.url}")
            }
        }

        val failure = runCatching { buildMirror(engine).syncOnce() }.exceptionOrNull()

        assertTrue(failure is AndroidSyncStaleGenerationException)
        val counts = db.androidSyncDao().healthCounts(GENERATION_ID)
        assertEquals(0, counts.failed)
        assertEquals(1, counts.pending)
    }

    @Test fun runnerTreatsStaleGenerationAsRetrySignal() = runBlocking {
        val asset = AndroidSyncAssetDto(
            seq = 1,
            asset_id = "asset-1",
            asset_kind = "post_thumbnail",
            owner_id = "sample_video",
            owner_kind = "video",
            bucket = "thumbnails",
            server_url = "/api/android/sync/generation/$GENERATION_ID/assets/asset-1",
            content_type = "image/jpeg",
            size_bytes = 10L,
            sha256 = "expected-hash",
            state = "ready",
            effective_recency_ms = nowMs,
        )
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> respondJson(
                    AndroidSyncLatestResponse(
                        generation = AndroidSyncGenerationDto(
                            generation_id = GENERATION_ID,
                            created_at_ms = nowMs,
                            status = "published",
                            source_version = "test",
                            asset_count = 1,
                            ready_asset_count = 1,
                        ),
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/items" -> respondJson(
                    AndroidSyncItemsResponse(
                        generation_id = GENERATION_ID,
                        items = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = GENERATION_ID,
                        assets = listOf(asset),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                "/api/android/sync/generation/$GENERATION_ID/assets/asset-1" -> respond(
                    "asset changed; request latest generation",
                    HttpStatusCode.Conflict,
                    headersOf("Content-Type", ContentType.Text.Plain.toString()),
                )
                else -> error("Unexpected request ${request.url}")
            }
        }
        val mirror = buildMirror(engine)
        val job = launch { mirror.run() }
        try {
            mirror.trigger()
            val retry = waitForLog("android_sync_generation_stale_retry")

            assertEquals("true", retry.fields["retry_scheduled"])
            assertFalse(logSink.snapshot().any { it.event == "android_sync_unhandled" })
        } finally {
            job.cancel()
        }
    }

    @Test fun runnerCompletesStaleGenerationTriggerWhenRetryIsDisabled() = runBlocking {
        val asset = AndroidSyncAssetDto(
            seq = 1,
            asset_id = "asset-1",
            asset_kind = "post_thumbnail",
            owner_id = "sample_video",
            owner_kind = "video",
            bucket = "thumbnails",
            server_url = "/api/android/sync/generation/$GENERATION_ID/assets/asset-1",
            content_type = "image/jpeg",
            size_bytes = 10L,
            sha256 = "expected-hash",
            state = "ready",
            effective_recency_ms = nowMs,
        )
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> respondJson(
                    AndroidSyncLatestResponse(
                        generation = AndroidSyncGenerationDto(
                            generation_id = GENERATION_ID,
                            created_at_ms = nowMs,
                            status = "published",
                            source_version = "test",
                            asset_count = 1,
                            ready_asset_count = 1,
                        ),
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/items" -> respondJson(
                    AndroidSyncItemsResponse(
                        generation_id = GENERATION_ID,
                        items = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = GENERATION_ID,
                        assets = listOf(asset),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                "/api/android/sync/generation/$GENERATION_ID/assets/asset-1" -> respond(
                    "asset changed; request latest generation",
                    HttpStatusCode.Conflict,
                    headersOf("Content-Type", ContentType.Text.Plain.toString()),
                )
                else -> error("Unexpected request ${request.url}")
            }
        }
        val mirror = buildMirror(engine, refreshRetryEnabledProvider = { false })
        val job = launch { mirror.run() }
        try {
            mirror.trigger()
            val retry = waitForLog("android_sync_generation_stale_retry")

            assertEquals("false", retry.fields["retry_scheduled"])
            withTimeout(1_000L) {
                while (mirror.hasPendingOrActiveWork()) delay(10L)
            }
        } finally {
            job.cancel()
        }
    }

    @Test fun completedAssetImportRefreshesLegacyUrlsAndRetriesFailedRows() = runBlocking {
        val dao = db.androidSyncDao()
        dao.upsertGeneration(generationEntity(GENERATION_ID, createdAtMs = nowMs, assetsImportedAtMs = nowMs))
        val oldAsset = AndroidSyncAssetEntity(
            generationId = GENERATION_ID,
            seq = 1,
            assetId = "asset-1",
            assetKind = "post_thumbnail",
            ownerId = "sample_video",
            ownerKind = "video",
            bucket = "thumbnails",
            serverUrl = "/api/android/sync/assets/asset-1",
            contentType = "image/jpeg",
            sizeBytes = "good-body".length.toLong(),
            serverState = "ready",
            effectiveRecencyMs = nowMs,
        )
        dao.importAssets(listOf(oldAsset), nowMs)
        dao.markFailed(
            generationId = oldAsset.generationId,
            assetId = oldAsset.assetId,
            assetKind = oldAsset.assetKind,
            nextAttemptAtMs = nowMs + 60_000,
            lastError = "verify_failed",
            nowMs = nowMs,
        )
        val refreshedAsset = AndroidSyncAssetDto(
            seq = oldAsset.seq,
            asset_id = oldAsset.assetId,
            asset_kind = oldAsset.assetKind,
            owner_id = oldAsset.ownerId,
            owner_kind = oldAsset.ownerKind,
            bucket = oldAsset.bucket,
            server_url = "/api/android/sync/generation/$GENERATION_ID/assets/${oldAsset.assetId}",
            content_type = oldAsset.contentType,
            size_bytes = oldAsset.sizeBytes,
            state = "ready",
            effective_recency_ms = nowMs,
        )
        val assetPageRequests = AtomicInteger(0)
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> respondJson(
                    AndroidSyncLatestResponse(
                        generation = AndroidSyncGenerationDto(
                            generation_id = GENERATION_ID,
                            created_at_ms = nowMs,
                            status = "published",
                            source_version = "test",
                            asset_count = 1,
                            ready_asset_count = 1,
                        ),
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/items" -> respondJson(
                    AndroidSyncItemsResponse(
                        generation_id = GENERATION_ID,
                        items = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/assets" -> {
                    assetPageRequests.incrementAndGet()
                    respondJson(
                        AndroidSyncAssetsResponse(
                            generation_id = GENERATION_ID,
                            assets = listOf(refreshedAsset),
                            end_of_stream = true,
                        ),
                    )
                }
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                "/api/android/sync/generation/$GENERATION_ID/assets/asset-1" -> respond(
                    ByteReadChannel("good-body"),
                    HttpStatusCode.OK,
                    headersOf("Content-Type" to listOf("image/jpeg")),
                )
                else -> error("Unexpected request ${request.url}")
            }
        }

        buildMirror(engine).syncOnce()

        assertEquals(1, assetPageRequests.get())
        val row = dao.latestVerifiedAssetsForOwner("sample_video", listOf("post_thumbnail")).single()
        assertEquals(refreshedAsset.server_url, row.serverUrl)
        assertEquals(0, row.attemptCount)
        assertNull(row.lastError)
    }

    @Test fun assetWithoutSizeOrHashFailsWithoutDownload() = runBlocking {
        val assetRequests = AtomicInteger(0)
        val asset = AndroidSyncAssetDto(
            seq = 1,
            asset_id = "asset-1",
            asset_kind = "post_media",
            owner_id = "owner-1",
            owner_kind = "feed_item",
            bucket = "feed",
            server_url = "/api/android/sync/assets/asset-1",
            content_type = "application/octet-stream",
            size_bytes = 0L,
            state = "ready",
            effective_recency_ms = nowMs,
        )
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> respondJson(
                    AndroidSyncLatestResponse(
                        generation = AndroidSyncGenerationDto(
                            generation_id = GENERATION_ID,
                            created_at_ms = nowMs,
                            status = "published",
                            source_version = "test",
                            asset_count = 1,
                            ready_asset_count = 1,
                        ),
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/items" -> respondJson(
                    AndroidSyncItemsResponse(
                        generation_id = GENERATION_ID,
                        items = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = GENERATION_ID,
                        assets = listOf(asset),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                "/api/android/sync/assets/asset-1" -> {
                    assetRequests.incrementAndGet()
                    respond(
                        ByteReadChannel("unexpected-body"),
                        HttpStatusCode.OK,
                        headersOf("Content-Type" to listOf("application/octet-stream")),
                    )
                }
                else -> error("Unexpected request ${request.url}")
            }
        }
        val mirror = buildMirror(engine)

        mirror.syncOnce()

        val counts = db.androidSyncDao().healthCounts(GENERATION_ID)
        assertEquals(0, assetRequests.get())
        assertEquals(1, counts.failed)
        assertEquals(0, counts.pending)
        assertEquals(0, counts.verified)
    }

    @Test fun latestGenerationRequestIncludesRetentionPreferences() = runBlocking {
        var feedDays: String? = null
        var youtubeDays: String? = null
        var momentsDays: String? = null
        var storyHours: String? = null
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> {
                    feedDays = request.url.parameters["feed_days"]
                    youtubeDays = request.url.parameters["youtube_days"]
                    momentsDays = request.url.parameters["moments_days"]
                    storyHours = request.url.parameters["story_hours"]
                    respondJson(
                        AndroidSyncLatestResponse(
                            generation = AndroidSyncGenerationDto(
                                generation_id = GENERATION_ID,
                                created_at_ms = nowMs,
                                status = "published",
                                source_version = "test",
                            ),
                        ),
                    )
                }
                "/api/android/sync/generation/$GENERATION_ID/items" -> respondJson(
                    AndroidSyncItemsResponse(
                        generation_id = GENERATION_ID,
                        items = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = GENERATION_ID,
                        assets = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                else -> error("Unexpected request ${request.url}")
            }
        }
        val mirror = buildMirror(
            engine,
            retentionProvider = { AndroidSyncRetentionRequest(feedDays = 3, youtubeDays = 2, momentsDays = 7, storyHours = 48) },
        )

        mirror.syncOnce()

        assertEquals("3", feedDays)
        assertEquals("2", youtubeDays)
        assertEquals("7", momentsDays)
        assertEquals("48", storyHours)
    }

    @Test fun subtitleAssetMetadataIsImportedForPlayerDefaults() = runBlocking {
        val asset = AndroidSyncAssetDto(
            seq = 1,
            asset_id = "youtube_video_vid_1_subtitle_0",
            asset_kind = "subtitle",
            owner_id = "vid_1",
            owner_kind = "video",
            bucket = "videos",
            server_url = "/api/android/sync/assets/youtube_video_vid_1_subtitle_0",
            content_type = "text/vtt",
            size_bytes = 10L,
            sha256 = "1ad72dfe1ab32b83b28a7f07765316ecd416bc453c24d47afd18353b050aa012",
            state = "ready",
            is_auto = true,
            audio_language = "en",
            effective_recency_ms = nowMs,
        )
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> respondJson(
                    AndroidSyncLatestResponse(
                        generation = AndroidSyncGenerationDto(
                            generation_id = GENERATION_ID,
                            created_at_ms = nowMs,
                            status = "published",
                            source_version = "test",
                            asset_count = 1,
                            ready_asset_count = 1,
                        ),
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/items" -> respondJson(
                    AndroidSyncItemsResponse(
                        generation_id = GENERATION_ID,
                        items = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = GENERATION_ID,
                        assets = listOf(asset),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                "/api/android/sync/assets/youtube_video_vid_1_subtitle_0" -> respond(
                    ByteReadChannel("WEBVTT\n\nok"),
                    HttpStatusCode.OK,
                    headersOf("Content-Type" to listOf("text/vtt")),
                )
                else -> error("Unexpected request ${request.url}")
            }
        }
        val mirror = buildMirror(engine)

        mirror.syncOnce()

        val row = db.androidSyncDao()
            .latestVerifiedAssetsForOwner("vid_1", listOf("subtitle"))
            .single()
        assertTrue(row.subtitleIsAuto)
        assertEquals("en", row.audioLanguage)
    }

    @Test fun canonicalVideoUrlIsImportedFromAndroidSyncPayload() = runBlocking {
        val item = AndroidSyncItemDto(
            seq = 1,
            item_kind = "videos",
            item_id = "sample_tiktok_video_1",
            payload = BundleEnvelope(
                primary_kind = "videos",
                primary = buildJsonObject {
                    put("video_id", "sample_tiktok_video_1")
                    put("channel_id", "sample_channel")
                    put("title", "Synced clip")
                    put("duration_label", "1:02:06")
                    put("published_at", 123L)
                    put("canonical_url", "https://www.tiktok.com/@sample_user/video/sample_video_1")
                },
            ),
        )
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> respondJson(
                    AndroidSyncLatestResponse(
                        generation = AndroidSyncGenerationDto(
                            generation_id = GENERATION_ID,
                            created_at_ms = nowMs,
                            status = "published",
                            source_version = "test",
                            item_count = 1,
                        ),
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/items" -> respondJson(
                    AndroidSyncItemsResponse(
                        generation_id = GENERATION_ID,
                        items = listOf(item),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = GENERATION_ID,
                        assets = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                else -> error("Unexpected request ${request.url}")
            }
        }

        buildMirror(engine).syncOnce()

        assertEquals(
            "https://www.tiktok.com/@sample_user/video/sample_video_1",
            db.videoDao().getById("sample_tiktok_video_1")?.canonicalUrl,
        )
        assertEquals("1:02:06", db.videoDao().getById("sample_tiktok_video_1")?.durationLabel)
    }

    @Test fun itemImportReplaysGenerationImportedByOlderProjectionVersion() = runBlocking {
        val dao = db.androidSyncDao()
        val item = AndroidSyncItemDto(
            seq = 1,
            item_kind = "videos",
            item_id = "sample_tiktok_video_1",
            payload = BundleEnvelope(
                primary_kind = "videos",
                primary = buildJsonObject {
                    put("video_id", "sample_tiktok_video_1")
                    put("channel_id", "sample_channel")
                    put("title", "Synced clip")
                    put("published_at", 123L)
                    put("canonical_url", "https://www.tiktok.com/@sample_user/video/sample_video_1")
                },
            ),
        )
        dao.upsertGeneration(
            generationEntity(
                generationId = GENERATION_ID,
                createdAtMs = nowMs,
                itemsImportedAtMs = nowMs - 1,
            ),
        )
        db.videoDao().upsert(
            VideoEntity(
                videoId = "sample_tiktok_video_1",
                channelId = "sample_channel",
                title = "Old clip",
                publishedAt = 123L,
                canonicalUrl = null,
            ),
        )
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> respondJson(
                    AndroidSyncLatestResponse(
                        generation = AndroidSyncGenerationDto(
                            generation_id = GENERATION_ID,
                            created_at_ms = nowMs,
                            status = "published",
                            source_version = "test",
                            item_count = 1,
                        ),
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/items" -> respondJson(
                    AndroidSyncItemsResponse(
                        generation_id = GENERATION_ID,
                        items = listOf(item),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = GENERATION_ID,
                        assets = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                else -> error("Unexpected request ${request.url}")
            }
        }

        buildMirror(engine).syncOnce()

        assertEquals(
            "https://www.tiktok.com/@sample_user/video/sample_video_1",
            db.videoDao().getById("sample_tiktok_video_1")?.canonicalUrl,
        )
    }

    @Test fun itemImportResumesAfterLastFullyIngestedPage() = runBlocking {
        val dao = db.androidSyncDao()
        val firstItem = AndroidSyncItemDto(
            seq = 1,
            item_kind = "channels",
            item_id = "sample_channel_one",
            payload = BundleEnvelope(
                primary_kind = "channels",
                primary = buildJsonObject {
                    put("channel_id", "sample_channel_one")
                    put("source_id", "sample_one")
                    put("name", "Sample Channel One")
                    put("platform", "youtube")
                },
            ),
        )
        val secondItem = AndroidSyncItemDto(
            seq = 2,
            item_kind = "channels",
            item_id = "sample_channel_two",
            payload = BundleEnvelope(
                primary_kind = "channels",
                primary = buildJsonObject {
                    put("channel_id", "sample_channel_two")
                    put("source_id", "sample_two")
                    put("name", "Sample Channel Two")
                    put("platform", "youtube")
                },
            ),
        )
        val firstEngine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> respondJson(
                    AndroidSyncLatestResponse(
                        generation = AndroidSyncGenerationDto(
                            generation_id = GENERATION_ID,
                            created_at_ms = nowMs,
                            status = "published",
                            source_version = "test",
                            item_count = 2,
                        ),
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/items" -> {
                    if (request.url.parameters["after"] == "1") {
                        respond("{malformed", HttpStatusCode.OK, jsonHeaders())
                    } else {
                        respondJson(
                            AndroidSyncItemsResponse(
                                generation_id = GENERATION_ID,
                                items = listOf(firstItem),
                                next = "1",
                                end_of_stream = false,
                            ),
                        )
                    }
                }
                "/api/android/sync/generation/$GENERATION_ID/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = GENERATION_ID,
                        assets = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                else -> error("Unexpected request ${request.url}")
            }
        }

        val firstResult = runCatching { buildMirror(firstEngine).syncOnce() }

        assertTrue(firstResult.isFailure)
        assertNotNull(db.channelDao().getById("sample_channel_one"))
        assertNull(db.channelDao().getById("sample_channel_two"))
        assertEquals(1L, dao.importedItemSeq(GENERATION_ID))
        client.close()

        val requestedAfterMarkers = Collections.synchronizedList(mutableListOf<String?>())
        val secondEngine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> respondJson(
                    AndroidSyncLatestResponse(
                        generation = AndroidSyncGenerationDto(
                            generation_id = GENERATION_ID,
                            created_at_ms = nowMs,
                            status = "published",
                            source_version = "test",
                            item_count = 2,
                        ),
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/items" -> {
                    val after = request.url.parameters["after"]
                    requestedAfterMarkers += after
                    assertEquals("1", after)
                    respondJson(
                        AndroidSyncItemsResponse(
                            generation_id = GENERATION_ID,
                            items = listOf(secondItem),
                            end_of_stream = true,
                        ),
                    )
                }
                "/api/android/sync/generation/$GENERATION_ID/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = GENERATION_ID,
                        assets = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                else -> error("Unexpected request ${request.url}")
            }
        }

        buildMirror(secondEngine).syncOnce()

        assertEquals(listOf("1"), requestedAfterMarkers.toList())
        assertNotNull(db.channelDao().getById("sample_channel_one"))
        assertNotNull(db.channelDao().getById("sample_channel_two"))
        assertEquals(2L, dao.importedItemSeq(GENERATION_ID))
        assertTrue(
            logSink.snapshot().any {
                it.event == "android_sync_items_resume" && it.fields["after"] == 1L
            },
        )
    }

    @Test fun itemImportParseFailureDoesNotCompleteOrAdvanceFailedPage() = runBlocking {
        val dao = db.androidSyncDao()
        val firstItem = AndroidSyncItemDto(
            seq = 1,
            item_kind = "channels",
            item_id = "sample_channel_one",
            payload = BundleEnvelope(
                primary_kind = "channels",
                primary = buildJsonObject {
                    put("channel_id", "sample_channel_one")
                    put("source_id", "sample_one")
                    put("name", "Sample Channel One")
                    put("platform", "youtube")
                },
            ),
        )
        val malformedItem = AndroidSyncItemDto(
            seq = 2,
            item_kind = "feed_items",
            item_id = "bad_tweet",
            payload = BundleEnvelope(
                primary_kind = "feed_items",
                primary = buildJsonObject {
                    put("tweet_id", "bad_tweet")
                    put("published_at", 2L)
                },
            ),
        )
        val correctedItem = AndroidSyncItemDto(
            seq = 2,
            item_kind = "feed_items",
            item_id = "bad_tweet",
            payload = BundleEnvelope(
                primary_kind = "feed_items",
                primary = buildJsonObject {
                    put("tweet_id", "bad_tweet")
                    put("author_handle", "alice")
                    put("published_at", 2L)
                },
            ),
        )
        val firstEngine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> respondJson(
                    AndroidSyncLatestResponse(
                        generation = AndroidSyncGenerationDto(
                            generation_id = GENERATION_ID,
                            created_at_ms = nowMs,
                            status = "published",
                            source_version = "test",
                            item_count = 2,
                        ),
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/items" -> {
                    if (request.url.parameters["after"] == "1") {
                        respondJson(
                            AndroidSyncItemsResponse(
                                generation_id = GENERATION_ID,
                                items = listOf(malformedItem),
                                end_of_stream = true,
                            ),
                        )
                    } else {
                        respondJson(
                            AndroidSyncItemsResponse(
                                generation_id = GENERATION_ID,
                                items = listOf(firstItem),
                                next = "1",
                                end_of_stream = false,
                            ),
                        )
                    }
                }
                "/api/android/sync/generation/$GENERATION_ID/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = GENERATION_ID,
                        assets = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                else -> error("Unexpected request ${request.url}")
            }
        }

        val firstResult = runCatching { buildMirror(firstEngine).syncOnce() }

        assertTrue(firstResult.isFailure)
        assertNotNull(db.channelDao().getById("sample_channel_one"))
        assertNull(db.feedItemDao().getById("bad_tweet"))
        assertEquals(1L, dao.importedItemSeq(GENERATION_ID))
        assertEquals(0, dao.countItemsImportCompleteForImporter(GENERATION_ID, ANDROID_SYNC_ITEM_IMPORTER_VERSION))
        waitForLog("android_sync_item_parse_failed")
        client.close()

        val requestedAfterMarkers = Collections.synchronizedList(mutableListOf<String?>())
        val secondEngine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> respondJson(
                    AndroidSyncLatestResponse(
                        generation = AndroidSyncGenerationDto(
                            generation_id = GENERATION_ID,
                            created_at_ms = nowMs,
                            status = "published",
                            source_version = "test",
                            item_count = 2,
                        ),
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/items" -> {
                    val after = request.url.parameters["after"]
                    requestedAfterMarkers += after
                    assertEquals("1", after)
                    respondJson(
                        AndroidSyncItemsResponse(
                            generation_id = GENERATION_ID,
                            items = listOf(correctedItem),
                            end_of_stream = true,
                        ),
                    )
                }
                "/api/android/sync/generation/$GENERATION_ID/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = GENERATION_ID,
                        assets = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                else -> error("Unexpected request ${request.url}")
            }
        }

        buildMirror(secondEngine).syncOnce()

        assertEquals(listOf("1"), requestedAfterMarkers.toList())
        assertNotNull(db.feedItemDao().getById("bad_tweet"))
        assertEquals(2L, dao.importedItemSeq(GENERATION_ID))
        assertEquals(1, dao.countItemsImportCompleteForImporter(GENERATION_ID, ANDROID_SYNC_ITEM_IMPORTER_VERSION))
    }

    @Test fun itemImportLogsPageDecodeAndIngestCounters() = runBlocking {
        val firstItem = AndroidSyncItemDto(
            seq = 1,
            item_kind = "channels",
            item_id = "sample_channel",
            payload = BundleEnvelope(
                primary_kind = "channels",
                primary = buildJsonObject {
                    put("channel_id", "sample_channel")
                    put("source_id", "sample")
                    put("name", "Sample Channel")
                    put("platform", "youtube")
                },
            ),
        )
        val secondItem = AndroidSyncItemDto(
            seq = 2,
            item_kind = "channels",
            item_id = "sample_channel_two",
            payload = BundleEnvelope(
                primary_kind = "channels",
                primary = buildJsonObject {
                    put("channel_id", "sample_channel_two")
                    put("source_id", "sample_two")
                    put("name", "Sample Channel Two")
                    put("platform", "youtube")
                },
            ),
        )
        var rawItemsBody = ""
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> respondJson(
                    AndroidSyncLatestResponse(
                        generation = AndroidSyncGenerationDto(
                            generation_id = GENERATION_ID,
                            created_at_ms = nowMs,
                            status = "published",
                            source_version = "test",
                            item_count = 2,
                        ),
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/items" -> {
                    rawItemsBody = iglooJson.encodeToString(
                        AndroidSyncItemsResponse(
                            generation_id = GENERATION_ID,
                            items = listOf(firstItem, secondItem),
                            end_of_stream = true,
                        ),
                    )
                    respond(rawItemsBody, HttpStatusCode.OK, jsonHeaders())
                }
                "/api/android/sync/generation/$GENERATION_ID/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = GENERATION_ID,
                        assets = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                else -> error("Unexpected request ${request.url}")
            }
        }

        buildMirror(engine).syncOnce()

        val pageLog = waitForLog("android_sync_items_page")
        assertEquals(rawItemsBody.encodeToByteArray().size, pageLog.fields["page_bytes"])
        assertTrue((pageLog.fields["decode_ms"] as Long) >= 0L)
        assertTrue((pageLog.fields["ledger_write_ms"] as Long) >= 0L)
        assertTrue((pageLog.fields["changed_item_query_ms"] as Long) >= 0L)
        assertEquals(2, pageLog.fields["changed"])
        assertEquals(0, pageLog.fields["skipped"])
        assertEquals(1, pageLog.fields["ingest_transactions"])
        assertEquals(2, pageLog.fields["ingest_ok"])
        assertEquals(0, pageLog.fields["ingest_unknown"])
        assertEquals(0, pageLog.fields["ingest_parse_failed"])
        assertTrue((pageLog.fields["ingest_transaction_ms"] as Long) >= 0L)
        assertNotNull(db.channelDao().getById("sample_channel"))
        assertNotNull(db.channelDao().getById("sample_channel_two"))
    }

    @Test fun unchangedPayloadFromOlderProjectionVersionStillRefreshesLocalProjection() = runBlocking {
        val dao = db.androidSyncDao()
        val oldGenerationId = "android-sync-old"
        val currentGenerationId = "android-sync-current"
        val item = AndroidSyncItemDto(
            seq = 1,
            item_kind = "videos",
            item_id = "sample_tiktok_video_1",
            payload = BundleEnvelope(
                primary_kind = "videos",
                primary = buildJsonObject {
                    put("video_id", "sample_tiktok_video_1")
                    put("channel_id", "sample_channel")
                    put("title", "Synced clip")
                    put("published_at", 123L)
                    put("canonical_url", "https://www.tiktok.com/@sample_user/video/sample_video_1")
                },
            ),
        )
        dao.upsertGeneration(
            generationEntity(
                generationId = oldGenerationId,
                createdAtMs = nowMs - 1,
                itemsImportedAtMs = nowMs - 1,
            ),
        )
        dao.upsertItems(
            listOf(
                AndroidSyncItemEntity(
                    generationId = oldGenerationId,
                    seq = item.seq,
                    itemKind = item.item_kind,
                    itemId = item.item_id,
                    payloadJson = iglooJson.encodeToString(item.payload),
                ),
            ),
        )
        db.videoDao().upsert(
            VideoEntity(
                videoId = "sample_tiktok_video_1",
                channelId = "sample_channel",
                title = "Old clip",
                publishedAt = 123L,
                canonicalUrl = null,
            ),
        )
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> respondJson(
                    AndroidSyncLatestResponse(
                        generation = AndroidSyncGenerationDto(
                            generation_id = currentGenerationId,
                            created_at_ms = nowMs,
                            status = "published",
                            source_version = "test",
                            item_count = 1,
                        ),
                    ),
                )
                "/api/android/sync/generation/$currentGenerationId/items" -> respondJson(
                    AndroidSyncItemsResponse(
                        generation_id = currentGenerationId,
                        items = listOf(item),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/generation/$currentGenerationId/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = currentGenerationId,
                        assets = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                else -> error("Unexpected request ${request.url}")
            }
        }

        buildMirror(engine).syncOnce()

        assertEquals(
            "https://www.tiktok.com/@sample_user/video/sample_video_1",
            db.videoDao().getById("sample_tiktok_video_1")?.canonicalUrl,
        )
    }

    @Test fun feedRankSnapshotIsImportedFromAndroidSyncPayload() = runBlocking {
        val feedItems = listOf(
            AndroidSyncItemDto(
                seq = 1,
                item_kind = "feed_items",
                item_id = "rank_one",
                payload = BundleEnvelope(
                    primary_kind = "feed_items",
                    primary = buildJsonObject {
                        put("tweet_id", "rank_one")
                        put("author_handle", "alice")
                        put("published_at", 1L)
                    },
                ),
            ),
            AndroidSyncItemDto(
                seq = 2,
                item_kind = "feed_items",
                item_id = "rank_two",
                payload = BundleEnvelope(
                    primary_kind = "feed_items",
                    primary = buildJsonObject {
                        put("tweet_id", "rank_two")
                        put("author_handle", "bob")
                        put("published_at", 2L)
                    },
                ),
            ),
        )
        val item = AndroidSyncItemDto(
            seq = 3,
            item_kind = "feed_rank",
            item_id = "snapshot",
            payload = BundleEnvelope(
                primary_kind = "feed_rank",
                primary = buildJsonObject {
                    put("snapshot_at", 1234L)
                    put("row_count", 2)
                    put(
                        "rows",
                        kotlinx.serialization.json.Json.parseToJsonElement(
                            """
                            [
                              {"tweet_id":"rank_one","rank_position":1},
                              {"tweet_id":"rank_two","rank_position":2}
                            ]
                            """.trimIndent(),
                        ),
                    )
                },
            ),
        )
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> respondJson(
                    AndroidSyncLatestResponse(
                        generation = AndroidSyncGenerationDto(
                            generation_id = GENERATION_ID,
                            created_at_ms = nowMs,
                            status = "published",
                            source_version = "test",
                            item_count = 3,
                        ),
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/items" -> respondJson(
                    AndroidSyncItemsResponse(
                        generation_id = GENERATION_ID,
                        items = feedItems + item,
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = GENERATION_ID,
                        assets = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                else -> error("Unexpected request ${request.url}")
            }
        }

        buildMirror(engine).syncOnce()

        assertEquals(2, db.feedRankDao().count())
        assertEquals(1234L, db.feedRankDao().currentSnapshotAt())
    }

    @Test fun bookmarkMetadataIsImportedFromAndroidSyncPayload() = runBlocking {
        db.bookmarkCategoryDao().upsert(BookmarkCategoryEntity(categoryId = 2L, name = "stale", createdAt = 1L))
        db.bookmarkLabelDao().upsert(BookmarkLabelEntity(label = "stale-label", syncedAt = 1L))
        val item = AndroidSyncItemDto(
            seq = 1,
            item_kind = "bookmark_metadata",
            item_id = "snapshot",
            payload = BundleEnvelope(
                primary_kind = "bookmark_metadata",
                primary = buildJsonObject {
                    put("version", 1)
                    put("snapshot_at", 777L)
                    put(
                        "categories",
                        kotlinx.serialization.json.Json.parseToJsonElement(
                            """[{"category_id":8,"name":"archive","archive_path":"/archive","created_at":500}]""",
                        ),
                    )
                    put(
                        "labels",
                        kotlinx.serialization.json.Json.parseToJsonElement(
                            """[{"label":"saved label"}]""",
                        ),
                    )
                },
            ),
        )
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> respondJson(
                    AndroidSyncLatestResponse(
                        generation = AndroidSyncGenerationDto(
                            generation_id = GENERATION_ID,
                            created_at_ms = nowMs,
                            status = "published",
                            source_version = "test",
                            item_count = 1,
                        ),
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/items" -> respondJson(
                    AndroidSyncItemsResponse(
                        generation_id = GENERATION_ID,
                        items = listOf(item),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = GENERATION_ID,
                        assets = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                else -> error("Unexpected request ${request.url}")
            }
        }

        buildMirror(engine).syncOnce()

        assertEquals(listOf(8L), db.bookmarkCategoryDao().all().map { it.categoryId })
        assertEquals("/archive", db.bookmarkCategoryDao().getById(8L)?.archivePath)
        assertEquals(listOf("saved label"), db.bookmarkLabelDao().all().map { it.label })
        assertEquals(777L, db.bookmarkLabelDao().all().single().syncedAt)
    }

    @Test fun userStateAttachmentsAreImportedFromAndroidSyncPayload() = runBlocking {
        val channelItem = AndroidSyncItemDto(
            seq = 1,
            item_kind = "channels",
            item_id = "youtube_stateful",
            payload = BundleEnvelope(
                primary_kind = "channels",
                primary = buildJsonObject {
                    put("channel_id", "youtube_stateful")
                    put("name", "Stateful")
                    put("platform", "youtube")
                },
                attachments = buildJsonObject {
                    put(
                        "user_state",
                        kotlinx.serialization.json.Json.parseToJsonElement(
                            """{"version":1,"channel_follows":[{"channel_id":"youtube_stateful","followed":true,"followed_at":2000}],"channel_stars":[{"channel_id":"youtube_stateful","starred":true,"starred_at":2001}]}""",
                        ),
                    )
                },
            ),
        )
        val feedItem = AndroidSyncItemDto(
            seq = 2,
            item_kind = "feed_items",
            item_id = "tw_stateful",
            payload = BundleEnvelope(
                primary_kind = "feed_items",
                primary = buildJsonObject {
                    put("tweet_id", "tw_stateful")
                    put("author_handle", "alice")
                    put("published_at", 123L)
                },
                attachments = buildJsonObject {
                    put(
                        "user_state",
                        kotlinx.serialization.json.Json.parseToJsonElement(
                            """{"version":1,"feed_likes":[{"tweet_id":"tw_stateful","liked":true,"liked_at":3000}],"feed_seen":[{"tweet_id":"tw_stateful","seen":true,"seen_at":3001}],"muted_accounts":[{"handle":"alice","muted":true,"muted_at":3002}]}""",
                        ),
                    )
                },
            ),
        )
        val videoItem = AndroidSyncItemDto(
            seq = 3,
            item_kind = "videos",
            item_id = "vid_stateful",
            payload = BundleEnvelope(
                primary_kind = "videos",
                primary = buildJsonObject {
                    put("video_id", "vid_stateful")
                    put("channel_id", "youtube_stateful")
                    put("title", "Stateful video")
                    put("published_at", 124L)
                },
                attachments = buildJsonObject {
                    put(
                        "user_state",
                        kotlinx.serialization.json.Json.parseToJsonElement(
                            """{"version":1,"bookmarks":[{"video_id":"vid_stateful","bookmarked":true,"category_id":7,"bookmarked_at":4000}],"moment_views":[{"video_id":"vid_stateful","viewed":true,"viewed_at":4001}],"watch_history":[{"video_id":"vid_stateful","playback_position":55.0,"duration":120.0,"progress_updated_at_ms":4002,"progress_source":"server","last_watched":4002}]}""",
                        ),
                    )
                },
            ),
        )
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> respondJson(
                    AndroidSyncLatestResponse(
                        generation = AndroidSyncGenerationDto(
                            generation_id = GENERATION_ID,
                            created_at_ms = nowMs,
                            status = "published",
                            source_version = "test",
                            item_count = 3,
                        ),
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/items" -> respondJson(
                    AndroidSyncItemsResponse(
                        generation_id = GENERATION_ID,
                        items = listOf(channelItem, feedItem, videoItem),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = GENERATION_ID,
                        assets = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                else -> error("Unexpected request ${request.url}")
            }
        }

        buildMirror(engine).syncOnce()

        assertTrue(db.channelFollowDao().exists("youtube_stateful"))
        assertTrue(db.channelStarDao().exists("youtube_stateful"))
        assertTrue(db.feedLikeDao().exists("tw_stateful"))
        assertTrue(db.feedSeenDao().exists("tw_stateful"))
        assertTrue(db.mutedAccountDao().exists("alice"))
        assertEquals(7L, db.bookmarkDao().getById("vid_stateful")?.categoryId)
        assertTrue(db.momentViewDao().exists("vid_stateful"))
        assertEquals(55.0, db.watchHistoryDao().getById("vid_stateful")?.playbackPosition ?: 0.0, 0.001)
    }

    @Test fun previewTrackJsonAssetIsImportedAndDownloaded() = runBlocking {
        val body = """{"version":1,"duration_ms":1000,"tile_width":160,"tile_height":90,"columns":5,"cues":[]}"""
        val asset = AndroidSyncAssetDto(
            seq = 1,
            asset_id = "youtube_video_vid_1_preview_track_json_0",
            asset_kind = "preview_track_json",
            owner_id = "vid_1",
            owner_kind = "video",
            bucket = "videos",
            server_url = "/api/android/sync/assets/youtube_video_vid_1_preview_track_json_0",
            content_type = "application/json",
            size_bytes = body.length.toLong(),
            state = "ready",
            effective_recency_ms = nowMs,
        )
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> respondJson(
                    AndroidSyncLatestResponse(
                        generation = AndroidSyncGenerationDto(
                            generation_id = GENERATION_ID,
                            created_at_ms = nowMs,
                            status = "published",
                            source_version = "test",
                            asset_count = 1,
                            ready_asset_count = 1,
                        ),
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/items" -> respondJson(
                    AndroidSyncItemsResponse(
                        generation_id = GENERATION_ID,
                        items = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = GENERATION_ID,
                        assets = listOf(asset),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                "/api/android/sync/assets/youtube_video_vid_1_preview_track_json_0" -> respond(
                    ByteReadChannel(body),
                    HttpStatusCode.OK,
                    headersOf("Content-Type" to listOf("application/json")),
                )
                else -> error("Unexpected request ${request.url}")
            }
        }

        buildMirror(engine).syncOnce()

        val row = db.androidSyncDao()
            .latestVerifiedAssetsForOwner("vid_1", listOf("preview_track_json"))
            .single()
        assertEquals("application/json", row.contentType)
        assertEquals("preview_track_json", row.assetKind)
    }

    @Test fun syncImportsItemsBeforeAssetDrain() = runBlocking {
        val requestOrder = Collections.synchronizedList(mutableListOf<String>())
        val itemsSeen = CountDownLatch(1)
        val asset = AndroidSyncAssetDto(
            seq = 1,
            asset_id = "asset-1",
            asset_kind = "video_stream",
            owner_id = "clip-1",
            owner_kind = "video",
            bucket = "videos",
            server_url = "/api/android/sync/assets/asset-1",
            content_type = "video/mp4",
            size_bytes = "video-body".length.toLong(),
            state = "ready",
            effective_recency_ms = nowMs,
        )
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> respondJson(
                    AndroidSyncLatestResponse(
                        generation = AndroidSyncGenerationDto(
                            generation_id = GENERATION_ID,
                            created_at_ms = nowMs,
                            status = "published",
                            source_version = "test",
                            asset_count = 1,
                            ready_asset_count = 1,
                        ),
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/items" -> {
                    requestOrder += "items"
                    itemsSeen.countDown()
                    respondJson(
                        AndroidSyncItemsResponse(
                            generation_id = GENERATION_ID,
                            items = emptyList(),
                            end_of_stream = true,
                        ),
                    )
                }
                "/api/android/sync/generation/$GENERATION_ID/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = GENERATION_ID,
                        assets = listOf(asset),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                "/api/android/sync/assets/asset-1" -> {
                    requestOrder += "asset"
                    assertTrue(
                        "item import should start before asset drain is allowed to finish",
                        itemsSeen.await(2, TimeUnit.SECONDS),
                    )
                    respond(
                        ByteReadChannel("video-body"),
                        HttpStatusCode.OK,
                        headersOf("Content-Type" to listOf("video/mp4")),
                    )
                }
                else -> error("Unexpected request ${request.url}")
            }
        }
        val mirror = buildMirror(engine)

        mirror.syncOnce()

        assertEquals(1, db.androidSyncDao().countItemsImportComplete(GENERATION_ID))
        assertTrue(requestOrder.contains("items"))
        assertTrue(requestOrder.contains("asset"))
    }

    @Test fun claimableAssetsPrioritizesVisibleImagesBeforeBulkProfiles() = runBlocking {
        val assets = listOf(
            "avatar" to "profile",
            "banner" to "profile",
            "avatar" to "retention",
            "banner" to "retention",
            "preview_sprite" to null,
            "preview_track_json" to null,
            "subtitle" to null,
            "dearrow_thumbnail" to null,
            "post_audio" to null,
            "post_media" to null,
            "video_stream" to null,
            "post_thumbnail" to null,
        )
        db.androidSyncDao().importAssets(
            assets.mapIndexed { index, (kind, reason) ->
                AndroidSyncAssetEntity(
                    generationId = GENERATION_ID,
                    seq = (index + 1).toLong(),
                    assetId = "asset-$kind-${reason ?: "regular"}",
                    assetKind = kind,
                    ownerId = "owner-$kind-${reason ?: "regular"}",
                    ownerKind = "video",
                    bucket = "bucket",
                    serverUrl = "/api/android/sync/assets/asset-$kind-${reason ?: "regular"}",
                    contentType = "application/octet-stream",
                    sizeBytes = 0,
                    serverState = "ready",
                    requiredReason = reason,
                    effectiveRecencyMs = nowMs,
                )
            },
            nowMs,
        )

        val ordered = db.androidSyncDao()
            .claimableAssets(GENERATION_ID, nowMs, assets.size)
            .map { "${it.assetKind}:${it.requiredReason ?: "regular"}" }

        assertEquals(
            listOf(
                "post_thumbnail:regular",
                "banner:retention",
                "avatar:retention",
                "post_media:regular",
                "post_audio:regular",
                "video_stream:regular",
                "subtitle:regular",
                "dearrow_thumbnail:regular",
                "banner:profile",
                "avatar:profile",
                "preview_track_json:regular",
                "preview_sprite:regular",
            ),
            ordered,
        )
    }

    @Test fun newGenerationAdoptsPreviouslyVerifiedAssetWithSameIdentityAndHash() = runBlocking {
        val file = tmpFolder.newFile("existing-banner.jpg").also { it.writeText("banner") }
        val oldAsset = AndroidSyncAssetEntity(
            generationId = "android-sync-old",
            seq = 1,
            assetId = "twitter_channel_twitter_alice_banner",
            assetKind = "banner",
            ownerId = "twitter_alice",
            ownerKind = "channel",
            bucket = "banners",
            serverUrl = "/api/android/sync/assets/twitter_channel_twitter_alice_banner",
            contentType = "image/jpeg",
            sizeBytes = file.length(),
            sha256 = "same-hash",
            serverState = "ready",
            effectiveRecencyMs = nowMs,
        )
        val newAsset = oldAsset.copy(generationId = "android-sync-new", seq = 2)

        db.androidSyncDao().importAssets(listOf(oldAsset), nowMs)
        db.androidSyncDao().markVerified(
            generationId = oldAsset.generationId,
            assetId = oldAsset.assetId,
            assetKind = oldAsset.assetKind,
            localPath = file.absolutePath,
            fileSize = file.length(),
            nowMs = nowMs + 1,
        )
        db.androidSyncDao().importAssets(listOf(newAsset), nowMs + 2)

        val adopted = db.androidSyncDao()
            .adoptVerifiedAssetsFromPreviousGenerations(newAsset.generationId, nowMs + 3)
        val counts = db.androidSyncDao().healthCounts(newAsset.generationId)
        val claimable = db.androidSyncDao().claimableAssets(newAsset.generationId, nowMs + 4, 10)

        assertEquals(1, adopted)
        assertEquals(1, counts.verified)
        assertEquals(file.length(), counts.verifiedBytes)
        assertTrue(claimable.isEmpty())
    }

    @Test fun latestVerifiedAssetsForOwnerCollapsesRepeatedGenerationRowsByMediaIndex() = runBlocking {
        val dao = db.androidSyncDao()
        val oldGeneration = generationEntity("android-sync-old", createdAtMs = 1)
        val currentGeneration = generationEntity("android-sync-current", createdAtMs = 2)
        val oldFirst = tmpFolder.newFile("old-slide-0.jpg").also { it.writeText("old-0") }
        val oldSecond = tmpFolder.newFile("old-slide-1.jpg").also { it.writeText("old-1") }
        val currentFirst = tmpFolder.newFile("current-slide-0.jpg").also { it.writeText("current-0") }
        val currentSecond = tmpFolder.newFile("current-slide-1.jpg").also { it.writeText("current-1") }

        dao.upsertGeneration(oldGeneration)
        dao.upsertGeneration(currentGeneration)
        dao.importAssets(
            listOf(
                slideSyncAsset(oldGeneration.generationId, index = 0, sizeBytes = oldFirst.length()),
                slideSyncAsset(oldGeneration.generationId, index = 1, sizeBytes = oldSecond.length()),
                slideSyncAsset(currentGeneration.generationId, index = 0, sizeBytes = currentFirst.length()),
                slideSyncAsset(currentGeneration.generationId, index = 1, sizeBytes = currentSecond.length()),
            ),
            nowMs,
        )
        listOf(
            oldGeneration.generationId to oldFirst,
            oldGeneration.generationId to oldSecond,
            currentGeneration.generationId to currentFirst,
            currentGeneration.generationId to currentSecond,
        ).forEachIndexed { index, (generationId, file) ->
            dao.markVerified(
                generationId = generationId,
                assetId = slideAssetId(index % 2),
                assetKind = "post_media",
                localPath = file.absolutePath,
                fileSize = file.length(),
                nowMs = nowMs + 1,
            )
        }

        val rows = dao.latestVerifiedAssetsForOwner("sample_slide_video", listOf("post_media"))
            .sortedBy { it.mediaIndex }

        assertEquals(listOf(0, 1), rows.map { it.mediaIndex })
        assertEquals(listOf(currentGeneration.generationId, currentGeneration.generationId), rows.map { it.generationId })
        assertEquals(listOf(currentFirst.absolutePath, currentSecond.absolutePath), rows.map { it.localPath })
    }

    @Test fun itemMarkerStallFailsWithoutMarkingImportComplete() = runBlocking {
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> respondJson(
                    AndroidSyncLatestResponse(
                        generation = AndroidSyncGenerationDto(
                            generation_id = GENERATION_ID,
                            created_at_ms = nowMs,
                            status = "published",
                            source_version = "test",
                            item_count = 1,
                        ),
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = GENERATION_ID,
                        assets = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/items" -> respondJson(
                    AndroidSyncItemsResponse(
                        generation_id = GENERATION_ID,
                        items = emptyList(),
                        next = "",
                        end_of_stream = false,
                    ),
                )
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                else -> error("Unexpected request ${request.url}")
            }
        }
        val mirror = buildMirror(engine)

        val failure = runCatching { mirror.syncOnce() }.exceptionOrNull()

        assertTrue(failure is IllegalStateException)
        assertEquals(0, db.androidSyncDao().countItemsImportComplete(GENERATION_ID))
    }

    @Test fun assetMarkerStallFailsWithoutMarkingImportComplete() = runBlocking {
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> respondJson(
                    AndroidSyncLatestResponse(
                        generation = AndroidSyncGenerationDto(
                            generation_id = GENERATION_ID,
                            created_at_ms = nowMs,
                            status = "published",
                            source_version = "test",
                            asset_count = 1,
                        ),
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = GENERATION_ID,
                        assets = emptyList(),
                        next = "",
                        end_of_stream = false,
                    ),
                )
                "/api/android/sync/generation/$GENERATION_ID/items" -> respondJson(
                    AndroidSyncItemsResponse(
                        generation_id = GENERATION_ID,
                        items = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                else -> error("Unexpected request ${request.url}")
            }
        }
        val mirror = buildMirror(engine)

        val failure = runCatching { mirror.syncOnce() }.exceptionOrNull()

        assertTrue(failure is IllegalStateException)
        assertEquals(0, db.androidSyncDao().countAssetsImportComplete(GENERATION_ID))
    }

    @Test fun pruneGenerationsKeepsOnlyCurrentLedgerRows() = runBlocking {
        val dao = db.androidSyncDao()
        val oldGeneration = generationEntity("android-sync-old", createdAtMs = 1)
        val currentGeneration = generationEntity("android-sync-current", createdAtMs = 2)

        dao.upsertGeneration(oldGeneration)
        dao.upsertGeneration(currentGeneration)
        dao.upsertItems(
            listOf(
                AndroidSyncItemEntity(
                    generationId = oldGeneration.generationId,
                    seq = 1,
                    itemKind = "videos",
                    itemId = "old-video",
                    payloadJson = "{}",
                ),
                AndroidSyncItemEntity(
                    generationId = currentGeneration.generationId,
                    seq = 1,
                    itemKind = "videos",
                    itemId = "current-video",
                    payloadJson = "{}",
                ),
            ),
        )
        dao.importAssets(
            listOf(
                AndroidSyncAssetEntity(
                    generationId = oldGeneration.generationId,
                    seq = 1,
                    assetId = "old-asset",
                    assetKind = "post_media",
                    ownerId = "old-video",
                    ownerKind = "video",
                    bucket = "bucket",
                    serverUrl = "/api/android/sync/assets/old-asset",
                    sizeBytes = 0,
                    serverState = "ready",
                    effectiveRecencyMs = nowMs,
                ),
                AndroidSyncAssetEntity(
                    generationId = currentGeneration.generationId,
                    seq = 1,
                    assetId = "current-asset",
                    assetKind = "post_media",
                    ownerId = "current-video",
                    ownerKind = "video",
                    bucket = "bucket",
                    serverUrl = "/api/android/sync/assets/current-asset",
                    sizeBytes = 0,
                    serverState = "ready",
                    effectiveRecencyMs = nowMs,
                ),
            ),
            nowMs,
        )

        val pruned = dao.pruneGenerationsExcept(currentGeneration.generationId)

        assertEquals(1, pruned)
        assertEquals(currentGeneration.generationId, dao.latestGenerationId())
        assertEquals(1, dao.maxImportedItemSeq(currentGeneration.generationId))
        assertEquals(1, dao.maxImportedAssetSeq(currentGeneration.generationId))
        assertEquals(0, dao.maxImportedItemSeq(oldGeneration.generationId))
        assertEquals(0, dao.maxImportedAssetSeq(oldGeneration.generationId))
    }

    @Test fun syncDeletesUnreferencedSyncAssetFilesBeforePruningOldGeneration() = runBlocking {
        val dao = db.androidSyncDao()
        val oldGeneration = generationEntity("android-sync-old", createdAtMs = 1)
        val oldFile = java.io.File(tmpFolder.root, "sync/shorts_videos/old-asset.bin").apply {
            parentFile?.mkdirs()
            writeText("old asset")
        }
        val orphanFile = java.io.File(tmpFolder.root, "sync/shorts_videos/orphan.bin").apply {
            parentFile?.mkdirs()
            writeText("orphan asset")
        }
        val oldAsset = AndroidSyncAssetEntity(
            generationId = oldGeneration.generationId,
            seq = 1,
            assetId = "old-asset",
            assetKind = "video_stream",
            ownerId = "dropped_short",
            ownerKind = "video",
            bucket = "shorts_videos",
            serverUrl = "/api/android/sync/assets/old-asset",
            contentType = "application/octet-stream",
            sizeBytes = oldFile.length(),
            serverState = "ready",
            effectiveRecencyMs = nowMs,
        )
        dao.upsertGeneration(oldGeneration)
        dao.importAssets(listOf(oldAsset), nowMs)
        dao.markVerified(
            generationId = oldAsset.generationId,
            assetId = oldAsset.assetId,
            assetKind = oldAsset.assetKind,
            localPath = oldFile.absolutePath,
            fileSize = oldFile.length(),
            nowMs = nowMs,
        )
        val engine = MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/android/sync/generation/latest" -> respondJson(
                    AndroidSyncLatestResponse(
                        generation = AndroidSyncGenerationDto(
                            generation_id = "android-sync-current",
                            created_at_ms = nowMs,
                            status = "published",
                            source_version = "current",
                        ),
                    ),
                )
                "/api/android/sync/generation/android-sync-current/items" -> respondJson(
                    AndroidSyncItemsResponse(
                        generation_id = "android-sync-current",
                        items = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/generation/android-sync-current/assets" -> respondJson(
                    AndroidSyncAssetsResponse(
                        generation_id = "android-sync-current",
                        assets = emptyList(),
                        end_of_stream = true,
                    ),
                )
                "/api/android/sync/health" -> respond("""{"ok":true}""", HttpStatusCode.OK, jsonHeaders())
                else -> error("Unexpected request ${request.url}")
            }
        }

        buildMirror(engine).syncOnce()

        assertFalse(oldFile.exists())
        assertFalse(orphanFile.exists())
        assertEquals(0, dao.maxImportedAssetSeq(oldGeneration.generationId))
    }

    @Test fun pruneContentOutsideGenerationDropsOnlyUnprotectedRows() = runBlocking {
        val dao = db.androidSyncDao()
        val generation = generationEntity("android-sync-current", createdAtMs = 2)
        dao.upsertGeneration(generation)

        db.channelDao().upsert(ChannelEntity("tiktok_keep", name = "Keep", platform = "tiktok"))
        db.channelDao().upsert(ChannelEntity("tiktok_drop", name = "Drop", platform = "tiktok"))
        db.channelDao().upsert(ChannelEntity("tiktok_saved", name = "Saved", platform = "tiktok"))
        db.channelProfileDao().upsert(ChannelProfileEntity("tiktok_profile_only", platform = "tiktok", displayName = "Profile Only"))
        db.channelProfileDao().upsert(ChannelProfileEntity("tiktok_stale_profile", platform = "tiktok", displayName = "Stale Profile"))
        db.channelProfileDao().upsert(ChannelProfileEntity("tiktok_drop", platform = "tiktok", displayName = "Drop"))
        db.channelProfileDao().upsert(ChannelProfileEntity("tiktok_saved", platform = "tiktok", displayName = "Saved"))
        db.channelStarDao().upsert(ChannelStarEntity("tiktok_drop", starredAt = 20))
        db.channelSettingDao().upsert(ChannelSettingEntity("tiktok_drop", maxVideos = 10, updatedAt = 20))
        db.videoDao().upsert(VideoEntity("kept_short", "tiktok_keep", publishedAt = 10))
        db.videoDao().upsert(VideoEntity("dropped_short", "tiktok_drop", publishedAt = 10))
        db.videoDao().upsert(VideoEntity("saved_short", "tiktok_saved", publishedAt = 10))
        db.bookmarkDao().upsert(BookmarkEntity(videoId = "saved_short", categoryId = 0, bookmarkedAt = 20))

        db.feedItemDao().upsert(FeedItemEntity("sample_tweet_kept", authorHandle = "sample_author_a", publishedAt = 10, channelId = "twitter_sample_author_a", contentHash = "sample_kept_hash"))
        db.feedItemDao().upsert(FeedItemEntity("sample_tweet_dropped", authorHandle = "sample_author_b", publishedAt = 10, channelId = "twitter_sample_author_b", contentHash = "sample_drop_hash"))
        db.feedItemDao().upsert(FeedItemEntity("sample_tweet_liked", authorHandle = "sample_author_c", publishedAt = 10, channelId = "twitter_sample_author_c"))
        db.feedItemDao().upsert(FeedItemEntity("sample_parent_stale", authorHandle = "sample_author_d", publishedAt = 5, channelId = "twitter_sample_author_d"))
        db.feedLikeDao().upsert(FeedLikeEntity("sample_tweet_liked", likedAt = 20))
        db.feedThreadContextDao().replaceForLeaf(
            "sample_tweet_kept",
            listOf(FeedThreadContextEntity("sample_tweet_kept", "sample_parent_stale", "sample_parent_stale", 0)),
        )
        db.retweetSourceDao().upsert(
            listOf(
                RetweetSourceEntity(contentHash = "sample_kept_hash", retweeterHandle = "sample_reposter_a", tweetId = "sample_tweet_kept", publishedAt = 20),
                RetweetSourceEntity(contentHash = "sample_drop_hash", retweeterHandle = "sample_reposter_b", tweetId = "sample_tweet_dropped", publishedAt = 20),
            ),
        )
        dao.importAssets(
            listOf(
                AndroidSyncAssetEntity(
                    generationId = generation.generationId,
                    seq = 1,
                    assetId = "kept_media",
                    assetKind = "post_media",
                    ownerId = "sample_tweet_kept",
                    ownerKind = "feed_item",
                    bucket = "twitter_media",
                    serverUrl = "/api/android/sync/generation/${generation.generationId}/assets/kept_media",
                    contentType = "image/jpeg",
                    sizeBytes = 1,
                    serverState = "ready",
                    effectiveRecencyMs = nowMs,
                ),
            ),
            nowMs,
        )
        db.mediaInventoryDao().upsert(
            listOf(
                legacyMedia("kept_media", ownerId = "sample_tweet_kept"),
                legacyMedia("stale_media", ownerId = "sample_tweet_kept"),
                legacyMedia("orphan_media", ownerId = "sample_tweet_missing"),
                legacyMedia("unowned_media", ownerId = null),
            ),
        )

        dao.upsertItems(
            listOf(
                AndroidSyncItemEntity(generation.generationId, 1, "videos", "kept_short", "{}"),
                AndroidSyncItemEntity(generation.generationId, 2, "videos", "saved_short", "{}"),
                AndroidSyncItemEntity(generation.generationId, 3, "feed_items", "sample_tweet_kept", "{}"),
                AndroidSyncItemEntity(generation.generationId, 4, "feed_items", "sample_tweet_liked", "{}"),
                AndroidSyncItemEntity(generation.generationId, 5, "channels", "tiktok_keep", "{}"),
                AndroidSyncItemEntity(generation.generationId, 6, "channels", "tiktok_saved", "{}"),
                AndroidSyncItemEntity(generation.generationId, 7, "channel_profiles", "tiktok_profile_only", "{}"),
                AndroidSyncItemEntity("android-sync-old", 1, "channel_profiles", "tiktok_stale_profile", "{}"),
            ),
        )

        val counts = dao.pruneContentOutsideGeneration(generation.generationId)

        assertEquals(1, counts.videos)
        assertEquals(2, counts.feedItems)
        assertEquals(1, counts.channels)
        assertEquals(2, counts.legacyAssets)
        assertEquals(4, counts.sideRows)
        assertNotNull(db.videoDao().getById("kept_short"))
        assertNull(db.videoDao().getById("dropped_short"))
        assertNotNull(db.videoDao().getById("saved_short"))
        assertNotNull(db.feedItemDao().getById("sample_tweet_kept"))
        assertNull(db.feedItemDao().getById("sample_tweet_dropped"))
        assertNotNull(db.feedItemDao().getById("sample_tweet_liked"))
        assertNull(db.feedItemDao().getById("sample_parent_stale"))
        assertEquals(0, tableCount("feed_thread_context"))
        assertEquals(1, db.retweetSourceDao().countForContentHash("sample_kept_hash"))
        assertEquals(0, db.retweetSourceDao().countForContentHash("sample_drop_hash"))
        assertNull(db.channelDao().getById("tiktok_drop"))
        assertNotNull(db.channelDao().getById("tiktok_saved"))
        assertNull(db.channelProfileDao().getById("tiktok_drop"))
        assertNotNull(db.channelProfileDao().getById("tiktok_saved"))
        assertNotNull(db.channelProfileDao().getById("tiktok_profile_only"))
        assertNull(db.channelProfileDao().getById("tiktok_stale_profile"))
        assertFalse(db.channelStarDao().exists("tiktok_drop"))
        assertNull(db.channelSettingDao().getById("tiktok_drop"))
        assertEquals(listOf("kept_media"), db.mediaInventoryDao().forOwner("sample_tweet_kept").map { it.assetId })
        assertEquals(emptyList<MediaInventoryEntity>(), db.mediaInventoryDao().forOwner("sample_tweet_missing"))
        assertEquals(2, tableCount("media_inventory"))
    }

    @Test fun pruneContentOutsideGenerationPreservesPendingChannelSideRows() = runBlocking {
        val dao = db.androidSyncDao()
        val generation = generationEntity("android-sync-current", createdAtMs = 2)
        dao.upsertGeneration(generation)

        db.channelDao().upsert(ChannelEntity("tiktok_pending", name = "Pending", platform = "tiktok"))
        db.channelFollowDao().upsert(ChannelFollowEntity("tiktok_pending", followedAt = 20))
        db.channelStarDao().upsert(ChannelStarEntity("tiktok_pending", starredAt = 21))
        db.channelSettingDao().upsert(ChannelSettingEntity("tiktok_pending", maxVideos = 10, updatedAt = 22))
        db.outboxDao().insert(pendingOutbox(OutboxKind.CODE_FOLLOW, itemId = "tiktok_pending"))
        db.outboxDao().insert(pendingOutbox(OutboxKind.CODE_STAR, itemId = "tiktok_pending"))
        db.outboxDao().insert(
            pendingOutbox(
                OutboxKind.CODE_CHANNEL_SETTING,
                itemId = "tiktok_pending",
                field = "max_videos",
            ),
        )

        dao.pruneContentOutsideGeneration(generation.generationId)

        assertTrue(db.channelFollowDao().exists("tiktok_pending"))
        assertTrue(db.channelStarDao().exists("tiktok_pending"))
        assertNotNull(db.channelSettingDao().getById("tiktok_pending"))
        assertNotNull(db.channelDao().getById("tiktok_pending"))
    }

    private fun tableCount(table: String): Int {
        db.openHelper.readableDatabase.query("SELECT COUNT(*) FROM $table").use { cursor ->
            assertTrue(cursor.moveToFirst())
            return cursor.getInt(0)
        }
    }

    private fun legacyMedia(
        assetId: String,
        ownerId: String?,
    ) = MediaInventoryEntity(
        assetId = assetId,
        assetKind = "post_media",
        scope = "sync_compat",
        ownerId = ownerId,
        bucket = "twitter_media",
        serverUrl = "/api/media/$assetId",
        state = "cached",
        addedAtMs = nowMs,
    )

    private fun buildMirror(
        engine: MockEngine,
        retentionProvider: suspend () -> AndroidSyncRetentionRequest = {
            AndroidSyncRetentionRequest(feedDays = 7, youtubeDays = 7, momentsDays = 7, storyHours = 48)
        },
        refreshRetryDelayMs: Long = 30_000L,
        metadataRetryDelaysMs: List<Long> = listOf(1_000L, 5_000L, 15_000L),
        refreshRetryEnabledProvider: () -> Boolean = { true },
    ): AndroidSyncMirror {
        client = HttpClient(engine) {
            expectSuccess = false
            install(ContentNegotiation) {
                json(iglooJson)
            }
        }
        val foregroundPromoter = NoopForegroundPromoter()
        val reachability = Reachability(
            scope = scope,
            probe = { true },
            foregroundFlow = MutableSharedFlow(extraBufferCapacity = 1),
        )
        val baseUrlProvider = ServerBaseUrlProvider { BASE_URL }
        return AndroidSyncMirror(
            scope = scope,
            db = db,
            dao = db.androidSyncDao(),
            outboxDao = db.outboxDao(),
            api = AndroidSyncApi(client, baseUrlProvider::baseUrl),
            client = client,
            baseUrlProvider = baseUrlProvider,
            reachability = reachability,
            foregroundPromoter = foregroundPromoter,
            mediaRoot = tmpFolder.root,
            logger = logger,
            retentionProvider = retentionProvider,
            nowMsProvider = { nowMs },
            refreshRetryDelayMs = refreshRetryDelayMs,
            metadataRetryDelaysMs = metadataRetryDelaysMs,
            refreshRetryEnabledProvider = refreshRetryEnabledProvider,
        )
    }

    private fun pendingOutbox(kind: String, itemId: String? = null, field: String? = null) =
        OutboxEntity(
            kind = kind,
            itemId = itemId,
            field = field,
            payloadJson = "{}",
            state = "pending",
        )

    private inner class NoopForegroundPromoter : ForegroundPromoter(
        context = ApplicationProvider.getApplicationContext(),
        logger = logger,
    ) {
        override fun startActiveDrain() = Unit
        override fun finishActiveDrain() = Unit
        override fun startDownloading(assetIds: Collection<String>) = Unit
        override fun finishedBatch(assetIds: Collection<String>) = Unit
    }

    private inline fun <reified T> MockRequestHandleScope.respondJson(body: T) =
        respond(iglooJson.encodeToString(body), HttpStatusCode.OK, jsonHeaders())

    private fun MockRequestHandleScope.respondOk(body: String) =
        respond(body, HttpStatusCode.OK, headersOf("Content-Type", ContentType.Application.Json.toString()))

    private fun jsonHeaders() = headersOf("Content-Type", ContentType.Application.Json.toString())

    private fun sha256Hex(body: String): String {
        val digest = MessageDigest.getInstance("SHA-256").digest(body.toByteArray())
        return digest.joinToString("") { "%02x".format(it) }
    }

    private fun generationEntity(
        generationId: String,
        createdAtMs: Long,
        itemsImportedAtMs: Long? = null,
        assetsImportedAtMs: Long? = null,
    ) = AndroidSyncGenerationEntity(
        generationId = generationId,
        createdAtMs = createdAtMs,
        status = "ready",
        sourceVersion = generationId,
        retentionJson = "{}",
        itemCount = 1,
        assetCount = 1,
        readyAssetCount = 1,
        serverMissingAssetCount = 0,
        totalBytes = 0,
        itemsImportedAtMs = itemsImportedAtMs,
        assetsImportedAtMs = assetsImportedAtMs,
    )

    private fun slideSyncAsset(
        generationId: String,
        index: Int,
        sizeBytes: Long,
    ) = AndroidSyncAssetEntity(
        generationId = generationId,
        seq = (index + 1).toLong(),
        assetId = slideAssetId(index),
        assetKind = "post_media",
        mediaIndex = index,
        ownerId = "sample_slide_video",
        ownerKind = "tiktok_video",
        bucket = "shorts_videos",
        serverUrl = "/api/media/slide/sample_slide_video/$index",
        contentType = "image/jpeg",
        sizeBytes = sizeBytes,
        sha256 = "slide-$index",
        serverState = "ready",
        effectiveRecencyMs = nowMs,
    )

    private fun slideAssetId(index: Int): String =
        "tiktok_tiktok_video_sample_slide_video_post_media_$index"

    private companion object {
        const val BASE_URL = "http://example.local"
        const val GENERATION_ID = "android-sync-test"
    }
}
