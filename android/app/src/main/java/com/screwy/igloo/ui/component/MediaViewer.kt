package com.screwy.igloo.ui.component

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.calculatePan
import androidx.compose.foundation.gestures.calculateZoom
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.gestures.detectVerticalDragGestures
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.BrokenImage
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.input.pointer.positionChanged
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.dp
import androidx.media3.common.MediaItem as Media3Item
import androidx.media3.common.Player
import coil3.compose.AsyncImage
import com.screwy.igloo.R
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.net.IglooHostProvider
import com.screwy.igloo.net.auth.AuthTokenProvider
import com.screwy.igloo.player.buildIglooPlayer
import com.screwy.igloo.ui.theme.iglooColors
import kotlin.math.abs
import org.koin.compose.koinInject

/** Shape for one media viewer page. */
sealed class MediaItem {
    data class Image(val uri: MediaUri, val aspectRatio: Float) : MediaItem()

    data class Video(val streamUri: MediaUri, val thumbnailUri: MediaUri, val aspectRatio: Float) :
        MediaItem()

    data class Gif(val streamUri: MediaUri, val aspectRatio: Float) : MediaItem()
}

data class MediaSet(
    val items: List<MediaItem>,
)

internal fun clampZoom(current: Float, scaleDelta: Float, min: Float = 1f, max: Float = 5f): Float =
    (current * scaleDelta).coerceIn(min, max)

internal fun isSwipeDownDismiss(deltaY: Float, thresholdPx: Float): Boolean = deltaY > thresholdPx

internal fun shouldHandleImageTransform(scale: Float, pointerCount: Int): Boolean =
    scale > 1f || pointerCount > 1

internal fun boundedZoomPanOffset(
    current: Offset,
    pan: Offset,
    scale: Float,
    size: IntSize,
): Offset {
    if (scale <= 1f || size.width <= 0 || size.height <= 0) return Offset.Zero
    val maxX = size.width * (scale - 1f) / 2f
    val maxY = size.height * (scale - 1f) / 2f
    return Offset(
        x = (current.x + pan.x).coerceIn(-maxX, maxX),
        y = (current.y + pan.y).coerceIn(-maxY, maxY),
    )
}

@Composable
fun MediaViewer(
    media: MediaSet,
    initialIndex: Int = 0,
    onDismiss: () -> Unit,
    modifier: Modifier = Modifier,
) {
    if (media.items.isEmpty()) return

    val density = LocalDensity.current
    val dismissThresholdPx = with(density) { 96.dp.toPx() }
    val initialPage = initialIndex.coerceIn(0, media.items.lastIndex)
    val pagerState = rememberPagerState(initialPage = initialPage, pageCount = { media.items.size })
    var dragY by remember { mutableFloatStateOf(0f) }

    BackHandler(onBack = onDismiss)

    Box(
        modifier =
            modifier
                .fillMaxSize()
                .background(Color.Black)
                .clickable(
                    interactionSource = remember { MutableInteractionSource() },
                    indication = null,
                    onClick = {},
                )
                .pointerInput(Unit) {
                    detectVerticalDragGestures(
                        onVerticalDrag = { _, amount -> dragY += amount },
                        onDragEnd = {
                            if (isSwipeDownDismiss(dragY, dismissThresholdPx)) onDismiss()
                            dragY = 0f
                        },
                        onDragCancel = { dragY = 0f },
                    )
                },
    ) {
        HorizontalPager(
            state = pagerState,
            beyondViewportPageCount = 1,
            modifier = Modifier.fillMaxSize(),
        ) { page ->
            val item = media.items[page]
            val active = pagerState.currentPage == page
            when (item) {
                is MediaItem.Image -> MediaImagePage(item)
                is MediaItem.Video ->
                    MediaVideoPage(
                        streamUri = item.streamUri,
                        posterUri = item.thumbnailUri,
                        active = active,
                        muted = false,
                        loop = true,
                    )
                is MediaItem.Gif ->
                    MediaVideoPage(
                        streamUri = item.streamUri,
                        posterUri = MediaUri.Missing,
                        active = active,
                        muted = true,
                        loop = true,
                    )
            }
        }
    }
}

@Composable
private fun MediaImagePage(item: MediaItem.Image) {
    val cacheKey = mediaImageMemoryCacheKey(item.uri)
    var scale by remember(item.uri) { mutableFloatStateOf(1f) }
    var offset by remember(item.uri) { mutableStateOf(Offset.Zero) }
    var size by remember(item.uri) { mutableStateOf(IntSize.Zero) }
    Box(
        modifier =
            Modifier.fillMaxSize().background(Color.Black).onSizeChanged { newSize ->
                size = newSize
            },
        contentAlignment = Alignment.Center,
    ) {
        when (val uri = item.uri) {
            is MediaUri.Local,
            is MediaUri.Remote ->
                AsyncImage(
                    model =
                        rememberMediaImageModel(
                            uri = uri,
                            memoryCacheKey = cacheKey,
                            placeholderMemoryCacheKey = cacheKey,
                        ),
                    contentDescription = null,
                    contentScale = ContentScale.Fit,
                    modifier =
                        Modifier.fillMaxSize()
                            .graphicsLayer {
                                scaleX = scale
                                scaleY = scale
                                translationX = offset.x
                                translationY = offset.y
                            }
                            .pointerInput(item.uri, size) {
                                awaitEachGesture {
                                    do {
                                        val event = awaitPointerEvent()
                                        val zoomChange = event.calculateZoom()
                                        val panChange = event.calculatePan()
                                        val pointerCount = event.changes.count { it.pressed }
                                        val transformActive =
                                            shouldHandleImageTransform(scale, pointerCount) ||
                                                abs(zoomChange - 1f) > 0.01f
                                        if (transformActive) {
                                            val nextScale = clampZoom(scale, zoomChange)
                                            scale = nextScale
                                            offset =
                                                boundedZoomPanOffset(
                                                    current =
                                                        if (nextScale <= 1f) Offset.Zero
                                                        else offset,
                                                    pan =
                                                        if (nextScale <= 1f) Offset.Zero
                                                        else panChange,
                                                    scale = nextScale,
                                                    size = size,
                                                )
                                            event.changes
                                                .filter {
                                                    it.positionChanged() ||
                                                        abs(zoomChange - 1f) > 0.01f
                                                }
                                                .forEach { it.consume() }
                                        }
                                    } while (event.changes.any { it.pressed })
                                }
                            }
                            .alpha(mediaAlpha(uri)),
                )
            is MediaUri.Missing -> MissingMediaIcon()
        }
        if (isIglooRemoteOffline(item.uri)) DownloadPendingBadge()
    }
}

