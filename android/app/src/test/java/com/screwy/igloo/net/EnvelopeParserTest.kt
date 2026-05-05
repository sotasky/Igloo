package com.screwy.igloo.net

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.RoomTestSupport
import com.screwy.igloo.net.interceptors.EnvelopeParser
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.client.request.get
import io.ktor.client.statement.bodyAsText
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import io.ktor.utils.io.ByteReadChannel
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.concurrent.atomic.AtomicInteger

/**
 * EnvelopeParser MUST extract `server_time_ms` into PreferencesRepo's offset pref
 * without mutating stream cursors. Inbound loops own `next_marker`; mutation
 * ACK `sync_version` values live in a different version space and must not overwrite
 * stream replay state. Body passes through unchanged.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], manifest = Config.NONE)
class EnvelopeParserTest {

    private lateinit var db: IglooDatabase
    private lateinit var scope: CoroutineScope
    private lateinit var prefs: PreferencesRepo

    @Before fun setUp() {
        db = RoomTestSupport.freshDb()
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        prefs = PreferencesRepo(db.preferenceDao(), scope, nowMsProvider = { 10_000L })
    }

    @After fun tearDown() {
        scope.cancel()
        db.close()
    }

    private fun buildClient(responseBody: String, status: HttpStatusCode = HttpStatusCode.OK): HttpClient {
        val engine = MockEngine { _ ->
            respond(
                content = ByteReadChannel(responseBody),
                status = status,
                headers = headersOf("Content-Type", "application/json"),
            )
        }
        return HttpClient(engine) {
            EnvelopeParser.install(this, { prefs }, nowMsProvider = { 10_000L })
            expectSuccess = false
        }
    }

    private suspend fun waitFor(timeoutMs: Long = 5_000L, predicate: suspend () -> Boolean) {
        withTimeout(timeoutMs) { while (!predicate()) delay(20) }
    }

    @Test fun serverTimeMs_writesOffset() = runBlocking {
        val body = """{"ok":true,"server_time_ms":12345}"""
        val client = buildClient(body)
        val response = client.get("/api/health")
        val bodyText = response.bodyAsText()
        client.close()

        // Body passed through unchanged.
        assertEquals(body, bodyText)

        // Offset = server_time_ms - deviceNow = 12345 - 10000 = 2345.
        waitFor { prefs.serverTimeOffsetMs().first() == 2345L }
        assertEquals(2345L, prefs.serverTimeOffsetMs().first())
    }

    @Test fun syncVersionAndStream_doesNotTouchCursor() = runBlocking {
        val body = """{"ok":true,"server_time_ms":10000,"sync_version":789,"sync_stream":"feed"}"""
        val client = buildClient(body)
        client.get("/api/feed/delta")
        client.close()

        val cursor = db.cursorDao().get("feed")
        assertEquals(null, cursor)
    }

    @Test fun syncStreamOmitted_noCursorUpsert() = runBlocking {
        val body = """{"ok":true,"server_time_ms":10000,"sync_version":789}"""
        val client = buildClient(body)
        client.get("/api/health")
        client.close()

        // Sanity: sync_version alone without stream doesn't write anything.
        delay(200)
        val allStreams = db.cursorDao().allFlow().first()
        assertEquals(emptyList<Any>(), allStreams.map { it.stream })
    }

    @Test fun nonJsonContentType_skipped() = runBlocking {
        val onlineMarks = AtomicInteger(0)
        val engine = MockEngine { _ ->
            respond(
                content = ByteReadChannel(byteArrayOf(0xFF.toByte(), 0xD8.toByte())),
                status = HttpStatusCode.OK,
                headers = headersOf("Content-Type", "image/jpeg"),
            )
        }
        val client = HttpClient(engine) {
            EnvelopeParser.install(
                config = this,
                prefsProvider = { prefs },
                nowMsProvider = { 0L },
                hostResolver = { "example.com" },
                onReachable = { onlineMarks.incrementAndGet() },
            )
            expectSuccess = false
        }
        client.get("https://example.com/api/media/thumbnail/abc")
        client.close()
        delay(200)
        // Offset should remain at its default (0).
        assertEquals(0L, prefs.serverTimeOffsetMs().first())
        assertEquals(1, onlineMarks.get())
    }

    @Test fun nonSuccessStatus_skipped() = runBlocking {
        val body = """{"ok":false,"error_code":"invalid","server_time_ms":12345}"""
        val client = buildClient(body, status = HttpStatusCode.BadRequest)
        client.get("/anything")
        client.close()
        delay(200)
        // Parser bails on non-2xx — error paths read envelope via IglooError.classify().
        assertEquals(0L, prefs.serverTimeOffsetMs().first())
    }
}
