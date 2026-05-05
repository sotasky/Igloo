package com.screwy.igloo.net

import com.screwy.igloo.net.auth.NoAuthTokenProvider
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.client.request.get
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import io.ktor.utils.io.ByteReadChannel
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Test
import java.io.IOException
import java.util.concurrent.atomic.AtomicInteger

class HttpClientReachabilityHooksTest {

    @Test fun transportFailure_downgradesReachability() = runBlocking {
        val downgradeCalls = AtomicInteger(0)
        val client = buildIglooClient(
            engine = MockEngine { throw IOException("failed to connect") },
            prefsProvider = { null },
            tokenProvider = NoAuthTokenProvider,
            hostProvider = IglooHostProvider { "stub.local" },
            onTransportFailure = { downgradeCalls.incrementAndGet() },
        )

        runCatching { client.get("https://stub.local/api/health") }
        client.close()

        assertEquals(1, downgradeCalls.get())
    }

    @Test fun successfulIglooResponse_marksReachabilityOnline() = runBlocking {
        val onlineCalls = AtomicInteger(0)
        val client = buildIglooClient(
            engine = MockEngine {
                respond(
                    content = ByteReadChannel("""{"ok":true,"server_time_ms":12345}"""),
                    status = HttpStatusCode.OK,
                    headers = headersOf("Content-Type", "application/json"),
                )
            },
            prefsProvider = { null },
            tokenProvider = NoAuthTokenProvider,
            hostProvider = IglooHostProvider { "stub.local" },
            onReachable = { onlineCalls.incrementAndGet() },
        )

        client.get("https://stub.local/api/health")
        client.close()

        assertEquals(1, onlineCalls.get())
    }
}
