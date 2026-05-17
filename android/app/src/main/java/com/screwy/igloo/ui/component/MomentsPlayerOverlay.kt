package com.screwy.igloo.ui.component

import android.view.LayoutInflater
import android.view.ViewGroup
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectHorizontalDragGestures
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.PressInteraction
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.BrokenImage
import androidx.compose.material.icons.filled.Check
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Shadow
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.media3.common.MediaItem
import androidx.media3.common.Player
import androidx.media3.common.VideoSize
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.ui.AspectRatioFrameLayout
import androidx.media3.ui.PlayerView
import coil3.compose.AsyncImage
import com.screwy.igloo.R
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.perf.PerfProbe
import com.screwy.igloo.ui.theme.IglooColors
import com.screwy.igloo.ui.theme.iglooColors
import kotlinx.coroutines.delay

@Composable
internal fun MomentSlideDots(
    currentPage: Int,
    pageCount: Int,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier,
        horizontalArrangement = Arrangement.spacedBy(6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        repeat(pageCount) { page ->
            Box(
                modifier = Modifier
                    .size(if (page == currentPage) 8.dp else 6.dp)
                    .clip(CircleShape)
                    .background(
                        if (page == currentPage) Color.White
                        else Color.White.copy(alpha = 0.45f),
                    ),
            )
        }
    }
}

/**
 * Progress bar pinned to the bottom of a moments page. 4dp visible track,
 * 48dp touch target so users can easily hit it without occluding the
 * video. Single tap jumps to that position; horizontal drag scrubs. Poll cadence
 * is 150ms — fast enough to feel live, cheap enough to ignore on the main thread.
 */
@Composable
internal fun MomentsVideoProgressBar(
    player: ExoPlayer,
    modifier: Modifier = Modifier,
) {
    val colors = MaterialTheme.iglooColors
    var progress by remember(player) { mutableStateOf(0f) }
    var isDragging by remember(player) { mutableStateOf(false) }
    var dragProgress by remember(player) { mutableStateOf(0f) }
    var barWidthPx by remember(player) { mutableIntStateOf(1) }

    DisposableEffect(player) {
        fun fields() = mapOf("surface" to "moments", "cadence_ms" to 150)
        val key = PerfProbe.collectorStart("playback_poll", ::fields)
        onDispose { PerfProbe.collectorEnd("playback_poll", key, ::fields) }
    }

    LaunchedEffect(player) {
        while (true) {
            if (!isDragging) {
                val dur = player.duration
                if (dur > 0L) {
                    progress = (player.currentPosition.toFloat() / dur).coerceIn(0f, 1f)
                }
            }
            delay(150L)
        }
    }

    val shown = if (isDragging) dragProgress else progress
    Box(
        modifier = modifier
            .fillMaxWidth()
            .height(48.dp)
            .onSizeChanged { barWidthPx = it.width.coerceAtLeast(1) }
            .pointerInput(player) {
                detectTapGestures { offset ->
                    val frac = (offset.x / barWidthPx).coerceIn(0f, 1f)
                    val dur = player.duration
                    if (dur > 0L) {
                        player.seekTo((frac * dur).toLong())
                        progress = frac
                    }
                }
            }
            .pointerInput(player) {
                detectHorizontalDragGestures(
                    onDragStart = { offset ->
                        isDragging = true
                        dragProgress = (offset.x / barWidthPx).coerceIn(0f, 1f)
                    },
                    onHorizontalDrag = { _, delta ->
                        dragProgress = (dragProgress + delta / barWidthPx).coerceIn(0f, 1f)
                    },
                    onDragEnd = {
                        val dur = player.duration
                        if (dur > 0L) player.seekTo((dragProgress * dur).toLong())
                        progress = dragProgress
                        isDragging = false
                    },
                    onDragCancel = { isDragging = false },
                )
            },
        contentAlignment = Alignment.BottomStart,
    ) {
        // Background track
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .height(4.dp)
                .clip(RoundedCornerShape(percent = 50))
                .background(Color.White.copy(alpha = 0.24f)),
        )
        // Filled portion in the user's accent color
        Box(
            modifier = Modifier
                .fillMaxWidth(shown)
                .height(4.dp)
                .clip(RoundedCornerShape(percent = 50))
                .background(colors.primary),
        )
    }
}

