package com.screwy.igloo.sync

import androidx.test.core.app.ApplicationProvider
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.data.entity.AndroidSyncHeadEntity
import com.screwy.igloo.data.entity.AndroidSyncStateEntity
import com.screwy.igloo.data.entity.BookmarkEntity
import com.screwy.igloo.data.entity.FeedLikeEntity
import com.screwy.igloo.data.entity.FeedRankEntity
import com.screwy.igloo.data.entity.FeedSeenEntity
import com.screwy.igloo.data.entity.MomentViewEntity
import com.screwy.igloo.data.entity.MomentsCursorEntity
import com.screwy.igloo.data.entity.OfflineVideoDownloadEntity
import com.screwy.igloo.data.entity.OutboxEntity
import com.screwy.igloo.data.entity.VideoEntity
import com.screwy.igloo.data.entity.WatchHistoryEntity
import com.screwy.igloo.log.InMemoryLogSink
import com.screwy.igloo.log.Logger
import com.screwy.igloo.media.ForegroundPromoter
import com.screwy.igloo.net.AndroidSyncApi
import com.screwy.igloo.net.AndroidSyncChangeDto
import com.screwy.igloo.net.AndroidSyncPageResponse
import com.screwy.igloo.net.AndroidSyncRetentionRequest
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.net.iglooJson
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.MockRequestHandleScope
import io.ktor.client.engine.mock.respond
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.http.ContentType
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import io.ktor.serialization.kotlinx.json.json
import java.io.File
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.encodeToString
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
class AndroidVideoBinaryRetentionTest {
    @get:Rule val temporaryFolder = TemporaryFolder()

