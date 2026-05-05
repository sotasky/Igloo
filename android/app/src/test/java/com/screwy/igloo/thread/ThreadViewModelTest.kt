package com.screwy.igloo.thread

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.outbox.OutboxWriter
import com.screwy.igloo.testutil.ViewModelTestTracker
import com.screwy.igloo.ui.UiEffects
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
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
class ThreadViewModelTest {

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

    private fun newViewModel(): ThreadViewModel =
        viewModels.track(
            ThreadViewModel(
                db = db,
                outboxWriter = writer,
                uiEffects = uiEffects,
                baseUrlProvider = com.screwy.igloo.net.ServerBaseUrlProvider { "https://igloo.test" },
            )
        )

    @Test fun load_returnsChainRootToLeaf() = runBlocking {
        db.channelDao().upsert(ChannelEntity(channelId = "twitter_user_alpha", name = "Alpha", platform = "twitter"))
        db.feedItemDao().upsert(listOf(
            FeedItemEntity(tweetId = "t1", authorHandle = "user_alpha", channelId = "twitter_user_alpha"),
            FeedItemEntity(
                tweetId = "t2",
                authorHandle = "user_alpha",
                channelId = "twitter_user_alpha",
                isReply = true,
                replyToStatus = "t1",
            ),
            FeedItemEntity(
                tweetId = "t3",
                authorHandle = "user_alpha",
                channelId = "twitter_user_alpha",
                isReply = true,
                replyToStatus = "t2",
            ),
        ))
        val vm = newViewModel()

        vm.loadBlocking("t3")

        assertEquals(listOf("t1", "t2", "t3"), vm.chain.value.map { it.item.tweetId })
    }

    @Test fun load_unknownTweetEmitsEmptyChain() = runBlocking {
        val vm = newViewModel()

        vm.loadBlocking("unknown")

        assertEquals(emptyList<String>(), vm.chain.value.map { it.item.tweetId })
    }

    @Test fun load_embeddedQuoteTweetEmitsFullQuoteRow() = runBlocking {
        db.channelDao().upsert(ChannelEntity(channelId = "twitter_user_alpha", name = "Alpha", platform = "twitter"))
        db.feedItemDao().upsert(
            FeedItemEntity(
                tweetId = "wrapper",
                authorHandle = "user_alpha",
                channelId = "twitter_user_alpha",
                quoteTweetId = "quoted_tweet",
                quoteAuthorHandle = "@Quote_Author",
                quoteAuthorDisplayName = "Quote Author",
                quoteAuthorAvatarUrl = "https://example.test/quote.jpg",
                quoteBodyText = "The full quoted post text should be readable after opening the quote.",
                quoteLang = "en",
                quoteMediaJson = """[{"type":"image","url":"https://example.test/quote.jpg"}]""",
                quotePublishedAt = 1234L,
                quoteCanonicalUrl = "https://x.com/Quote_Author/status/quoted_tweet",
            ),
        )
        val vm = newViewModel()

        vm.loadBlocking("quoted_tweet")

        val row = vm.chain.value.single()
        assertEquals("quoted_tweet", row.item.tweetId)
        assertEquals("Quote_Author", row.item.authorHandle)
        assertEquals("twitter_quote_author", row.item.channelId)
        assertEquals("Quote Author", row.channelName)
        assertEquals("The full quoted post text should be readable after opening the quote.", row.item.bodyText)
        assertEquals("""[{"type":"image","url":"https://example.test/quote.jpg"}]""", row.item.mediaJson)
        assertEquals("https://x.com/Quote_Author/status/quoted_tweet", row.item.canonicalUrl)
    }

    @Test fun toggleLike_updatesThreadChainAndQueuesOutbox() = runBlocking {
        db.channelDao().upsert(ChannelEntity(channelId = "twitter_user_alpha", name = "Alpha", platform = "twitter"))
        db.feedItemDao().upsert(FeedItemEntity(
            tweetId = "t1",
            authorHandle = "user_alpha",
            channelId = "twitter_user_alpha",
        ))
        val vm = newViewModel()
        vm.loadBlocking("t1")
        assertEquals(0, vm.chain.value.single().isLiked)

        vm.toggleLike(tweetId = "t1", newValue = true)

        val updated = withTimeoutOrNull(1_500L) {
            while (vm.chain.value.singleOrNull()?.isLiked != 1) delay(10)
            true
        }
        assertEquals(true, updated)
        assertEquals(1, db.outboxDao().countByState("pending"))
    }
}
