package com.screwy.igloo.media

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.data.entity.MediaInventoryEntity
import com.screwy.igloo.log.InMemoryLogSink
import com.screwy.igloo.log.Logger
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
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
class CacheOpsTest {

    @get:Rule val tmpFolder = TemporaryFolder()

    private lateinit var db: IglooDatabase
    private lateinit var scope: CoroutineScope
    private lateinit var logger: Logger
    private lateinit var mediaRoot: java.io.File

    @Before fun setUp() {
        db = RoomTestSupport.freshDb()
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        val prefs = com.screwy.igloo.data.PreferencesRepo(
            db.preferenceDao(),
            scope,
            nowMsProvider = { 1_000_000L },
        )
        logger = Logger(prefs = prefs, sink = InMemoryLogSink(), scope = scope, nowMsProvider = { 1_000_000L })
        mediaRoot = tmpFolder.newFolder("media")
    }

    @After fun tearDown() {
        scope.cancel()
        db.close()
    }

    // ─── stats aggregates by bucket ───────────────────────────────────────────

    @Test fun stats_aggregatesByBucket() = runBlocking {
        // avatars bucket: 2 cached, 1 failed
        db.mediaInventoryDao().upsert(inventoryRow("av-1", "avatar", "ch-1", "avatars", "cached", fileSize = 1024))
        db.mediaInventoryDao().upsert(inventoryRow("av-2", "avatar", "ch-2", "avatars", "cached", fileSize = 2048))
        db.mediaInventoryDao().upsert(inventoryRow("av-3", "avatar", "ch-3", "avatars", "failed"))

        // twitter_media bucket: 1 pending
        db.mediaInventoryDao().upsert(inventoryRow("tw-1", "post_thumbnail", "tw-owner-1", "twitter_media", "pending"))

        val stats = buildCacheOps().stats()

        val avatarStats = stats.first { it.bucket == "avatars" }
        assertEquals(3, avatarStats.entries)
        assertEquals(2, avatarStats.cached)
        assertEquals(1024L + 2048L, avatarStats.bytes)
        assertEquals(1, avatarStats.failed)

        val twitterStats = stats.first { it.bucket == "twitter_media" }
        assertEquals(1, twitterStats.entries)
        assertEquals(0, twitterStats.cached)
        assertEquals(0L, twitterStats.bytes)
        assertEquals(0, twitterStats.failed)
    }

    @Test fun stats_countsInventoryAndSyncDiskBytesByBucket() = runBlocking {
        db.mediaInventoryDao().upsert(inventoryRow(
            "tw-legacy",
            "post_media",
            "tw-owner",
            "twitter_media",
            "cached",
            fileSize = 1,
        ))
        val legacyFile = java.io.File(mediaRoot, "twitter_media/legacy.bin").also {
            it.parentFile?.mkdirs()
            it.writeBytes(ByteArray(10))
        }
        val syncFile = java.io.File(mediaRoot, "sync/twitter_media/sync.bin").also {
            it.parentFile?.mkdirs()
            it.writeBytes(ByteArray(15))
        }
        val syncOnly = java.io.File(mediaRoot, "sync/avatars/avatar.bin").also {
            it.parentFile?.mkdirs()
            it.writeBytes(ByteArray(7))
        }

        val stats = buildCacheOps().stats()

        assertTrue(legacyFile.exists())
        assertTrue(syncFile.exists())
        assertTrue(syncOnly.exists())
        assertEquals(25L, stats.first { it.bucket == "twitter_media" }.bytes)
        assertEquals(7L, stats.first { it.bucket == "avatars" }.bytes)
    }

    // ─── clearCache(null) — deletes all rows + files, triggers Sync ─────────────

    @Test fun clearCache_all_deletesRowsAndFiles() = runBlocking {
        // Seed rows in two buckets
        db.mediaInventoryDao().upsert(inventoryRow("av-1", "avatar", "ch-1", "avatars", "cached"))
        db.mediaInventoryDao().upsert(inventoryRow("tw-1", "post_thumbnail", "tw-owner-1", "twitter_media", "cached"))
        val syncFile = java.io.File(mediaRoot, "sync/twitter_media/tw-sync.jpg").also {
            it.parentFile?.mkdirs()
            it.createNewFile()
        }
        insertVerifiedSyncAsset("generation-clear-all", "tw-sync", "post_thumbnail", "tw-owner-1", "twitter_media", syncFile)
        // Create matching files on disk
        val avatarsDir = java.io.File(mediaRoot, "avatars").also { it.mkdirs() }
        val twitterDir = java.io.File(mediaRoot, "twitter_media").also { it.mkdirs() }
        val avatarFile = java.io.File(avatarsDir, "av-1.jpg").also { it.createNewFile() }
        val twitterFile = java.io.File(twitterDir, "tw-1.jpg").also { it.createNewFile() }

        assertTrue("avatar file must exist before clear", avatarFile.exists())
        assertTrue("twitter file must exist before clear", twitterFile.exists())

        buildCacheOps().clearCache(null)

        // Rows gone
        assertEquals(0, db.mediaInventoryDao().statsByBucket().sumOf { it.entries })
        assertEquals(null, db.androidSyncDao().latestVerifiedLocalPath("tw-owner-1", "post_thumbnail"))

        // Files gone
        assertFalse("avatar file should be deleted", avatarFile.exists())
        assertFalse("twitter file should be deleted", twitterFile.exists())
        assertFalse("sync twitter file should be deleted", syncFile.exists())

        // mediaRoot itself survives
        assertTrue("mediaRoot must survive", mediaRoot.exists())

    }