    private lateinit var db: IglooDatabase
    private lateinit var scope: CoroutineScope
    private lateinit var logger: Logger
    private val clients = mutableListOf<HttpClient>()
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
        clients.forEach(HttpClient::close)
        scope.cancel()
        db.close()
    }

    @Test
    fun oldAndTemporaryStreamsAreNotClaimableButAuxiliaryAssetsRemainClaimable() = runBlocking {
        db.videoDao().upsert(video("sample_old_video", publishedAt = 0))
        db.videoDao().upsert(video("sample_temp_video", publishedAt = nowMs, isTemp = true))
        db.bookmarkDao().upsert(BookmarkEntity(videoId = "sample_old_video", bookmarkedAt = nowMs))
        db.feedLikeDao().upsert(FeedLikeEntity(tweetId = "sample_old_video", likedAt = nowMs))
        for (asset in listOf(
                youtubeAsset("sample_old_stream", "sample_old_video", "video_stream"),
                youtubeAsset("sample_old_thumbnail", "sample_old_video", "thumbnail"),
                youtubeAsset("sample_old_preview", "sample_old_video", "post_media", contentType = "image/jpeg"),
                youtubeAsset("sample_old_subtitle", "sample_old_video", "subtitle"),
                youtubeAsset("sample_temp_stream", "sample_temp_video", "video_stream"),
                youtubeAsset("sample_temp_thumbnail", "sample_temp_video", "thumbnail"),
                youtubeAsset("sample_temp_preview", "sample_temp_video", "post_media", contentType = "image/jpeg"),
                youtubeAsset("sample_temp_subtitle", "sample_temp_video", "subtitle"),
            )) {
            db.androidSyncDao().upsertAsset(asset)
        }

        val claimable =
            db.androidSyncDao().claimableAssets(
                nowMs = nowMs,
                youtubeCutoffMs = nowMs - RETENTION.youtubeDays * DAY_MS,
                limit = 20,
            )

        assertEquals(
            setOf(
                "sample_old_thumbnail",
                "sample_old_preview",
                "sample_old_subtitle",
                "sample_temp_thumbnail",
                "sample_temp_preview",
                "sample_temp_subtitle",
            ),
            claimable.map(AndroidSyncAssetEntity::assetId).toSet(),
        )
    }

    @Test
    fun requestedOldPrimaryAssetBecomesClaimableAndCompletesAsManualDownload() = runBlocking {
        val videoId = "sample_old_video"
        db.videoDao().upsert(video(videoId, publishedAt = 0))
        db.androidSyncDao().upsertAsset(
            youtubeAsset(
                assetId = "sample_video_audio",
                videoId = videoId,
                assetKind = "post_audio",
                sizeBytes = 6,
                nextAttemptAtMs = Long.MAX_VALUE,
            ),
        )
        var syncTriggers = 0
        val actions =
            OfflineVideoDownloads(
                db = db,
                syncDao = db.androidSyncDao(),
                downloads = db.offlineVideoDownloadDao(),
                mediaRoot = temporaryFolder.root,
                nowMsProvider = { nowMs },
                syncTrigger = { syncTriggers++ },
            )

        actions.requestDownload(videoId)

        assertEquals("requested", db.offlineVideoDownloadDao().get(videoId)?.state)
        assertEquals(1, syncTriggers)
        assertEquals(
            listOf("sample_video_audio"),
            db.androidSyncDao()
                .claimableAssets(
                    nowMs = nowMs,
                    youtubeCutoffMs = nowMs - RETENTION.youtubeDays * DAY_MS,
                    limit = 20,
                )
                .map(AndroidSyncAssetEntity::assetId),
        )

        buildAssetDrainer(MockEngine { respond("stream", HttpStatusCode.OK) })
            .drain(youtubeCutoffMs = nowMs - RETENTION.youtubeDays * DAY_MS)

        val stream = requireNotNull(db.androidSyncDao().asset("sample_video_audio"))
        assertTrue(File(requireNotNull(stream.localPath)).isFile)
        assertEquals("downloaded", db.offlineVideoDownloadDao().get(videoId)?.state)
    }

    @Test
    fun pruningOldAutomaticStreamKeepsVideoAndAuxiliaryMetadata() = runBlocking {
        val videoId = "sample_old_video"
        db.videoDao().upsert(video(videoId, publishedAt = 0))
        db.bookmarkDao().upsert(BookmarkEntity(videoId = videoId, bookmarkedAt = nowMs))
        db.feedLikeDao().upsert(FeedLikeEntity(tweetId = videoId, likedAt = nowMs))
        db.androidSyncDao().upsertHead(
            AndroidSyncHeadEntity(
                ownerKind = "video",
                ownerId = videoId,
                retentionBucket = "youtube",
                retainAtMs = 0,
            ),
        )
        val streamFile = syncFile("sample_old_stream.mp4", "stream")
        val thumbnailFile = syncFile("sample_old_thumbnail.jpg", "image")
        val subtitleFile = syncFile("sample_old_subtitle.vtt", "WEBVTT")
        for (asset in listOf(
                youtubeAsset(
                    "sample_old_stream",
                    videoId,
                    "video_stream",
                    localPath = streamFile.absolutePath,
                ),
                youtubeAsset(
                    "sample_old_thumbnail",
                    videoId,
                    "thumbnail",
                    localPath = thumbnailFile.absolutePath,
                ),
                youtubeAsset(
                    "sample_old_subtitle",
                    videoId,
                    "subtitle",
                    localPath = subtitleFile.absolutePath,
                ),
            )) {
            db.androidSyncDao().upsertAsset(asset)
        }

        buildMirror(MockEngine { error("pruning should not request the network") }).prune()

        assertNotNull(db.videoDao().getById(videoId))
        assertNull(db.androidSyncDao().asset("sample_old_stream")?.localPath)
        assertFalse(streamFile.exists())
        assertEquals(thumbnailFile.absolutePath, db.androidSyncDao().asset("sample_old_thumbnail")?.localPath)
        assertTrue(thumbnailFile.isFile)
        assertEquals(subtitleFile.absolutePath, db.androidSyncDao().asset("sample_old_subtitle")?.localPath)
        assertTrue(subtitleFile.isFile)
    }

    @Test
    fun temporaryVideoTombstoneRemovesAllAndroidStateDespiteSavedState() = runBlocking {
        val videoId = "sample_temp_video"
        val streamFile = syncFile("sample_temp_stream.mp4", "stream")
        db.videoDao().upsert(video(videoId, publishedAt = nowMs, isTemp = true))
        db.androidSyncDao().upsertHead(
            AndroidSyncHeadEntity("video", videoId, "youtube", nowMs),
        )
        db.androidSyncDao().upsertAsset(
            youtubeAsset(
                "sample_temp_stream",
                videoId,
                "video_stream",
                localPath = streamFile.absolutePath,
            ),
        )
        db.offlineVideoDownloadDao().upsert(
            OfflineVideoDownloadEntity(videoId, state = "downloaded", updatedAtMs = nowMs),
        )
        db.bookmarkDao().upsert(BookmarkEntity(videoId = videoId, bookmarkedAt = nowMs))
        db.feedLikeDao().upsert(FeedLikeEntity(tweetId = videoId, likedAt = nowMs))
        db.feedSeenDao().upsert(FeedSeenEntity(tweetId = videoId, seenAt = nowMs))
        db.momentViewDao().upsert(MomentViewEntity(videoId = videoId, viewedAt = nowMs))
        db.watchHistoryDao().upsert(
            WatchHistoryEntity(
                videoId = videoId,
                playbackPosition = 15.0,
                duration = 60.0,
                updatedAtMs = nowMs,
            ),
        )
        db.momentsCursorDao().upsert(
            MomentsCursorEntity(
                scope = "all",
                videoId = videoId,
                positionMs = 0,
                sortAtMs = nowMs,
                updatedAtMs = nowMs,
            ),
        )
        db.feedRankDao().upsert(listOf(FeedRankEntity(tweetId = videoId, rankPosition = 0, snapshotAt = nowMs)))
        db.outboxDao().insert(
            OutboxEntity(
                kind = "progress",
                itemId = videoId,
                payloadJson = "{}",
                createdAtMs = nowMs,
            ),
        )
        db.androidSyncDao().upsertHead(
            AndroidSyncHeadEntity("bookmark", videoId, "", nowMs),
        )
        db.androidSyncDao().upsertSyncState(changesState("cursor-a"))
        val engine =
            MockEngine { request ->
                when (request.url.encodedPath) {
                    "/api/android/sync/changes" ->
                        respondJson(
                            AndroidSyncPageResponse(
                                changes = listOf(deleteChange("video", videoId)),
                                next_cursor = "cursor-b",
                                end_of_stream = true,
                            ),
                        )
                    "/api/android/sync/health" -> respond("{}", HttpStatusCode.OK, jsonHeaders())
                    else -> error("Unexpected request ${request.url}")
                }
            }

        buildMirror(engine).syncOnce()

        assertNull(db.videoDao().getById(videoId))
        assertNull(db.offlineVideoDownloadDao().get(videoId))
        assertNull(db.androidSyncDao().asset("sample_temp_stream"))
        assertFalse(streamFile.exists())
        assertNull(db.bookmarkDao().getById(videoId))
        assertNull(db.feedLikeDao().getById(videoId))
        assertNull(db.feedSeenDao().getById(videoId))
        assertNull(db.momentViewDao().getById(videoId))
        assertNull(db.watchHistoryDao().getById(videoId))
        assertNull(db.momentsCursorDao().get("all"))
        assertEquals(0, db.feedRankDao().count())
        assertTrue(db.outboxDao().pendingRows().isEmpty())
        assertFalse(db.androidSyncDao().headIds("bookmark").contains(videoId))
    }

    @Test
    fun deletingPrimaryStreamLeavesThumbnailAndSubtitleMetadataIntact() = runBlocking {
        val videoId = "sample_video"
        db.videoDao().upsert(video(videoId, publishedAt = nowMs))
        val streamFile = syncFile("sample_stream.mp4", "stream")
        val thumbnailFile = syncFile("sample_thumbnail.jpg", "image")
        val previewFile = syncFile("sample_preview.jpg", "image")
        val subtitleFile = syncFile("sample_subtitle.vtt", "WEBVTT")
        for (asset in listOf(
                youtubeAsset(
                    "sample_stream",
                    videoId,
                    "video_stream",
                    localPath = streamFile.absolutePath,
                ),
                youtubeAsset(
                    "sample_thumbnail",
                    videoId,
                    "thumbnail",
                    localPath = thumbnailFile.absolutePath,
                ),
                youtubeAsset(
                    "sample_preview",
                    videoId,
                    "post_media",
                    contentType = "image/jpeg",
                    localPath = previewFile.absolutePath,
                ),
                youtubeAsset(
                    "sample_subtitle",
                    videoId,
                    "subtitle",
                    localPath = subtitleFile.absolutePath,
                ),
            )) {
            db.androidSyncDao().upsertAsset(asset)
        }
        db.offlineVideoDownloadDao().upsert(
            OfflineVideoDownloadEntity(videoId, state = "downloaded", updatedAtMs = nowMs),
        )
        var syncTriggers = 0

        OfflineVideoDownloads(
                db = db,
                syncDao = db.androidSyncDao(),
                downloads = db.offlineVideoDownloadDao(),
                mediaRoot = temporaryFolder.root,
                nowMsProvider = { nowMs },
                syncTrigger = { syncTriggers++ },
            )
            .removeDownload(videoId)

        assertNull(db.androidSyncDao().asset("sample_stream")?.localPath)
        assertFalse(streamFile.exists())
        assertEquals(thumbnailFile.absolutePath, db.androidSyncDao().asset("sample_thumbnail")?.localPath)
        assertTrue(thumbnailFile.isFile)
        assertEquals(previewFile.absolutePath, db.androidSyncDao().asset("sample_preview")?.localPath)
        assertTrue(previewFile.isFile)
        assertEquals(subtitleFile.absolutePath, db.androidSyncDao().asset("sample_subtitle")?.localPath)
        assertTrue(subtitleFile.isFile)
        assertEquals("removed", db.offlineVideoDownloadDao().get(videoId)?.state)
        assertEquals(1, syncTriggers)
    }

    private fun video(
        id: String,
        publishedAt: Long,
        isTemp: Boolean = false,
    ) =
        VideoEntity(
            videoId = id,
            channelId = "youtube_UCsample_channel",
            ownerKind = "youtube_video",
            publishedAt = publishedAt,
            isTemp = isTemp,
        )

    private fun youtubeAsset(
        assetId: String,
        videoId: String,
        assetKind: String,
        sizeBytes: Long = 1,
        contentType: String? = null,
        localPath: String? = null,
        nextAttemptAtMs: Long = 0,
    ) =
        AndroidSyncAssetEntity(
            assetId = assetId,
            assetKind = assetKind,
            ownerId = videoId,
            ownerKind = "youtube_video",
            bucket = "youtube",
            contentType =
                contentType ?: when (assetKind) {
                    "video_stream", "post_media" -> "video/mp4"
                    "post_audio" -> "audio/mp4"
                    "subtitle" -> "text/vtt"
                    else -> "image/jpeg"
                },
            sizeBytes = sizeBytes,
            revision = 1,
            localPath = localPath,
            verifiedAtMs = localPath?.let { nowMs },
            nextAttemptAtMs = nextAttemptAtMs,
        )

    private fun syncFile(name: String, contents: String): File =
        File(File(temporaryFolder.root, "sync"), name).also { file ->
            requireNotNull(file.parentFile).mkdirs()
            file.writeText(contents)
        }

    private fun buildMirror(engine: MockEngine): AndroidSyncMirror {
        val client = syncClient(engine)
        val baseUrlProvider = ServerBaseUrlProvider { BASE_URL }
        return AndroidSyncMirror(
            db = db,
            dao = db.androidSyncDao(),
            api = AndroidSyncApi(client, baseUrlProvider::baseUrl),
            client = client,
            baseUrlProvider = baseUrlProvider,
            reachability = reachability(),
            foregroundPromoter = foregroundPromoter(),
            mediaRoot = temporaryFolder.root,
            logger = logger,
            retentionProvider = { RETENTION },
            serverNowMsProvider = { nowMs },
            metadataRetryDelaysMs = emptyList(),
        )
    }

    private fun buildAssetDrainer(engine: MockEngine): AndroidSyncAssetDrainer =
        AndroidSyncAssetDrainer(
            dao = db.androidSyncDao(),
            client = client(engine),
            baseUrlProvider = ServerBaseUrlProvider { BASE_URL },
            reachability = reachability(),
            foregroundPromoter = foregroundPromoter(),
            mediaRoot = temporaryFolder.root,
            logger = logger,
            nowMsProvider = { nowMs },
        )

    private fun reachability() =
        Reachability(
            scope = scope,
            probe = { true },
            foregroundFlow = MutableSharedFlow(extraBufferCapacity = 1),
        )

    private fun foregroundPromoter() =
        ForegroundPromoter(
            context = ApplicationProvider.getApplicationContext(),
            logger = logger,
            startForegroundService = {},
            stopForegroundService = {},
        )

    private fun client(engine: MockEngine) =
        HttpClient(engine) { expectSuccess = false }.also(clients::add)

    private fun syncClient(engine: MockEngine) =
        HttpClient(engine) {
            expectSuccess = false
            install(ContentNegotiation) { json(iglooJson) }
        }
            .also(clients::add)

    private fun changesState(cursor: String) =
        AndroidSyncStateEntity(
            mode = "changes",
            cursor = cursor,
            feedDays = RETENTION.feedDays,
            youtubeDays = RETENTION.youtubeDays,
            momentsDays = RETENTION.momentsDays,
            storyHours = RETENTION.storyHours,
        )

    private fun deleteChange(ownerKind: String, ownerId: String) =
        AndroidSyncChangeDto(ownerKind, ownerId, "delete", "", 0, null)

    private inline fun <reified T> MockRequestHandleScope.respondJson(body: T) =
        respond(iglooJson.encodeToString(body), HttpStatusCode.OK, jsonHeaders())

    private fun jsonHeaders() = headersOf("Content-Type", ContentType.Application.Json.toString())

    private companion object {
        const val BASE_URL = "http://example.local"
        const val DAY_MS = 86_400_000L
        val RETENTION = AndroidSyncRetentionRequest(7, 7, 7, 48)
    }
}
