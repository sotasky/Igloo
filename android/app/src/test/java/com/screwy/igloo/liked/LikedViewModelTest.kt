package com.screwy.igloo.liked

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.data.entity.FeedLikeEntity
import com.screwy.igloo.outbox.OutboxWriter
import com.screwy.igloo.sync.Scheduler
import com.screwy.igloo.testutil.ViewModelTestTracker
import com.screwy.igloo.ui.UiState
import com.screwy.igloo.ui.UiEffects
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
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * Liked route tests: it should be the same feed surface backed by the liked-only query,
 * with the same mutation wiring as Feed.
 */
@OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class LikedViewModelTest {

    private lateinit var db: IglooDatabase
    private lateinit var scope: CoroutineScope
    private lateinit var prefs: PreferencesRepo
    private lateinit var writer: OutboxWriter
    private lateinit var scheduler: Scheduler
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
        scheduler = mockk(relaxed = true)
        uiEffects = UiEffects()
    }

    @After fun tearDown() {
        viewModels.clearAll()
        scope.cancel()
        db.close()
        Dispatchers.resetMain()
    }

    private fun newViewModel(): LikedViewModel =
        viewModels.track(
            LikedViewModel(
                db = db,
                outboxWriter = writer,
                scheduler = scheduler,
                uiEffects = uiEffects,
                baseUrlProvider = com.screwy.igloo.net.ServerBaseUrlProvider { "https://igloo.test" },
            )
        )

    private fun subscribe(vm: LikedViewModel): Job = scope.launch {
        launch { vm.uiState.collect {} }
        launch { vm.rows.collect {} }
    }

    @Test fun uiState_flipsToData_withLikedRowsOnly() = runBlocking {
        db.channelDao().upsert(ChannelEntity(
            channelId = "twitter_alice",
            name = "Alice",
            platform = "twitter",
        ))
        db.feedItemDao().upsert(listOf(
            FeedItemEntity(tweetId = "t1", authorHandle = "alice", channelId = "twitter_alice", syncSeq = 1),
            FeedItemEntity(tweetId = "t2", authorHandle = "alice", channelId = "twitter_alice", syncSeq = 2),
            FeedItemEntity(tweetId = "t3", authorHandle = "alice", channelId = "twitter_alice", syncSeq = 3),
        ))
        db.feedLikeDao().upsert(FeedLikeEntity("t1", likedAt = 300))
        db.feedLikeDao().upsert(FeedLikeEntity("t3", likedAt = 100))

        val vm = newViewModel()
        val sub = subscribe(vm)
        val ok = withTimeoutOrNull(2_000L) {
            while (vm.uiState.value !is UiState.Data<*> || vm.rows.value.map { it.row.item.tweetId } != listOf("t1", "t3")) {
                delay(10)
            }
            true
        }
        sub.cancel()

        assertEquals(true, ok)
        assertEquals(listOf("t1", "t3"), vm.rows.value.map { it.row.item.tweetId })
    }

    @Test fun toggleFollow_set_enqueuesFollowAndUpdatesLocalState() = runBlocking {
        val vm = newViewModel()
        vm.toggleFollow(channelId = "twitter_alice", newValue = true)

        val ok = withTimeoutOrNull(1_500L) {
            while (!db.channelFollowDao().exists("twitter_alice")) delay(10)
            true
        }

        assertEquals(true, ok)
        assertEquals(1, db.outboxDao().countByState("pending"))
    }

    @Test fun toggleMute_set_enqueuesMuteAndUpdatesLocalState() = runBlocking {
        val vm = newViewModel()
        vm.toggleMute(handle = "alice", newValue = true)

        val ok = withTimeoutOrNull(1_500L) {
            while (!db.mutedAccountDao().exists("alice")) delay(10)
            true
        }

        assertEquals(true, ok)
        assertEquals(1, db.outboxDao().countByState("pending"))
    }

}
