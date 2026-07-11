package com.screwy.igloo.ui.component

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.calculatePan
import androidx.compose.foundation.gestures.calculateZoom
import androidx.compose.foundation.gestures.detectHorizontalDragGestures
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.gestures.detectVerticalDragGestures
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.VolumeOff
import androidx.compose.material.icons.automirrored.filled.VolumeUp
import androidx.compose.material.icons.automirrored.outlined.OpenInNew
import androidx.compose.material.icons.filled.Bookmark
import androidx.compose.material.icons.filled.BrokenImage
import androidx.compose.material.icons.filled.Favorite
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.outlined.BookmarkBorder
import androidx.compose.material.icons.outlined.FavoriteBorder
import androidx.compose.material.icons.outlined.Share
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
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
import androidx.compose.ui.text.style.TextOverflow
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
import kotlinx.coroutines.delay
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
    val parentMediaCount: Int,
    val parentIsTextOnly: Boolean = false,
    val authorHandle: String,
    val authorDisplayName: String,
    val authorChannelId: String,
    val bodyText: String = "",
    val quoteBodyText: String = "",
    val canonicalUrl: String = "",
    val quoteCanonicalUrl: String = "",
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
    initialVideoPositionMs: Long = 0L,
    isLiked: Boolean,
    isBookmarked: Boolean,
    onDismiss: () -> Unit,
    onBookmarkToggle: () -> Unit,
    onLikeToggle: () -> Unit,
    onAuthorClick: () -> Unit,
    onShare: (url: String) -> Unit = {},
    onOpenExternal: (url: String) -> Unit = {},
    modifier: Modifier = Modifier,
) {
    if (media.items.isEmpty()) return

    val density = LocalDensity.current
    val dismissThresholdPx = with(density) { 96.dp.toPx() }
    val initialPage = initialIndex.coerceIn(0, media.items.lastIndex)
    val pagerState = rememberPagerState(initialPage = initialPage, pageCount = { media.items.size })
    var dragY by remember { mutableFloatStateOf(0f) }
    var muted by remember(media, initialPage) { mutableStateOf(false) }
    var videoPositionMs by
        remember(media, initialPage) { mutableLongStateOf(initialVideoPositionMs) }
    var videoProgress by remember(media, initialPage) { mutableFloatStateOf(0f) }
    var videoSeekToFraction by
        remember(media, initialPage) { mutableStateOf<((Float) -> Unit)?>(null) }
    val currentItem by remember { derivedStateOf { media.items.getOrNull(pagerState.currentPage) } }
    val isVideoPage = currentItem is MediaItem.Video || currentItem is MediaItem.Gif
    val bodyText by remember {
        derivedStateOf {
            if (pagerState.currentPage < media.parentMediaCount) media.bodyText
            else media.quoteBodyText
        }
    }
    val canonicalUrl by remember {
        derivedStateOf {
            if (
                pagerState.currentPage < media.parentMediaCount || media.quoteCanonicalUrl.isBlank()
            ) {
                media.canonicalUrl
            } else {
                media.quoteCanonicalUrl
            }
        }
    }
    val dismiss = { onDismiss() }

    BackHandler(onBack = dismiss)

    LaunchedEffect(pagerState.currentPage, isVideoPage) {
        videoProgress = 0f
        videoSeekToFraction = null
    }

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
                            if (isSwipeDownDismiss(dragY, dismissThresholdPx)) dismiss()
                            dragY = 0f
                        },
                        onDragCancel = { dragY = 0f },
                    )
                }
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
                        pageIndex = page,
                        streamUri = item.streamUri,
                        posterUri = item.thumbnailUri,
                        active = active,
                        muted = muted,
                        loop = true,
                        initialPositionMs = if (page == initialPage) initialVideoPositionMs else 0L,
                        onPositionUpdate = { if (active) videoPositionMs = it },
                        onProgressUpdate = { if (active) videoProgress = it },
                        onSeekAvailable = { seek -> if (active) videoSeekToFraction = seek },
                    )
                is MediaItem.Gif ->
                    MediaVideoPage(
                        pageIndex = page,
                        streamUri = item.streamUri,
                        posterUri = MediaUri.Missing,
                        active = active,
                        muted = true,
                        loop = true,
                        initialPositionMs = if (page == initialPage) initialVideoPositionMs else 0L,
                        onPositionUpdate = { if (active) videoPositionMs = it },
                        onProgressUpdate = { if (active) videoProgress = it },
                        onSeekAvailable = { seek -> if (active) videoSeekToFraction = seek },
                    )
            }
        }

        OverlayTopBar(
            media = media,
            showMute = isVideoPage,
            muted = muted,
            onMuteToggle = { muted = !muted },
            onAuthorClick = onAuthorClick,
            modifier = Modifier.align(Alignment.TopCenter),
        )

        OverlayBottomBar(
            pageCount = media.items.size,
            currentPage = pagerState.currentPage,
            bodyText = bodyText,
            canonicalUrl = canonicalUrl,
            isLiked = isLiked,
            isBookmarked = isBookmarked,
            onOpenExternal = onOpenExternal,
            onShare = onShare,
            onLikeToggle = onLikeToggle,
            onBookmarkToggle = onBookmarkToggle,
            videoProgress = if (isVideoPage && videoSeekToFraction != null) videoProgress else null,
            onVideoSeek = videoSeekToFraction,
            modifier = Modifier.align(Alignment.BottomCenter),
        )
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
    pageIndex: Int,
    streamUri: MediaUri,
    posterUri: MediaUri,
    active: Boolean,
    muted: Boolean,
    loop: Boolean,
    initialPositionMs: Long,
    onPositionUpdate: (Long) -> Unit,
    onProgressUpdate: (Float) -> Unit,
    onSeekAvailable: (((Float) -> Unit)?) -> Unit,
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
    var durationMs by remember(player) { mutableLongStateOf(0L) }
    var positionMs by remember(player) { mutableLongStateOf(initialPositionMs) }
    val currentOnPositionUpdate by rememberUpdatedState(onPositionUpdate)
    val currentOnProgressUpdate by rememberUpdatedState(onProgressUpdate)
    val seekToFraction: ((Float) -> Unit)? =
        remember(player) {
            val current = player ?: return@remember null
            { fraction: Float ->
                val duration = current.duration
                if (duration > 0L) {
                    val bounded = fraction.coerceIn(0f, 1f)
                    val target = (bounded * duration).toLong()
                    current.seekTo(target)
                    positionMs = target
                    currentOnPositionUpdate(target)
                    currentOnProgressUpdate(bounded)
                }
            }
        }

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
            currentOnPositionUpdate(current.currentPosition)
            current.removeListener(listener)
            current.release()
        }
    }
    DisposableEffect(active, seekToFraction) {
        if (active) onSeekAvailable(seekToFraction)
        onDispose { if (active) onSeekAvailable(null) }
    }
    LaunchedEffect(player, muted) { player?.volume = if (muted) 0f else 1f }
    LaunchedEffect(player, active) {
        if (active) {
            player?.playWhenReady = true
        } else {
            player?.pause()
        }
    }
    LaunchedEffect(player, initialPositionMs) {
        if (initialPositionMs > 0L) player?.seekTo(initialPositionMs)
    }
    LaunchedEffect(player, isPlaying) {
        val current = player ?: return@LaunchedEffect
        while (true) {
            durationMs = current.duration.coerceAtLeast(0L)
            positionMs = current.currentPosition.coerceAtLeast(0L)
            currentOnPositionUpdate(positionMs)
            if (durationMs > 0L) {
                currentOnProgressUpdate(
                    (positionMs.toFloat() / durationMs.toFloat()).coerceIn(0f, 1f)
                )
            }
            delay(if (isPlaying) 250L else 500L)
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
private fun OverlayTopBar(
    media: MediaSet,
    showMute: Boolean,
    muted: Boolean,
    onMuteToggle: () -> Unit,
    onAuthorClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val muteLabel = stringResource(R.string.action_mute)
    val unmuteLabel = stringResource(R.string.action_unmute)
    val label =
        media.authorDisplayName.ifBlank {
            media.authorHandle.takeIf { it.isNotBlank() }?.let { "@$it" }.orEmpty()
        }
    Row(
        modifier = modifier.fillMaxWidth().padding(start = 14.dp, end = 6.dp, top = 22.dp),
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Row(
            modifier = Modifier.clickable(onClick = onAuthorClick),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Avatar(channelId = media.authorChannelId, size = 30.dp, onClick = onAuthorClick)
            Text(
                text = label,
                color = Color.White,
                style = MaterialTheme.typography.bodyMedium,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        if (showMute) {
            IconButton(onClick = onMuteToggle) {
                Icon(
                    imageVector =
                        if (muted) Icons.AutoMirrored.Filled.VolumeOff
                        else Icons.AutoMirrored.Filled.VolumeUp,
                    contentDescription = if (muted) unmuteLabel else muteLabel,
                    tint = Color.White,
                )
            }
        }
    }
}

@Composable
private fun OverlayBottomBar(
    pageCount: Int,
    currentPage: Int,
    bodyText: String,
    canonicalUrl: String,
    isLiked: Boolean,
    isBookmarked: Boolean,
    onOpenExternal: (String) -> Unit,
    onShare: (String) -> Unit,
    onLikeToggle: () -> Unit,
    onBookmarkToggle: () -> Unit,
    videoProgress: Float?,
    onVideoSeek: ((Float) -> Unit)?,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier =
            modifier
                .fillMaxWidth()
                .navigationBarsPadding()
                .padding(start = 10.dp, end = 10.dp, bottom = 10.dp)
    ) {
        if (pageCount > 1) {
            PageDots(pageCount, currentPage, Modifier.align(Alignment.CenterHorizontally))
        }
        if (bodyText.isNotBlank()) {
            Text(
                text = bodyText,
                color = Color.White,
                style = MaterialTheme.typography.bodyMedium,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.padding(horizontal = 6.dp, vertical = 8.dp),
            )
        }
        if (videoProgress != null && onVideoSeek != null) {
            VideoScrubber(
                progress = videoProgress,
                onSeek = onVideoSeek,
                modifier = Modifier.fillMaxWidth().padding(horizontal = 4.dp, vertical = 8.dp),
            )
        }
        MediaActionRow(
            canonicalUrl = canonicalUrl,
            isLiked = isLiked,
            isBookmarked = isBookmarked,
            onOpenExternal = onOpenExternal,
            onShare = onShare,
            onLikeToggle = onLikeToggle,
            onBookmarkToggle = onBookmarkToggle,
        )
    }
}

@Composable
private fun MediaActionRow(
    canonicalUrl: String,
    isLiked: Boolean,
    isBookmarked: Boolean,
    onOpenExternal: (String) -> Unit,
    onShare: (String) -> Unit,
    onLikeToggle: () -> Unit,
    onBookmarkToggle: () -> Unit,
) {
    val colors = MaterialTheme.iglooColors
    val openExternallyLabel = stringResource(R.string.action_open_externally)
    val shareLabel = stringResource(R.string.action_share)
    val likeLabel = stringResource(R.string.action_like)
    val unlikeLabel = stringResource(R.string.action_unlike)
    val bookmarkLabel = stringResource(R.string.action_bookmark)
    val removeBookmarkLabel = stringResource(R.string.action_remove_bookmark)
    val enabled = canonicalUrl.isNotBlank()
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically,
    ) {
        IconButton(onClick = { onOpenExternal(canonicalUrl) }, enabled = enabled) {
            Icon(
                imageVector = Icons.AutoMirrored.Outlined.OpenInNew,
                contentDescription = openExternallyLabel,
                tint = if (enabled) Color.White else Color.White.copy(alpha = 0.35f),
            )
        }
        IconButton(onClick = { onShare(canonicalUrl) }, enabled = enabled) {
            Icon(
                imageVector = Icons.Outlined.Share,
                contentDescription = shareLabel,
                tint = if (enabled) Color.White else Color.White.copy(alpha = 0.35f),
            )
        }
        IconButton(onClick = onLikeToggle) {
            Icon(
                imageVector = if (isLiked) Icons.Filled.Favorite else Icons.Outlined.FavoriteBorder,
                contentDescription = if (isLiked) unlikeLabel else likeLabel,
                tint = if (isLiked) colors.primary else Color.White,
            )
        }
        IconButton(onClick = onBookmarkToggle) {
            Icon(
                imageVector =
                    if (isBookmarked) Icons.Filled.Bookmark else Icons.Outlined.BookmarkBorder,
                contentDescription = if (isBookmarked) removeBookmarkLabel else bookmarkLabel,
                tint = if (isBookmarked) colors.primary else Color.White,
            )
        }
    }
}

@Composable
private fun PageDots(count: Int, current: Int, modifier: Modifier = Modifier) {
    val colors = MaterialTheme.iglooColors
    Row(
        modifier = modifier.padding(bottom = 4.dp),
        horizontalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        repeat(count) { index ->
            Box(
                modifier =
                    Modifier.size(6.dp)
                        .clip(CircleShape)
                        .background(
                            if (index == current) colors.primary
                            else Color.White.copy(alpha = 0.38f)
                        )
            )
        }
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

@Composable
private fun VideoScrubber(progress: Float, onSeek: (Float) -> Unit, modifier: Modifier = Modifier) {
    val colors = MaterialTheme.iglooColors
    var widthPx by remember { mutableIntStateOf(1) }
    var dragProgress by remember { mutableFloatStateOf(progress.coerceIn(0f, 1f)) }
    var isDragging by remember { mutableStateOf(false) }
    val shownProgress = if (isDragging) dragProgress else progress.coerceIn(0f, 1f)

    Box(
        modifier =
            modifier
                .fillMaxWidth()
                .height(40.dp)
                .onSizeChanged { widthPx = it.width.coerceAtLeast(1) }
                .pointerInput(onSeek) {
                    detectTapGestures { offset -> onSeek((offset.x / widthPx).coerceIn(0f, 1f)) }
                }
                .pointerInput(onSeek) {
                    detectHorizontalDragGestures(
                        onDragStart = { offset ->
                            isDragging = true
                            dragProgress = (offset.x / widthPx).coerceIn(0f, 1f)
                            onSeek(dragProgress)
                        },
                        onHorizontalDrag = { _, delta ->
                            dragProgress = (dragProgress + delta / widthPx).coerceIn(0f, 1f)
                            onSeek(dragProgress)
                        },
                        onDragEnd = {
                            onSeek(dragProgress)
                            isDragging = false
                        },
                        onDragCancel = { isDragging = false },
                    )
                },
        contentAlignment = Alignment.CenterStart,
    ) {
        Box(
            modifier =
                Modifier.fillMaxWidth()
                    .height(5.dp)
                    .clip(CircleShape)
                    .background(Color.White.copy(alpha = 0.42f))
        )
        Box(
            modifier =
                Modifier.fillMaxWidth(shownProgress)
                    .height(5.dp)
                    .clip(CircleShape)
                    .background(colors.primary)
        )
    }
}
