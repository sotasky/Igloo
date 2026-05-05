package com.screwy.igloo.media

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.data.entity.ChannelProfileEntity
import com.screwy.igloo.data.entity.MediaInventoryEntity
import com.screwy.igloo.data.entity.VideoEntity
import java.io.File
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
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
class MediaResolversTest {

    @get:Rule val tmpFolder = TemporaryFolder()

    private lateinit var db: IglooDatabase
    private lateinit var prefsScope: CoroutineScope
    private val baseUrl = "https://igloo.example"

    @Before fun setUp() {
        db = RoomTestSupport.freshDb()
        prefsScope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    }

    @After fun tearDown() {
        prefsScope.cancel()
        db.close()
    }

    // ─── thumbnailForPost — cached row, file on disk ───────────────────────────

    @Test fun thumbnailForPost_cachedRow_returnsLocal() = runBlocking {
        val file = tmpFolder.newFile("asset-a.jpg")
        db.mediaInventoryDao().upsert(inventoryRow(
            assetId = "asset-a",
            assetKind = "post_thumbnail",
            ownerId = "owner-1",
            state = "cached",
            localPath = file.absolutePath,
        ))

        val result = buildResolvers().thumbnailForPost("owner-1", OwnerKind.Tweet)

        assertTrue("expected Local", result is MediaUri.Local)
        assertEquals(file, (result as MediaUri.Local).file)
    }

    // ─── thumbnailForPost — cached but file missing on disk → Remote ──────────

    @Test fun thumbnailForPost_cachedButFileMissing_fallsBackToRemote() = runBlocking {
        db.mediaInventoryDao().upsert(inventoryRow(
            assetId = "asset-b",
            assetKind = "post_thumbnail",
            ownerId = "owner-2",
            state = "cached",
            localPath = "/nonexistent/path/asset-b.jpg",
            serverUrl = "/api/thumb/asset-b",
        ))

        val result = buildResolvers().thumbnailForPost("owner-2", OwnerKind.Tweet)

        assertTrue("expected Remote", result is MediaUri.Remote)
        assertEquals("$baseUrl/api/thumb/asset-b", (result as MediaUri.Remote).url)
    }

    // ─── thumbnailForPost — pending row → Remote ──────────────────────────────

    @Test fun thumbnailForPost_pendingRow_returnsRemote() = runBlocking {
        db.mediaInventoryDao().upsert(inventoryRow(
            assetId = "asset-c",
            assetKind = "post_thumbnail",
            ownerId = "owner-3",
            state = "pending",
            serverUrl = "/api/thumb/asset-c",
        ))

        val result = buildResolvers().thumbnailForPost("owner-3", OwnerKind.Tweet)

        assertTrue("expected Remote", result is MediaUri.Remote)
        assertEquals("$baseUrl/api/thumb/asset-c", (result as MediaUri.Remote).url)
    }

    // ─── thumbnailForPost — tombstoned row → Remote ───────────────────────────

    @Test fun thumbnailForPost_tombstonedRow_returnsRemote() = runBlocking {
        // Tombstoned entries still exist in the table; spec says "entry exists, any state
        // other than Missing" → Remote. UI can still try to fetch from server directly.
        db.mediaInventoryDao().upsert(inventoryRow(
            assetId = "asset-d",
            assetKind = "post_thumbnail",
            ownerId = "owner-4",
            state = "tombstoned",
            serverUrl = "/api/thumb/asset-d",
        ))

        val result = buildResolvers().thumbnailForPost("owner-4", OwnerKind.Tweet)

        assertTrue("expected Remote for tombstoned", result is MediaUri.Remote)
    }

    // ─── thumbnailForPost — no row → Missing ──────────────────────────────────

    @Test fun thumbnailForPost_noRow_returnsMissing() = runBlocking {
        val result = buildResolvers().thumbnailForPost("owner-unknown", OwnerKind.Tweet)
        assertEquals(MediaUri.Missing, result)
    }

