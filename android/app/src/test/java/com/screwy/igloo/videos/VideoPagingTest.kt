package com.screwy.igloo.videos

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.data.entity.OfflineVideoDownloadEntity
import com.screwy.igloo.data.entity.VideoEntity
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class VideoPagingTest {

    private lateinit var db: IglooDatabase
    private lateinit var scope: CoroutineScope

    @Before fun setUp() {
        db = RoomTestSupport.freshDb()
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    }

    @After fun tearDown() {
        scope.cancel()
        db.close()
    }

    @Test fun youtubePagingIsBoundedAndKeepsPublishedThenVideoIdOrder() = runBlocking {
        db.videoDao().upsert(
            listOf(
                video("video_new", publishedAt = 50L),
                video("video_same_a", publishedAt = 40L),
                video("video_same_z", publishedAt = 40L),
                video("video_old", publishedAt = 30L),
                video("video_oldest", publishedAt = 20L),
                video(
                    "tiktok_newer",
                    publishedAt = 100L,
                    channelId = "tiktok_channel",
                    ownerKind = "tiktok_video",
                ),
            ),
        )

        val loader = VideoPageLoader(
            scope = scope,
            pageFlow = db.videoReadDao()::youtubeVideosPageFlow,
            pageSize = 2,
        )

        val firstPage = awaitPage(loader) { !it.isInitialLoading }
        assertEquals(listOf("video_new", "video_same_z"), firstPage.ids())
        assertTrue(firstPage.canLoadMore)

        loader.loadMore()
        val secondPage = awaitPage(loader) { it.items.size == 4 && !it.isLoadingMore }
        assertEquals(
            listOf("video_new", "video_same_z", "video_same_a", "video_old"),
            secondPage.ids(),
        )
        assertTrue(secondPage.canLoadMore)

        loader.loadMore()
        val finalPage = awaitPage(loader) { it.items.size == 5 && !it.isLoadingMore }
        assertEquals(
            listOf("video_new", "video_same_z", "video_same_a", "video_old", "video_oldest"),
            finalPage.ids(),
        )
        assertFalse(finalPage.canLoadMore)
    }

    @Test fun downloadedPageIncludesTemporaryAndCompletedManualVideosOnly() = runBlocking {
        val temporary = video("temporary_video", publishedAt = 60L, isTemp = true)
        val completed = video("completed_video", publishedAt = 50L)
        val completedWithoutStream = video("incomplete_video", publishedAt = 40L)
        val imageOnly = video("image_only_video", publishedAt = 35L)
        val requested = video("requested_video", publishedAt = 30L)
        val ordinary = video("ordinary_video", publishedAt = 20L)
        db.videoDao().upsert(listOf(temporary, completed, completedWithoutStream, imageOnly, requested, ordinary))
        db.offlineVideoDownloadDao().upsert(
            OfflineVideoDownloadEntity("completed_video", state = "downloaded", updatedAtMs = 1L),
        )
        db.offlineVideoDownloadDao().upsert(
            OfflineVideoDownloadEntity("incomplete_video", state = "downloaded", updatedAtMs = 1L),
        )
        db.offlineVideoDownloadDao().upsert(
            OfflineVideoDownloadEntity("image_only_video", state = "downloaded", updatedAtMs = 1L),
        )
        db.offlineVideoDownloadDao().upsert(
            OfflineVideoDownloadEntity("requested_video", state = "requested", updatedAtMs = 1L),
        )
        db.androidSyncDao().upsertAsset(
            AndroidSyncAssetEntity(
                assetId = "completed_audio",
                assetKind = "post_audio",
                ownerId = "completed_video",
                ownerKind = "youtube_video",
                bucket = "youtube",
                contentType = "audio/mp4",
                revision = 1L,
                localPath = "/cache/completed.mp4",
            ),
        )
        db.androidSyncDao().upsertAsset(
            AndroidSyncAssetEntity(
                assetId = "image_only_preview",
                assetKind = "post_media",
                ownerId = "image_only_video",
                ownerKind = "youtube_video",
                bucket = "youtube",
                contentType = "image/jpeg",
                revision = 1L,
                localPath = "/cache/image.jpg",
            ),
        )

        val rows = db.videoReadDao().downloadedVideosPageFlow(limit = 10).first()

        assertEquals(listOf("temporary_video", "completed_video"), rows.map { it.video.videoId })
    }

    @Test fun playerNavigationUsesCanonicalYoutubeOwnerKind() = runBlocking {
        db.videoDao().upsert(
            listOf(
                video("older_video", publishedAt = 10L, channelId = "unprefixed_channel"),
                video("newer_video", publishedAt = 20L, channelId = "unprefixed_channel"),
            ),
        )

        assertEquals("older_video", db.videoDao().getNextVideoId("newer_video"))
        assertEquals("newer_video", db.videoDao().getPreviousVideoId("older_video"))
    }

    private suspend fun awaitPage(
        loader: VideoPageLoader,
        predicate: (VideoListPageState) -> Boolean,
    ): VideoListPageState =
        withTimeout(2_000L) { loader.state.first(predicate) }

    private fun VideoListPageState.ids(): List<String> = items.map { it.video.videoId }

    private fun video(
        videoId: String,
        publishedAt: Long,
        channelId: String = "youtube_channel",
        ownerKind: String = "youtube_video",
        isTemp: Boolean = false,
    ): VideoEntity =
        VideoEntity(
            videoId = videoId,
            channelId = channelId,
            ownerKind = ownerKind,
            publishedAt = publishedAt,
            isTemp = isTemp,
        )
}