@Composable
private fun MediaVideoPage(
    streamUri: MediaUri,
    posterUri: MediaUri,
    active: Boolean,
    muted: Boolean,
    loop: Boolean,
) {
    val context = LocalContext.current
    val authTokens: AuthTokenProvider = koinInject()
    val iglooHostProvider: IglooHostProvider = koinInject()
    val remoteOffline = isIglooRemoteOffline(streamUri)
    val player =
        remember(streamUri, loop, remoteOffline, authTokens.bearerTokenSync()) {
            if (streamUri is MediaUri.Missing || remoteOffline) {
                null
            } else {
                buildIglooPlayer(context, authTokens, iglooHostProvider).also { player ->
                    player.repeatMode = if (loop) Player.REPEAT_MODE_ALL else Player.REPEAT_MODE_OFF
                    val mediaItem =
                        when (streamUri) {
                            is MediaUri.Local ->
                                Media3Item.fromUri(streamUri.file.toURI().toString())
                            is MediaUri.Remote -> Media3Item.fromUri(streamUri.url)
                            is MediaUri.Missing -> null
                        }
                    mediaItem?.let {
                        player.setMediaItem(it)
                        player.prepare()
                    }
                    player.playWhenReady = active
                }
            }
        }
    var firstFrame by remember(player) { mutableStateOf(false) }
    var isPlaying by remember(player) { mutableStateOf(player?.isPlaying == true) }

    DisposableEffect(player) {
        val current = player ?: return@DisposableEffect onDispose {}
        val listener =
            object : Player.Listener {
                override fun onRenderedFirstFrame() {
                    firstFrame = true
                }

                override fun onIsPlayingChanged(playing: Boolean) {
                    isPlaying = playing
                }
            }
        current.addListener(listener)
        onDispose {
            current.removeListener(listener)
            current.release()
        }
    }
    LaunchedEffect(player, muted) { player?.volume = if (muted) 0f else 1f }
    LaunchedEffect(player, active) {
        if (active) {
            player?.playWhenReady = true
        } else {
            player?.pause()
        }
    }

    val playLabel = stringResource(R.string.action_play)
    Box(
        modifier =
            Modifier.fillMaxSize().background(Color.Black).pointerInput(player) {
                detectTapGestures {
                    val current = player ?: return@detectTapGestures
                    current.playWhenReady = !current.isPlaying
                }
            },
        contentAlignment = Alignment.Center,
    ) {
        if (player != null) {
            IglooVideoSurface(player = player, modifier = Modifier.fillMaxSize())
        }
        if (!firstFrame || player == null) {
            Poster(posterUri = posterUri, dimOffline = remoteOffline)
        }
        if (player == null || !isPlaying) {
            Icon(
                imageVector = Icons.Filled.PlayArrow,
                contentDescription = playLabel,
                tint = Color.White.copy(alpha = 0.86f),
                modifier = Modifier.size(72.dp),
            )
        }
        if (remoteOffline) DownloadPendingBadge()
    }
}

@Composable
private fun Poster(posterUri: MediaUri, dimOffline: Boolean) {
    val alphaValue = if (dimOffline) 0.55f else mediaAlpha(posterUri)
    when (posterUri) {
        is MediaUri.Local,
        is MediaUri.Remote ->
            AsyncImage(
                model =
                    rememberMediaImageModel(
                        uri = posterUri,
                        memoryCacheKey = mediaImageMemoryCacheKey(posterUri),
                        placeholderMemoryCacheKey = mediaImageMemoryCacheKey(posterUri),
                    ),
                contentDescription = null,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize().alpha(alphaValue),
            )
        is MediaUri.Missing ->
            Box(
                modifier = Modifier.fillMaxSize().background(Color.Black),
                contentAlignment = Alignment.Center,
            ) {
                Icon(
                    imageVector = Icons.Filled.PlayArrow,
                    contentDescription = null,
                    tint = MaterialTheme.iglooColors.onSurfaceFaint,
                    modifier = Modifier.size(52.dp),
                )
            }
    }
}

@Composable
private fun MissingMediaIcon() {
    Icon(
        imageVector = Icons.Default.BrokenImage,
        contentDescription = stringResource(R.string.content_description_missing_media),
        tint = MaterialTheme.iglooColors.onSurfaceFaint,
        modifier = Modifier.size(52.dp),
    )
}