    @Test fun thumbnailForPost_videoRowWithoutInventory_returnsRemoteThumbnailFallback() = runBlocking {
        db.videoDao().upsert(
            VideoEntity(
                videoId = "video-fallback",
                channelId = "youtube_channel",
                thumbnailPath = "media/youtube/channel/video-fallback.webp",
                filePath = "media/youtube/channel/video-fallback.mp4",
                publishedAt = 1L,
            ),
        )

        val result = buildResolvers().thumbnailForPost("video-fallback", OwnerKind.YouTubeVideo)

        assertTrue(result is MediaUri.Remote)
        assertEquals("$baseUrl/api/media/thumbnail/video-fallback", (result as MediaUri.Remote).url)
    }

    @Test fun thumbnailForPost_slideshowRowWithoutInventory_returnsRemoteSlideFallback() = runBlocking {
        db.videoDao().upsert(
            VideoEntity(
                videoId = "short-fallback",
                channelId = "tiktok_channel",
                thumbnailPath = "media/tiktok/short-fallback.image",
                filePath = "media/tiktok/short-fallback.mp4",
                mediaKind = "slideshow",
                publishedAt = 1L,
            ),
        )

        val result = buildResolvers().thumbnailForPost("short-fallback", OwnerKind.TikTokVideo)

        assertTrue(result is MediaUri.Remote)
        assertEquals("$baseUrl/api/media/slide/short-fallback/0", (result as MediaUri.Remote).url)
    }

    // ─── thumbnailForPost — post_thumbnail missing, falls back to post_media ──

    @Test fun thumbnailForPost_postThumbnailMissing_fallsBackToPostMedia() = runBlocking {
        db.mediaInventoryDao().upsert(inventoryRow(
            assetId = "asset-e",
            assetKind = "post_media",
            ownerId = "owner-5",
            state = "pending",
            serverUrl = "/api/media/asset-e",
        ))

        val result = buildResolvers().thumbnailForPost("owner-5", OwnerKind.Tweet)

        assertTrue("expected Remote via post_media fallback", result is MediaUri.Remote)
        assertEquals("$baseUrl/api/media/asset-e", (result as MediaUri.Remote).url)
    }

    @Test fun thumbnailForPost_verifiedSyncThumbnail_returnsLocal() = runBlocking {
        val file = tmpFolder.newFile("sync-thumb.jpg")
        insertVerifiedSyncAsset(
            generationId = "android-sync-complete",
            assetId = "tweet-1-thumb",
            assetKind = "post_thumbnail",
            ownerId = "tweet-1",
            localPath = file.absolutePath,
        )

        val result = buildResolvers().thumbnailForPost("tweet-1", OwnerKind.Tweet)

        assertTrue("expected Local from Sync ledger", result is MediaUri.Local)
        assertEquals(file, (result as MediaUri.Local).file)
    }

    @Test fun thumbnailForPostFlow_verifiedSyncThumbnailWinsOverLegacyRemote() = runBlocking {
        db.mediaInventoryDao().upsert(inventoryRow(
            assetId = "legacy-thumb",
            assetKind = "post_thumbnail",
            ownerId = "tweet-flow",
            state = "pending",
            serverUrl = "/api/media/thumbnail/tweet-flow",
        ))
        val file = tmpFolder.newFile("sync-flow-thumb.jpg")
        insertVerifiedSyncAsset(
            generationId = "android-sync-older-complete",
            assetId = "tweet-flow-thumb",
            assetKind = "post_thumbnail",
            ownerId = "tweet-flow",
            localPath = file.absolutePath,
        )

        val result = buildResolvers().thumbnailForPostFlow("tweet-flow", OwnerKind.Tweet).first()

        assertTrue("expected Local from Sync ledger", result is MediaUri.Local)
        assertEquals(file, (result as MediaUri.Local).file)
    }

    // ─── avatarForChannel — cached → Local ────────────────────────────────────

    @Test fun avatarForChannel_cached_returnsLocal() = runBlocking {
        val file = tmpFolder.newFile("chan-1.jpg")
        db.mediaInventoryDao().upsert(inventoryRow(
            assetId = "avatar-1",
            assetKind = "avatar",
            ownerId = "channel-1",
            state = "cached",
            localPath = file.absolutePath,
        ))

        val result = buildResolvers().avatarForChannel("channel-1")

        assertTrue("expected Local", result is MediaUri.Local)
        assertEquals(file, (result as MediaUri.Local).file)
    }