    // ─── clearCache(bucket) — only touches that bucket ────────────────────────

    @Test fun clearCache_perBucket_onlyTouchesThatBucket() = runBlocking {
        db.mediaInventoryDao().upsert(inventoryRow("av-1", "avatar", "ch-1", "avatars", "cached"))
        db.mediaInventoryDao().upsert(inventoryRow("tw-1", "post_thumbnail", "tw-owner-1", "twitter_media", "cached"))
        val avatarsDir = java.io.File(mediaRoot, "avatars").also { it.mkdirs() }
        val twitterDir = java.io.File(mediaRoot, "twitter_media").also { it.mkdirs() }
        val avatarFile = java.io.File(avatarsDir, "av-1.jpg").also { it.createNewFile() }
        val twitterFile = java.io.File(twitterDir, "tw-1.jpg").also { it.createNewFile() }
        val syncAvatarFile = java.io.File(mediaRoot, "sync/avatars/sync-av.jpg").also {
            it.parentFile?.mkdirs()
            it.createNewFile()
        }
        val syncTwitterFile = java.io.File(mediaRoot, "sync/twitter_media/sync-tw.jpg").also {
            it.parentFile?.mkdirs()
            it.createNewFile()
        }
        insertVerifiedSyncAsset("generation-clear-bucket", "sync-av", "avatar", "ch-1", "avatars", syncAvatarFile)
        insertVerifiedSyncAsset(
            "generation-clear-bucket",
            "sync-tw",
            "post_thumbnail",
            "tw-owner-1",
            "twitter_media",
            syncTwitterFile,
        )

        buildCacheOps().clearCache("avatars")

        // avatars rows and files gone
        val allStats = db.mediaInventoryDao().statsByBucket()
        assertTrue("avatars bucket should have no rows", allStats.none { it.bucket == "avatars" })
        assertFalse("avatar file should be deleted", avatarFile.exists())

        // twitter_media untouched
        val twitterStats = allStats.firstOrNull { it.bucket == "twitter_media" }
        assertEquals("twitter_media entries should remain", 1, twitterStats?.entries)
        assertTrue("twitter file should survive", twitterFile.exists())
        assertFalse("sync avatar file should be deleted", syncAvatarFile.exists())
        assertEquals(null, db.androidSyncDao().latestVerifiedLocalPath("ch-1", "avatar"))
        assertTrue("sync twitter file should survive", syncTwitterFile.exists())
        assertEquals(syncTwitterFile.absolutePath, db.androidSyncDao().latestVerifiedLocalPath("tw-owner-1", "post_thumbnail"))

    }

    @Test fun clearCache_triggersSyncDrain() = runBlocking {
        var syncTriggers = 0

        buildCacheOps(syncTrigger = { syncTriggers++ }).clearCache(null)

        assertEquals(1, syncTriggers)
    }

    // ─── Wiring ────────────────────────────────────────────────────────────────

    private fun buildCacheOps(syncTrigger: () -> Unit = {}) = CacheOps(
        dao = db.mediaInventoryDao(),
        syncDao = db.androidSyncDao(),
        mediaRoot = mediaRoot,
        logger = logger,
        syncTrigger = syncTrigger,
        nowMsProvider = { 1_000_000L },
    )

    // ─── Entity factory ────────────────────────────────────────────────────────

    private fun inventoryRow(
        assetId: String,
        assetKind: String,
        ownerId: String,
        bucket: String,
        state: String,
        fileSize: Long? = null,
    ) = MediaInventoryEntity(
        assetId = assetId,
        assetKind = assetKind,
        scope = "subscriptions",
        ownerId = ownerId,
        bucket = bucket,
        serverUrl = "/api/assets/$assetId",
        state = state,
        fileSize = fileSize,
        addedAtMs = 1_000_000L,
    )

    private suspend fun insertVerifiedSyncAsset(
        generationId: String,
        assetId: String,
        assetKind: String,
        ownerId: String,
        bucket: String,
        file: java.io.File,
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
                    bucket = bucket,
                    serverUrl = "/api/android/sync/assets/$assetId",
                    contentType = "application/octet-stream",
                    sizeBytes = file.length(),
                    sha256 = "sha-$assetId",
                    serverState = "ready",
                    requiredReason = "retention",
                    effectiveRecencyMs = 1_000_000L,
                ),
            ),
            nowMs = 1_000_000L,
        )
        db.androidSyncDao().markVerified(
            generationId = generationId,
            assetId = assetId,
            assetKind = assetKind,
            localPath = file.absolutePath,
            fileSize = file.length(),
            nowMs = 1_000_001L,
        )
    }
}
