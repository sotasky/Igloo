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
import io.ktor.http.isSuccess
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.json.JsonObject

class AndroidSyncApi(
    private val client: HttpClient,
    private val baseUrlProvider: () -> String,
) {
    suspend fun bootstrap(
        retention: AndroidSyncRetentionRequest,
        after: String?,
    ): AndroidSyncPageResponse =
        client.get(baseUrlProvider() + "/api/android/sync/bootstrap") {
            syncMetadataTimeout()
            parameter("feed_days", retention.feedDays)
            parameter("youtube_days", retention.youtubeDays)
            parameter("moments_days", retention.momentsDays)
            parameter("story_hours", retention.storyHours)
            parameter("full_youtube_metadata", FULL_YOUTUBE_METADATA_REQUEST)
            if (!after.isNullOrEmpty()) parameter("after", after)
        }.decodeSyncResponse("bootstrap")

    suspend fun changes(
        retention: AndroidSyncRetentionRequest,
        after: String,
    ): AndroidSyncPageResponse =
        client.get(baseUrlProvider() + "/api/android/sync/changes") {
            syncMetadataTimeout()
            parameter("feed_days", retention.feedDays)
            parameter("youtube_days", retention.youtubeDays)
            parameter("moments_days", retention.momentsDays)
            parameter("story_hours", retention.storyHours)
            parameter("full_youtube_metadata", FULL_YOUTUBE_METADATA_REQUEST)
            parameter("after", after)
        }.decodeSyncResponse("changes")

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
            socketTimeoutMillis = SYNC_METADATA_REQUEST_TIMEOUT_MS
        }
    }

    private companion object {
        const val FULL_YOUTUBE_METADATA_REQUEST = 1
        const val SYNC_METADATA_REQUEST_TIMEOUT_MS = 60_000L
        const val SYNC_CONNECT_TIMEOUT_MS = 15_000L
    }
}

class AndroidSyncHttpException(
    val label: String,
    val statusCode: Int,
    body: String,
) : IllegalStateException(syncHttpErrorMessage(label, statusCode, body)) {
    val errorCode: String? =
        runCatching { iglooJson.decodeFromString<AndroidSyncErrorEnvelope>(body).error_code }
            .getOrNull()

    val isTransient: Boolean
        get() = statusCode == 408 || statusCode == 429 || statusCode in 500..599

    val downgradesReachability: Boolean
        get() = statusCode == 408 || statusCode == 502 || statusCode == 503 || statusCode == 504

    val isSyncResetRequired: Boolean
        get() = statusCode == 409 && errorCode == "sync_reset_required"

    val isAssetChanged: Boolean
        get() = statusCode == 409 && errorCode == "asset_changed"
}

class AndroidSyncDecodeException(
    val label: String,
    body: String,
    cause: Throwable,
) : IllegalStateException("Sync decode failed for $label: ${body.syncErrorPreview()}", cause)

private suspend inline fun <reified T> HttpResponse.decodeSyncResponse(label: String): T {
    val raw = bodyAsText()
    if (!status.isSuccess()) throw AndroidSyncHttpException(label, status.value, raw)
    return try {
        iglooJson.decodeFromString(raw)
    } catch (e: Exception) {
        throw AndroidSyncDecodeException(label, raw, e)
    }
}

private fun syncHttpErrorMessage(label: String, statusCode: Int, body: String): String {
    val preview = body.syncErrorPreview()
    return if (preview.isBlank() || preview.startsWith("<")) {
        "Sync HTTP $statusCode for $label"
    } else {
        "Sync HTTP $statusCode for $label: $preview"
    }
}

private val syncWhitespace = Regex("\\s+")

private fun String.syncErrorPreview(): String = trim().replace(syncWhitespace, " ").take(200)

@Serializable
private data class AndroidSyncErrorEnvelope(val error_code: String? = null)

@Serializable
data class AndroidSyncRetentionRequest(
    @SerialName("feed_days") val feedDays: Int,
    @SerialName("youtube_days") val youtubeDays: Int,
    @SerialName("moments_days") val momentsDays: Int,
    @SerialName("story_hours") val storyHours: Int,
)

@Serializable
data class AndroidSyncPageResponse(
    val changes: List<AndroidSyncChangeDto>,
    val next_cursor: String,
    val end_of_stream: Boolean,
)

@Serializable
data class AndroidSyncChangeDto(
    val owner_kind: String,
    val owner_id: String,
    val operation: String,
    val retention_bucket: String,
    val retain_at_ms: Long,
    val payload: JsonObject? = null,
)

@Serializable
data class AndroidSyncAssetDto(
    val asset_id: String,
    val asset_kind: String,
    val media_index: Int,
    val owner_id: String,
    val owner_kind: String,
    val bucket: String,
    val content_type: String,
    val size_bytes: Long,
    val revision: Long,
    val state: String,
    val is_auto: Boolean?,
)

@Serializable
data class AndroidSyncHealthRequest(
    val cursor: String,
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
    val missing: Int,
)

@Serializable data class AndroidSyncHealthBytePayload(val verified: Long)
