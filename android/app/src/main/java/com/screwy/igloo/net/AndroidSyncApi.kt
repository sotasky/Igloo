package com.screwy.igloo.net

import io.ktor.client.HttpClient
import io.ktor.client.plugins.timeout
import io.ktor.client.request.get
import io.ktor.client.request.parameter
import io.ktor.client.request.post
import io.ktor.client.request.setBody
import io.ktor.client.statement.HttpResponse
import io.ktor.client.statement.bodyAsText
import io.ktor.http.ContentType
import io.ktor.http.contentType
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.decodeFromString

class AndroidSyncApi(
    private val client: HttpClient,
    private val baseUrlProvider: () -> String,
) {
    suspend fun latestGeneration(retention: AndroidSyncRetentionRequest): AndroidSyncLatestResponse =
        client.get(baseUrlProvider() + "/api/android/sync/generation/latest") {
            syncMetadataTimeout()
            parameter("feed_days", retention.feedDays)
            parameter("youtube_days", retention.youtubeDays)
            parameter("moments_days", retention.momentsDays)
            parameter("story_hours", retention.storyHours)
        }.bodyAsText().decodeSync("latest_generation")

    suspend fun items(generationId: String, after: String? = null): AndroidSyncItemsResponse =
        client.get(baseUrlProvider() + "/api/android/sync/generation/$generationId/items") {
            syncMetadataTimeout()
            if (!after.isNullOrEmpty()) parameter("after", after)
        }.bodyAsText().decodeSync("items:$generationId")

    suspend fun assets(generationId: String, after: String? = null): AndroidSyncAssetsResponse =
        client.get(baseUrlProvider() + "/api/android/sync/generation/$generationId/assets") {
            syncMetadataTimeout()
            if (!after.isNullOrEmpty()) parameter("after", after)
        }.bodyAsText().decodeSync("assets:$generationId")

    suspend fun health(req: AndroidSyncHealthRequest): HttpResponse =
        client.post(baseUrlProvider() + "/api/android/sync/health") {
            syncMetadataTimeout()
            contentType(ContentType.Application.Json)
            setBody(req)
        }

    private fun io.ktor.client.request.HttpRequestBuilder.syncMetadataTimeout() {
        timeout {
            requestTimeoutMillis = SYNC_METADATA_REQUEST_TIMEOUT_MS
            connectTimeoutMillis = SYNC_CONNECT_TIMEOUT_MS
            socketTimeoutMillis = SYNC_SOCKET_TIMEOUT_MS
        }
    }

    private companion object {
        const val SYNC_METADATA_REQUEST_TIMEOUT_MS = 5 * 60 * 1000L
        const val SYNC_CONNECT_TIMEOUT_MS = 30 * 1000L
        const val SYNC_SOCKET_TIMEOUT_MS = SYNC_METADATA_REQUEST_TIMEOUT_MS
    }
}

private inline fun <reified T> String.decodeSync(label: String): T =
    try {
        iglooJson.decodeFromString(this)
    } catch (e: Exception) {
        val preview = take(600).replace('\n', ' ')
        throw IllegalStateException("Sync decode failed for $label: $preview", e)
    }

@Serializable
data class AndroidSyncLatestResponse(
    val generation: AndroidSyncGenerationDto,
    val ok: Boolean = true,
    val server_time_ms: Long? = null,
)

@Serializable
data class AndroidSyncGenerationDto(
    val generation_id: String,
    val created_at_ms: Long,
    val status: String,
    val source_version: String,
    val retention: Map<String, Int> = emptyMap(),
    val item_count: Int = 0,
    val asset_count: Int = 0,
    val ready_asset_count: Int = 0,
    val server_missing_asset_count: Int = 0,
    val total_bytes: Long = 0,
    val content_counts: Map<String, Int> = emptyMap(),
    val asset_counts: Map<String, Int> = emptyMap(),
)

@Serializable
data class AndroidSyncRetentionRequest(
    @SerialName("feed_days") val feedDays: Int,
    @SerialName("youtube_days") val youtubeDays: Int,
    @SerialName("moments_days") val momentsDays: Int,
    @SerialName("story_hours") val storyHours: Int,
)

@Serializable
data class AndroidSyncItemsResponse(
    val generation_id: String,
    val items: List<AndroidSyncItemDto> = emptyList(),
    val next: String = "",
    val end_of_stream: Boolean = false,
    val ok: Boolean = true,
    val server_time_ms: Long? = null,
)

@Serializable
data class AndroidSyncItemDto(
    val seq: Long,
    val item_kind: String,
    val item_id: String,
    val payload: BundleEnvelope,
)

@Serializable
data class AndroidSyncAssetsResponse(
    val generation_id: String,
    val assets: List<AndroidSyncAssetDto> = emptyList(),
    val next: String = "",
    val end_of_stream: Boolean = false,
    val ok: Boolean = true,
    val server_time_ms: Long? = null,
)

@Serializable
data class AndroidSyncAssetDto(
    val seq: Long,
    val asset_id: String,
    val asset_kind: String,
    val owner_id: String,
    val owner_kind: String,
    val bucket: String,
    val server_url: String,
    val content_type: String? = null,
    val size_bytes: Long = 0,
    val sha256: String? = null,
    val state: String = "ready",
    val required_reason: String? = null,
    val is_auto: Boolean? = null,
    val audio_language: String? = null,
    val effective_recency_ms: Long = 0,
)

@Serializable
data class AndroidSyncHealthRequest(
    val generation_id: String,
    val reported_at_ms: Long,
    val retention: AndroidSyncRetentionRequest,
    val counts: AndroidSyncHealthCountPayload,
    val bytes: AndroidSyncHealthBytePayload,
)

@Serializable
data class AndroidSyncHealthCountPayload(
    val total: Int,
    val verified: Int,
    val pending: Int,
    val failed: Int,
    val missing: Int,
)

@Serializable
data class AndroidSyncHealthBytePayload(
    val verified: Long,
)