    @Test fun avatarForChannel_verifiedSyncAvatar_returnsLocal() = runBlocking {
        val file = tmpFolder.newFile("sync-avatar.jpg")
        insertVerifiedSyncAsset(
            generationId = "android-sync-complete",
            assetId = "channel-sync-avatar",
            assetKind = "avatar",
            ownerId = "channel-sync",
            localPath = file.absolutePath,
        )

        val result = buildResolvers().avatarForChannel("channel-sync")

        assertTrue("expected Local from Sync ledger", result is MediaUri.Local)
        assertEquals(file, (result as MediaUri.Local).file)
    }

    // ─── avatarForChannel — no row → Remote fallback ──────────────────────────
    // Server always serves /api/media/avatar/{id} (cached bytes or proxy), so
    // when the inventory/manifest didn't emit an avatar row (e.g. an un-followed
    // TikTok channel that surfaced in the moments feed) the resolver falls
    // through to that endpoint. Prevents empty silhouettes.

    @Test fun avatarForChannel_noRow_returnsRemoteFallback() = runBlocking {
        val result = buildResolvers().avatarForChannel("channel-unknown")
        assertTrue("expected Remote fallback", result is MediaUri.Remote)
        assertEquals(
            "https://igloo.example/api/media/avatar/channel-unknown",
            (result as MediaUri.Remote).url,
        )
    }

    @Test fun avatarForChannel_noInventoryRow_prefersProfileAvatarUrl() = runBlocking {
        db.channelProfileDao().upsert(
            ChannelProfileEntity(
                channelId = "youtube_channel",
                platform = "youtube",
                displayName = "Example Channel",
                avatarUrl = "https://cdn.example/avatar.png",
                bannerUrl = "https://cdn.example/banner.png",
            ),
        )

        val result = buildResolvers().avatarForChannel("youtube_channel")

        assertTrue("expected profile avatar URL", result is MediaUri.Remote)
        assertEquals("https://cdn.example/avatar.png", (result as MediaUri.Remote).url)
    }

    @Test fun bannerForChannel_cached_returnsLocal() = runBlocking {
        val file = tmpFolder.newFile("channel-banner.jpg")
        db.mediaInventoryDao().upsert(inventoryRow(
            assetId = "banner-1",
            assetKind = "banner",
            ownerId = "channel-1",
            state = "cached",
            localPath = file.absolutePath,
            bucket = "banners",
        ))

        val result = buildResolvers().bannerForChannel("channel-1")

        assertTrue("expected Local", result is MediaUri.Local)
        assertEquals(file, (result as MediaUri.Local).file)
    }

    @Test fun bannerForChannel_verifiedSyncBanner_returnsLocal() = runBlocking {
        val file = tmpFolder.newFile("sync-banner.jpg")
        insertVerifiedSyncAsset(
            generationId = "android-sync-complete",
            assetId = "channel-sync-banner",
            assetKind = "banner",
            ownerId = "channel-sync",
            localPath = file.absolutePath,
        )

        val result = buildResolvers().bannerForChannel("channel-sync")

        assertTrue("expected Local from Sync ledger", result is MediaUri.Local)
        assertEquals(file, (result as MediaUri.Local).file)
    }

    @Test fun bannerForChannel_noInventoryRow_usesServerBannerFallbackForTikTokProfile() = runBlocking {
        db.channelProfileDao().upsert(
            ChannelProfileEntity(
                channelId = "tiktok_channel",
                platform = "tiktok",
                handle = "channel",
                bannerUrl = "igloo:synth-banner:clip_1",
            ),
        )

        val result = buildResolvers().bannerForChannel("tiktok_channel")

        assertTrue("expected Remote fallback", result is MediaUri.Remote)
        assertEquals("$baseUrl/api/media/banner/tiktok_channel", (result as MediaUri.Remote).url)
    }

    // ─── videoStream — cached → Local ─────────────────────────────────────────

