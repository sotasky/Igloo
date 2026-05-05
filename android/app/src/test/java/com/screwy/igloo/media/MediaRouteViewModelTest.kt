package com.screwy.igloo.media

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.data.entity.BookmarkEntity
import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.outbox.OutboxWriter
import com.screwy.igloo.testutil.ViewModelTestTracker
import com.screwy.igloo.ui.UiState
import com.screwy.igloo.ui.UiEffects
import com.screwy.igloo.ui.component.BookmarkPayload
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
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
class MediaRouteViewModelTest {
    private lateinit var db: IglooDatabase
    private lateinit var scope: CoroutineScope
    private lateinit var prefs: PreferencesRepo
    private lateinit var writer: OutboxWriter
    private val viewModels = ViewModelTestTracker()

    @Before fun setUp() {
        Dispatchers.setMain(UnconfinedTestDispatcher())
        db = RoomTestSupport.freshDb()
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        prefs = PreferencesRepo(db.preferenceDao(), scope, nowMsProvider = { 1_000L })
        writer = OutboxWriter(
            db = db,
            prefs = prefs,
            scope = scope,
            nowMsProvider = { 1_000L },
            writeDebounceMs = 50L,
        )
    }

    @After fun tearDown() {
        viewModels.clearAll()
        scope.cancel()
        db.close()
        Dispatchers.resetMain()
    }

    @Test fun mediaStateTracksLikeSideTableAfterToggles() = runBlocking {
        seedMediaTweet()
        val vm = newViewModel()
        val sub = subscribe(vm)
        assertLoaded(vm)
        assertEquals(0, vm.mediaState.value?.row?.isLiked)

        vm.toggleLike()
        assertRowFlag(vm) { it?.row?.isLiked == 1 }

        vm.toggleLike()
        assertRowFlag(vm) { it?.row?.isLiked == 0 }
        sub.cancel()
    }

    @Test fun mediaStateTracksBookmarkSideTableAfterConfirmAndRemove() = runBlocking {
        seedMediaTweet()
        val vm = newViewModel()
        val sub = subscribe(vm)
        assertLoaded(vm)
        assertEquals(0, vm.mediaState.value?.row?.isBookmarked)

        vm.toggleBookmark()
        val pending = withTimeoutOrNull(2_000L) {
            vm.pendingBookmark.first { it != null }
        }
        assertEquals("t1", pending?.itemId)

        vm.confirmBookmark(BookmarkPayload(categoryId = 0L, customTitle = null, mediaIndices = null))
        assertRowFlag(vm) { it?.row?.isBookmarked == 1 }

        vm.toggleBookmark()
        withTimeoutOrNull(2_000L) {
            vm.pendingBookmark.first { it != null }
        }
        vm.removePendingBookmark()
        assertRowFlag(vm) { it?.row?.isBookmarked == 0 }
        sub.cancel()
    }

    @Test fun mediaStateReflectsExistingBookmarkWhenRouteOpens() = runBlocking {
        seedMediaTweet()
        db.bookmarkDao().upsert(BookmarkEntity(videoId = "t1", categoryId = 7L, bookmarkedAt = 50L))

        val vm = newViewModel()
        val sub = subscribe(vm)
        assertLoaded(vm)

        assertEquals(1, vm.mediaState.value?.row?.isBookmarked)
        assertEquals(7L, vm.mediaState.value?.row?.bookmarkCategoryId)
        sub.cancel()
    }

    private fun newViewModel(): MediaRouteViewModel =
        viewModels.track(
            MediaRouteViewModel(
                ownerKind = "tweet",
                ownerId = "t1",
                requestedIndex = 0,
                db = db,
                outboxWriter = writer,
                baseUrlProvider = ServerBaseUrlProvider { "http://igloo.local" },
                uiEffects = UiEffects(),
            ),
        )

    private fun subscribe(vm: MediaRouteViewModel) = scope.launch {
        launch { vm.uiState.collect {} }
        launch { vm.mediaState.collect {} }
    }

    private suspend fun assertLoaded(vm: MediaRouteViewModel) {
        val ok = withTimeoutOrNull(2_000L) {
            while (vm.uiState.value !is UiState.Data<*> || vm.mediaState.value == null) delay(10)
            true
        }
        assertEquals(true, ok)
    }

    private suspend fun assertRowFlag(
        vm: MediaRouteViewModel,
        predicate: (MediaRouteState?) -> Boolean,
    ) {
        val ok = withTimeoutOrNull(2_000L) {
            while (!predicate(vm.mediaState.value)) delay(10)
            true
        }
        assertEquals(true, ok)
    }

    private suspend fun seedMediaTweet() {
        db.channelDao().upsert(
            ChannelEntity(
                channelId = "twitter_alice",
                name = "Alice",
                platform = "twitter",
            ),
        )
        db.feedItemDao().upsert(
            FeedItemEntity(
                tweetId = "t1",
                authorHandle = "alice",
                authorDisplayName = "Alice",
                channelId = "twitter_alice",
                bodyText = "hello",
                mediaJson = """[{"type":"photo","url":"https://example.com/image.jpg"}]""",
                syncSeq = 1,
            ),
        )
    }
}
