package com.screwy.igloo.sync

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.log.InMemoryLogSink
import com.screwy.igloo.log.Logger
import com.screwy.igloo.net.BundleEnvelope
import com.screwy.igloo.net.ChannelsApi
import com.screwy.igloo.net.DeltaResponse
import com.screwy.igloo.net.FeedApi
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.net.ShortsApi
import com.screwy.igloo.net.VideoApi
import com.screwy.igloo.net.iglooJson
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.MockRequestHandleScope
import io.ktor.client.engine.mock.respond
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.http.ContentType
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import io.ktor.serialization.kotlinx.json.json
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.emptyFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.After
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class InboundReconcilerTest {

    private lateinit var db: IglooDatabase
    private lateinit var scope: CoroutineScope
    private lateinit var prefs: PreferencesRepo
    private lateinit var logger: Logger
    private lateinit var logSink: InMemoryLogSink
    private lateinit var client: HttpClient
    private var nowMs: Long = 10_000L

    @Before fun setUp() {
        db = RoomTestSupport.freshDb()
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        prefs = PreferencesRepo(db.preferenceDao(), scope, nowMsProvider = { nowMs })
        logSink = InMemoryLogSink()
        logger = Logger(prefs = prefs, sink = logSink, scope = scope, nowMsProvider = { nowMs })
    }

    @After fun tearDown() {
        if (::client.isInitialized) client.close()
        scope.cancel()
        db.close()
    }

    @Test fun mixedParseFailurePreservesCursorBeforeNextPage() = runBlocking {
        runMixedParseFailureCase(endOfStream = false, nextMarker = "opaque-next")
    }

    @Test fun mixedParseFailurePreservesCursorAtEndOfStream() = runBlocking {
        runMixedParseFailureCase(endOfStream = true, nextMarker = "opaque-final")
    }

    private suspend fun runMixedParseFailureCase(endOfStream: Boolean, nextMarker: String) {
        val reconciler = buildReconciler(
            MockEngine { request ->
                when (request.url.encodedPath) {
                    "/api/feed/delta" -> respondJson(
                        DeltaResponse(
                            bundles = listOf(
                                BundleEnvelope(
                                    primary_kind = "feed_items",
                                    primary = buildJsonObject {
                                        put("tweet_id", "good_tweet")
                                        put("author_handle", "alice")
                                        put("published_at", 1L)
                                    },
                                ),
                                BundleEnvelope(
                                    primary_kind = "feed_items",
                                    primary = buildJsonObject {
                                        put("tweet_id", "bad_tweet")
                                        put("published_at", 2L)
                                    },
                                ),
                            ),
                            next_marker = nextMarker,
                            end_of_stream = endOfStream,
                        ),
                    )
                    else -> error("unexpected path ${request.url.encodedPath}")
                }
            },
        )
        val job = scope.launch { reconciler.run() }
        try {
            reconciler.triggerStream(SyncStream.Feed)
            waitForLog("stream_parse_failed")
            waitForLog("inbound_pass_done")
        } finally {
            job.cancelAndJoin()
        }

        assertNotNull(db.feedItemDao().getById("good_tweet"))
        assertNull(db.feedItemDao().getById("bad_tweet"))
        assertNull(db.cursorDao().get(SyncStream.Feed.cursorKey))
        assertTrue(logSink.snapshot().any { it.event == "stream_parse_failed" })
    }

    private fun buildReconciler(engine: MockEngine): InboundReconciler {
        client = HttpClient(engine) {
            install(ContentNegotiation) {
                json(iglooJson)
            }
        }
        val baseUrlProvider = { "https://igloo.local" }
        val reachability = Reachability(
            scope = scope,
            probe = { true },
            foregroundFlow = emptyFlow(),
        ).also { it.markOnline() }
        return InboundReconciler(
            db = db,
            prefs = prefs,
            cursorDao = db.cursorDao(),
            outboxDao = db.outboxDao(),
            feedApi = FeedApi(client, baseUrlProvider),
            videoApi = VideoApi(client, baseUrlProvider),
            shortsApi = ShortsApi(client, baseUrlProvider),
            channelsApi = ChannelsApi(client, baseUrlProvider),
            reachability = reachability,
            logger = logger,
            nowMsProvider = { nowMs },
        )
    }

    private suspend fun waitForLog(event: String) {
        withTimeout(3_000L) {
            while (true) {
                if (logSink.snapshot().any { it.event == event }) return@withTimeout
                delay(10L)
            }
        }
    }

    private inline fun <reified T> MockRequestHandleScope.respondJson(body: T) =
        respond(iglooJson.encodeToString(body), HttpStatusCode.OK, jsonHeaders())

    private fun jsonHeaders() = headersOf("Content-Type", ContentType.Application.Json.toString())
}
