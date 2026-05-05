package com.screwy.igloo.net.interceptors

import io.ktor.client.plugins.api.createClientPlugin
import io.ktor.client.request.headers
import io.ktor.http.HttpHeaders

/**
 * Per-host User-Agent overrides. The default app agent is already browser-shaped;
 * keep this plugin as a narrow escape hatch for any host that forces a different
 * common browser string.
 *
 * Host lookup is case-insensitive. Prefer leaving `NetDefaults.UA_OVERRIDES` empty.
 */
class UASpoofInterceptorConfig {
    var overrides: Map<String, String> = emptyMap()
}

val UASpoofInterceptor = createClientPlugin("IglooUASpoofInterceptor", ::UASpoofInterceptorConfig) {
    val table = pluginConfig.overrides.mapKeys { it.key.lowercase() }

    onRequest { request, _ ->
        val host = request.url.host.lowercase()
        val agent = table[host] ?: return@onRequest
        request.headers {
            remove(HttpHeaders.UserAgent)
            append(HttpHeaders.UserAgent, agent)
        }
    }
}
