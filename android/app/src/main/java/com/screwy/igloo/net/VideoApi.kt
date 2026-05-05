package com.screwy.igloo.net

import io.ktor.client.HttpClient
import io.ktor.client.call.body
import io.ktor.client.plugins.timeout
import io.ktor.client.request.get
import io.ktor.client.request.parameter

/** `GET /api/videos/delta` — YouTube bundle stream (02-sync.md §3 + #7 inline attachments). */
class VideoApi(
    private val client: HttpClient,
    private val baseUrlProvider: () -> String,
) {
    suspend fun videosDelta(since: String? = null): DeltaResponse =
        client.get(baseUrlProvider() + "/api/videos/delta") {
            timeout {
                requestTimeoutMillis = NetDefaults.DELTA_REQUEST_TIMEOUT.inWholeMilliseconds
                connectTimeoutMillis = NetDefaults.DELTA_CONNECT_TIMEOUT.inWholeMilliseconds
                socketTimeoutMillis = NetDefaults.DELTA_SOCKET_TIMEOUT.inWholeMilliseconds
            }
            if (!since.isNullOrEmpty()) parameter("since", since)
        }.body()
}
