package com.screwy.igloo.net

import io.ktor.client.HttpClient
import io.ktor.client.call.body
import io.ktor.client.plugins.timeout
import io.ktor.client.request.get
import io.ktor.client.request.parameter
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonObject

/** `GET /api/mutations/delta` — inbound user-state / interaction sync stream. */
class MutationDeltaApi(
    private val client: HttpClient,
    private val baseUrlProvider: () -> String,
) {
    suspend fun delta(since: String? = null, limit: Int = 500): MutationDeltaResponse =
        client.get(baseUrlProvider() + "/api/mutations/delta") {
            timeout {
                requestTimeoutMillis = NetDefaults.DELTA_REQUEST_TIMEOUT.inWholeMilliseconds
                connectTimeoutMillis = NetDefaults.DELTA_CONNECT_TIMEOUT.inWholeMilliseconds
                socketTimeoutMillis = NetDefaults.DELTA_SOCKET_TIMEOUT.inWholeMilliseconds
            }
            if (!since.isNullOrEmpty()) parameter("since", since)
            parameter("limit", limit.coerceIn(1, 500))
        }.body()
}

@Serializable
data class MutationDeltaResponse(
    val version: Long = 0,
    val changes: List<MutationChange> = emptyList(),
    val truncated: Boolean = false,
    val ok: Boolean = true,
    val server_time_ms: Long? = null,
)

@Serializable
data class MutationChange(
    val version: Long,
    val type: String,
    val item_id: String,
    val value: JsonObject = JsonObject(emptyMap()),
    val created_at: Long = 0,
)
