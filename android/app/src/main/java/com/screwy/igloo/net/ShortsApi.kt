package com.screwy.igloo.net

import io.ktor.client.HttpClient
import io.ktor.client.call.body
import io.ktor.client.plugins.timeout
import io.ktor.client.request.get
import io.ktor.client.request.parameter

/** `GET /api/shorts/delta` — TikTok + Instagram videos (02-sync.md §3). */
class ShortsApi(
    private val client: HttpClient,
    private val baseUrlProvider: () -> String,
) {
    suspend fun shortsDelta(since: String? = null): DeltaResponse =
        client.get(baseUrlProvider() + "/api/shorts/delta") {
            timeout {
                requestTimeoutMillis = NetDefaults.DELTA_REQUEST_TIMEOUT.inWholeMilliseconds
                connectTimeoutMillis = NetDefaults.DELTA_CONNECT_TIMEOUT.inWholeMilliseconds
                socketTimeoutMillis = NetDefaults.DELTA_SOCKET_TIMEOUT.inWholeMilliseconds
            }
            if (!since.isNullOrEmpty()) parameter("since", since)
        }.body()
}
