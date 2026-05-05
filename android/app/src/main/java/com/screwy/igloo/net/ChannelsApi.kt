package com.screwy.igloo.net

import io.ktor.client.HttpClient
import io.ktor.client.call.body
import io.ktor.client.plugins.timeout
import io.ktor.client.request.get
import io.ktor.client.request.parameter

/** `GET /api/channels/delta` — channel bundle stream (02-sync.md §3). */
class ChannelsApi(
    private val client: HttpClient,
    private val baseUrlProvider: () -> String,
) {
    suspend fun channelsDelta(since: String? = null): DeltaResponse =
        client.get(baseUrlProvider() + "/api/channels/delta") {
            timeout {
                requestTimeoutMillis = NetDefaults.DELTA_REQUEST_TIMEOUT.inWholeMilliseconds
                connectTimeoutMillis = NetDefaults.DELTA_CONNECT_TIMEOUT.inWholeMilliseconds
                socketTimeoutMillis = NetDefaults.DELTA_SOCKET_TIMEOUT.inWholeMilliseconds
            }
            if (!since.isNullOrEmpty()) parameter("since", since)
        }.body()
}