    @Test fun videoStream_cached_returnsLocal() = runBlocking {
        val file = tmpFolder.newFile("video-1.mp4")
        db.mediaInventoryDao().upsert(inventoryRow(
            assetId = "vid-1",
            assetKind = "video_stream",
            ownerId = "video-id-1",
            state = "cached",
            localPath = file.absolutePath,
        ))

        val result = buildResolvers().videoStream("video-id-1")

        assertTrue("expected Local", result is MediaUri.Local)
        assertEquals(file, (result as MediaUri.Local).file)
    }

    // ─── videoStream — no video_stream but has post_media → Remote ────────────

    @Test fun videoStream_noStreamButHasPostMedia_fallsBack() = runBlocking {
        db.mediaInventoryDao().upsert(inventoryRow(
            assetId = "vid-2",
            assetKind = "post_media",
            ownerId = "video-id-2",
            state = "pending",
            serverUrl = "/api/media/vid-2",
        ))

        val result = buildResolvers().videoStream("video-id-2")

        assertTrue("expected Remote via post_media fallback", result is MediaUri.Remote)
        assertEquals("$baseUrl/api/media/vid-2", (result as MediaUri.Remote).url)
    }

    @Test fun videoStream_verifiedSyncPostMedia_returnsLocal() = runBlocking {
        val file = tmpFolder.newFile("sync-post-media.mp4")
        insertVerifiedSyncAsset(
            generationId = "android-sync-complete",
            assetId = "tweet-video-media",
            assetKind = "post_media",
            ownerId = "tweet-video",
            localPath = file.absolutePath,
            contentType = "video/mp4",
        )

        val result = buildResolvers().videoStream("tweet-video")

        assertTrue("expected Local from Sync ledger", result is MediaUri.Local)
        assertEquals(file, (result as MediaUri.Local).file)
    }

    @Test fun videoStream_verifiedSyncImagePostMedia_isNotTreatedAsPlayableVideo() = runBlocking {
        val file = tmpFolder.newFile("sync-still.jpg")
        insertVerifiedSyncAsset(
            generationId = "android-sync-complete",
            assetId = "still-media",
            assetKind = "post_media",
            ownerId = "still-video",
            localPath = file.absolutePath,
            contentType = "image/jpeg",
        )

        val result = buildResolvers().videoStream("still-video")

        assertEquals(MediaUri.Missing, result)
    }

    // ─── videoStream — no inventory row → Missing ─────────────────────────────

    @Test fun videoStream_noInventoryRow_returnsMissing() = runBlocking {
        val result = buildResolvers().videoStream("video-id-3")
        assertEquals(MediaUri.Missing, result)
    }

    @Test fun videoStream_videoRowWithoutInventory_returnsRemoteStreamFallback() = runBlocking {
        db.videoDao().upsert(
            VideoEntity(
                videoId = "video-fallback-stream",
                channelId = "youtube_channel",
                filePath = "media/youtube/channel/video-fallback-stream.mp4",
                publishedAt = 1L,
            ),
        )

        val result = buildResolvers().videoStream("video-fallback-stream")

        assertTrue(result is MediaUri.Remote)
        assertEquals("$baseUrl/api/media/stream/video-fallback-stream", (result as MediaUri.Remote).url)
    }

    @Test fun thumbnailForPost_videoRowWithoutPaths_returnsRemoteThumbnailFallback() = runBlocking {
        db.videoDao().upsert(
            VideoEntity(
                videoId = "video-fallback-no-paths",
                channelId = "youtube_channel",
                publishedAt = 1L,
            ),
        )

        val result = buildResolvers().thumbnailForPost("video-fallback-no-paths", OwnerKind.YouTubeVideo)

        assertTrue(result is MediaUri.Remote)
        assertEquals("$baseUrl/api/media/thumbnail/video-fallback-no-paths", (result as MediaUri.Remote).url)
    }

