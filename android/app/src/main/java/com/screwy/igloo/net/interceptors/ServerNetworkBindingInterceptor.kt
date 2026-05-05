package com.screwy.igloo.net.interceptors

import io.ktor.client.plugins.api.createClientPlugin

class ServerNetworkBindingInterceptorConfig {
    lateinit var hostResolver: () -> String
    var beforeServerRequest: () -> Unit = {}
}

val ServerNetworkBindingInterceptor = createClientPlugin(
    "IglooServerNetworkBindingInterceptor",
    ::ServerNetworkBindingInterceptorConfig,
) {
    val resolveHost = pluginConfig.hostResolver
    val beforeServerRequest = pluginConfig.beforeServerRequest

    onRequest { request, _ ->
        val iglooHost = resolveHost()
        if (iglooHost.isEmpty() || request.url.host.lowercase() != iglooHost) return@onRequest
        beforeServerRequest()
    }
}
