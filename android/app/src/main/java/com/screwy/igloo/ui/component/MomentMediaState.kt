package com.screwy.igloo.ui.component

import androidx.media3.common.Player
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.media.OwnerKind

private const val MomentWideVideoThreshold = 0.85f

internal enum class MomentMediaMode {
    Video,
    Image,
    Slideshow,
}

internal fun momentMediaMode(mediaKind: String?, slideCount: Int): MomentMediaMode {
    val normalizedKind = mediaKind?.trim()?.lowercase().orEmpty()
    return when {
        slideCount > 1 || normalizedKind == "slideshow" -> MomentMediaMode.Slideshow
        normalizedKind == "image" -> MomentMediaMode.Image
        else -> MomentMediaMode.Video
    }
}

internal fun momentSlideCount(mediaKind: String?, slideCount: Int): Int =
    when (momentMediaMode(mediaKind, slideCount)) {
        MomentMediaMode.Video -> 0
        MomentMediaMode.Image -> 1
        MomentMediaMode.Slideshow -> slideCount.coerceAtLeast(1)
    }

internal fun resolveInitialMomentThumbnailUri(
    videoId: String,
    thumbnailPath: String?,
    mediaKind: String?,
    slideCount: Int,
    ownerKind: OwnerKind,
    baseUrl: String,
    fallbackThumbnailUri: MediaUri = MediaUri.Missing,
): MediaUri =
    MediaCellModel(
        mediaId = videoId,
        ownerKind = ownerKind,
        thumbnailPath = thumbnailPath,
        mediaKind = mediaKind,
        slideCount = slideCount,
        fallbackThumbnailUri = fallbackThumbnailUri,
        allowServerThumbnailFallback = ownerKind != OwnerKind.Tweet,
    ).initialThumbnailUri(baseUrl)

internal fun shouldPlayMomentPage(
    isCurrentPage: Boolean,
    @Suppress("UNUSED_PARAMETER") isScrollInProgress: Boolean,
): Boolean = isCurrentPage

internal fun nextMomentPageForAutoSwipe(
    currentPage: Int,
    lastIndex: Int,
    autoSwipeEnabled: Boolean,
): Int? {
    if (!autoSwipeEnabled || currentPage !in 0..lastIndex) return null
    return if (currentPage < lastIndex) currentPage + 1 else 0
}

internal data class MomentVideoSurfaceState(
    val playerReady: Boolean = false,
    val isWide: Boolean = false,
    val isVertical: Boolean = false,
    val hasExpectedMedia: Boolean = false,
    val renderedFirstFrame: Boolean = false,
    val renderedFrameCount: Int = 0,
    val playerIsPlaying: Boolean = false,
    val playerPositionMs: Long = 0L,
    val videoWidth: Int = 0,
    val videoHeight: Int = 0,
)

internal fun isVerticalMomentVideo(width: Int, height: Int): Boolean =
    width > 0 && height > 0 && height > width

internal fun isWideMomentVideo(width: Int, height: Int): Boolean =
    width > 0 && height > 0 && width.toFloat() / height.toFloat() > MomentWideVideoThreshold

internal fun shouldShowMomentThumbnailFallback(
    remoteOffline: Boolean,
    surfaceState: MomentVideoSurfaceState,
): Boolean =
    remoteOffline || !shouldShowMomentVideoSurface(surfaceState)

internal fun shouldShowMomentVideoFallbackLayer(
    fallback: Boolean,
    sharedPlayer: Boolean,
    isActive: Boolean,
    pagerScrolling: Boolean,
    hasLoadedMedia: Boolean,
): Boolean =
    fallback && (!sharedPlayer || isActive || !pagerScrolling || !hasLoadedMedia)

internal fun shouldShowMomentVideoSurface(surfaceState: MomentVideoSurfaceState): Boolean =
    surfaceState.hasExpectedMedia && surfaceState.renderedFirstFrame

internal fun momentVideoSurfaceAlpha(surfaceState: MomentVideoSurfaceState): Float =
    if (shouldShowMomentVideoSurface(surfaceState)) 1f else 0f

internal fun shouldShowMomentsVideoProgressBar(
    isActive: Boolean,
    shouldPrepare: Boolean,
    streamUri: MediaUri,
    remoteOffline: Boolean,
    surfaceState: MomentVideoSurfaceState,
): Boolean =
    isActive &&
        shouldPrepare &&
        streamUri !is MediaUri.Missing &&
        !remoteOffline &&
        surfaceState.hasExpectedMedia &&
        surfaceState.renderedFirstFrame

internal fun shouldPrepareMomentVideoPlayer(
    isActive: Boolean,
    shouldPrepare: Boolean,
    sharedPlayer: Boolean,
): Boolean =
    shouldPrepare && (!sharedPlayer || isActive)

internal fun shouldMountMomentVideoSurface(
    isActive: Boolean,
    shouldPrepare: Boolean,
    sharedPlayer: Boolean,
    streamUri: MediaUri,
    remoteOffline: Boolean,
): Boolean =
    shouldPrepareMomentVideoPlayer(
        isActive = isActive,
        shouldPrepare = shouldPrepare,
        sharedPlayer = sharedPlayer,
    ) &&
        streamUri !is MediaUri.Missing &&
        !remoteOffline

internal fun momentVideoSurfaceStateFor(
    expectedMediaId: String,
    currentMediaId: String?,
    playbackState: Int,
    videoWidth: Int,
    videoHeight: Int,
    renderedFirstFrame: Boolean = false,
    renderedFrameCount: Int = 0,
    playerIsPlaying: Boolean = false,
    playerPositionMs: Long = 0L,
): MomentVideoSurfaceState {
    val matches = currentMediaId == expectedMediaId
    val matchingFrameCount = if (matches) renderedFrameCount else 0
    val hasFreshFrame = matchingFrameCount > 0
    val ready = matches && playbackState == Player.STATE_READY && hasFreshFrame
    return MomentVideoSurfaceState(
        playerReady = ready,
        isWide = ready && isWideMomentVideo(videoWidth, videoHeight),
        isVertical = matches && isVerticalMomentVideo(videoWidth, videoHeight),
        hasExpectedMedia = matches,
        renderedFirstFrame = matches && hasFreshFrame,
        renderedFrameCount = matchingFrameCount,
        playerIsPlaying = matches && playerIsPlaying,
        playerPositionMs = if (matches) playerPositionMs else 0L,
        videoWidth = if (matches) videoWidth else 0,
        videoHeight = if (matches) videoHeight else 0,
    )
}