    @Test fun videoStream_videoRowWithoutPaths_returnsRemoteStreamFallback() = runBlocking {
        db.videoDao().upsert(
            VideoEntity(
                videoId = "video-fallback-no-paths-stream",
                channelId = "youtube_channel",
                publishedAt = 1L,
            ),
        )

        val result = buildResolvers().videoStream("video-fallback-no-paths-stream")

        assertTrue(result is MediaUri.Remote)
        assertEquals("$baseUrl/api/media/stream/video-fallback-no-paths-stream", (result as MediaUri.Remote).url)
    }

    // ─── server_url is path — baseUrl is prepended ────────────────────────────

    @Test fun serverUrl_prefixedWithBaseUrl() = runBlocking {
        db.mediaInventoryDao().upsert(inventoryRow(
            assetId = "asset-path",
            assetKind = "video_stream",
            ownerId = "video-path-owner",
            state = "pending",
            serverUrl = "/api/media/stream/v1",
        ))

        val result = buildResolvers().videoStream("video-path-owner")

        assertTrue(result is MediaUri.Remote)
        assertEquals("https://igloo.example/api/media/stream/v1", (result as MediaUri.Remote).url)
    }

    // ─── DeArrow thumbnail mode ────────────────────────────────────────────────

    @Test fun dearrowMode_off_returnsOriginalThumbnail() = runBlocking {
        // Video has a dearrow_thumb_path but mode is off — resolver must NOT emit ?da=1.
        db.videoDao().upsert(
            VideoEntity(
                videoId = "da-off-video",
                channelId = "youtube_channel",
                dearrowThumbPath = "dearrow/da-off-video.jpg",
                publishedAt = 1L,
            ),
        )

        val prefs = PreferencesRepo(db.preferenceDao(), prefsScope)
        prefs.putString(PreferencesRepo.Keys.DEARROW_MODE, "off")

        val result = buildResolvers(prefs).thumbnailForPost("da-off-video", OwnerKind.YouTubeVideo)

        assertTrue("expected Remote", result is MediaUri.Remote)
        val url = (result as MediaUri.Remote).url
        assertTrue("URL must not contain ?da=1, got: $url", !url.contains("?da=1"))
    }

    @Test fun dearrowMode_default_andVideoHasDearrowThumb_emitsDaQuery() = runBlocking {
        db.videoDao().upsert(
            VideoEntity(
                videoId = "da-default-video",
                channelId = "youtube_channel",
                dearrowThumbPath = "dearrow/da-default-video.jpg",
                publishedAt = 1L,
            ),
        )

        val prefs = PreferencesRepo(db.preferenceDao(), prefsScope)
        prefs.putString(PreferencesRepo.Keys.DEARROW_MODE, "default")

        val result = buildResolvers(prefs).thumbnailForPost("da-default-video", OwnerKind.YouTubeVideo)

        assertTrue("expected Remote", result is MediaUri.Remote)
        assertEquals(
            "$baseUrl/api/media/thumbnail/da-default-video?da=1",
            (result as MediaUri.Remote).url,
        )
    }

    @Test fun dearrowMode_default_prefersVerifiedSyncDearrowThumbnail() = runBlocking {
        val file = tmpFolder.newFile("dearrow-local.jpg")
        db.videoDao().upsert(
            VideoEntity(
                videoId = "da-sync-video",
                channelId = "youtube_channel",
                dearrowThumbPath = "dearrow/da-sync-video.jpg",
                publishedAt = 1L,
            ),
        )
        insertVerifiedSyncAsset(
            generationId = "gen-da",
            assetId = "youtube_video_da-sync-video_dearrow_thumbnail",
            assetKind = "dearrow_thumbnail",
            ownerId = "da-sync-video",
            localPath = file.absolutePath,
        )

        val prefs = PreferencesRepo(db.preferenceDao(), prefsScope)
        prefs.putString(PreferencesRepo.Keys.DEARROW_MODE, "default")

        val result = buildResolvers(prefs).thumbnailForPost("da-sync-video", OwnerKind.YouTubeVideo)

        assertTrue("expected Local", result is MediaUri.Local)
        assertEquals(file, (result as MediaUri.Local).file)
    }

