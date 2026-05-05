package com.screwy.igloo.outbox

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.data.entity.FeedLikeEntity
import com.screwy.igloo.data.entity.OutboxEntity
import com.screwy.igloo.log.Logger
import com.screwy.igloo.net.IglooHostProvider
import com.screwy.igloo.net.OutboxApi
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.net.auth.NoAuthTokenProvider
import com.screwy.igloo.net.buildIglooClient
import com.screwy.igloo.ui.UiEffects
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import io.ktor.utils.io.ByteReadChannel
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.flowOf
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

/**
 * OutboxDrain — claim / dispatch / apply-result / TTL. Uses a MockEngine so the HTTP
 * layer is deterministic. Calls `runDrainPass` directly (internal-visible) rather
 * than spinning up the full `run()` coroutine, so each test is a single pass.
 * 03-outbox.md §5–§11.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class OutboxDrainTest {

    private lateinit var db: IglooDatabase
    private lateinit var scope: CoroutineScope
    private lateinit var prefs: PreferencesRepo
    private lateinit var logger: Logger
    private lateinit var uiEffects: UiEffects
    private var nowMs: Long = 10_000_000L  // comfortably past the 24h TTL cutoff

    @Before fun setUp() {
        db = RoomTestSupport.freshDb()
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        prefs = PreferencesRepo(db.preferenceDao(), scope, nowMsProvider = { nowMs })
        logger = Logger(prefs = prefs, sink = com.screwy.igloo.log.InMemoryLogSink(), scope = scope, nowMsProvider = { nowMs })
        uiEffects = UiEffects()
    }

    @After fun tearDown() {
        scope.cancel()
        db.close()
    }

    // ─── Success path ─────────────────────────────────────────────────────────

    @Test fun drain_ack_deletesRow() = runBlocking {
        val drain = buildDrain(MockEngine { okJson("""{"ok":true,"sync_version":1,"sync_stream":"feed","server_time_ms":100}""") })

        db.outboxDao().insert(pendingLike("t1", action = "set", createdAtMs = nowMs - 1000))
        drain.runDrainPass()

        assertEquals(0, db.outboxDao().countByState("pending"))
        assertEquals(0, db.outboxDao().countByState("dead"))
    }

    // ─── Transient retry ──────────────────────────────────────────────────────

    @Test fun drain_5xx_scheduleRetry_bumpsAttemptCount() = runBlocking {
        val drain = buildDrain(MockEngine {
            respond(ByteReadChannel("""{"ok":false}"""), HttpStatusCode.ServiceUnavailable, jsonHeaders())
        })

        db.outboxDao().insert(pendingLike("t1", action = "set", createdAtMs = nowMs - 1000))
        drain.runDrainPass()

        // Claim with a future `nowMs` large enough to pass any backoff.
        val rows = db.outboxDao().claimPending(nowMs = nowMs + 10L * 60_000L)
        val row = rows.single()
        assertEquals("pending", row.state)
        assertEquals(1, row.attemptCount)
        assertTrue("nextAttempt=${row.nextAttemptAtMs} nowMs=$nowMs", row.nextAttemptAtMs > nowMs)
    }

    @Test fun drain_retryingLogDebug_doesNotEnqueueRecursiveDebugRows() = runBlocking {
        prefs.setDebugMode(true)
        val writer = OutboxWriter(
            db = db,
            prefs = prefs,
            scope = scope,
            nowMsProvider = { nowMs },
            writeDebounceMs = 0L,
        )
        logger = Logger(prefs = prefs, sink = writer.logSink, scope = scope, nowMsProvider = { nowMs })

        val drain = buildDrain(MockEngine {
            respond(ByteReadChannel("""{"ok":false}"""), HttpStatusCode.ServiceUnavailable, jsonHeaders())
        })

        db.outboxDao().insert(
            OutboxEntity(
                kind = OutboxKind.CODE_LOG_DEBUG,
                payloadJson = """{"event":"debug_event","timestamp_ms":$nowMs}""",
                state = "pending",
                createdAtMs = nowMs - 1000,
            ),
        )

        drain.runDrainPass()

        val logRows = db.outboxDao().logRowsFlow(limit = 10).first()
        assertEquals(1, logRows.size)
        assertEquals(OutboxKind.CODE_LOG_DEBUG, logRows.single().kind)
        assertEquals("pending", logRows.single().state)
        assertEquals(1, logRows.single().attemptCount)
    }

    // ─── Dead + rollback ──────────────────────────────────────────────────────

    @Test fun drain_4xx_marksDead_andRollbackApplies() = runBlocking {
        val drain = buildDrain(MockEngine {
            respond(ByteReadChannel("""{"ok":false,"error_code":"bad_target"}"""), HttpStatusCode.BadRequest, jsonHeaders())
        })

        db.feedLikeDao().upsert(FeedLikeEntity("t1", likedAt = 100L))
        db.outboxDao().insert(pendingLike("t1", action = "set", createdAtMs = nowMs - 1000))
        drain.runDrainPass()

        assertEquals(0, db.outboxDao().countByState("pending"))
        assertEquals(1, db.outboxDao().countByState("dead"))
        assertFalse(db.feedLikeDao().exists("t1"))
    }

    // ─── Offline skip ─────────────────────────────────────────────────────────

    @Test fun drain_skipsWhenOffline() = runBlocking {
        val drain = buildDrain(
            MockEngine { error("drain shouldn't call HTTP when offline") },
            initialReachability = Reachability.State.Offline,
        )
        db.outboxDao().insert(pendingLike("t1", action = "set", createdAtMs = nowMs - 1000))
        drain.runDrainPass()

        assertEquals(1, db.outboxDao().countByState("pending"))
    }

    @Test fun drain_onlinePass_emitsCompletionSignal() = runBlocking {
        val drain = buildDrain(MockEngine { okJson("""{"ok":true}""") })
        val completion = async { withTimeout(1_000L) { drain.passCompleted.first() } }

        drain.runDrainPass()

        completion.await()
    }

    @Test fun drain_offlineSkip_doesNotEmitCompletionSignal() = runBlocking {
        val drain = buildDrain(
            MockEngine { error("drain shouldn't call HTTP when offline") },
            initialReachability = Reachability.State.Offline,
        )
        val completion = async {
            runCatching { withTimeout(200L) { drain.passCompleted.first() } }.isSuccess
        }

        drain.runDrainPass()

        assertFalse(completion.await())
    }

    @Test fun debugBacklog_isDroppedWhenDebugModeDisabled() = runBlocking {
        prefs.setDebugMode(false)
        val paths = mutableListOf<String>()
        val drain = buildDrain(MockEngine { req ->
            paths += req.url.encodedPath
            okJson("""{"ok":true}""")
        })

        db.outboxDao().insert(
            OutboxEntity(
                kind = OutboxKind.CODE_LOG_DEBUG,
                payloadJson = """{"event":"debug_event","timestamp_ms":$nowMs}""",
                state = "pending",
                createdAtMs = nowMs - 1000,
            ),
        )
        db.outboxDao().insert(pendingLike("t1", action = "set", createdAtMs = nowMs - 900))

        drain.runDrainPass()

        assertEquals(listOf("/api/mutations/like"), paths)
        assertEquals(0, db.outboxDao().countByState("pending"))
        assertEquals(0, db.outboxDao().countByState("acked"))
    }

    // ─── Batched kinds ────────────────────────────────────────────────────────

    @Test fun seen_batchesIntoOneCall() = runBlocking {
        val paths = mutableListOf<String>()
        val drain = buildDrain(MockEngine { req ->
            paths += req.url.encodedPath
            okJson("""{"ok":true}""")
        })

        db.outboxDao().insert(pendingSeen("t1", createdAtMs = nowMs - 1000))
        db.outboxDao().insert(pendingSeen("t2", createdAtMs = nowMs - 900))
        db.outboxDao().insert(pendingSeen("t3", createdAtMs = nowMs - 800))
        drain.runDrainPass()

        assertEquals(1, paths.size)
        assertEquals("/api/mutations/seen", paths.single())
        assertEquals(0, db.outboxDao().countByState("pending"))
    }

    // ─── TTL ──────────────────────────────────────────────────────────────────

    @Test fun ttl_stuckPendingPast24h_flipsToDead_andRollbackFires() = runBlocking {
        val drain = buildDrain(MockEngine { okJson("""{"ok":true}""") })

        // 25h-old pending row — Clause 1 flips it to dead (+ rollback fires in the
        // same tx), then Clause 2 sweeps the just-dead row because it's also past
        // the 24h cutoff. End state: outbox empty, rollback applied. 03-outbox.md §10.
        val oldTs = nowMs - 25L * 60L * 60L * 1000L
        db.feedLikeDao().upsert(FeedLikeEntity("t1", likedAt = oldTs))
        db.outboxDao().insert(pendingLike("t1", action = "set", createdAtMs = oldTs))

        drain.runDrainPass()

        assertEquals(0, db.outboxDao().countByState("pending"))
        assertEquals(0, db.outboxDao().countByState("dead"))
        assertFalse("feed_likes row should have been rolled back", db.feedLikeDao().exists("t1"))
    }

    @Test fun ttl_deadPast24h_deleted() = runBlocking {
        val drain = buildDrain(MockEngine { okJson("""{"ok":true}""") })

        val oldTs = nowMs - 25L * 60L * 60L * 1000L
        db.outboxDao().insert(pendingLike("t1", action = "set", createdAtMs = oldTs).copy(state = "dead"))
        drain.runDrainPass()

        assertEquals(0, db.outboxDao().countByState("dead"))
    }

    // ─── Wiring ────────────────────────────────────────────────────────────────

    private fun buildDrain(
        engine: MockEngine,
        initialReachability: Reachability.State = Reachability.State.Online,
    ): OutboxDrain {
        val hostProvider = IglooHostProvider { "igloo.example" }
        val client: HttpClient = buildIglooClient(
            engine = engine,
            prefsProvider = { prefs },
            tokenProvider = NoAuthTokenProvider,
            hostProvider = hostProvider,
            nowMsProvider = { nowMs },
        )
        val api = OutboxApi(client = client) { "http://igloo.example" }
        val dispatcher = OutboxDispatcher(
            api = api,
            db = db,
            authTokens = NoAuthTokenProvider,
            logger = logger,
            uiEffects = uiEffects,
            nowMsProvider = { nowMs },
        )
        val reachability = Reachability(
            scope = scope,
            probe = { true },
            foregroundFlow = flowOf(false),
        )
        when (initialReachability) {
            is Reachability.State.Online -> reachability.markOnline()
            else -> reachability.downgrade()
        }
        return OutboxDrain(
            outboxDao = db.outboxDao(),
            dispatcher = dispatcher,
            db = db,
            prefs = prefs,
            reachability = reachability,
            logger = logger,
            nowMsProvider = { nowMs },
        )
    }

    // ─── Helpers ───────────────────────────────────────────────────────────────

    private fun pendingLike(tweetId: String, action: String, createdAtMs: Long): OutboxEntity =
        OutboxEntity(
            kind = OutboxKind.CODE_LIKE,
            itemId = tweetId,
            payloadJson = """{"tweet_id":"$tweetId","action":"$action","updated_at_ms":$createdAtMs}""",
            state = "pending",
            createdAtMs = createdAtMs,
        )

    private fun pendingSeen(tweetId: String, createdAtMs: Long): OutboxEntity =
        OutboxEntity(
            kind = OutboxKind.CODE_SEEN,
            itemId = tweetId,
            payloadJson = """{"tweet_id":"$tweetId"}""",
            state = "pending",
            createdAtMs = createdAtMs,
        )

    private fun io.ktor.client.engine.mock.MockRequestHandleScope.okJson(body: String) =
        respond(ByteReadChannel(body), HttpStatusCode.OK, jsonHeaders())

    private fun jsonHeaders() = headersOf("Content-Type", "application/json")
}
