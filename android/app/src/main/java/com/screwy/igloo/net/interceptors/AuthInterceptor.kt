package com.screwy.igloo.net.interceptors

import com.screwy.igloo.net.auth.AuthTokenProvider
import io.ktor.client.call.HttpClientCall
import io.ktor.client.call.save
import io.ktor.client.plugins.api.Send
import io.ktor.client.plugins.api.createClientPlugin
import io.ktor.client.request.HttpRequestBuilder
import io.ktor.client.request.headers
import io.ktor.client.request.takeFrom
import io.ktor.client.statement.bodyAsText
import io.ktor.http.HttpHeaders
import io.ktor.util.AttributeKey
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonPrimitive

/**
 * Host-gated Bearer header for Igloo-server requests + 401 refresh handshake.
 *
 * Request side: only requests to the Igloo server get the Bearer token. Image
 * loads from CDNs (twimg.com, yt3.ggpht) don't leak the token.
 * Cleartext requests only get a Bearer token for local development hosts; LAN
 * and public Igloo origins must use HTTPS before credentials are attached.
 *
 * Response side: on a 401 from the Igloo server (other than `/api/auth/login`
 * or `/api/auth/refresh`), the plugin:
 *
 *   1. Saves the call body so we can both peek at the `error_code` envelope field and
 *      still propagate the original 401 intact if the refresh bails.
 *   2. Acquires `refreshMutex` so concurrent 401s produce one refresh, not N. Under the
 *      lock, it checks whether another caller already refreshed (the current bearer
 *      differs from the one we sent) — if so it returns that cached value directly.
 *   3. Calls `tokenProvider.onAuthExpired(errorCode)` for the refresh-or-terminal
 *      decision. `null` return = terminal (AuthRepo fired `logout` + `RequireLogin`);
 *      we propagate the original 401.
 *   4. On success, retries the original request exactly once with the new bearer. The
 *      retried request carries the `authRetriedAttr` attribute so a second 401 stops
 *      the cycle (no infinite loop on borked tokens).
 */
class AuthInterceptorConfig {
    lateinit var tokenProvider: AuthTokenProvider

    /**
     * Resolves the lowercase host of the Igloo server. Returns empty string when the
     * server URL is unknown (pre-login, or malformed pref).
     */
    lateinit var hostResolver: () -> String

    /** Shared across all concurrent 401-callers. Swappable for tests. */
    var refreshMutex: Mutex = Mutex()
}

/**
 * Request-attribute flag. Present on a retry request so a repeat 401 propagates to the
 * caller instead of triggering another refresh attempt.
 */
private val authRetriedAttr = AttributeKey<Boolean>("IglooAuthRetried")

private val envelopeJson = Json { ignoreUnknownKeys = true; isLenient = true }

private val SkipAuthPaths = setOf("/api/auth/login", "/api/auth/refresh")

val AuthInterceptor = createClientPlugin("IglooAuthInterceptor", ::AuthInterceptorConfig) {
    val tokens = pluginConfig.tokenProvider
    val resolveHost = pluginConfig.hostResolver
    val refreshMutex = pluginConfig.refreshMutex

    onRequest { request, _ ->
        val requestHost = request.url.host.lowercase()
        val iglooHost = resolveHost()
        if (iglooHost.isEmpty() || requestHost != iglooHost) return@onRequest
        if (!bearerAllowedForScheme(request.url.protocol.name, requestHost)) return@onRequest
        val token = tokens.bearerTokenSync() ?: return@onRequest
        request.headers {
            if (!contains(HttpHeaders.Authorization)) {
                append(HttpHeaders.Authorization, "Bearer $token")
            }
        }
    }

    on(Send) { request ->
        val call: HttpClientCall = proceed(request)
        if (call.response.status.value != 401) return@on call

        val requestHost = call.request.url.host.lowercase()
        val iglooHost = resolveHost()
        if (iglooHost.isEmpty() || requestHost != iglooHost) return@on call
        if (!bearerAllowedForScheme(call.request.url.protocol.name, requestHost)) return@on call

        val path = call.request.url.encodedPath
        if (path in SkipAuthPaths) return@on call
        if (request.attributes.getOrNull(authRetriedAttr) == true) return@on call

        // Save so we can read the body for error_code AND still return it to the caller
        // if the refresh bails. Ktor consumes receive channels on first read.
        val saved = call.save()
        val errorCode = extractErrorCode(saved)
        val originalAuth = request.headers[HttpHeaders.Authorization]

        val newToken = refreshMutex.withLock {
            val current = tokens.bearerTokenSync()
            if (current != null && originalAuth != "Bearer $current") {
                // Someone else already refreshed under the same lock — use theirs.
                current
            } else {
                tokens.onAuthExpired(errorCode)
            }
        } ?: return@on saved

        val retry = HttpRequestBuilder().apply {
            takeFrom(request)
            attributes.put(authRetriedAttr, true)
            headers.remove(HttpHeaders.Authorization)
            headers.append(HttpHeaders.Authorization, "Bearer $newToken")
        }
        proceed(retry)
    }
}

private fun bearerAllowedForScheme(scheme: String, host: String): Boolean =
    when (scheme.lowercase()) {
        "https" -> true
        "http" -> isLocalCleartextHost(host)
        else -> false
    }

private fun isLocalCleartextHost(host: String): Boolean =
    when (host.trim().trim('[', ']').lowercase()) {
        "localhost", "127.0.0.1", "::1", "10.0.2.2" -> true
        else -> false
    }

private suspend fun extractErrorCode(call: HttpClientCall): String? {
    val text = runCatching { call.response.bodyAsText() }.getOrNull() ?: return null
    if (text.isBlank() || text.firstOrNull() != '{') return null
    val obj = runCatching { envelopeJson.parseToJsonElement(text) as? JsonObject }.getOrNull()
        ?: return null
    return obj["error_code"]?.jsonPrimitive?.contentOrNull
}
