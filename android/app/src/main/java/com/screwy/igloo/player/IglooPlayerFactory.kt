package com.screwy.igloo.player

import android.content.Context
import androidx.media3.datasource.DefaultDataSource
import androidx.media3.datasource.DefaultHttpDataSource
import androidx.media3.datasource.ResolvingDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.DefaultLoadControl
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import com.screwy.igloo.net.IglooHostProvider
import com.screwy.igloo.net.NetDefaults
import com.screwy.igloo.net.auth.AuthTokenProvider
import com.screwy.igloo.net.isIglooServerUrl

/**
 * Media3 player factory for local files, Igloo server media, and public CDN media.
 *
 * Public CDN requests use the same browser-shaped UA as the app HTTP stack and never
 * receive Igloo credentials. Bearer auth is added only when the full playback URL
 * resolves to the configured Igloo server host.
 */
fun buildIglooPlayer(
    context: Context,
    tokenProvider: AuthTokenProvider,
    hostProvider: IglooHostProvider,
): ExoPlayer = buildIglooPlayer(
    context = context,
    tokenProvider = tokenProvider,
    iglooHostResolver = hostProvider::hostSync,
)

internal fun buildIglooPlayer(
    context: Context,
    tokenProvider: AuthTokenProvider,
    iglooHostResolver: () -> String,
): ExoPlayer {
    val httpFactory = DefaultHttpDataSource.Factory()
        .setUserAgent(NetDefaults.PUBLIC_BROWSER_USER_AGENT)
    val resolvingHttpFactory = ResolvingDataSource.Factory(httpFactory) { dataSpec ->
        val authHeaders = iglooMediaRequestHeaders(
            url = dataSpec.uri.toString(),
            iglooHost = iglooHostResolver(),
            bearerToken = tokenProvider.bearerTokenSync(),
            existingHeaders = dataSpec.httpRequestHeaders,
        )
        if (authHeaders.isEmpty()) dataSpec else dataSpec.withAdditionalHeaders(authHeaders)
    }
    val dataSourceFactory = DefaultDataSource.Factory(context, resolvingHttpFactory)
    val loadControl = DefaultLoadControl.Builder()
        .setBufferDurationsMs(
            /* minBufferMs = */ 1_500,
            /* maxBufferMs = */ 12_000,
            /* bufferForPlaybackMs = */ 100,
            /* bufferForPlaybackAfterRebufferMs = */ 250,
        )
        .build()
    return ExoPlayer.Builder(context)
        .setLoadControl(loadControl)
        .setMediaSourceFactory(DefaultMediaSourceFactory(dataSourceFactory))
        .build()
}

internal fun iglooMediaRequestHeaders(
    url: String,
    iglooHost: String,
    bearerToken: String?,
    existingHeaders: Map<String, String> = emptyMap(),
): Map<String, String> {
    val token = bearerToken?.takeIf { it.isNotBlank() } ?: return emptyMap()
    if (!isIglooServerUrl(url, iglooHost)) return emptyMap()
    if (existingHeaders.keys.any { it.equals("Authorization", ignoreCase = true) }) return emptyMap()
    return mapOf("Authorization" to "Bearer $token")
}
