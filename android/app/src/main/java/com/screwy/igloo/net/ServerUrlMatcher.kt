package com.screwy.igloo.net

import java.net.URI

internal fun isIglooServerUrl(url: String, iglooHost: String): Boolean {
    if (iglooHost.isBlank()) return false
    val host = runCatching { URI(url).host?.lowercase() }.getOrNull() ?: return false
    return host == iglooHost.lowercase()
}
