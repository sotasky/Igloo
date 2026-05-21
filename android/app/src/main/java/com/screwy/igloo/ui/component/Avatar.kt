package com.screwy.igloo.ui.component

import android.content.Context
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Person
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import coil3.ImageLoader
import coil3.compose.AsyncImage
import coil3.network.NetworkHeaders
import coil3.network.httpHeaders
import coil3.request.ImageRequest
import com.screwy.igloo.R
import com.screwy.igloo.media.MediaResolvers
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.net.IglooHostProvider
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.net.auth.AuthTokenProvider
import com.screwy.igloo.ui.theme.iglooColors
import kotlinx.coroutines.flow.flowOf
import org.koin.compose.koinInject

/**
 * Circular channel avatar.
 *
 * Resolves the avatar [MediaUri] from [MediaResolvers.avatarForChannel] for [channelId]
 * and renders Coil for Local/Remote, or a themed silhouette for Missing.
 */
@Composable
fun Avatar(
    channelId: String,
    size: Dp = 40.dp,
    modifier: Modifier = Modifier,
    onClick: (() -> Unit)? = null,
    fadeWhenRemoteOffline: Boolean = true,
    showPendingBadge: Boolean = true,
    initialUri: MediaUri? = null,
    remoteFallbackUrl: String? = null,
) {
    val resolvers: MediaResolvers = koinInject()
    val baseUrlProvider: ServerBaseUrlProvider = koinInject()
    val imageLoader: ImageLoader = koinInject()
    val fallbackInitialUri = initialUri
        ?: if (channelId.isEmpty() || baseUrlProvider.baseUrl().isBlank()) MediaUri.Missing
        else MediaUri.Remote(baseUrlProvider.baseUrl() + "/api/media/avatar/" + channelId)
    val uri by (if (channelId.isEmpty()) flowOf(MediaUri.Missing) else resolvers.avatarForChannelFlow(channelId))
        .collectAsState(initial = fallbackInitialUri)
    val explicitRemoteFallback = remoteFallbackUrl
        ?.trim()
        ?.takeIf { it.startsWith("https://") || it.startsWith("http://") }
    val displayUri = when {
        explicitRemoteFallback != null && uri.prefersExplicitAvatarFallback() -> MediaUri.Remote(explicitRemoteFallback)
        else -> uri
    }

    val colors = MaterialTheme.iglooColors
    val showBadge = showPendingBadge && isIglooRemoteOffline(displayUri)
    val backgroundColor = if (displayUri is MediaUri.Missing) colors.surfaceVariant else null
    val alphaValue = if (fadeWhenRemoteOffline) mediaAlpha(displayUri) else 1f
    val channelAvatarDescription = stringResource(R.string.content_description_channel_avatar)

    Box(
        modifier = modifier
            .size(size)
            .clip(CircleShape)
            .let { if (backgroundColor != null) it.background(backgroundColor) else it }
            .let { if (onClick != null) it.clickable(onClick = onClick) else it }
            .alpha(alphaValue),
        contentAlignment = Alignment.Center,
    ) {
        AvatarPlaceholder(size = size)
        when (val u = displayUri) {
            is MediaUri.Local -> AsyncImage(
                model = u.file,
                contentDescription = channelAvatarDescription,
                imageLoader = imageLoader,
                modifier = Modifier.size(size).clip(CircleShape),
                contentScale = ContentScale.Crop,
            )
            is MediaUri.Remote -> AsyncImage(
                model = rememberRemoteImageModel(u.url),
                contentDescription = channelAvatarDescription,
                imageLoader = imageLoader,
                modifier = Modifier.size(size).clip(CircleShape),
                contentScale = ContentScale.Crop,
            )
            is MediaUri.Missing -> Unit
        }
        if (showBadge) DownloadPendingBadge()
    }
}

private fun MediaUri.prefersExplicitAvatarFallback(): Boolean =
    this is MediaUri.Missing || (this is MediaUri.Remote && url.contains("/api/media/avatar/"))

@Composable
private fun AvatarPlaceholder(size: Dp) {
    val colors = MaterialTheme.iglooColors
    val channelAvatarDescription = stringResource(R.string.content_description_channel_avatar)
    Icon(
        imageVector = Icons.Default.Person,
        contentDescription = channelAvatarDescription,
        tint = colors.onSurfaceFaint,
        modifier = Modifier.size(size * 0.55f),
    )
}

@Composable
internal fun rememberRemoteImageModel(
    url: String,
    memoryCacheKey: String? = null,
    placeholderMemoryCacheKey: String? = null,
): Any {
    val context = LocalContext.current
    val authTokens: AuthTokenProvider = koinInject()
    val iglooHostProvider: IglooHostProvider = koinInject()
    val token = authTokens.bearerTokenSync()
    val iglooHost = iglooHostProvider.hostSync()
    return remember(url, token, iglooHost, memoryCacheKey, placeholderMemoryCacheKey) {
        buildRemoteImageModel(
            context = context,
            url = url,
            bearerToken = token,
            iglooHost = iglooHost,
            memoryCacheKey = memoryCacheKey,
            placeholderMemoryCacheKey = placeholderMemoryCacheKey,
        )
    }
}

internal fun buildRemoteImageModel(
    context: Context,
    url: String,
    bearerToken: String?,
    iglooHost: String,
    memoryCacheKey: String? = null,
    placeholderMemoryCacheKey: String? = null,
): Any {
    val authorizedToken = bearerToken?.takeIf { token ->
        token.isNotBlank() && shouldAuthorizeIglooImage(url, iglooHost)
    }
    if (authorizedToken == null && memoryCacheKey.isNullOrBlank() && placeholderMemoryCacheKey.isNullOrBlank()) {
        return url
    }
    val builder = ImageRequest.Builder(context).data(url)
    val authCacheKey = if (authorizedToken == null) null else "igloo-auth:$url"
    if (authorizedToken != null) {
        val headers = NetworkHeaders.Builder()
            .set("Authorization", authorizedToken)
            .build()
        builder.httpHeaders(headers)
        builder.diskCacheKey("igloo-auth:$url")
    }
    (memoryCacheKey ?: authCacheKey)?.takeIf { it.isNotBlank() }?.let(builder::memoryCacheKey)
    placeholderMemoryCacheKey?.takeIf { it.isNotBlank() }?.let(builder::placeholderMemoryCacheKey)
    return builder.build()
}

internal fun shouldAuthorizeIglooImage(url: String, iglooHost: String): Boolean {
    return isIglooServerUrl(url, iglooHost)
}