/** Black drop shadow reused by overlay text + icons so they stay legible on any frame. */
internal val DropShadow = Shadow(
    color = Color.Black.copy(alpha = 0.65f),
    offset = Offset(0f, 1f),
    blurRadius = 4f,
)

private val CaptionShadow = Shadow(
    color = Color.Black.copy(alpha = 0.45f),
    offset = Offset(0f, 1f),
    blurRadius = 2f,
)

/**
 * One flat action icon for the right-edge column. No background pill / halo —
 * just icon plus shadow. Tint rules:
 *  - pressed       → [accent] (tactile flash while the finger is down)
 *  - [isActive]    → [accent] (persistent "this mode is on" indicator)
 *  - otherwise     → white
 */
@Composable
internal fun ShadowIcon(
    icon: androidx.compose.ui.graphics.vector.ImageVector,
    contentDescription: String,
    onClick: () -> Unit,
    isActive: Boolean = false,
    accent: Color = Color.White,
    enabled: Boolean = true,
) {
    val interactionSource = remember { MutableInteractionSource() }
    val isPressed by interactionSource.collectIsPressedAsState()
    val tint = when {
        !enabled -> Color.White.copy(alpha = 0.38f)
        isActive || isPressed -> accent
        else -> Color.White
    }
    val clickModifier = if (enabled) {
        Modifier.pointerInput(Unit) {
            detectTapGestures(
                onPress = { offset ->
                    val press = PressInteraction.Press(offset)
                    interactionSource.emit(press)
                    val released = tryAwaitRelease()
                    interactionSource.emit(
                        if (released) PressInteraction.Release(press)
                        else PressInteraction.Cancel(press),
                    )
                },
                onTap = { onClick() },
            )
        }
    } else {
        Modifier
    }

    Box(
        modifier = Modifier
            .size(44.dp)
            .then(clickModifier),
        contentAlignment = Alignment.Center,
    ) {
        Icon(
            imageVector = icon,
            contentDescription = contentDescription,
            tint = tint,
            modifier = Modifier.size(28.dp),
        )
    }
}

@Composable
internal fun MomentRailAvatar(
    item: MomentItem,
    onChannelClick: (channelId: String) -> Unit,
    onStoryClick: (channelId: String, firstVideoId: String) -> Unit,
    onFollowChannel: (channelId: String) -> Unit,
    onRequestUnfollowChannel: (MomentItem) -> Unit,
    colors: IglooColors,
    modifier: Modifier = Modifier,
) {
    val accent = colors.primary
    val storyTarget = item.storyFirstVideoId.takeIf {
        it.isNotBlank() && item.storyRingState != StoryRingState.None
    }
    Box(
        modifier = modifier.size(50.dp),
        contentAlignment = Alignment.Center,
    ) {
        Avatar(
            channelId = item.channelId,
            size = 44.dp,
            modifier = Modifier.storyRingBorder(item.storyRingState, colors),
            onClick = {
                if (storyTarget != null) {
                    onStoryClick(item.channelId, storyTarget)
                } else {
                    onChannelClick(item.channelId)
                }
            },
            showPendingBadge = false,
        )
        val badgeModifier = Modifier
            .align(Alignment.BottomCenter)
            .size(18.dp)
            .clip(CircleShape)
            .background(if (item.isAuthorFollowed) Color.White else accent)
            .border(2.dp, Color.Black.copy(alpha = 0.70f), CircleShape)
            .clickable {
                if (item.isAuthorFollowed) {
                    onRequestUnfollowChannel(item)
                } else {
                    onFollowChannel(item.channelId)
                }
            }
        Box(
            modifier = badgeModifier,
            contentAlignment = Alignment.Center,
        ) {
            val followingLabel = stringResource(R.string.action_following)
            val followLabel = stringResource(R.string.action_follow)
            Icon(
                imageVector = if (item.isAuthorFollowed) Icons.Filled.Check else Icons.Filled.Add,
                contentDescription = if (item.isAuthorFollowed) followingLabel else followLabel,
                tint = if (item.isAuthorFollowed) accent else Color.White,
                modifier = Modifier.size(12.dp),
            )
        }
    }
}

