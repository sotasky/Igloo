package com.screwy.igloo.ui.component

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.rememberLazyGridState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.screwy.igloo.R
import com.screwy.igloo.data.Dearrow
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.isYoutubeChannelId
import com.screwy.igloo.data.platformKeyFromChannelId
import com.screwy.igloo.data.stripPlatformPrefix
import com.screwy.igloo.data.entity.VideoGridItem
import com.screwy.igloo.data.entity.VideoEntity
import com.screwy.igloo.media.MediaResolvers
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.media.OwnerKind
import com.screwy.igloo.ui.nav.WideVideoGridMinCellWidthDp
import com.screwy.igloo.ui.nav.rememberIglooAdaptiveLayout
import com.screwy.igloo.ui.theme.iglooColors
import kotlinx.coroutines.launch
import org.koin.compose.koinInject

/**
 * YouTube-style video grid with watch-progress bar.
 * "VideoGrid".
 */
@Composable
fun VideoGrid(
    items: List<VideoGridItem>,
    columns: Int = 2,
    onVideoClick: (videoId: String) -> Unit,
    onVideoClickWithPoster: (videoId: String, posterUri: MediaUri) -> Unit = { videoId, _ -> onVideoClick(videoId) },
    onChannelClick: (channelId: String) -> Unit,
    headerContent: (@Composable () -> Unit)? = null,
    showScrollFabs: Boolean = false,
    modifier: Modifier = Modifier,
) {
    val resolvers: MediaResolvers = koinInject()
    val prefs: PreferencesRepo = koinInject()
    val dearrowMode by prefs.dearrowMode().collectAsState(initial = PreferencesRepo.Defaults.DEARROW_MODE)
    val adaptiveLayout = rememberIglooAdaptiveLayout()
    val gridCells = if (adaptiveLayout.isWide) {
        GridCells.Adaptive(WideVideoGridMinCellWidthDp.dp)
    } else {
        GridCells.Fixed(columns)
    }
    val gridState = rememberLazyGridState()
    val scope = rememberCoroutineScope()
    val headerOffset = if (headerContent != null) 1 else 0
    val scrollArrows by remember(items.size, showScrollFabs) {
        derivedStateOf {
            scrollArrowVisibility(
                showScrollFabs = showScrollFabs,
                itemCount = items.size,
                visibleItemCount = gridState.layoutInfo.visibleItemsInfo.size,
                firstVisibleItemIndex = gridState.firstVisibleItemIndex,
                firstVisibleItemScrollOffset = gridState.firstVisibleItemScrollOffset,
            )
        }
    }
    GridMediaPrefetchEffect(
        gridState = gridState,
        itemCount = items.size,
        headerOffset = headerOffset,
        resolvers = resolvers,
    ) { index ->
        val item = items.getOrNull(index) ?: return@GridMediaPrefetchEffect emptyList()
        listOf(
            MediaPrefetchTarget(
                ownerId = item.video.videoId,
                ownerKind = OwnerKind.YouTubeVideo,
                channelIds = setOf(item.video.channelId),
            ),
        )
    }

    Box(modifier = modifier.fillMaxSize()) {
        LazyVerticalGrid(
            columns = gridCells,
            state = gridState,
            modifier = Modifier.fillMaxSize(),
        ) {
            if (headerContent != null) {
                item(
                    key = "__header__",
                    span = { GridItemSpan(maxLineSpan) },
                    contentType = "channel_header",
                ) {
                    headerContent()
                }
            }
            items(
                count = items.size,
                key = { items[it].video.videoId },
                contentType = { "video_cell" },
            ) { index ->
                VideoCell(
                    item = items[index],
                    resolvers = resolvers,
                    dearrowMode = dearrowMode,
                    onVideoClick = { posterUri -> onVideoClickWithPoster(items[index].video.videoId, posterUri) },
                    onChannelClick = { onChannelClick(items[index].video.channelId) },
                    channelLabel = videoGridChannelLabel(items[index]),
                )
            }
        }

        when {
            scrollArrows.showTop -> ScrollToTopFab(
                onClick = { scope.launch { gridState.scrollToItem(0) } },
                modifier = Modifier
                    .align(Alignment.BottomEnd)
                    .padding(12.dp),
            )
            scrollArrows.showBottom -> ScrollToBottomFab(
                onClick = {
                    scope.launch { gridState.scrollToItem(items.lastIndex + headerOffset) }
                },
                modifier = Modifier
                    .align(Alignment.BottomEnd)
                    .padding(12.dp),
            )
        }
    }
}

