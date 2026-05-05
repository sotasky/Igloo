package com.screwy.igloo.sync

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.data.entity.BookmarkCategoryEntity
import com.screwy.igloo.data.entity.BookmarkEntity
import com.screwy.igloo.data.entity.FeedLikeEntity
import com.screwy.igloo.data.entity.OutboxEntity
import com.screwy.igloo.log.InMemoryLogSink
import com.screwy.igloo.log.Logger
import com.screwy.igloo.net.MutationDeltaApi
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.net.iglooJson
import com.screwy.igloo.outbox.OutboxKind
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import io.ktor.serialization.kotlinx.json.json
import io.ktor.utils.io.ByteReadChannel
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.emptyFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
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
class MutationDeltaSyncTest {

    private lateinit var db: IglooDatabase
    private lateinit var scope: CoroutineScope
    private lateinit var prefs: PreferencesRepo
    private lateinit var logger: Logger
    private var nowMs: Long = 10_000L

    @Before fun setUp() {
        db = RoomTestSupport.freshDb()
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        prefs = PreferencesRepo(db.preferenceDao(), scope, nowMsProvider = { nowMs })
        logger = Logger(prefs = prefs, sink = InMemoryLogSink(), scope = scope, nowMsProvider = { nowMs })
    }

    @After fun tearDown() {
        scope.cancel()
        db.close()
    }

