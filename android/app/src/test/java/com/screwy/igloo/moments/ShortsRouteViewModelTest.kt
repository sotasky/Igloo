package com.screwy.igloo.moments

import com.screwy.igloo.bookmarks.BookmarkFilter
import com.screwy.igloo.bookmarks.bookmarkPlaylistId
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.data.entity.BookmarkEntity
import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.ChannelFollowEntity
import com.screwy.igloo.data.entity.MomentViewEntity
import com.screwy.igloo.data.entity.VideoEntity
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.outbox.OutboxWriter
import com.screwy.igloo.testutil.ViewModelTestTracker
import com.screwy.igloo.ui.UiEffects
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import kotlinx.coroutines.withTimeoutOrNull
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class ShortsRouteViewModelTest {

    private lateinit var db: IglooDatabase
    private lateinit var scope: CoroutineScope
    private lateinit var prefs: PreferencesRepo
    private lateinit var writer: OutboxWriter
    private lateinit var uiEffects: UiEffects
    private val viewModels = ViewModelTestTracker()

    @Before fun setUp() {
        Dispatchers.setMain(UnconfinedTestDispatcher())
        db = RoomTestSupport.freshDb()
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        prefs = PreferencesRepo(db.preferenceDao(), scope, nowMsProvider = { 0L })
        writer = OutboxWriter(
            db = db,
            prefs = prefs,
            scope = scope,
            nowMsProvider = { 0L },
            writeDebounceMs = 50L,
        )
        uiEffects = UiEffects()
    }

    @After fun tearDown() {
        viewModels.clearAll()
        scope.cancel()
        db.close()
        Dispatchers.resetMain()
    }

    private fun subscribe(vm: ShortsRouteViewModel): Job = scope.launch {
        launch { vm.items.collect {} }
        launch { vm.startIndex.collect {} }
        launch { vm.uiState.collect {} }
    }

    @Test fun allMomentsItemsExposeSyncedCanonicalUrlWithoutSynthesis() = runBlocking {
        db.channelDao().upsert(ChannelEntity(
            channelId = "tiktok_alice", name = "Alice", platform = "tiktok",
            sourceId = "alice",
        ))
        db.channelFollowDao().upsert(ChannelFollowEntity(channelId = "tiktok_alice"))
        db.videoDao().upsert(VideoEntity(
			videoId = "tiktok_clip_1",
			channelId = "tiktok_alice",
			ownerKind = "tiktok_video",
            title = "Short",
            canonicalUrl = "https://www.tiktok.com/@canonical/video/clip_1",
            publishedAt = 1L,
        ))

        val vm = viewModels.track(ShortsRouteViewModel(
            playlistSpec = ShortsPlaylistSpec.allMoments(),
            startVideoId = "tiktok_clip_1",
            db = db,
            outboxWriter = writer,
            prefs = prefs,
            uiEffects = uiEffects,
            baseUrlProvider = ServerBaseUrlProvider { "https://example.test" },
        ))
        val sub = subscribe(vm)
        val ok = withTimeoutOrNull(2_000L) {
            while (vm.items.value.isEmpty()) delay(10)
            true
        }
        sub.cancel()

        assertEquals(true, ok)
        assertEquals("https://www.tiktok.com/@canonical/video/clip_1", vm.items.value.single().canonicalUrl)
    }

    @Test fun storyTrayPlaylistWrapsFromSelectedChannel() = runBlocking {
        db.channelDao().upsert(listOf(
            ChannelEntity("tiktok_newer", name = "Newer", platform = "tiktok", sourceId = "newer"),
            ChannelEntity("tiktok_older", name = "Older", platform = "tiktok", sourceId = "older"),
        ))
        db.channelFollowDao().upsert(ChannelFollowEntity(channelId = "tiktok_newer"))
        db.channelFollowDao().upsert(ChannelFollowEntity(channelId = "tiktok_older"))
        val now = System.currentTimeMillis()
        db.videoDao().upsert(listOf(
            VideoEntity("v_newer_first", "tiktok_newer", "tiktok_video", title = "Newer first", publishedAt = now - 2_000L, sourceKind = "story"),
            VideoEntity("v_newer_last", "tiktok_newer", "tiktok_video", title = "Newer last", publishedAt = now - 1_000L, sourceKind = "story"),
            VideoEntity("v_older", "tiktok_older", "tiktok_video", title = "Older story", publishedAt = now - 3_000L, sourceKind = "story"),
        ))

        val vm = viewModels.track(ShortsRouteViewModel(
            playlistSpec = ShortsPlaylistSpec.storyTray(),
            startVideoId = "v_older",
            db = db,
            outboxWriter = writer,
            prefs = prefs,
            uiEffects = uiEffects,
            baseUrlProvider = ServerBaseUrlProvider { "https://example.test" },
        ))
        val sub = subscribe(vm)
        val ok = withTimeoutOrNull(2_000L) {
            while (vm.items.value.size < 3) delay(10)
            true
        }
        sub.cancel()

        assertEquals(true, ok)
        assertEquals(listOf("v_older", "v_newer_first", "v_newer_last"), vm.items.value.map { it.videoId })
        assertEquals(0, vm.startIndex.value)
    }

    @Test fun storyTrayPlaylistOrderDoesNotChangeAfterViewingStories() = runBlocking {
        db.channelDao().upsert(listOf(
            ChannelEntity("tiktok_sample_one", name = "Sample One", platform = "tiktok", sourceId = "sample_one"),
            ChannelEntity("tiktok_sample_two", name = "Sample Two", platform = "tiktok", sourceId = "sample_two"),
            ChannelEntity("tiktok_sample_old", name = "Sample Old", platform = "tiktok", sourceId = "sample_old"),
        ))
        db.channelFollowDao().upsert(ChannelFollowEntity(channelId = "tiktok_sample_one"))
        db.channelFollowDao().upsert(ChannelFollowEntity(channelId = "tiktok_sample_two"))
        db.channelFollowDao().upsert(ChannelFollowEntity(channelId = "tiktok_sample_old"))
        val now = System.currentTimeMillis()
        db.videoDao().upsert(listOf(
            VideoEntity("v_sample_one", "tiktok_sample_one", "tiktok_video", title = "Sample One", publishedAt = now - 1_000L, sourceKind = "story"),
            VideoEntity("v_sample_two", "tiktok_sample_two", "tiktok_video", title = "Sample Two", publishedAt = now - 2_000L, sourceKind = "story"),
            VideoEntity("v_sample_old", "tiktok_sample_old", "tiktok_video", title = "Sample Old", publishedAt = now - 3_000L, sourceKind = "story"),
        ))

        val vm = viewModels.track(ShortsRouteViewModel(
            playlistSpec = ShortsPlaylistSpec.storyTray(),
            startVideoId = "v_sample_two",
            db = db,
            outboxWriter = writer,
            prefs = prefs,
            uiEffects = uiEffects,
            baseUrlProvider = ServerBaseUrlProvider { "https://example.test" },
        ))
        val sub = subscribe(vm)
        val loaded = withTimeoutOrNull(2_000L) {
            while (vm.items.value.size < 3) delay(10)
            true
        }
        assertEquals(true, loaded)
        assertEquals(listOf("v_sample_two", "v_sample_old", "v_sample_one"), vm.items.value.map { it.videoId })

        db.momentViewDao().upsert(MomentViewEntity("v_sample_two", viewedAt = now))
        delay(250L)
        sub.cancel()

        assertEquals(listOf("v_sample_two", "v_sample_old", "v_sample_one"), vm.items.value.map { it.videoId })
    }

    @Test fun bookmarksPlaylistUsesRouteFilter() = runBlocking {
        db.videoDao().upsert(listOf(
            VideoEntity("art_new", "tiktok_artist", "tiktok_video", title = "Art new", mediaKind = "video", publishedAt = 30L),
            VideoEntity("music_new", "tiktok_artist", "tiktok_video", title = "Music new", mediaKind = "video", publishedAt = 20L),
            VideoEntity("art_old", "tiktok_artist", "tiktok_video", title = "Art old", mediaKind = "video", publishedAt = 10L),
        ))
        db.bookmarkDao().upsert(BookmarkEntity("art_new", categoryId = 34L, customTitle = "art", bookmarkedAt = 300L))
        db.bookmarkDao().upsert(BookmarkEntity("music_new", categoryId = 5L, customTitle = "music", bookmarkedAt = 200L))
        db.bookmarkDao().upsert(BookmarkEntity("art_old", categoryId = 34L, customTitle = "art", bookmarkedAt = 100L))

        val vm = viewModels.track(ShortsRouteViewModel(
            playlistSpec = ShortsPlaylistSpec.bookmarks(bookmarkPlaylistId(BookmarkFilter.Category(34L))),
            startVideoId = "art_new",
            db = db,
            outboxWriter = writer,
            prefs = prefs,
            uiEffects = uiEffects,
            baseUrlProvider = ServerBaseUrlProvider { "https://example.test" },
        ))
        val sub = subscribe(vm)
        val loaded = withTimeoutOrNull(2_000L) {
            while (vm.items.value.size < 2) delay(10)
            true
        }
        sub.cancel()

        assertEquals(true, loaded)
        assertEquals(listOf("art_new", "art_old"), vm.items.value.map { it.videoId })
        assertEquals(0, vm.startIndex.value)
    }

}