    @Test fun dearrowMode_casual_andVideoHasDearrowThumb_emitsDaQuery() = runBlocking {
        db.videoDao().upsert(
            VideoEntity(
                videoId = "da-casual-video",
                channelId = "youtube_channel",
                dearrowThumbPath = "dearrow/da-casual-video.jpg",
                publishedAt = 1L,
            ),
        )

        val prefs = PreferencesRepo(db.preferenceDao(), prefsScope)
        prefs.putString(PreferencesRepo.Keys.DEARROW_MODE, "casual")

        val result = buildResolvers(prefs).thumbnailForPost("da-casual-video", OwnerKind.YouTubeVideo)

        assertTrue("expected Remote", result is MediaUri.Remote)
        assertEquals(
            "$baseUrl/api/media/thumbnail/da-casual-video?da=1",
            (result as MediaUri.Remote).url,
        )
    }

    @Test fun dearrowMode_default_andVideoHasNoDearrowThumb_usesFallback() = runBlocking {
        // Mode is on but video has no dearrow_thumb_path — should use normal fallback URL.
        db.videoDao().upsert(
            VideoEntity(
                videoId = "da-no-thumb-video",
                channelId = "youtube_channel",
                dearrowThumbPath = null,
                publishedAt = 1L,
            ),
        )

        val prefs = PreferencesRepo(db.preferenceDao(), prefsScope)
        prefs.putString(PreferencesRepo.Keys.DEARROW_MODE, "default")

        val result = buildResolvers(prefs).thumbnailForPost("da-no-thumb-video", OwnerKind.YouTubeVideo)

        assertTrue("expected Remote", result is MediaUri.Remote)
        val url = (result as MediaUri.Remote).url
        assertTrue("URL must not contain ?da=1, got: $url", !url.contains("?da=1"))
        assertEquals("$baseUrl/api/media/thumbnail/da-no-thumb-video", url)
    }

    // ─── Wiring ────────────────────────────────────────────────────────────────

    private fun buildResolvers(prefs: PreferencesRepo = defaultPrefs()): MediaResolversImpl =
        MediaResolversImpl(
            dao = db.mediaInventoryDao(),
            syncDao = db.androidSyncDao(),
            channelProfileDao = db.channelProfileDao(),
            videoDao = db.videoDao(),
            baseUrlProvider = { baseUrl },
            prefs = prefs,
        )

    /** Default prefs with dearrow off (schema default) — preserves existing test behavior. */
    private fun defaultPrefs(): PreferencesRepo =
        PreferencesRepo(db.preferenceDao(), prefsScope)

    // ─── Entity factory ────────────────────────────────────────────────────────

    private fun inventoryRow(
        assetId: String,
        assetKind: String,
        ownerId: String,
        state: String,
        localPath: String? = null,
        serverUrl: String = "/api/assets/$assetId",
        bucket: String = "twitter_media",
    ) = MediaInventoryEntity(
        assetId = assetId,
        assetKind = assetKind,
        scope = "subscriptions",
        ownerId = ownerId,
        bucket = bucket,
        serverUrl = serverUrl,
        localPath = localPath,
        state = state,
        addedAtMs = 1_000_000L,
    )

    private suspend fun insertVerifiedSyncAsset(
        generationId: String,
        assetId: String,
        assetKind: String,
        ownerId: String,
        localPath: String,
        contentType: String = "image/jpeg",
    ) {
        db.androidSyncDao().importAssets(
            listOf(
                AndroidSyncAssetEntity(
                    generationId = generationId,
                    seq = 1L,
                    assetId = assetId,
                    assetKind = assetKind,
                    ownerId = ownerId,
                    ownerKind = "tweet",
                    bucket = "twitter_media",
                    serverUrl = "/api/android/sync/assets/$assetId",
                    contentType = contentType,
                    sizeBytes = File(localPath).length(),
                    sha256 = "sha-$assetId",
                    serverState = "ready",
                    requiredReason = "retention",
                    effectiveRecencyMs = 2_000_000L,
                ),
            ),
            nowMs = 2_000_000L,
        )
        db.androidSyncDao().markVerified(
            generationId = generationId,
            assetId = assetId,
            assetKind = assetKind,
            localPath = localPath,
            fileSize = File(localPath).length(),
            nowMs = 2_000_001L,
        )
    }
}
