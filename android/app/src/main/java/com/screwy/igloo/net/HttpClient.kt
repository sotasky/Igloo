package com.screwy.igloo.net

import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.net.auth.AuthTokenProvider
import com.screwy.igloo.net.interceptors.AuthInterceptor
import com.screwy.igloo.net.interceptors.EnvelopeParser
import com.screwy.igloo.net.interceptors.ServerNetworkBindingInterceptor
import com.screwy.igloo.net.interceptors.UASpoofInterceptor
import io.ktor.client.HttpClient
import io.ktor.client.HttpClientConfig
import io.ktor.client.engine.HttpClientEngine
import io.ktor.client.engine.HttpClientEngineFactory
import io.ktor.client.engine.android.Android
import io.ktor.client.plugins.HttpResponseValidator
import io.ktor.client.plugins.HttpTimeout
import io.ktor.client.plugins.UserAgent
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.serialization.kotlinx.json.json
import kotlinx.serialization.json.Json
import java.io.IOException

/**
 * Shared Igloo HTTP client.
 * Ktor `HttpClient` across the app, installed features applied in the documented order.
 *
 * Production wiring uses the `Android` engine (`buildIglooClient`); tests override via
 * `buildIglooClient(engine = MockEngine { ... })` so every other plugin + interceptor
 * exercises the same code path.
 *
 * `prefsProvider` is a lambda rather than a direct reference because the per-user Room
 * DB isn't open until after login; the HTTP client must be constructible pre-login so
 * `AuthApi.login` can issue its request. Post-login, the lambda returns the live
 * `PreferencesRepo` for shared envelope side effects.
 */

val iglooJson: Json = Json {
    ignoreUnknownKeys = true
    coerceInputValues = true
    isLenient = true
    encodeDefaults = true
}

fun buildIglooClient(
    prefsProvider: () -> PreferencesRepo?,
    tokenProvider: AuthTokenProvider,
    hostProvider: IglooHostProvider,
    uaOverrides: Map<String, String> = NetDefaults.UA_OVERRIDES,
    nowMsProvider: () -> Long = { System.currentTimeMillis() },
    beforeIglooRequest: () -> Unit = {},
    onReachable: () -> Unit = {},
    onTransportFailure: () -> Unit = {},
): HttpClient = HttpClient(Android) {
    configureIgloo(
        prefsProvider,
        tokenProvider,
        hostProvider,
        uaOverrides,
        nowMsProvider,
        beforeIglooRequest,
        onReachable,
        onTransportFailure,
    )
}

fun <T : HttpClientEngineConfig> buildIglooClient(
    engineFactory: HttpClientEngineFactory<T>,
    prefsProvider: () -> PreferencesRepo?,
    tokenProvider: AuthTokenProvider,
    hostProvider: IglooHostProvider,
    uaOverrides: Map<String, String> = NetDefaults.UA_OVERRIDES,
    nowMsProvider: () -> Long = { System.currentTimeMillis() },
    beforeIglooRequest: () -> Unit = {},
    onReachable: () -> Unit = {},
    onTransportFailure: () -> Unit = {},
): HttpClient = HttpClient(engineFactory) {
    configureIgloo(
        prefsProvider,
        tokenProvider,
        hostProvider,
        uaOverrides,
        nowMsProvider,
        beforeIglooRequest,
        onReachable,
        onTransportFailure,
    )
}

fun buildIglooClient(
    engine: HttpClientEngine,
    prefsProvider: () -> PreferencesRepo?,
    tokenProvider: AuthTokenProvider,
    hostProvider: IglooHostProvider,
    uaOverrides: Map<String, String> = NetDefaults.UA_OVERRIDES,
    nowMsProvider: () -> Long = { System.currentTimeMillis() },
    beforeIglooRequest: () -> Unit = {},
    onReachable: () -> Unit = {},
    onTransportFailure: () -> Unit = {},
): HttpClient = HttpClient(engine) {
    configureIgloo(
        prefsProvider,
        tokenProvider,
        hostProvider,
        uaOverrides,
        nowMsProvider,
        beforeIglooRequest,
        onReachable,
        onTransportFailure,
    )
}

private fun HttpClientConfig<*>.configureIgloo(
    prefsProvider: () -> PreferencesRepo?,
    tokenProvider: AuthTokenProvider,
    hostProvider: IglooHostProvider,
    uaOverrides: Map<String, String>,
    nowMsProvider: () -> Long,
    beforeIglooRequest: () -> Unit,
    onReachable: () -> Unit,
    onTransportFailure: () -> Unit,
) {
    expectSuccess = false // callers inspect status via IglooError.classify

    install(ContentNegotiation) {
        json(iglooJson)
    }

    install(HttpTimeout) {
        requestTimeoutMillis = NetDefaults.DEFAULT_REQUEST_TIMEOUT.inWholeMilliseconds
        connectTimeoutMillis = NetDefaults.DEFAULT_CONNECT_TIMEOUT.inWholeMilliseconds
        socketTimeoutMillis = NetDefaults.DEFAULT_SOCKET_TIMEOUT.inWholeMilliseconds
    }

    install(UserAgent) {
        agent = NetDefaults.USER_AGENT
    }

    install(ServerNetworkBindingInterceptor) {
        this.hostResolver = hostProvider::hostSync
        this.beforeServerRequest = beforeIglooRequest
    }

    install(AuthInterceptor) {
        this.tokenProvider = tokenProvider
        this.hostResolver = hostProvider::hostSync
    }

    install(UASpoofInterceptor) {
        overrides = uaOverrides
    }

    HttpResponseValidator {
        handleResponseExceptionWithRequest { cause, request ->
            val iglooHost = hostProvider.hostSync()
            if (iglooHost.isEmpty() || request.url.host.lowercase() != iglooHost) return@handleResponseExceptionWithRequest
            if (cause.isTransportFailure()) onTransportFailure()
        }
    }

    EnvelopeParser.install(
        config = this,
        prefsProvider = prefsProvider,
        nowMsProvider = nowMsProvider,
        hostResolver = hostProvider::hostSync,
        onReachable = onReachable,
    )
}

private fun Throwable.isTransportFailure(): Boolean {
    generateSequence(this) { it.cause }.forEach { cause ->
        if (cause is IOException) return true
        val simpleName = cause::class.simpleName.orEmpty()
        if (simpleName == "ConnectTimeoutException" ||
            simpleName == "SocketTimeoutException" ||
            simpleName == "HttpRequestTimeoutException"
        ) {
            return true
        }
        val message = cause.message?.lowercase().orEmpty()
        if (message.contains("failed to connect") ||
            message.contains("unable to resolve host") ||
            message.contains("timeout")
        ) {
            return true
        }
    }
    return false
}

// Re-exported so call-sites don't need to import Ktor engine types.
typealias HttpClientEngineConfig = io.ktor.client.engine.HttpClientEngineConfig
