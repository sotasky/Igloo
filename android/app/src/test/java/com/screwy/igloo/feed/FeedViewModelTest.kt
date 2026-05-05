package com.screwy.igloo.feed

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.data.entity.BookmarkEntity
import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.data.entity.FeedLikeEntity
import com.screwy.igloo.data.entity.FeedRankEntity
import com.screwy.igloo.data.entity.FeedSeenEntity
import com.screwy.igloo.outbox.OutboxKind
import com.screwy.igloo.outbox.OutboxWriter
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.sync.Scheduler
import com.screwy.igloo.testutil.ViewModelTestTracker
import com.screwy.igloo.ui.UiEffects
import com.screwy.igloo.ui.UiState
import io.mockk.mockk
import io.mockk.verify
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
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
 * `FeedViewModel` — first-emit Loading → Data/Empty transition, toggleLike enqueues
 * the right outbox kind.
 */
@OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class FeedViewModelTest {

    private lateinit var db: IglooDatabase
    private lateinit var scope: CoroutineScope
    private lateinit var prefs: PreferencesRepo
    private lateinit var writer: OutboxWriter
    private lateinit var scheduler: Scheduler
    private lateinit var uiEffects: UiEffects
    private val viewModels = ViewModelTestTracker()

    @Before fun setUp() {
        Dispatchers.setMain(Dispatchers.Default)
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

    private fun newViewModel(): FeedViewModel =
        viewModels.track(
            FeedViewModel(
                db = db,
                outboxWriter = writer,
                scheduler = scheduler,
                uiEffects = uiEffects,
                baseUrlProvider = ServerBaseUrlProvider { "https://igloo.local" },
            ),
        )

    /**
     * VM's stateIn(WhileSubscribed) only wakes the upstream when someone collects. The
     * Compose route subscribes via `collectAsStateWithLifecycle` — tests simulate that
     * by launching a throwaway collector on the same scope for the VM's lifetime.
     */
    private fun subscribe(vm: FeedViewModel): Job = scope.launch {
        launch { vm.uiState.collect {} }
        launch { vm.rows.collect {} }
    }

    @Test fun uiState_flipsToData_whenRoomEmitsNonEmpty() = runBlocking {
        db.channelDao().upsert(ChannelEntity(
            channelId = "twitter_alice", name = "Alice", platform = "twitter",
        ))
        db.feedItemDao().upsert(FeedItemEntity(
            tweetId = "t1", authorHandle = "alice", channelId = "twitter_alice", syncSeq = 1,
        ))

        val vm = newViewModel()
        val data = withTimeoutOrNull(15_000L) {
            vm.uiState.first { it is UiState.Data<*> }
        }
        val rows = withTimeoutOrNull(15_000L) {
            vm.rows.first { it.size == 1 }
        }
        assertEquals(true, data is UiState.Data<*>)
        assertEquals(1, rows?.size)
    }

    @Test fun uiState_flipsToEmpty_whenRoomEmitsEmptyList() = runBlocking {
        val vm = newViewModel()
        val empty = withTimeoutOrNull(15_000L) {
            vm.uiState.first { it is UiState.Empty }
        }
        assertEquals(true, empty is UiState.Empty)
    }

    @Test fun toggleLike_set_enqueuesLikeSetKind() = runBlocking {
        val vm = newViewModel()
        vm.toggleLike(tweetId = "t1", newValue = true)

        val ok = withTimeoutOrNull(1_500L) {
            while (!db.feedLikeDao().exists("t1")) delay(10)
            true
        }
        assertEquals(true, ok)
        assertEquals(1, db.outboxDao().countByState("pending"))
    }

    @Test fun refresh_triggersFeedStream() = runBlocking {
        val vm = newViewModel()
        vm.refresh()
        // refresh() launches a coroutine that calls scheduler.triggerStream then holds
        // _isRefreshing = true for ~1s before flipping back. Wait until the scheduler
        // call lands, which confirms the coroutine ran.
        val ok = withTimeoutOrNull(1_500L) {
            while (vm.isRefreshing.value != true) delay(10)
            true
        }
        assertEquals(true, ok)
        verify { scheduler.triggerStream(com.screwy.igloo.sync.SyncStream.Feed) }
    }

    @Test fun seenWrite_doesNotRemoveVisibleRowUntilExplicitRefresh() = runBlocking {
        db.channelDao().upsert(ChannelEntity(
            channelId = "twitter_alice", name = "Alice", platform = "twitter",
        ))
        db.feedItemDao().upsert(listOf(
            FeedItemEntity(tweetId = "t1", authorHandle = "alice", channelId = "twitter_alice", syncSeq = 1),
            FeedItemEntity(tweetId = "t2", authorHandle = "alice", channelId = "twitter_alice", syncSeq = 2),
        ))

        val vm = newViewModel()
        val sub = subscribe(vm)
        val initial = withTimeoutOrNull(2_000L) {
            while (vm.rows.value.map { it.row.item.tweetId } != listOf("t2", "t1")) delay(10)
            true
        }
        assertEquals(true, initial)

        db.feedSeenDao().upsert(FeedSeenEntity("t2", seenAt = 10))
        delay(250L)
        assertEquals(listOf("t2", "t1"), vm.rows.value.map { it.row.item.tweetId })
        assertEquals(false, vm.newPostsAvailable.value)

        vm.refresh()
        val refreshed = withTimeoutOrNull(5_000L) {
            while (vm.rows.value.map { it.row.item.tweetId } != listOf("t1")) delay(10)
            true
        }
        sub.cancel()
        assertEquals(true, refreshed)
    }

    @Test fun toggleMuteUpdatesSnapshotHeadAndClearsCueState() = runBlocking {
        db.channelDao().upsert(ChannelEntity(
            channelId = "twitter_alice", name = "Alice", platform = "twitter",
        ))
        db.feedItemDao().upsert(listOf(
            FeedItemEntity(tweetId = "t1", authorHandle = "alice", channelId = "twitter_alice", syncSeq = 1),
            FeedItemEntity(tweetId = "t2", authorHandle = "bob", channelId = "twitter_bob", syncSeq = 2),
        ))

        val vm = newViewModel()
        val sub = subscribe(vm)
        val initial = withTimeoutOrNull(2_000L) {
            while (vm.rows.value.map { it.row.item.tweetId } != listOf("t2", "t1")) delay(10)
            true
        }
        assertEquals(true, initial)

        vm.toggleMute("bob", true)
        val muted = withTimeoutOrNull(2_000L) {
            while (
                vm.rows.value.map { it.row.item.tweetId } != listOf("t1") ||
                !db.mutedAccountDao().exists("bob")
            ) {
                delay(10)
            }
            true
        }
        assertEquals(true, muted)

        db.feedItemDao().upsert(FeedItemEntity(
            tweetId = "t0", authorHandle = "alice", channelId = "twitter_alice", syncSeq = 0,
        ))
        delay(250L)
        sub.cancel()
        assertEquals(listOf("t1"), vm.rows.value.map { it.row.item.tweetId })
        assertEquals(false, vm.newPostsAvailable.value)
        assertEquals(emptyList<String>(), vm.newPostPosters.value.map { it.channelId })
    }

    @Test fun snapshotLoadKeepsOneBoundedInMemoryListWithoutLoadMore() = runBlocking {
        db.channelDao().upsert(ChannelEntity(
            channelId = "twitter_alice", name = "Alice", platform = "twitter",
        ))
        db.feedItemDao().upsert(
            (1..520).map { index ->
                FeedItemEntity(
                    tweetId = "t$index",
                    authorHandle = "alice",
                    channelId = "twitter_alice",
                    syncSeq = index.toLong(),
                )
            },
        )

        val vm = newViewModel()
        val sub = subscribe(vm)
        val loaded = withTimeoutOrNull(2_000L) {
            while (vm.rows.value.size != FeedViewModel.FEED_LIMIT) delay(10)
            true
        }
        sub.cancel()
        assertEquals(true, loaded)
        assertEquals("t520", vm.rows.value.first().row.item.tweetId)
        assertEquals("t21", vm.rows.value.last().row.item.tweetId)
    }

    @Test fun sideTableActionStateUpdatesVisibleSnapshot() = runBlocking {
        db.channelDao().upsert(ChannelEntity(
            channelId = "twitter_alice", name = "Alice", platform = "twitter",
        ))
        db.feedItemDao().upsert(FeedItemEntity(
            tweetId = "t1", authorHandle = "alice", channelId = "twitter_alice", syncSeq = 1,
        ))

        val vm = newViewModel()
        val sub = subscribe(vm)
        val initial = withTimeoutOrNull(2_000L) {
            while (vm.rows.value.singleOrNull()?.row?.item?.tweetId != "t1") delay(10)
            true
        }
        assertEquals(true, initial)
        assertEquals(0, vm.rows.value.single().row.isLiked)
        assertEquals(0, vm.rows.value.single().row.isBookmarked)

        db.feedLikeDao().upsert(FeedLikeEntity("t1", likedAt = 42))
        db.bookmarkDao().upsert(BookmarkEntity(videoId = "t1", bookmarkedAt = 50, categoryId = 7))

        val updated = withTimeoutOrNull(2_000L) {
            while (
                vm.rows.value.singleOrNull()?.row?.isLiked != 1 ||
                vm.rows.value.singleOrNull()?.row?.isBookmarked != 1
            ) {
                delay(10)
            }
            true
        }
        assertEquals(true, updated)
        assertEquals(42L, vm.rows.value.single().row.likedAt)
        assertEquals(7L, vm.rows.value.single().row.bookmarkCategoryId)

        db.feedLikeDao().delete("t1")
        db.bookmarkDao().delete("t1")
        val cleared = withTimeoutOrNull(2_000L) {
            while (
                vm.rows.value.singleOrNull()?.row?.isLiked != 0 ||
                vm.rows.value.singleOrNull()?.row?.isBookmarked != 0
            ) {
                delay(10)
            }
            true
        }
        sub.cancel()
        assertEquals(true, cleared)
        assertEquals(null, vm.rows.value.single().row.likedAt)
        assertEquals(null, vm.rows.value.single().row.bookmarkCategoryId)
    }

    @Test fun newRankedHeadShowsRefreshCueWithoutAutoInsertingRow() = runBlocking {
        db.channelDao().upsert(ChannelEntity(
            channelId = "twitter_alice", name = "Alice", platform = "twitter",
        ))
        db.feedItemDao().upsert(listOf(
            FeedItemEntity(tweetId = "t1", authorHandle = "alice", channelId = "twitter_alice", syncSeq = 1),
            FeedItemEntity(tweetId = "t2", authorHandle = "alice", channelId = "twitter_alice", syncSeq = 2),
        ))

        val vm = newViewModel()
        val sub = subscribe(vm)
        val initial = withTimeoutOrNull(2_000L) {
            while (vm.rows.value.map { it.row.item.tweetId } != listOf("t2", "t1")) delay(10)
            true
        }
        assertEquals(true, initial)

        db.feedItemDao().upsert(listOf(
            FeedItemEntity(tweetId = "t3", authorHandle = "alice", channelId = "twitter_alice", syncSeq = 3),
            FeedItemEntity(tweetId = "t4", authorHandle = "bob", channelId = "twitter_bob", syncSeq = 4),
            FeedItemEntity(tweetId = "t5", authorHandle = "alice", channelId = "twitter_alice", syncSeq = 5),
            FeedItemEntity(tweetId = "t6", authorHandle = "carol", channelId = "twitter_carol", syncSeq = 6),
        ))
        db.feedRankDao().upsert(listOf(
            FeedRankEntity(tweetId = "t3", rankPosition = 1, snapshotAt = 20),
            FeedRankEntity(tweetId = "t4", rankPosition = 2, snapshotAt = 20),
            FeedRankEntity(tweetId = "t5", rankPosition = 3, snapshotAt = 20),
            FeedRankEntity(tweetId = "t6", rankPosition = 4, snapshotAt = 20),
            FeedRankEntity(tweetId = "t2", rankPosition = 5, snapshotAt = 20),
            FeedRankEntity(tweetId = "t1", rankPosition = 6, snapshotAt = 20),
        ))

        val expectedPosters = listOf("twitter_alice", "twitter_bob", "twitter_carol")
        val cueShown = withTimeoutOrNull(5_000L) {
            while (
                !vm.newPostsAvailable.value ||
                vm.rows.value.map { it.row.item.tweetId } != listOf("t2", "t1") ||
                vm.newPostPosters.value.map { it.channelId } != expectedPosters
            ) {
                delay(10)
            }
            true
        }
        assertEquals(true, cueShown)
        assertEquals(listOf("t2", "t1"), vm.rows.value.map { it.row.item.tweetId })
        assertEquals(expectedPosters, vm.newPostPosters.value.map { it.channelId })

        vm.refresh()
        val refreshed = withTimeoutOrNull(5_000L) {
            while (vm.rows.value.map { it.row.item.tweetId } != listOf("t3", "t4", "t5", "t6", "t2", "t1")) delay(10)
            true
        }
        sub.cancel()
        assertEquals(true, refreshed)
        assertEquals(false, vm.newPostsAvailable.value)
        assertEquals(emptyList<String>(), vm.newPostPosters.value.map { it.channelId })
    }
}