internal fun videoGridChannelLabel(item: VideoGridItem): String {
    val fallback = stripPlatformPrefix(item.video.channelId)
    val platform = platformKeyFromChannelId(item.video.channelId)
    // YouTube source IDs are stable channel identifiers, not user-facing handles.
    // Keep them available for routing/fallback, but do not append them beside names.
    val channelHandle = if (isYoutubeChannelId(item.video.channelId)) {
        ""
    } else if (platform == "tiktok") {
        sequenceOf(item.channelSourceId, fallback)
            .map { platformHandleCandidate(platform, it) }
            .firstOrNull { it.isNotBlank() }
            .orEmpty()
    } else {
        normalizeHandle(item.channelSourceId ?: fallback)
    }
    val channelLabel = displayLabel(
        primary = item.channelName,
        handle = channelHandle,
        fallback = fallback,
    )
    return if (shouldShowHandle(channelLabel, channelHandle)) {
        "$channelLabel (@$channelHandle)"
    } else {
        channelLabel
    }
}

@Composable
private fun VideoCell(
    item: VideoGridItem,
    resolvers: MediaResolvers,
    dearrowMode: String = "off",
    onVideoClick: (posterUri: MediaUri) -> Unit,
    onChannelClick: () -> Unit,
    channelLabel: String,
) {
    val colors = MaterialTheme.iglooColors
    val video = item.video

    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(4.dp),
        verticalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        VideoThumbnail(
            videoId = video.videoId,
            resolvers = resolvers,
            durationLabel = videoDurationBadgeLabel(video),
            progress = progressFraction(item.playbackPosition, item.watchDuration),
            onClick = onVideoClick,
        )

        Text(
            text = Dearrow.resolveTitle(
                dearrowMode,
                video.title,
                video.dearrowTitle,
                video.dearrowTitleCasual,
                video.displayTitle,
                video.displayTitleCasual,
            )
                .ifEmpty { stringResource(R.string.common_untitled) },
            style = MaterialTheme.typography.bodyMedium,
            color = colors.onSurface,
            minLines = 2,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier
                .fillMaxWidth()
                .clickable { onVideoClick(MediaUri.Missing) },
        )

        ChannelAndTimeRow(
            channelId = video.channelId,
            channelLabel = channelLabel,
            publishedAtMs = video.publishedAt,
            onChannelClick = onChannelClick,
        )
    }
}

@Composable
private fun ChannelAndTimeRow(
    channelId: String,
    channelLabel: String,
    publishedAtMs: Long,
    onChannelClick: () -> Unit,
) {
    val colors = MaterialTheme.iglooColors
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier.fillMaxWidth(),
    ) {
        // Timestamp is card metadata, not part of the channel identity cluster.
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(6.dp),
            modifier = Modifier
                .weight(1f)
                .clickable(onClick = onChannelClick),
        ) {
            Avatar(channelId = channelId, size = 20.dp)
            Text(
                text = channelLabel,
                style = MaterialTheme.typography.bodySmall,
                color = colors.onSurfaceMuted,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f),
            )
        }
        Text(
            text = localizedRelativeTime(publishedAtMs),
            style = MaterialTheme.typography.bodySmall,
            color = colors.onSurfaceFaint,
            modifier = Modifier.padding(start = 6.dp),
        )
    }
}

internal fun videoDurationBadgeLabel(video: VideoEntity): String =
    video.durationLabel.trim()

@Composable
private fun VideoThumbnail(
    videoId: String,
    resolvers: MediaResolvers,
    durationLabel: String,
    progress: Float,
    onClick: (posterUri: MediaUri) -> Unit,
) {
    val colors = MaterialTheme.iglooColors
    val uri by resolvers.thumbnailForPostFlow(videoId, OwnerKind.YouTubeVideo)
        .collectAsState(initial = MediaUri.Missing)

    val backgroundColor = if (uri is MediaUri.Missing) colors.surfaceVariant else colors.surface
    val showBadge = isIglooRemoteOffline(uri)

    Box(
        modifier = Modifier
            .fillMaxWidth()
            .aspectRatio(16f / 9f)
            .clip(RoundedCornerShape(6.dp))
            .background(backgroundColor)
            .clickable { onClick(uri) }
            .alpha(mediaAlpha(uri)),
        contentAlignment = Alignment.Center,
    ) {
        MediaCellArtwork(
            thumbnailUri = uri,
            contentDescription = stringResource(R.string.content_description_video_thumbnail),
            missingIconSize = 32.dp,
        )

        if (durationLabel.isNotEmpty()) {
            Box(
                modifier = Modifier
                    .align(Alignment.BottomEnd)
                    .padding(4.dp)
                    .clip(RoundedCornerShape(3.dp))
                    .background(Color.Black.copy(alpha = 0.65f))
                    .padding(horizontal = 4.dp, vertical = 1.dp),
            ) {
                Text(
                    text = durationLabel,
                    style = MaterialTheme.typography.labelSmall,
                    color = Color.White,
                )
            }
        }

        if (progress > 0f) {
            Box(
                modifier = Modifier
                    .align(Alignment.BottomStart)
                    .fillMaxWidth(progress)
                    .height(2.dp)
                    .background(colors.primary.copy(alpha = 0.8f)),
            )
        }

        if (showBadge) DownloadPendingBadge()
    }
}
