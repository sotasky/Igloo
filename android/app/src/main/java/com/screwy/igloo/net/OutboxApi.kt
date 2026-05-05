package com.screwy.igloo.net

import io.ktor.client.HttpClient
import io.ktor.client.request.post
import io.ktor.client.request.put
import io.ktor.client.request.setBody
import io.ktor.client.statement.HttpResponse
import io.ktor.http.ContentType
import io.ktor.http.contentType
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * Outbox dispatcher's HTTP surface. Covers:
 *  - `POST /api/logs/{server,debug}` (server-side-changes #12).
 *  - `POST` + `PUT` `/api/mutations/{kind}` (server-side-changes #11).
 *
 * Every method returns the raw `HttpResponse` so the dispatcher can classify status
 * via `HttpResponse.classify()` before reading the body. Body extraction happens
 * only on 2xx (via `.body<T>()`); on 4xx/5xx the classifier reads the envelope in
 * `IglooError.classify` and the body is ignored.
 *
 * Types stay thin and close to the server's body shapes in
 * `internal/web/mutations_api.go`.
 */
class OutboxApi(
    private val client: HttpClient,
    private val baseUrlProvider: () -> String,
) {

    // ─── Logs (#12) ─────────────────────────────────────────────────────────

    suspend fun postLogServer(batch: LogBatchRequest): HttpResponse =
        client.post(baseUrlProvider() + "/api/logs/server") {
            contentType(ContentType.Application.Json)
            setBody(batch)
        }

    suspend fun postLogDebug(batch: LogBatchRequest): HttpResponse =
        client.post(baseUrlProvider() + "/api/logs/debug") {
            contentType(ContentType.Application.Json)
            setBody(batch)
        }

    // ─── Mutations (#11) ────────────────────────────────────────────────────

    suspend fun like(req: LikeRequest): HttpResponse =
        client.post(baseUrlProvider() + "/api/mutations/like") {
            contentType(ContentType.Application.Json)
            setBody(req)
        }

    suspend fun bookmark(req: BookmarkRequest): HttpResponse =
        client.post(baseUrlProvider() + "/api/mutations/bookmark") {
            contentType(ContentType.Application.Json)
            setBody(req)
        }

    suspend fun follow(req: ToggleRequest): HttpResponse =
        client.post(baseUrlProvider() + "/api/mutations/follow") {
            contentType(ContentType.Application.Json)
            setBody(req)
        }

    suspend fun star(req: ToggleRequest): HttpResponse =
        client.post(baseUrlProvider() + "/api/mutations/star") {
            contentType(ContentType.Application.Json)
            setBody(req)
        }

    suspend fun mute(req: MuteRequest): HttpResponse =
        client.post(baseUrlProvider() + "/api/mutations/mute") {
            contentType(ContentType.Application.Json)
            setBody(req)
        }

    suspend fun seen(req: SeenRequest): HttpResponse =
        client.post(baseUrlProvider() + "/api/mutations/seen") {
            contentType(ContentType.Application.Json)
            setBody(req)
        }

    suspend fun momentView(req: MomentViewRequest): HttpResponse =
        client.post(baseUrlProvider() + "/api/mutations/moment_view") {
            contentType(ContentType.Application.Json)
            setBody(req)
        }

    suspend fun createCategory(req: CreateCategoryRequest): HttpResponse =
        client.post(baseUrlProvider() + "/api/mutations/create_category") {
            contentType(ContentType.Application.Json)
            setBody(req)
        }

    suspend fun channelSetting(req: ChannelSettingRequest): HttpResponse =
        client.put(baseUrlProvider() + "/api/mutations/channel_setting") {
            contentType(ContentType.Application.Json)
            setBody(req)
        }

    suspend fun progress(req: ProgressRequest): HttpResponse =
        client.put(baseUrlProvider() + "/api/mutations/progress") {
            contentType(ContentType.Application.Json)
            setBody(req)
        }

    suspend fun momentsCursor(req: MomentsCursorRequest): HttpResponse =
        client.put(baseUrlProvider() + "/api/mutations/moments_cursor") {
            contentType(ContentType.Application.Json)
            setBody(req)
        }

    suspend fun bookmarkAlias(req: BookmarkAliasRequest): HttpResponse =
        client.put(baseUrlProvider() + "/api/mutations/bookmark_alias") {
            contentType(ContentType.Application.Json)
            setBody(req)
        }
}

// ─── Log payloads ──────────────────────────────────────────────────────────

@Serializable
data class LogBatchRequest(
    val entries: List<LogEntryPayload>,
    val device_id: String? = null,
)

@Serializable
data class LogEntryPayload(
    val level: String? = null,
    val event: String,
    val fields: Map<String, String>? = null,
    val timestamp_ms: Long,
)

// ─── Mutation payloads ─────────────────────────────────────────────────────

@Serializable
data class LikeRequest(val tweet_id: String, val action: String, val updated_at_ms: Long)

@Serializable
data class BookmarkRequest(
    val video_id: String,
    val action: String,
    val category_id: Long? = null,
    val custom_title: String? = null,
    val account_handles: String? = null,
    val media_indices: String? = null,
    val updated_at_ms: Long,
)

@Serializable
data class ToggleRequest(
    @SerialName("channel_id") val channelId: String,
    val action: String,
    val updated_at_ms: Long,
)

@Serializable
data class MuteRequest(val handle: String, val action: String, val updated_at_ms: Long)

@Serializable
data class SeenRequest(val tweet_ids: List<String>, val updated_at_ms: Long)

@Serializable
data class MomentViewRequest(val video_id: String, val updated_at_ms: Long)

@Serializable
data class CreateCategoryRequest(val name: String, val provisional_id: String, val updated_at_ms: Long)

@Serializable
data class CreateCategoryResponse(
    val category_id: Long,
    val provisional_id: String,
    val ok: Boolean = true,
    val sync_version: Long? = null,
    val sync_stream: String? = null,
    val server_time_ms: Long? = null,
)

@Serializable
data class ChannelSettingRequest(
    val channel_id: String,
    val field: String,
    val value: Long? = null,
    val updated_at_ms: Long,
)

@Serializable
data class ProgressRequest(
    val video_id: String,
    val position: Double,
    val duration: Double,
    val source: String,
    val updated_at_ms: Long,
)

@Serializable
data class MomentsCursorRequest(
    val video_id: String,
    val position_ms: Long,
    val updated_at_ms: Long,
    val scope: String = "all",
)

@Serializable
data class BookmarkAliasRequest(
    val original_handle: String,
    val display_alias: String,
    val updated_at_ms: Long,
)
