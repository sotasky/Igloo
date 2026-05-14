package com.screwy.igloo.net

import com.screwy.igloo.net.auth.AuthTokenProvider
import com.screwy.igloo.net.interceptors.AuthInterceptor
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.client.request.get
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import io.ktor.utils.io.ByteReadChannel
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * AuthInterceptor MUST add the Bearer header only for requests targeting the Igloo
 * server (host matches the `hostResolver` lambda). CDN requests (twimg, yt3.ggpht)
 * don't get the token leaked.
 */
class AuthInterceptorTest {

    private fun buildClient(
        token: String?,
        iglooHost: String = "igloo.example.com",
    ): Pair<HttpClient, MutableList<String?>> {
        val seenAuth = mutableListOf<String?>()
        val tokenProvider = object : AuthTokenProvider {
            override fun bearerTokenSync(): String? = token
        }
        val engine = MockEngine { request ->
            seenAuth += request.headers[HttpHeaders.Authorization]
            respond(
                content = ByteReadChannel("{}"),
                status = HttpStatusCode.OK,
                headers = io.ktor.http.headersOf("Content-Type", "application/json"),
            )
        }
        val client = HttpClient(engine) {
            install(AuthInterceptor) {
                this.tokenProvider = tokenProvider
                this.hostResolver = { iglooHost }
            }
            expectSuccess = false
        }
        return client to seenAuth
    }

    @Test fun iglooHost_getsBearerHeader() = runBlocking {
        val (client, seen) = buildClient(token = "abc123")
        client.get("https://igloo.example.com:8443/api/health")
        client.close()
        assertEquals("Bearer abc123", seen.single())
    }

    @Test fun otherHost_doesNotGetBearerHeader() = runBlocking {
        val (client, seen) = buildClient(token = "abc123")
        client.get("https://pbs.twimg.com/media/xyz.jpg")
        client.close()
        assertNull(seen.single())
    }

    @Test fun nullToken_skipsHeader() = runBlocking {
        val (client, seen) = buildClient(token = null)
        client.get("https://igloo.example.com:8443/api/health")
        client.close()
        assertNull(seen.single())
    }

    @Test fun emptyHostResolver_skipsHeader() = runBlocking {
        val (client, seen) = buildClient(token = "abc", iglooHost = "")
        client.get("https://igloo.example.com:8443/api/health")
        client.close()
        assertNull(seen.single())
    }

    @Test fun preExistingAuthorizationHeader_preserved() = runBlocking {
        val (client, seen) = buildClient(token = "should-not-apply")
        client.get("https://igloo.example.com:8443/api/health") {
            headers.append(HttpHeaders.Authorization, "Bearer custom")
        }
        client.close()
        assertEquals("Bearer custom", seen.single())
    }

    @Test fun hostMatchIsCaseInsensitive() = runBlocking {
        val (client, seen) = buildClient(token = "tok", iglooHost = "igloo.example.com")
        client.get("https://IGLOO.Example.COM:8443/api/health")
        client.close()
        assertEquals("Bearer tok", seen.single())
    }

    @Test fun remoteCleartextIglooHost_doesNotGetBearerHeader() = runBlocking {
        val (client, seen) = buildClient(token = "tok", iglooHost = "igloo.example.com")
        client.get("http://igloo.example.com:5001/api/health")
        client.close()
        assertNull(seen.single())
    }

    @Test fun localCleartextIglooHost_getsBearerHeader() = runBlocking {
        val (client, seen) = buildClient(token = "tok", iglooHost = "127.0.0.1")
        client.get("http://127.0.0.1:5001/api/health")
        client.close()
        assertEquals("Bearer tok", seen.single())
    }
}
