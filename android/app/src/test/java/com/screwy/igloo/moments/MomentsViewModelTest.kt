package com.screwy.igloo.moments

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.ChannelFollowEntity
import com.screwy.igloo.data.entity.VideoEntity
import com.screwy.igloo.data.entity.VideoRepostSourceEntity
import com.screwy.igloo.media.MediaResolvers
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.media.OwnerKind
import com.screwy.igloo.outbox.OutboxKind
import com.screwy.igloo.outbox.OutboxWriter
import com.screwy.igloo.sync.Scheduler
import com.screwy.igloo.testutil.ViewModelTestTracker
import com.screwy.igloo.ui.UiEffects
import com.screwy.igloo.ui.nav.FullscreenMediaTransition
import io.mockk.mockk
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
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.flowOf
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * `MomentsViewModel` — items flow through with resolver-produced thumbnails; view-events
 * enqueue MomentView outbox rows.
 * `moments`.
 */
@OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class MomentsViewModelTest {

    private lateinit var db: IglooDatabase
    private lateinit var scope: CoroutineScope
    private lateinit var prefs: PreferencesRepo
    private lateinit var writer: OutboxWriter
    private lateinit var scheduler: Scheduler
    private lateinit var uiEffects: UiEffects
    private lateinit var resolvers: MediaResolvers
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
        scheduler = mockk(relaxed = true)
        uiEffects = UiEffects()
        // Canned resolver: fixed Remote URI per videoId for deterministic assertions.
        resolvers = object : MediaResolvers {
            override suspend fun thumbnailForPost(ownerId: String, ownerKind: OwnerKind): MediaUri =
                MediaUri.Remote("https://example.test/thumb/$ownerId.jpg")
            override fun thumbnailForPostFlow(ownerId: String, ownerKind: OwnerKind) =
                flowOf(MediaUri.Remote("https://example.test/thumb/$ownerId.jpg"))
            override suspend fun avatarForChannel(channelId: String): MediaUri =
                MediaUri.Missing
            override fun avatarForChannelFlow(channelId: String) = flowOf(MediaUri.Missing)
            override suspend fun videoStream(videoId: String): MediaUri =
                MediaUri.Remote("https://example.test/stream/$videoId.mp4")
            override fun videoStreamFlow(videoId: String) =
                flowOf(MediaUri.Remote("https://example.test/stream/$videoId.mp4"))
        }
    }

    @After fun tearDown() {
        viewModels.clearAll()
        scope.cancel()
        db.close()
        Dispatchers.resetMain()
    }

    private fun newViewModel(): MomentsViewModel =
        viewModels.track(MomentsViewModel(db, writer, prefs, scheduler, uiEffects, resolvers))

    private fun subscribe(vm: MomentsViewModel): Job = scope.launch {
        launch { vm.uiState.collect {} }
        launch { vm.items.collect {} }
        launch { vm.playerItems.collect {} }
        launch { vm.startIndex.collect {} }
        launch { vm.autoplayEnabled.collect {} }
        launch { vm.muted.collect {} }
    }

    @Test fun items_carryResolverThumbnails_forTikTokRows() = runBlocking {
        db.channelDao().upsert(ChannelEntity(
            channelId = "tiktok_alice", name = "Alice", platform = "tiktok",
            sourceId = "alice",
        ))
        db.channelFollowDao().upsert(ChannelFollowEntity(channelId = "tiktok_alice"))
        db.videoDao().upsert(VideoEntity(
            videoId = "v1",
            channelId = "tiktok_alice",
            title = "Short",
            thumbnailPath = "/thumb/v1.jpg",
            publishedAt = 1L,
        ))

        val vm = newViewModel()
        val sub = subscribe(vm)
        val ok = withTimeoutOrNull(2_000L) {
            while (vm.items.value.isEmpty() || vm.playerItems.value.isEmpty()) delay(10)
            true
        }
        sub.cancel()
        assertEquals(true, ok)
        val first = vm.items.value.first()
        assertEquals("v1", first.videoId)
        assertEquals("tiktok_alice", first.channelId)
        assertEquals(com.screwy.igloo.media.OwnerKind.TikTokVideo, first.ownerKind)
        assertEquals("/thumb/v1.jpg", first.thumbnailPath)
        val player = vm.playerItems.value.first()
        assertEquals(com.screwy.igloo.media.OwnerKind.TikTokVideo, player.ownerKind)
        assertEquals("/thumb/v1.jpg", player.fallbackThumbnailPath)
    }

    @Test fun playerItemsExposeSyncedCanonicalUrlWithoutSynthesis() = runBlocking {
        db.channelDao().upsert(ChannelEntity(
            channelId = "tiktok_alice", name = "Alice", platform = "tiktok",
            sourceId = "alice",
        ))
        db.channelFollowDao().upsert(ChannelFollowEntity(channelId = "tiktok_alice"))
        db.videoDao().upsert(VideoEntity(
            videoId = "tiktok_clip_1",
            channelId = "tiktok_alice",
            title = "Short",
            canonicalUrl = "https://www.tiktok.com/@canonical/video/clip_1",
            publishedAt = 1L,
        ))

        val vm = newViewModel()
        val sub = subscribe(vm)
        val ok = withTimeoutOrNull(2_000L) {
            while (vm.playerItems.value.isEmpty()) delay(10)
            true
        }
        sub.cancel()

        assertEquals(true, ok)
        assertEquals("https://www.tiktok.com/@canonical/video/clip_1", vm.playerItems.value.single().canonicalUrl)
    }

    @Test fun playerItemsUseSyncedRepostAuthorLabel() = runBlocking {
        db.channelDao().upsert(ChannelEntity(
            channelId = "tiktok_author", name = "Author", platform = "tiktok",
            sourceId = "author",
        ))
        db.channelDao().upsert(ChannelEntity(
            channelId = "tiktok_reposter", name = "Reposter", platform = "tiktok",
            sourceId = "reposter",
        ))
        db.channelFollowDao().upsert(ChannelFollowEntity(channelId = "tiktok_reposter"))
        db.videoDao().upsert(VideoEntity(
            videoId = "reposted_clip",
            channelId = "tiktok_author",
            title = "Reposted",
            publishedAt = 1L,
        ))
        db.videoRepostSourceDao().upsert(listOf(VideoRepostSourceEntity(
            videoId = "reposted_clip",
            reposterChannelId = "tiktok_reposter",
            reposterHandle = "client_handle_should_not_render",
            reposterDisplayName = "Client Display Should Not Render",
            repostAuthorLabel = "Server Label",
            firstSeenAtMs = 123L,
        )))

        val vm = newViewModel()
        val sub = subscribe(vm)
        val ok = withTimeoutOrNull(2_000L) {
            while (vm.playerItems.value.isEmpty()) delay(10)
            true
        }
        sub.cancel()

        assertEquals(true, ok)
        val item = vm.playerItems.value.single()
        assertEquals("Server Label", item.repostAuthorLabel)
        assertEquals(0, item.repostOtherCount)
    }

    @Test fun startIndexFallsNearHiddenStoredCursor() = runBlocking {
        db.channelDao().upsert(listOf(
            ChannelEntity(channelId = "tiktok_alpha", name = "Alpha", platform = "tiktok", sourceId = "alpha"),
            ChannelEntity(channelId = "tiktok_beta", name = "Beta", platform = "tiktok", sourceId = "beta"),
        ))
        db.channelFollowDao().upsert(ChannelFollowEntity(channelId = "tiktok_alpha"))
        db.videoDao().upsert(listOf(
            VideoEntity(videoId = "alpha_old", channelId = "tiktok_alpha", title = "Old", publishedAt = 100L),
            VideoEntity(videoId = "beta_cursor", channelId = "tiktok_beta", title = "Hidden cursor", publishedAt = 200L),
            VideoEntity(videoId = "alpha_new", channelId = "tiktok_alpha", title = "New", publishedAt = 300L),
        ))
        prefs.setMomentsResumeVideoId("beta_cursor", scope = "all")

        val vm = newViewModel()
        val sub = subscribe(vm)
        val ok = withTimeoutOrNull(2_000L) {
            while (vm.playerItems.value.size < 2 || vm.startIndex.value != 1) delay(10)
            true
        }
        sub.cancel()

        assertEquals(true, ok)
        assertEquals(listOf("alpha_old", "alpha_new"), vm.playerItems.value.map { it.videoId })
        assertEquals(1, vm.startIndex.value)
    }

    @Test fun startIndexFallsToNextVisibleVideoWhenActiveCursorIsUnfollowed() = runBlocking {
        db.channelDao().upsert(listOf(
            ChannelEntity(channelId = "tiktok_alpha", name = "Alpha", platform = "tiktok", sourceId = "alpha"),
            ChannelEntity(channelId = "tiktok_beta", name = "Beta", platform = "tiktok", sourceId = "beta"),
        ))
        db.channelFollowDao().upsert(ChannelFollowEntity(channelId = "tiktok_alpha"))
        db.channelFollowDao().upsert(ChannelFollowEntity(channelId = "tiktok_beta"))
        db.videoDao().upsert(listOf(
            VideoEntity(videoId = "alpha_old", channelId = "tiktok_alpha", title = "Old", publishedAt = 100L),
            VideoEntity(videoId = "beta_current", channelId = "tiktok_beta", title = "Current", publishedAt = 200L),
            VideoEntity(videoId = "alpha_next", channelId = "tiktok_alpha", title = "Next", publishedAt = 300L),
        ))

        val vm = newViewModel()
        val sub = subscribe(vm)
        val loaded = withTimeoutOrNull(2_000L) {
            while (vm.playerItems.value.size < 3) delay(10)
            true
        }
        assertEquals(true, loaded)

        vm.onIndexChange(1)
        val cursorSaved = withTimeoutOrNull(2_000L) {
            while (prefs.momentsResumeVideoId(scope = "all").first() != "beta_current") delay(10)
            true
        }
        assertEquals(true, cursorSaved)

        vm.unfollowChannel("tiktok_beta")
        val advanced = withTimeoutOrNull(2_000L) {
            while (vm.playerItems.value.size != 2 || vm.startIndex.value != 1) delay(10)
            true
        }
        sub.cancel()

        assertEquals(true, advanced)
        assertEquals(listOf("alpha_old", "alpha_next"), vm.playerItems.value.map { it.videoId })
        assertEquals(1, vm.startIndex.value)
    }

    @Test fun startIndexUsesStoredSortWhenCursorVideoMoved() = runBlocking {
        db.channelDao().upsert(ChannelEntity(
            channelId = "tiktok_alpha",
            name = "Alpha",
            platform = "tiktok",
            sourceId = "alpha",
        ))
        db.channelFollowDao().upsert(ChannelFollowEntity(channelId = "tiktok_alpha"))
        db.videoDao().upsert(listOf(
            VideoEntity(videoId = "moved_cursor", channelId = "tiktok_alpha", title = "Moved", publishedAt = 100L),
            VideoEntity(videoId = "near_cursor", channelId = "tiktok_alpha", title = "Near", publishedAt = 300L),
        ))
        prefs.setMomentsResumeVideoId("moved_cursor", scope = "all")
        prefs.setMomentsResumeSortAtMs(250L, scope = "all")

        val vm = newViewModel()
        val sub = subscribe(vm)
        val ok = withTimeoutOrNull(2_000L) {
            while (vm.playerItems.value.size < 2 || vm.startIndex.value != 1) delay(10)
            true
        }
        sub.cancel()

        assertEquals(true, ok)
        assertEquals(listOf("moved_cursor", "near_cursor"), vm.playerItems.value.map { it.videoId })
        assertEquals(1, vm.startIndex.value)
    }

    @Test fun onViewEvent_enqueuesMomentView() = runBlocking {
        val vm = newViewModel()
        vm.onViewEvent("v1")
        val ok = withTimeoutOrNull(1_500L) {
            while (!db.outboxDao().hasPending(OutboxKind.CODE_MOMENT_VIEW, "v1", null)) delay(10)
            true
        }
        assertEquals(true, ok)
        assertEquals(1, db.outboxDao().countByState("pending"))
    }

    @Test fun playbackPrefs_roundTrip_throughViewModel() = runBlocking {
        prefs.setAutoplay(false)
        prefs.setMuteDefault(true)

        val vm = newViewModel()
        val sub = subscribe(vm)

        val loaded = withTimeoutOrNull(2_000L) {
            while (vm.autoplayEnabled.value || !vm.muted.value) delay(10)
            true
        }
        assertEquals(true, loaded)

        vm.setAutoplayEnabled(true)
        vm.setMuted(false)

        val persisted = withTimeoutOrNull(2_000L) {
            while (!prefs.autoplay().first() || prefs.muteDefault().first()) delay(10)
            true
        }
        sub.cancel()
        assertEquals(true, persisted)
    }

    @Test fun onCursorAdvance_persistsZeroPositionForMoments() = runBlocking {
        val vm = newViewModel()

        vm.onCursorAdvance("v1", 9_999L)

        val ok = withTimeoutOrNull(1_500L) {
            while (!db.outboxDao().hasPending(OutboxKind.CODE_MOMENTS_CURSOR, "all", null)) delay(10)
            true
        }
        assertEquals(true, ok)

        val row = db.outboxDao().claimKind(OutboxKind.CODE_MOMENTS_CURSOR, nowMs = Long.MAX_VALUE, limit = 1)
            .firstOrNull()
        assertTrue(row?.payloadJson?.contains("\"position_ms\":0") == true)
    }

    @Test fun fullscreenTransition_staysUntilMatchingMediaDismisses() = runBlocking {
        val vm = newViewModel()
        val transition = FullscreenMediaTransition(
            mediaId = "v1",
            posterUri = MediaUri.Remote("https://example.test/thumb/v1.jpg"),
        )

        vm.setPendingFullscreenTransition(transition)
        assertEquals(transition, vm.pendingFullscreenTransition.value)

        vm.clearPendingFullscreenTransition("other")
        assertEquals(transition, vm.pendingFullscreenTransition.value)

        vm.clearPendingFullscreenTransition("v1")
        assertNull(vm.pendingFullscreenTransition.value)
    }
}
