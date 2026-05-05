package com.screwy.igloo.ui.component

import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.media.OwnerKind

internal data class MediaCellModel(
    val mediaId: String,
    val ownerKind: OwnerKind,
    val thumbnailPath: String? = null,
    val mediaKind: String? = null,
    val slideCount: Int = 0,
    val fallbackThumbnailUri: MediaUri = MediaUri.Missing,
    val allowServerThumbnailFallback: Boolean = ownerKind != OwnerKind.Tweet,
) {
    fun initialThumbnailUri(baseUrl: String): MediaUri =
        resolveInitialMediaCellThumbnailUri(
            mediaId = mediaId,
            thumbnailPath = thumbnailPath,
            mediaKind = mediaKind,
            slideCount = slideCount,
            ownerKind = ownerKind,
            baseUrl = baseUrl,
            fallbackThumbnailUri = fallbackThumbnailUri,
            allowServerThumbnailFallback = allowServerThumbnailFallback,
        )
}

internal fun resolveInitialMediaCellThumbnailUri(
    mediaId: String,
    thumbnailPath: String?,
    mediaKind: String?,
    slideCount: Int,
    ownerKind: OwnerKind,
    baseUrl: String,
    fallbackThumbnailUri: MediaUri = MediaUri.Missing,
    allowServerThumbnailFallback: Boolean = ownerKind != OwnerKind.Tweet,
): MediaUri {
    if (fallbackThumbnailUri !is MediaUri.Missing) return fallbackThumbnailUri

    val path = thumbnailPath?.trim().orEmpty()
    if (path.isNotEmpty()) {
        return when {
            path.startsWith("http://") || path.startsWith("https://") -> MediaUri.Remote(path)
            path.startsWith("/") && baseUrl.isNotBlank() -> MediaUri.Remote(baseUrl.trimEnd('/') + path)
            else -> MediaUri.Missing
        }
    }

    if (!allowServerThumbnailFallback || baseUrl.isBlank()) return MediaUri.Missing
    val root = baseUrl.trimEnd('/')
    return when (momentMediaMode(mediaKind, slideCount)) {
        MomentMediaMode.Image,
        MomentMediaMode.Slideshow -> MediaUri.Remote("$root/api/media/slide/$mediaId/0")
        MomentMediaMode.Video -> MediaUri.Remote("$root/api/media/thumbnail/$mediaId")
    }
}

internal fun displayMediaCellThumbnail(
    resolvedThumbnailUri: MediaUri,
    fallbackThumbnailUri: MediaUri,
): MediaUri =
    if (resolvedThumbnailUri is MediaUri.Missing) fallbackThumbnailUri else resolvedThumbnailUri
