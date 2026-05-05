package com.screwy.igloo.videos

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.data.entity.VideoEntity
import com.screwy.igloo.sync.Scheduler
import com.screwy.igloo.sync.SyncStream
import com.screwy.igloo.testutil.ViewModelTestTracker
import com.screwy.igloo.ui.UiState
import io.mockk.mockk
import io.mockk.verify
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

/**
 * `VideosViewModel` — first-emission Loading → Data/Empty transition, refresh calls
 * `scheduler.triggerStream(Youtube)`.
 */
@OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class VideosViewModelTest {

    private lateinit var db: IglooDatabase
    private lateinit var scope: CoroutineScope
    private lateinit var scheduler: Scheduler
    private val viewModels = ViewModelTestTracker()

    @Before fun setUp() {
        Dispatchers.setMain(UnconfinedTestDispatcher())
        db = RoomTestSupport.freshDb()
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        scheduler = mockk(relaxed = true)
    }

    @After fun tearDown() {
        viewModels.clearAll()
        scope.cancel()
        db.close()
        Dispatchers.resetMain()
    }

    private fun newViewModel(): VideosViewModel =
        viewModels.track(VideosViewModel(db, scheduler))

    /**
     * VM's stateIn(WhileSubscribed) only wakes the upstream when someone collects. The
     * Compose route subscribes via `collectAsStateWithLifecycle` — tests simulate that
     * by launching a throwaway collector on the same scope for the VM's lifetime.
     */
    private fun subscribe(vm: VideosViewModel): Job = scope.launch {
        launch { vm.uiState.collect {} }
        launch { vm.items.collect {} }
    }

    @Test fun uiState_flipsToData_whenRoomEmitsYouTubeRow() = runBlocking {
        db.videoDao().upsert(VideoEntity(
            videoId = "v1",
            channelId = "youtube_alice",
            title = "Hello",
            publishedAt = 1L,
        ))

        val vm = newViewModel()
        val sub = subscribe(vm)
        val ok = withTimeoutOrNull(2_000L) {
            while (vm.uiState.value !is UiState.Data<*>) delay(10)
            true
        }
        sub.cancel()
        assertEquals(true, ok)
        assertEquals(1, vm.items.value.size)
    }

    @Test fun uiState_flipsToEmpty_whenOnlyNonYouTubeRowsExist() = runBlocking {
        // TikTok channel rows aren't served by VideoReadDao.videosFlow() — the DAO
        // filters `channel_id LIKE 'youtube_%'`, so VideosViewModel should see empty.
        db.videoDao().upsert(VideoEntity(
            videoId = "v1", channelId = "tiktok_alice", title = "Moment", publishedAt = 1L,
        ))
        val vm = newViewModel()
        val sub = subscribe(vm)
        val ok = withTimeoutOrNull(2_000L) {
            while (vm.uiState.value !is UiState.Empty) delay(10)
            true
        }
        sub.cancel()
        assertEquals(true, ok)
    }

    @Test fun refresh_triggersYoutubeStream() = runBlocking {
        val vm = newViewModel()
        vm.refresh()
        val ok = withTimeoutOrNull(1_500L) {
            while (vm.isRefreshing.value != true) delay(10)
            true
        }
        assertEquals(true, ok)
        verify { scheduler.triggerStream(SyncStream.Youtube) }
    }
}
