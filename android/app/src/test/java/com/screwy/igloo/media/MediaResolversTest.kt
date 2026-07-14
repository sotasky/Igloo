package com.screwy.igloo.media

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import java.io.File
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
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
    private lateinit var scope: CoroutineScope
    private var revision = 0L

    @Before
    fun setUp() = runBlocking {
        db = RoomTestSupport.freshDb()
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    }

    @After
    fun tearDown() {
        scope.cancel()
        db.close()
    }

    @Test
    fun ownerKindSelectsTheExactLocalAsset() = runBlocking {
        val tweet = tmpFolder.newFile("tweet.jpg")
        val video = tmpFolder.newFile("video.jpg")
        insert("tweet", "post_thumbnail", "tweet", "shared", localFile = tweet)
        insert("video", "post_thumbnail", "youtube_video", "shared", localFile = video)

        assertEquals(MediaUri.Local(tweet), resolvers().thumbnailForPost("shared", OwnerKind.Tweet))
        assertEquals(
            MediaUri.Local(video),
            resolvers().thumbnailForPost("shared", OwnerKind.YouTubeVideo),
        )
    }

    @Test
    fun readyAssetUsesRevisionEndpointOnlyWhileOnline() = runBlocking {
        insert("stream", "video_stream", "youtube_video", "video", contentType = "video/mp4")

        assertEquals(
            MediaUri.Remote("https://igloo.example/api/android/sync/assets/stream/file?revision=1"),
            resolvers().videoStream("video", OwnerKind.YouTubeVideo),
        )
        assertEquals(
            MediaUri.Missing,
            resolvers(allowRemote = false).videoStream("video", OwnerKind.YouTubeVideo),
        )
    }

    @Test
    fun missingInventoryAndImageOnlyVideoAreMissing() = runBlocking {
        insert("still", "post_media", "youtube_video", "video", contentType = "image/jpeg")

        assertEquals(MediaUri.Missing, resolvers().avatarForChannel("missing"))
        assertEquals(MediaUri.Missing, resolvers().videoStream("video", OwnerKind.YouTubeVideo))
    }

    @Test
    fun dearrowSelectionAppliesOnlyToYoutube() = runBlocking {
        val dearrow = tmpFolder.newFile("dearrow.jpg")
        val original = tmpFolder.newFile("original.jpg")
        insert("dearrow", "dearrow_thumbnail", "youtube_video", "video", localFile = dearrow)
        insert("original", "post_thumbnail", "tiktok_video", "video", localFile = original)
        val prefs = PreferencesRepo(db.preferenceDao(), scope)
        prefs.putString(PreferencesRepo.Keys.DEARROW_MODE, "default")

        assertEquals(
            MediaUri.Local(dearrow),
            resolvers(prefs).thumbnailForPost("video", OwnerKind.YouTubeVideo),
        )
        assertEquals(
            MediaUri.Local(original),
            resolvers(prefs).thumbnailForPost("video", OwnerKind.TikTokVideo),
        )
    }

    private fun resolvers(
        prefs: PreferencesRepo = PreferencesRepo(db.preferenceDao(), scope),
        allowRemote: Boolean = true,
    ) =
        MediaResolversImpl(
            syncDao = db.androidSyncDao(),
            baseUrlProvider = { "https://igloo.example" },
            prefs = prefs,
            remoteFallbackAllowed = flowOf(allowRemote),
        )

    private suspend fun insert(
        assetId: String,
        assetKind: String,
        ownerKind: String,
        ownerId: String,
        localFile: File? = null,
        contentType: String = "image/jpeg",
    ) {
        db.androidSyncDao()
            .upsertAsset(
                AndroidSyncAssetEntity(
                    assetId = assetId,
                    assetKind = assetKind,
                    ownerId = ownerId,
                    ownerKind = ownerKind,
                    bucket = "test",
                    contentType = contentType,
                    sizeBytes = localFile?.length() ?: 1,
                    revision = ++revision,
                    localPath = localFile?.absolutePath,
                    verifiedAtMs = localFile?.let { 1L },
                )
            )
    }
}
