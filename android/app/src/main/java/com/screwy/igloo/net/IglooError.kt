package com.screwy.igloo.net

import io.ktor.client.statement.HttpResponse
import io.ktor.client.statement.bodyAsText
import io.ktor.http.HttpStatusCode
import io.ktor.http.isSuccess
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonPrimitive

/**
 * Typed error envelope for any Igloo HTTP call. The outbox drain uses this
 * classification when deciding success, transient failure, dead-letter, or
 * auth-refresh behavior.
 *
 * API callers return `Result<T, IglooError>` (or unwrap to throw in trivial paths).
 * Outbox drain + reconcilers are the primary consumers — they branch on
 * `isTransient | isDead | requiresRefresh`.
 */
sealed class IglooError(
    val status: Int?,
    val errorCode: String?,
    val errorMessage: String?,
) {
    class Transient(status: Int?, code: String?, msg: String?) : IglooError(status, code, msg)
    class AuthRefreshRequired(status: Int, code: String?, msg: String?) : IglooError(status, code, msg)
    class Dead(status: Int, code: String?, msg: String?) : IglooError(status, code, msg)
    class Network(cause: Throwable) : IglooError(null, "network_error", cause.toString())
    class Malformed(body: String?) : IglooError(null, "malformed_envelope", body?.take(200))

    val isTransient: Boolean get() = this is Transient || this is Network
    val isDead: Boolean get() = this is Dead || this is Malformed
    val requiresRefresh: Boolean get() = this is AuthRefreshRequired

    override fun toString(): String =
        "${this::class.simpleName}(status=$status, code=$errorCode, msg=${errorMessage?.take(80)})"
}

private val classifyJson = Json { ignoreUnknownKeys = true; isLenient = true }

/**
 * Map a raw Ktor `HttpResponse` to a classification.
 *
 *  - 2xx → `null` (success — caller extracts the body).
 *  - 401 / 4xx / 5xx / 408 / 429 → non-null classification.
 *
 * Body is only read on non-2xx so that success-path `.body<T>()` calls remain
 * unaffected. Non-2xx paths read + buffer the body to extract `error_code` +
 * `error_message` from the envelope (per `envelope.go` `writeJSONError`).
 */
suspend fun HttpResponse.classify(): IglooError? {
    val statusCode = status.value
    if (status.isSuccess()) return null

    val bodyText = runCatching { bodyAsText() }.getOrNull()
    val envelope = bodyText?.let {
        runCatching { classifyJson.parseToJsonElement(it) as? JsonObject }.getOrNull()
    }
    val errorCode = envelope?.get("error_code")?.jsonPrimitive?.contentOrNull
    val errorMessage = envelope?.get("error_message")?.jsonPrimitive?.contentOrNull

    return when {
        statusCode == HttpStatusCode.Unauthorized.value -> {
            IglooError.AuthRefreshRequired(statusCode, errorCode, errorMessage)
        }
        statusCode == HttpStatusCode.RequestTimeout.value ||
            statusCode == HttpStatusCode.TooManyRequests.value ||
            statusCode in 500..599 -> {
            IglooError.Transient(statusCode, errorCode, errorMessage)
        }
        statusCode in 400..499 -> {
            IglooError.Dead(statusCode, errorCode, errorMessage)
        }
        else -> IglooError.Dead(statusCode, errorCode, errorMessage ?: "unexpected status")
    }
}