@Composable
internal fun CollapsedDescription(
    item: MomentItem,
    expanded: Boolean,
    onMentionClick: (String) -> Unit,
    onChannelClick: (String) -> Unit,
    onExpandedChange: (Boolean) -> Unit,
    modifier: Modifier = Modifier,
) {
    val linkColor = MaterialTheme.iglooColors.primary
    val uriHandler = LocalUriHandler.current
    val collapsedDescription = remember(item.description) {
        collapseMomentCaptionWhitespace(item.description)
    }
    var descriptionCanExpand by remember(item.videoId, collapsedDescription) { mutableStateOf(false) }
    val expandedHorizontalPadding = if (expanded) 8.dp else 0.dp
    Column(
        modifier = modifier
            .fillMaxWidth()
            .padding(
                start = momentCollapsedCaptionStartPaddingDp().dp,
                end = 16.dp,
            )
            .background(
                color = momentCaptionBackgroundColor(expanded),
                shape = RoundedCornerShape(8.dp),
            )
            .clickable(enabled = expanded) { onExpandedChange(false) },
    ) {
        Column(
            modifier = Modifier.padding(
                start = expandedHorizontalPadding,
                top = 8.dp,
                end = expandedHorizontalPadding,
                bottom = MomentCollapsedCaptionBottomPadding,
            ),
            verticalArrangement = Arrangement.spacedBy(3.dp),
        ) {
            momentRepostLabel(item)?.let { label ->
                Text(
                    text = label,
                    style = MaterialTheme.typography.labelSmall.copy(
                        fontWeight = FontWeight.SemiBold,
                        shadow = CaptionShadow,
                    ),
                    color = Color.White,
                )
            }
            val timestamp = localizedRelativeTime(item.publishedAt)
            if (item.publishedAt > 0L && timestamp.isNotEmpty()) {
                Text(
                    text = timestamp,
                    style = MaterialTheme.typography.labelSmall.copy(shadow = CaptionShadow),
                    color = Color.White.copy(alpha = 0.70f),
                )
            }
            Text(
                text = momentAuthorLabel(item),
                style = MaterialTheme.typography.titleMedium.copy(
                    fontWeight = FontWeight.Bold,
                    shadow = CaptionShadow,
                ),
                color = Color.White,
                modifier = Modifier.clickable { onChannelClick(item.channelId) },
            )
            if (collapsedDescription.isNotBlank()) {
                AtMentionText(
                    text = collapsedDescription,
                    onMentionClick = onMentionClick,
                    onUrlClick = uriHandler::openUri,
                    maxLines = momentCaptionDescriptionMaxLines(expanded),
                    overflow = TextOverflow.Ellipsis,
                    style = MaterialTheme.typography.bodyMedium.copy(
                        color = Color.White,
                        shadow = CaptionShadow,
                    ),
                    mentionColorOverride = linkColor,
                    urlColorOverride = linkColor,
                    onPlainTextClick = {
                        onExpandedChange(
                            momentCaptionExpandedAfterPlainTextClick(
                                expanded = expanded,
                                descriptionCanExpand = descriptionCanExpand,
                            ),
                        )
                    },
                    onTextLayout = { layout ->
                        if (!expanded) descriptionCanExpand = layout.hasVisualOverflow
                    },
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        }
    }
}

private val MomentCaptionWhitespace = Regex("\\s+")
private val MomentCollapsedCaptionBottomPadding = 4.dp

private fun collapseMomentCaptionWhitespace(text: String): String =
    text.replace(MomentCaptionWhitespace, " ").trim()

@androidx.annotation.OptIn(markerClass = [UnstableApi::class])
internal fun createMomentPlayerView(context: android.content.Context): PlayerView =
    (LayoutInflater.from(context).inflate(R.layout.moment_player_view, null) as PlayerView).apply {
        setBackgroundColor(android.graphics.Color.BLACK)
        setShutterBackgroundColor(android.graphics.Color.BLACK)
        setKeepContentOnPlayerReset(false)
        resizeMode = AspectRatioFrameLayout.RESIZE_MODE_ZOOM
    }

@androidx.annotation.OptIn(markerClass = [UnstableApi::class])
@Composable
internal fun VideoSurface(
    player: ExoPlayer,
    mediaKey: String,
    pageIndex: Int,
    onStateChange: (MomentVideoSurfaceState) -> Unit,
    sharedPlayerView: PlayerView? = null,
    modifier: Modifier = Modifier,
) {
    val context = LocalContext.current
    var surfaceState by remember(player, mediaKey) { mutableStateOf(MomentVideoSurfaceState()) }
    DisposableEffect(player, mediaKey) {
        var renderedFrameMediaId: String? = null
        var renderedFrameCount = 0
        fun currentSurfaceState(): MomentVideoSurfaceState {
            val size = player.videoSize
            val currentMediaId = player.currentMediaItem?.mediaId
            val matchingFrameCount = if (renderedFrameMediaId == currentMediaId) renderedFrameCount else 0
            return momentVideoSurfaceStateFor(
                expectedMediaId = mediaKey,
                currentMediaId = currentMediaId,
                playbackState = player.playbackState,
                videoWidth = size.width,
                videoHeight = size.height,
                renderedFrameCount = matchingFrameCount,
                playerIsPlaying = player.isPlaying,
                playerPositionMs = player.currentPosition,
            )
        }
        fun publish() {
            val next = currentSurfaceState()
            surfaceState = next
            onStateChange(next)
            PerfProbe.log(
                event = "moments_surface_state",
            ) {
                mapOf(
                    "page" to pageIndex,
                    "video" to momentDebugHash(mediaKey),
                    "current" to momentDebugHash(player.currentMediaItem?.mediaId),
                    "state" to player.playbackState.momentPlayerStateDebugName(),
                    "expected" to next.hasExpectedMedia,
                    "first_frame" to next.renderedFirstFrame,
                    "frames" to next.renderedFrameCount,
                    "playing" to next.playerIsPlaying,
                    "position_ms" to next.playerPositionMs,
                    "size" to "${next.videoWidth}x${next.videoHeight}",
                    "player" to Integer.toHexString(System.identityHashCode(player)),
                )
            }
        }
        publish()
        val listener = object : Player.Listener {
            override fun onMediaItemTransition(mediaItem: MediaItem?, reason: Int) {
                renderedFrameMediaId = mediaItem?.mediaId
                renderedFrameCount = 0
                publish()
            }

            override fun onVideoSizeChanged(videoSize: VideoSize) {
                publish()
            }

            override fun onPlaybackStateChanged(playbackState: Int) {
                if (shouldClearMomentRenderedFrame(playbackState)) {
                    renderedFrameCount = 0
                    renderedFrameMediaId = player.currentMediaItem?.mediaId
                }
                publish()
            }

            override fun onIsPlayingChanged(isPlaying: Boolean) {
                publish()
            }

            override fun onRenderedFirstFrame() {
                val currentMediaId = player.currentMediaItem?.mediaId.orEmpty()
                if (currentMediaId != mediaKey) return
                if (renderedFrameMediaId != currentMediaId) {
                    renderedFrameMediaId = currentMediaId
                    renderedFrameCount = 0
                }
                renderedFrameCount = (renderedFrameCount + 1).coerceAtLeast(1)
                publish()
            }
        }
        player.addListener(listener)
        onDispose {
            player.removeListener(listener)
            onStateChange(MomentVideoSurfaceState())
        }
    }

    val playerView = sharedPlayerView ?: remember(mediaKey) { createMomentPlayerView(context) }

    AndroidView(
        factory = {
            if (playerView.player != null) playerView.player = null
            (playerView.parent as? ViewGroup)?.removeView(playerView)
            playerView.alpha = 0f
            playerView
        },
        update = { view ->
            val attaching = view.player !== player
            if (attaching) {
                PerfProbe.log(
                    event = "moments_surface_attach",
                ) {
                    mapOf(
                        "page" to pageIndex,
                        "video" to momentDebugHash(mediaKey),
                        "player" to Integer.toHexString(System.identityHashCode(player)),
                        "view" to Integer.toHexString(System.identityHashCode(view)),
                    )
                }
            }
            view.alpha = momentVideoSurfaceAlpha(surfaceState)
            if (view.player !== player) view.player = player
            view.setBackgroundColor(android.graphics.Color.BLACK)
            view.resizeMode = momentsVideoResizeMode(
                width = surfaceState.videoWidth,
                height = surfaceState.videoHeight,
            )
        },
        modifier = modifier.alpha(momentVideoSurfaceAlpha(surfaceState)),
    )

    DisposableEffect(player) {
        onDispose {
            if (sharedPlayerView == null) {
                playerView.player = null
            }
        }
    }
}

@androidx.annotation.OptIn(markerClass = [UnstableApi::class])
internal fun momentsVideoResizeMode(width: Int, height: Int): Int =
    if (width <= 0 || height <= 0 || isVerticalMomentVideo(width, height)) {
        AspectRatioFrameLayout.RESIZE_MODE_ZOOM
    } else {
        AspectRatioFrameLayout.RESIZE_MODE_FIT
    }

internal fun momentFitWidthContentScale(): ContentScale = ContentScale.Fit

internal fun momentVideoFallbackContentScale(): ContentScale = ContentScale.Crop

internal fun momentVideoSurfaceZIndex(): Float = 0f

internal fun momentVideoFallbackZIndex(): Float = 1f

internal fun shouldClearMomentRenderedFrame(playbackState: Int): Boolean =
    playbackState == Player.STATE_IDLE

/**
 * Fit-width thumbnail fallback rendered in place of the PlayerView when there's
 * no mountable stream. Two callers per §7 lines 1301-1306:
 *  - `streamUri is MediaUri.Missing` → `alphaOverride = null`, thumbnail's own
 *    `mediaAlpha()` decides fade (Remote+offline thumbnails fade; Local+Missing
 *    thumbnails stay full).
 *  - `isIglooRemoteOffline(streamUri)` → `alphaOverride = 0.55f`, because
 *    the fade decision is on the *stream* not the thumbnail.
 */
@Composable
internal fun BoxScope.ThumbnailFallback(
    thumbnailUri: MediaUri,
    alphaOverride: Float?,
    brokenIconTint: Color,
    contentScale: ContentScale = momentFitWidthContentScale(),
    modifier: Modifier = Modifier,
) {
    when (thumbnailUri) {
        is MediaUri.Local -> AsyncImage(
            model = thumbnailUri.file,
            contentDescription = null,
            modifier = modifier
                .fillMaxSize()
                .alpha(alphaOverride ?: mediaAlpha(thumbnailUri)),
            contentScale = contentScale,
        )
        is MediaUri.Remote -> AsyncImage(
            model = rememberRemoteImageModel(thumbnailUri.url),
            contentDescription = null,
            modifier = modifier
                .fillMaxSize()
                .alpha(alphaOverride ?: mediaAlpha(thumbnailUri)),
            contentScale = contentScale,
        )
        is MediaUri.Missing -> Icon(
            imageVector = Icons.Default.BrokenImage,
            contentDescription = stringResource(R.string.content_description_missing_media),
            tint = brokenIconTint,
            modifier = modifier.align(Alignment.Center),
        )
    }
}
