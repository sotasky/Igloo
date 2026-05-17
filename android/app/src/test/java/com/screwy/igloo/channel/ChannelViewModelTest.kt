package com.screwy.igloo.channel

import com.screwy.igloo.R
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.VideoEntity
import com.screwy.igloo.media.OwnerKind
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.outbox.OutboxKind
import com.screwy.igloo.outbox.OutboxWriter
import com.screwy.igloo.sync.SyncStream
import com.screwy.igloo.testutil.FakeSchedulerActions
import com.screwy.igloo.testutil.ViewModelTestTracker
import com.screwy.igloo.ui.UiEffect
import com.screwy.igloo.ui.UiEffects
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.async
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import kotlinx.coroutines.withTimeout
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.coroutines.flow.flowOf
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * `ChannelViewModel` — combines channel + follow/star flows into a ChannelDisplay;
 * toggleFollow enqueues a Follow outbox row with the right action.
 */
@OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class ChannelViewModelTest {

    private lateinit var db: IglooDatabase
    private lateinit var scope: CoroutineScope
    private lateinit var prefs: PreferencesRepo
    private lateinit var writer: OutboxWriter
    private lateinit var scheduler: FakeSchedulerActions
    private lateinit var uiEffects: UiEffects
    private lateinit var reachability: Reachability
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
        scheduler = FakeSchedulerActions()
        uiEffects = UiEffects()
        reachability = Reachability(
            scope = scope,
            probe = { true },
            foregroundFlow = flowOf(false),
        )
    }

    @After fun tearDown() {
        viewModels.clearAll()
        scope.cancel()
        db.close()
        Dispatchers.resetMain()
    }

    private fun newViewModel(channelId: String): ChannelViewModel =
        viewModels.track(ChannelViewModel(
            channelId = channelId,
            db = db,
            outboxWriter = writer,
            prefs = prefs,
            scheduler = scheduler,
            uiEffects = uiEffects,
            reachability = reachability,
            baseUrlProvider = com.screwy.igloo.net.ServerBaseUrlProvider { "https://igloo.test" },
        ))

    private fun subscribe(vm: ChannelViewModel): Job = scope.launch {
        launch { vm.channel.collect {} }
        launch { vm.uiState.collect {} }
        launch { vm.momentThumbs.collect {} }
    }

    @Test fun channel_reflectsEntityPlatform() = runBlocking {
        val channelId = "twitter_alice"
        db.channelDao().upsert(ChannelEntity(
            channelId = channelId, name = "Alice", platform = "twitter",
            sourceId = "alice",
        ))

        val vm = newViewModel(channelId)
        val sub = subscribe(vm)
        val ok = withTimeoutOrNull(2_000L) {
            while (vm.channel.value.channel.sourceId != "alice") delay(10)
            true
        }
        sub.cancel()
        assertEquals(true, ok)
        assertEquals("twitter", vm.channel.value.channel.platform)
    }

    @Test fun missingChannelRow_buildsSyntheticDisplayWithoutPlatformUrl() = runBlocking {
        val channelId = "tiktok_ghost_user"
        val vm = newViewModel(channelId)
        val sub = subscribe(vm)
        val ok = withTimeoutOrNull(2_000L) {
            while (vm.channel.value.channel.channelId != channelId) delay(10)
            true
        }
        sub.cancel()

        assertEquals(true, ok)
        assertEquals("tiktok", vm.channel.value.channel.platform)
        assertEquals("ghost_user", vm.channel.value.channel.sourceId)
        assertEquals("ghost_user", vm.channel.value.channel.name)
        assertEquals(null, vm.channel.value.channel.url)
    }

    @Test fun init_triggersChannelsSync_whenChannelRowMissing() = runBlocking {
        // No pre-insert: Room has no row for this channelId, so construction
        // should kick a Channels sync to backfill.
        val channelId = "twitter_ghost"
        val vm = newViewModel(channelId)
        // The init block launches on viewModelScope (test Main dispatcher
        // here); poll until MockK records the call so we're not racing the
        // launched coroutine. Timeout bounds the failure case.
        val ok = withTimeoutOrNull(2_000L) {
            while (true) {
                try {
                    if (scheduler.triggeredStreams.count { it == SyncStream.Channels } == 1) {
                        return@withTimeoutOrNull true
                    }
                    throw AssertionError("Channels sync was not triggered exactly once")
                    return@withTimeoutOrNull true
                } catch (_: AssertionError) {
                    delay(10)
                }
            }
            @Suppress("UNREACHABLE_CODE") true
        }
        assertEquals(true, ok)
        // Keep `vm` referenced past verify so GC doesn't reap the viewModelScope
        // before the init block runs on slower CI.
        assertEquals(channelId, channelId.also { vm.hashCode() })
    }

    @Test fun init_doesNotTriggerChannelsSync_whenChannelRowPresent() = runBlocking {
        // Row already cached → no sync needed; triggerStream must not fire.
        val channelId = "twitter_alice"
        db.channelDao().upsert(ChannelEntity(
            channelId = channelId, name = "Alice", platform = "twitter", sourceId = "alice",
        ))
        val vm = newViewModel(channelId)
        // Give the init coroutine a chance to run before asserting the negative.
        // A short delay is the simplest way to "advance past" the viewModelScope
        // launch without a TestDispatcher rig (the VM uses Dispatchers.Main which
        // is set to Default here, not a test scheduler).
        delay(200L)
        assertEquals(0, scheduler.triggeredStreams.count { it == SyncStream.Channels })
        // Reference vm past the verify window — same reason as the sibling test.
        assertEquals(channelId, channelId.also { vm.hashCode() })
    }

    @Test fun toggleFollow_enqueuesFollowSet() = runBlocking {
        val channelId = "twitter_alice"
        db.channelDao().upsert(ChannelEntity(
            channelId = channelId, name = "Alice", platform = "twitter", sourceId = "alice",
        ))
        val vm = newViewModel(channelId)
        vm.toggleFollow(newValue = true)
        val ok = withTimeoutOrNull(1_500L) {
            while (!db.outboxDao().hasPending(OutboxKind.CODE_FOLLOW, channelId, null)) delay(10)
            true
        }
        assertEquals(true, ok)
        // Local side-table write also landed — optimistic UI.
        assertEquals(true, db.channelFollowDao().exists(channelId))
    }

    @Test fun toggleFollow_clear_emitsOfflineQueuedToast() = runBlocking {
        val channelId = "twitter_alice"
        db.channelDao().upsert(ChannelEntity(
            channelId = channelId, name = "Alice", platform = "twitter", sourceId = "alice",
        ))
        db.channelFollowDao().upsert(
            com.screwy.igloo.data.entity.ChannelFollowEntity(channelId = channelId, followedAt = 0L)
        )
        val vm = newViewModel(channelId)
        val effect = async(start = CoroutineStart.UNDISPATCHED) { uiEffects.flow.first() }

        vm.toggleFollow(newValue = false)

        assertEquals(
            UiEffect.ToastRes(
                resId = R.string.unfollow_queued_waiting,
                longDuration = true,
            ),
            withTimeout(2_000L) { effect.await() },
        )
    }

    @Test fun toggleFollow_updatesSyntheticChannelFollowState() = runBlocking {
        val channelId = "youtube_ghost_channel"
        val vm = newViewModel(channelId)
        val sub = subscribe(vm)

        vm.toggleFollow(newValue = true)
        val ok = withTimeoutOrNull(2_000L) {
            while (vm.channel.value.isFollowed != 1) delay(10)
            true
        }
        sub.cancel()

        assertEquals(true, ok)
        assertEquals(true, db.channelFollowDao().exists(channelId))
    }

    @Test fun channelMoments_mapDisplayNameAndHandleIntoGridItems() = runBlocking {
        val channelId = "tiktok_alice"
        db.channelDao().upsert(ChannelEntity(
            channelId = channelId,
            name = "Alice Doe",
            platform = "tiktok",
            sourceId = "alice",
        ))
        db.videoDao().upsert(
            VideoEntity(
                videoId = "clip_1",
                channelId = channelId,
                title = "Clip title",
                description = "Clip description",
                thumbnailPath = "/thumbs/clip_1.jpg",
                publishedAt = 123L,
            ),
        )

        val vm = newViewModel(channelId)
        val sub = subscribe(vm)

        val ok = withTimeoutOrNull(2_000L) {
            while (vm.momentThumbs.value.isEmpty()) delay(10)
            true
        }
        sub.cancel()
        assertEquals(true, ok)
        assertEquals("Alice Doe", vm.momentThumbs.value.single().authorDisplayName)
        assertEquals("@alice", vm.momentThumbs.value.single().authorHandle)
        assertEquals(OwnerKind.TikTokVideo, vm.momentThumbs.value.single().ownerKind)
        assertEquals("/thumbs/clip_1.jpg", vm.momentThumbs.value.single().thumbnailPath)
    }

    @Test fun resolveMentionAndNavigate_usesCurrentChannelPlatformForSyntheticRoutes() = runBlocking {
        val channelId = "youtube_creator"
        db.channelDao().upsert(ChannelEntity(
            channelId = channelId,
            name = "Creator",
            platform = "youtube",
            sourceId = "creator",
        ))

        val vm = newViewModel(channelId)
        val sub = subscribe(vm)
        val effect = async(start = CoroutineStart.UNDISPATCHED) { uiEffects.flow.first() }

        vm.resolveMentionAndNavigate("@Other.Creator")

        assertEquals(
            com.screwy.igloo.ui.UiEffect.NavigateTo("channel/youtube_other.creator"),
            withTimeout(2_000L) { effect.await() },
        )
        sub.cancel()
    }
}
