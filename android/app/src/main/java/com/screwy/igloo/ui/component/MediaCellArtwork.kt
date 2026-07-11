package com.screwy.igloo.ui.component

import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.BrokenImage
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import coil3.network.NetworkHeaders
import coil3.network.httpHeaders
import coil3.request.ImageRequest
import coil3.compose.AsyncImage
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.ui.theme.iglooColors

@Composable
internal fun BoxScope.MediaCellArtwork(
    thumbnailUri: MediaUri?,
    contentDescription: String?,
    modifier: Modifier = Modifier.fillMaxSize(),
    contentScale: ContentScale = ContentScale.Crop,
    missingIconSize: Dp = 28.dp,
    artworkAlpha: Float = 1f,
    showMissingWhenNull: Boolean = false,
) {
    when (thumbnailUri) {
        is MediaUri.Local -> AsyncImage(
            model = rememberMediaImageModel(
                uri = thumbnailUri,
                memoryCacheKey = mediaImageMemoryCacheKey(thumbnailUri),
            ),
            contentDescription = contentDescription,
            modifier = modifier.alpha(artworkAlpha),
            contentScale = contentScale,
        )
        is MediaUri.Remote -> AsyncImage(
            model = rememberMediaImageModel(
                uri = thumbnailUri,
                memoryCacheKey = mediaImageMemoryCacheKey(thumbnailUri),
            ),
            contentDescription = contentDescription,
            modifier = modifier.alpha(artworkAlpha),
            contentScale = contentScale,
        )
        is MediaUri.Missing -> MissingMediaCellIcon(contentDescription, missingIconSize, artworkAlpha)
        null -> if (showMissingWhenNull) {
            MissingMediaCellIcon(contentDescription, missingIconSize, artworkAlpha)
        }
    }
}

internal fun mediaImageMemoryCacheKey(uri: MediaUri): String? =
    when (uri) {
        is MediaUri.Local -> "igloo-media-thumb:local:${uri.file.absolutePath}"
        is MediaUri.Remote -> uri.url.takeIf { it.isNotBlank() }?.let { "igloo-media-thumb:remote:$it" }
        is MediaUri.Missing -> null
    }

internal fun buildMediaImageRequest(
    context: android.content.Context,
    uri: MediaUri,
    bearerToken: String?,
    iglooHost: String,
    widthPx: Int,
    heightPx: Int,
): ImageRequest? {
    val data = when (uri) {
        is MediaUri.Local -> uri.file
        is MediaUri.Remote -> uri.url.takeIf { it.isNotBlank() } ?: return null
        is MediaUri.Missing -> return null
    }
    val builder = ImageRequest.Builder(context)
        .data(data)
        .apply {
            mediaImageMemoryCacheKey(uri)?.let(::memoryCacheKey)
            if (widthPx > 0 && heightPx > 0) {
                size(widthPx, heightPx)
            }
        }
    if (uri is MediaUri.Remote && shouldAuthorizeIglooImage(uri.url, iglooHost) && !bearerToken.isNullOrBlank()) {
        val authCacheKey = "igloo-auth:${uri.url}"
        builder.httpHeaders(
            NetworkHeaders.Builder()
                .set("Authorization", bearerToken)
                .build(),
        )
        builder.diskCacheKey(authCacheKey)
        if (mediaImageMemoryCacheKey(uri) == null) {
            builder.memoryCacheKey(authCacheKey)
        }
    }
    return builder.build()
}

@Composable
internal fun rememberMediaImageModel(
    uri: MediaUri,
    memoryCacheKey: String? = null,
    placeholderMemoryCacheKey: String? = null,
): Any? {
    val context = LocalContext.current
    return when (uri) {
        is MediaUri.Local -> remember(
            context,
            uri.file,
            memoryCacheKey,
            placeholderMemoryCacheKey,
        ) {
            ImageRequest.Builder(context)
                .data(uri.file)
                .apply {
                    memoryCacheKey?.takeIf { it.isNotBlank() }?.let(::memoryCacheKey)
                    placeholderMemoryCacheKey?.takeIf { it.isNotBlank() }?.let(::placeholderMemoryCacheKey)
                }
                .build()
        }
        is MediaUri.Remote -> rememberRemoteImageModel(
            url = uri.url,
            memoryCacheKey = memoryCacheKey,
            placeholderMemoryCacheKey = placeholderMemoryCacheKey,
        )
        is MediaUri.Missing -> null
    }
}

@Composable
private fun BoxScope.MissingMediaCellIcon(
    contentDescription: String?,
    missingIconSize: Dp,
    artworkAlpha: Float,
) {
    Icon(
        imageVector = Icons.Default.BrokenImage,
        contentDescription = contentDescription,
        tint = MaterialTheme.iglooColors.onSurfaceFaint,
        modifier = Modifier
            .align(Alignment.Center)
            .size(missingIconSize)
            .alpha(artworkAlpha),
    )
}
