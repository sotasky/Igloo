package com.screwy.igloo.media

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.log.InMemoryLogSink
import com.screwy.igloo.log.Logger
import java.io.File
import java.io.IOException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
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
class CacheOpsTest {
    @get:Rule val tmpFolder = TemporaryFolder()

    private lateinit var db: IglooDatabase
    private lateinit var scope: CoroutineScope
    private lateinit var logger: Logger
    private lateinit var mediaRoot: File
    private var revision = 0L

    @Before
    fun setUp() = runBlocking {
        db = RoomTestSupport.freshDb()
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        logger =
            Logger(
                prefs = PreferencesRepo(db.preferenceDao(), scope),
                sink = InMemoryLogSink(),
                scope = scope,
            )
        mediaRoot = tmpFolder.newFolder("media")
    }

    @After
    fun tearDown() {
        scope.cancel()
        db.close()
    }

    @Test
    fun statsUseActiveInventoryInsteadOfWalkingDisk() = runBlocking {
        val avatar = assetFile("avatars/avatar.jpg", 15)
        insertAsset("avatar", "channel", "channel-1", "avatars", avatar)
        insertAsset("post", "tweet", "tweet-1", "twitter_media")
        assetFile("avatars/untracked.jpg", 70)

        assertEquals(
            listOf(
                CacheStats("avatars", entries = 1, cached = 1, bytes = 15),
                CacheStats("twitter_media", entries = 1, cached = 0, bytes = 0),
            ),
            buildCacheOps().stats(),
        )
    }

    @Test
    fun clearBucketDeletesOnlyRecordedFilesAndDemotesItsRows() = runBlocking {
        val avatar = assetFile("avatars/avatar.jpg", 5)
        val post = assetFile("twitter_media/post.jpg", 7)
        val untracked = assetFile("avatars/untracked.jpg", 9)
        insertAsset("avatar", "channel", "channel-1", "avatars", avatar)
        insertAsset("post", "tweet", "tweet-1", "twitter_media", post)

        buildCacheOps().clearCache("avatars")

        assertFalse(avatar.exists())
        assertTrue(untracked.exists())
        assertTrue(post.exists())
        assertNull(localPath("channel", "channel-1"))
        assertEquals(post.absolutePath, localPath("tweet", "tweet-1"))
    }

    @Test
    fun clearOwnerDoesNotTouchAnotherOwnerAndTriggersOnce() = runBlocking {
        val selected = assetFile("youtube_videos/selected.jpg", 5)
        val retained = assetFile("youtube_videos/retained.jpg", 7)
        insertAsset("selected", "youtube_video", "selected", "youtube_videos", selected)
        insertAsset("retained", "youtube_video", "retained", "youtube_videos", retained)
        var triggers = 0

        buildCacheOps { triggers++ }.clearOwner("youtube_video", "selected")

        assertFalse(selected.exists())
        assertTrue(retained.exists())
        assertNull(localPath("youtube_video", "selected"))
        assertEquals(retained.absolutePath, localPath("youtube_video", "retained"))
        assertEquals(1, triggers)
    }

    @Test
    fun outsideRootPathIsDemotedButNeverDeleted() = runBlocking {
        val outside = tmpFolder.newFile("outside.jpg")
        insertAsset("outside", "channel", "channel-1", "avatars", outside)
        var triggers = 0

        val failure =
            runCatching { buildCacheOps { triggers++ }.clearCache("avatars") }.exceptionOrNull()

        assertTrue(failure is IOException)
        assertTrue(outside.exists())
        assertNull(localPath("channel", "channel-1"))
        assertEquals(1, triggers)
    }

    private fun buildCacheOps(syncTrigger: () -> Unit = {}) =
        CacheOps(db.androidSyncDao(), mediaRoot, logger, syncTrigger)

    private suspend fun localPath(ownerKind: String, ownerId: String): String? =
        db.androidSyncDao().assetsForOwnerFlow(ownerKind, ownerId).first().single().localPath

    private suspend fun insertAsset(
        assetId: String,
        ownerKind: String,
        ownerId: String,
        bucket: String,
        file: File? = null,
    ) {
        db.androidSyncDao()
            .upsertAsset(
                AndroidSyncAssetEntity(
                    assetId = assetId,
                    assetKind = if (assetId == "avatar") "avatar" else "post_thumbnail",
                    ownerId = ownerId,
                    ownerKind = ownerKind,
                    bucket = bucket,
                    sizeBytes = file?.length() ?: 1,
                    revision = ++revision,
                    localPath = file?.absolutePath,
                    verifiedAtMs = file?.let { 1L },
                )
            )
    }

    private fun assetFile(relativePath: String, bytes: Int): File =
        File(mediaRoot, relativePath).also {
            it.parentFile?.mkdirs()
            it.writeBytes(ByteArray(bytes))
        }
}