    @Test fun sync_appliesUserStateCursorAndProgress() = runBlocking {
        val sync = buildSync(MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/mutations/delta" -> respondJson(
                    """
                    {
                      "version":4,
                      "truncated":false,
                      "changes":[
                        {"version":1,"type":"like","item_id":"tw1","value":{"action":"set","updated_at_ms":100}},
                        {"version":2,"type":"seen","item_id":"tw2","value":{"tweet_ids":["tw2","tw3"],"updated_at_ms":200}},
                        {"version":3,"type":"moments_cursor","item_id":"short1","value":{"position_ms":321,"updated_at_ms":300}},
                        {"version":4,"type":"progress","item_id":"yt1","value":{"position":42.5,"duration":120.0,"updated_at_ms":400,"source":"web"}}
                      ]
                    }
                    """.trimIndent(),
                )
                else -> error("unexpected path ${request.url.encodedPath}")
            }
        })

        val result = sync.sync()

        assertEquals(4, result.applied)
        assertTrue(db.feedLikeDao().exists("tw1"))
        assertTrue(db.feedSeenDao().exists("tw2"))
        assertTrue(db.feedSeenDao().exists("tw3"))
        assertEquals("short1", prefs.momentsResumeVideoId(scope = "all").first())
        assertEquals(321L, prefs.momentsResumePositionMs(scope = "all").first())
        assertEquals(42.5, db.watchHistoryDao().getById("yt1")!!.playbackPosition, 0.001)
        assertEquals("4", db.cursorDao().get(MutationDeltaSync.CURSOR_KEY)?.cursor)
    }

    @Test fun sync_preservesPendingLocalMutation() = runBlocking {
        db.feedLikeDao().upsert(FeedLikeEntity("tw1", likedAt = 1L))
        db.outboxDao().insert(
            OutboxEntity(
                kind = OutboxKind.CODE_LIKE,
                itemId = "tw1",
                payloadJson = "{}",
                createdAtMs = 1L,
            ),
        )
        val sync = buildSync(singleDeltaEngine("""{"version":1,"changes":[{"version":1,"type":"like","item_id":"tw1","value":{"action":"clear"}}]}"""))

        val result = sync.sync()

        assertEquals(0, result.applied)
        assertTrue(db.feedLikeDao().exists("tw1"))
        assertEquals("1", db.cursorDao().get(MutationDeltaSync.CURSOR_KEY)?.cursor)
    }

    @Test fun sync_drainsTruncatedPages() = runBlocking {
        val sync = buildSync(MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/mutations/delta" -> {
                    val since = request.url.parameters["since"]
                    if (since == null) {
                        respondJson("""{"version":2,"truncated":true,"changes":[{"version":1,"type":"seen","item_id":"a","value":{}}]}""")
                    } else {
                        respondJson("""{"version":2,"truncated":false,"changes":[{"version":2,"type":"seen","item_id":"b","value":{}}]}""")
                    }
                }
                else -> error("unexpected path ${request.url.encodedPath}")
            }
        })

        val result = sync.sync()

        assertEquals(2, result.applied)
        assertTrue(db.feedSeenDao().exists("a"))
        assertTrue(db.feedSeenDao().exists("b"))
        assertEquals("2", db.cursorDao().get(MutationDeltaSync.CURSOR_KEY)?.cursor)
    }

    @Test fun sync_appliesBookmarkCategoryUpsertAndDelete() = runBlocking {
        db.bookmarkCategoryDao().upsert(BookmarkCategoryEntity(categoryId = 4L, name = "old", createdAt = 1L))
        db.bookmarkDao().upsert(BookmarkEntity(videoId = "bookmarked", categoryId = 4L, bookmarkedAt = 2L))
        val sync = buildSync(singleDeltaEngine(
            """
            {
              "version":3,
              "truncated":false,
              "changes":[
                {"version":1,"type":"bookmark_category","item_id":"4","value":{"action":"set","category_id":4,"name":"renamed","archive_path":"/archive","created_at":50,"updated_at_ms":60}},
                {"version":2,"type":"bookmark_category","item_id":"6","value":{"action":"set","category_id":6,"name":"new","archive_path":"","created_at":70,"updated_at_ms":80}},
                {"version":3,"type":"bookmark_category","item_id":"4","value":{"action":"clear","category_id":4,"updated_at_ms":90}}
              ]
            }
            """.trimIndent(),
        ))

        val result = sync.sync()

        assertEquals(3, result.applied)
        assertEquals(0L, db.bookmarkDao().getById("bookmarked")?.categoryId)
        assertNull(db.bookmarkCategoryDao().getById(4L))
        val newCategory = db.bookmarkCategoryDao().getById(6L)
        assertNotNull(newCategory)
        assertEquals("new", newCategory!!.name)
        assertEquals(70L, newCategory.createdAt)
    }

    @Test fun sync_appliesExplicitEmptyBookmarkCustomTitle() = runBlocking {
        db.bookmarkDao().upsert(
            BookmarkEntity(
                videoId = "saved",
                categoryId = 7L,
                customTitle = "old label",
                accountHandles = "alice",
                mediaIndices = "0",
                bookmarkedAt = 1L,
            ),
        )
        val sync = buildSync(singleDeltaEngine(
            """
            {
              "version":1,
              "truncated":false,
              "changes":[
                {"version":1,"type":"bookmark","item_id":"saved","value":{"bookmarked":true,"category_id":7,"custom_title":"","account_handles":"alice","media_indices":"0","bookmarked_at":2,"updated_at_ms":3}}
              ]
            }
            """.trimIndent(),
        ))

        val result = sync.sync()

        assertEquals(1, result.applied)
        assertEquals("", db.bookmarkDao().getById("saved")?.customTitle)
    }

    @Test fun sync_failsWhenTruncatedMarkerStalls() = runBlocking {
        val sync = buildSync(singleDeltaEngine("""{"version":1,"truncated":true,"changes":[]}"""))

        val failure = runCatching { sync.sync() }.exceptionOrNull()

        assertNotNull(failure)
        assertTrue(failure is IllegalStateException)
    }

    private fun buildSync(engine: MockEngine): MutationDeltaSync {
        val client = HttpClient(engine) {
            install(ContentNegotiation) {
                json(iglooJson)
            }
        }
        val reachability = Reachability(
            scope = scope,
            probe = { true },
            foregroundFlow = emptyFlow(),
        ).also { it.markOnline() }
        return MutationDeltaSync(
            db = db,
            prefs = prefs,
            cursorDao = db.cursorDao(),
            outboxDao = db.outboxDao(),
            api = MutationDeltaApi(client = client, baseUrlProvider = { "https://igloo.local" }),
            reachability = reachability,
            logger = logger,
            nowMsProvider = { nowMs },
        )
    }

    private fun singleDeltaEngine(body: String): MockEngine =
        MockEngine { request ->
            when (request.url.encodedPath) {
                "/api/mutations/delta" -> respondJson(body)
                else -> error("unexpected path ${request.url.encodedPath}")
            }
        }

    private fun io.ktor.client.engine.mock.MockRequestHandleScope.respondJson(body: String) = respond(
        content = ByteReadChannel(body),
        status = HttpStatusCode.OK,
        headers = headersOf("Content-Type", "application/json"),
    )
}
