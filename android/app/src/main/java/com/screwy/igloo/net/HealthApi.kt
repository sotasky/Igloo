package com.screwy.igloo.net

import io.ktor.client.HttpClient
import io.ktor.client.plugins.timeout
import io.ktor.client.request.get
import io.ktor.client.statement.HttpResponse
import kotlin.time.Duration

/**
 * `GET /api/health` — the 5s-budget reachability probe. Returns the standard envelope
 * `{ok: true, server_time_ms}` with no auth required.
 */
class HealthApi(
    private val client: HttpClient,
    private val baseUrlProvider: () -> String,
    private val probeTimeout: Duration = NetDefaults.PROBE_REQUEST_TIMEOUT,
) {

    suspend fun health(): HttpResponse =
        client.get(baseUrlProvider() + "/api/health") {
            timeout {
                requestTimeoutMillis = probeTimeout.inWholeMilliseconds
            }
        }
}
