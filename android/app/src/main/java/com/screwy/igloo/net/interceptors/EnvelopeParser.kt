package com.screwy.igloo.net.interceptors

import android.util.Log
import com.screwy.igloo.data.PreferencesRepo
import io.ktor.client.HttpClientConfig
import io.ktor.client.plugins.observer.ResponseObserver
import io.ktor.client.statement.bodyAsText
import io.ktor.http.contentType
import io.ktor.http.isSuccess
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull

/**
 * Side-band response observer. On every 2xx JSON response, extracts envelope fields:
 *
 *  - `X-Igloo-Server-Time-Ms`, falling back to `server_time_ms` in legacy bodies,
 *    → `PreferencesRepo.setServerTimeOffsetMs(server_time_ms - deviceNow)`.
 * Current servers expose the header so bulk response bodies do not need duplicate
 * observer parsing. Legacy JSON bodies still pass through unchanged — Ktor's
 * `ResponseObserver` duplicates the receive channel so business-logic `.body()`
 * still resolves. Non-JSON bodies (binary media, file downloads) are skipped via
 * content-type sniffing when the header is absent. Error paths (4xx/5xx) own their
 * own envelope parsing in `IglooError.classify`.
 *
	 * PreferencesRepo is passed as a lambda so pre-login HTTP calls (`/api/auth/login`,
	 * `/api/health/live`) do not depend on the authenticated Room graph. Snapshot and
	 * mutation state stay with the call that owns and validates the typed response.
	 */
object EnvelopeParser {

    private const val TAG = "EnvelopeParser"
    private const val SERVER_TIME_HEADER = "X-Igloo-Server-Time-Ms"

    private val json = Json {
        ignoreUnknownKeys = true
        isLenient = true
        coerceInputValues = true
    }

    fun install(
        config: HttpClientConfig<*>,
        prefsProvider: () -> PreferencesRepo?,
        nowMsProvider: () -> Long = { System.currentTimeMillis() },
        hostResolver: (() -> String)? = null,
        onReachable: () -> Unit = {},
    ) {
        config.install(ResponseObserver) {
            onResponse { response ->
                val iglooHost = hostResolver?.invoke().orEmpty()
                if (iglooHost.isNotEmpty() && response.call.request.url.host.lowercase() == iglooHost) {
                    onReachable()
                }
                if (!response.status.isSuccess()) return@onResponse

                val headerServerTimeMs = response.headers[SERVER_TIME_HEADER]?.trim()?.toLongOrNull()
                if (headerServerTimeMs != null) {
                    updateServerTime(prefsProvider(), headerServerTimeMs, nowMsProvider)
                    return@onResponse
                }

                val contentType = response.contentType()
                if (contentType != null) {
                    val isJson = contentType.contentType.equals("application", ignoreCase = true) &&
                        contentType.contentSubtype.equals("json", ignoreCase = true)
                    if (!isJson) return@onResponse
                }

                val text = runCatching { response.bodyAsText() }.getOrNull() ?: return@onResponse
                if (text.isBlank() || text.firstOrNull() != '{') return@onResponse

                val obj = runCatching { json.parseToJsonElement(text) as? JsonObject }.getOrNull()
                    ?: return@onResponse

                val prefs = prefsProvider()
                val serverTimeMs = obj["server_time_ms"]?.jsonPrimitive?.longOrNull
                if (prefs != null && serverTimeMs != null) {
                    updateServerTime(prefs, serverTimeMs, nowMsProvider)
                }
            }
        }
    }

    private suspend fun updateServerTime(
        prefs: PreferencesRepo?,
        serverTimeMs: Long,
        nowMsProvider: () -> Long,
    ) {
        if (prefs == null) return
        runCatching { prefs.setServerTimeOffsetMs(serverTimeMs - nowMsProvider()) }
            .onFailure { Log.w(TAG, "setServerTimeOffsetMs failed", it) }
    }
}
