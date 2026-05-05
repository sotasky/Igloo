package com.screwy.igloo.net

import com.screwy.igloo.net.auth.NoAuthTokenProvider
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.client.request.get
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import io.ktor.utils.io.ByteReadChannel
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test

class HttpClientUserAgentTest {

    @Test
    fun publicOrigin_usesSharedBrowserUserAgent() = runBlocking {
        val seenAgents = mutableListOf<String?>()
        val client = buildIglooClient(
            engine = MockEngine { request ->
                seenAgents += request.headers[HttpHeaders.UserAgent]
                respond(
                    content = ByteReadChannel("{}"),
                    status = HttpStatusCode.OK,
                    headers = headersOf("Content-Type", "application/json"),
                )
            },
            prefsProvider = { null },
            tokenProvider = NoAuthTokenProvider,
            hostProvider = IglooHostProvider { "igloo.example.com" },
        )

        client.get("https://video.twimg.com/ext_tw_video/clip.mp4")
        client.close()

        assertEquals(NetDefaults.PUBLIC_BROWSER_USER_AGENT, seenAgents.single())
        assertFalse(seenAgents.single().orEmpty().contains("Igloo"))
    }

    @Test
    fun iglooOrigin_usesSharedBrowserUserAgent() = runBlocking {
        val seenAgents = mutableListOf<String?>()
        val client = buildIglooClient(
            engine = MockEngine { request ->
                seenAgents += request.headers[HttpHeaders.UserAgent]
                respond(
                    content = ByteReadChannel("""{"ok":true}"""),
                    status = HttpStatusCode.OK,
                    headers = headersOf("Content-Type", "application/json"),
                )
            },
            prefsProvider = { null },
            tokenProvider = NoAuthTokenProvider,
            hostProvider = IglooHostProvider { "igloo.example.com" },
        )

        client.get("https://igloo.example.com/api/health")
        client.close()

        assertEquals(NetDefaults.PUBLIC_BROWSER_USER_AGENT, seenAgents.single())
        assertFalse(seenAgents.single().orEmpty().contains("Igloo"))
    }
}
