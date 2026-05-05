package com.screwy.igloo.net

import com.screwy.igloo.net.auth.AuthTokenProvider
import com.screwy.igloo.net.interceptors.AuthInterceptor
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.MockRequestHandleScope
import io.ktor.client.engine.mock.respond
import io.ktor.client.request.HttpRequestData
import io.ktor.client.request.HttpResponseData
import io.ktor.client.request.get
import io.ktor.client.statement.bodyAsText
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import io.ktor.utils.io.ByteReadChannel
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.Deferred
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.withContext
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * AuthInterceptor 401 handshake — refresh-eligible path retries with the new bearer,
 * terminal codes propagate the 401, and the refresh mutex collapses concurrent 401s
 * into a single refresh.
 */
class AuthInterceptor401Test {

    private val iglooHost = "igloo.example.com"

    // ─── Refresh path ─────────────────────────────────────────────────────────

    @Test fun expired_refreshesAndRetries() = runBlocking {
        val tokens = FakeTokenProvider(initial = "acc-old")
        tokens.refreshTo("acc-new")
        val calls = AtomicInteger(0)
        val seenAuth = mutableListOf<String?>()
        val engine = MockEngine { request ->
            val n = calls.incrementAndGet()
            seenAuth += request.headers[HttpHeaders.Authorization]
            if (n == 1) respondEnvelope401("access_token_expired")
            else respondOk("""{"ok":true,"n":$n}""")
        }
        val client = buildClient(engine, tokens)

        val response = client.get("https://$iglooHost:8443/api/feed")

        assertEquals(HttpStatusCode.OK, response.status)
        assertEquals(2, calls.get())
        assertEquals("Bearer acc-old", seenAuth[0])
        assertEquals("Bearer acc-new", seenAuth[1])
        assertEquals(1, tokens.refreshCallCount)
        client.close()
    }

    // ─── Terminal code path ───────────────────────────────────────────────────

    @Test fun sessionRevoked_propagates401WithoutRetry() = runBlocking {
        val tokens = FakeTokenProvider(initial = "acc")
        tokens.rejectTerminal("session_revoked")
        val calls = AtomicInteger(0)
        val engine = MockEngine { _ ->
            calls.incrementAndGet()
            respondEnvelope401("session_revoked")
        }
        val client = buildClient(engine, tokens)

        val response = client.get("https://$iglooHost:8443/api/feed")

        assertEquals(HttpStatusCode.Unauthorized, response.status)
        assertEquals(1, calls.get())
        assertEquals(1, tokens.onAuthExpiredCalls.size)
        assertEquals("session_revoked", tokens.onAuthExpiredCalls[0])
        // body of the original 401 is still readable by the caller
        assertTrue(response.bodyAsText().contains("session_revoked"))
        client.close()
    }

    // ─── Non-igloo host (CDN) ───────────────────────────────────────────────

    @Test fun cdnHost_401_notRefreshed() = runBlocking {
        val tokens = FakeTokenProvider(initial = "acc")
        val engine = MockEngine { _ ->
            respondEnvelope401("access_token_expired")
        }
        val client = buildClient(engine, tokens)

        val response = client.get("https://pbs.twimg.com/media/xyz.jpg")

        assertEquals(HttpStatusCode.Unauthorized, response.status)
        assertTrue(tokens.onAuthExpiredCalls.isEmpty())
        client.close()
    }

    // ─── Mutex serialization ──────────────────────────────────────────────────

    @Test fun concurrent401s_collapseToOneRefresh() = runBlocking {
        val tokens = FakeTokenProvider(initial = "acc-old")
        tokens.refreshTo("acc-new")
        val calls = AtomicInteger(0)
        // First two requests return 401; everything else returns 200.
        val engine = MockEngine { request ->
            val n = calls.incrementAndGet()
            val header = request.headers[HttpHeaders.Authorization]
            if (header == "Bearer acc-old") respondEnvelope401("access_token_expired")
            else respondOk("""{"ok":true,"n":$n}""")
        }
        val client = buildClient(engine, tokens)

        coroutineScope {
            val a: Deferred<String> = async {
                client.get("https://$iglooHost/api/a").bodyAsText()
            }
            val b: Deferred<String> = async {
                client.get("https://$iglooHost/api/b").bodyAsText()
            }
            assertTrue(a.await().contains("\"ok\":true"))
            assertTrue(b.await().contains("\"ok\":true"))
        }

        // Both callers should have seen the same refresh fire exactly once.
        assertEquals("expected a single refresh despite two concurrent 401s",
            1, tokens.refreshCallCount)
        client.close()
    }

    // ─── helpers ──────────────────────────────────────────────────────────────

    private fun buildClient(engine: MockEngine, tokens: AuthTokenProvider): HttpClient =
        HttpClient(engine) {
            install(AuthInterceptor) {
                this.tokenProvider = tokens
                this.hostResolver = { iglooHost }
            }
            expectSuccess = false
        }

    private fun MockRequestHandleScope.respondEnvelope401(errorCode: String): HttpResponseData =
        respond(
            content = ByteReadChannel("""{"ok":false,"error_code":"$errorCode","error_message":"x"}"""),
            status = HttpStatusCode.Unauthorized,
            headers = headersOf("Content-Type", "application/json"),
        )

    private fun MockRequestHandleScope.respondOk(body: String): HttpResponseData =
        respond(
            content = ByteReadChannel(body),
            status = HttpStatusCode.OK,
            headers = headersOf("Content-Type", "application/json"),
        )

    /**
     * Minimal `AuthTokenProvider` for interceptor tests — tracks refresh-vs-terminal
     * behavior and serialises refresh calls behind its own latch so a slow refresh
     * simulates a real network round-trip.
     */
    private class FakeTokenProvider(initial: String?) : AuthTokenProvider {
        @Volatile private var current: String? = initial
        private var nextRefreshedToken: String? = null
        private var terminalCode: String? = null
        val onAuthExpiredCalls = mutableListOf<String?>()
        @Volatile var refreshCallCount = 0
        private val refreshSerialiser = Mutex()

        fun refreshTo(newToken: String) { nextRefreshedToken = newToken }
        fun rejectTerminal(errorCode: String) { terminalCode = errorCode }

        override fun bearerTokenSync(): String? = current

        override suspend fun onAuthExpired(errorCode: String?): String? {
            onAuthExpiredCalls += errorCode
            if (terminalCode != null && errorCode == terminalCode) return null
            return refreshSerialiser.lock().let {
                try {
                    // Tiny delay so the mutex test can interleave a second caller.
                    withContext(kotlinx.coroutines.Dispatchers.Default) { delay(30) }
                    refreshCallCount += 1
                    val next = nextRefreshedToken ?: return@let null
                    current = next
                    next
                } finally {
                    refreshSerialiser.unlock()
                }
            }
        }
    }
}
