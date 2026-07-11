package com.screwy.igloo.ui.component

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectHorizontalDragGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.rememberLazyGridState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.screwy.igloo.R
import com.screwy.igloo.data.stripPlatformPrefix
import com.screwy.igloo.media.MediaResolvers
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.media.OwnerKind
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.ui.nav.WideVerticalGridMinCellWidthDp
import com.screwy.igloo.ui.nav.rememberIglooAdaptiveLayout
import com.screwy.igloo.ui.theme.iglooColors
import kotlinx.coroutines.launch
import org.koin.compose.koinInject

/**
 * UI-layer shape for one moments-grid cell. The DAO's
 * [com.screwy.igloo.data.entity.MomentItem] is the joined row shape; the
 * VM maps it into this item with a resolved [MediaUri].
 */
data class MomentThumbnailItem(
	val videoId: String,
	val channelId: String,
	val ownerKind: OwnerKind,
	val mediaKind: String? = null,
    val slideCount: Int = 0,
    val durationMs: Long,
    /** Source-of-truth publish time for the bottom-right [TimestampBadge]; 0 = unknown. */
    val publishedAt: Long = 0L,
    val isViewed: Boolean,
    val authorDisplayName: String? = null,
    /**
     * Optional source handle rendered with the [Avatar] in the bottom-left gradient
     * strip of each cell, matching the screenshot. Empty string → no label rendered.
     */
    val authorHandle: String = "",
)

/**
 * Grid of moment thumbnails with per-cell left-swipe to channel + optional
 * scroll-to-top/bottom FABs.
 */
@Composable
fun MomentsGrid(
    items: List<MomentThumbnailItem>,
    initialIndex: Int = 0,
    onItemClick: (videoId: String, startIndex: Int) -> Unit,
    onSwipeLeftOnItem: (channelId: String) -> Unit,
    showScrollFabs: Boolean = true,
    headerContent: (@Composable () -> Unit)? = null,
    columns: Int = 3,
    modifier: Modifier = Modifier,
) {
    val resolvers: MediaResolvers = koinInject()
    val adaptiveLayout = rememberIglooAdaptiveLayout()
    val gridCells = if (adaptiveLayout.isWide) {
        GridCells.Adaptive(WideVerticalGridMinCellWidthDp.dp)
    } else {
        GridCells.Fixed(columns)
    }
    val headerOffset = if (headerContent != null) 1 else 0
    val safeInitialIndex = initialIndex.coerceAtLeast(0)
    val initialGridIndex = if (safeInitialIndex > 0) safeInitialIndex + headerOffset else 0
    val gridState = rememberLazyGridState(initialFirstVisibleItemIndex = initialGridIndex)
    val scope = rememberCoroutineScope()
    var initialScrollApplied by remember(safeInitialIndex) { mutableStateOf(false) }
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

    LaunchedEffect(items.size, safeInitialIndex) {
        if (!initialScrollApplied && items.isNotEmpty()) {
            val targetIndex = if (safeInitialIndex > 0) {
                safeInitialIndex.coerceAtMost(items.lastIndex) + headerOffset
            } else {
                0
            }
            gridState.scrollToItem(targetIndex)
            initialScrollApplied = true
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
                ownerId = item.videoId,
                ownerKind = item.ownerKind,
                channelIds = setOf(item.channelId),
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
                key = { items[it].videoId },
                contentType = { "moment_cell" },
            ) { index ->
                MomentCell(
                    item = items[index],
                    onClick = { onItemClick(items[index].videoId, index) },
                    onSwipeLeft = { onSwipeLeftOnItem(items[index].channelId) },
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

@Composable
private fun MomentCell(
    item: MomentThumbnailItem,
    onClick: () -> Unit,
    onSwipeLeft: () -> Unit,
) {
    val resolvers: MediaResolvers = koinInject()
    val colors = MaterialTheme.iglooColors
    val density = LocalDensity.current
    val swipeThresholdPx = with(density) { (-80).dp.toPx() }

	val thumbnailUri by resolvers.thumbnailForPostFlow(item.videoId, item.ownerKind)
		.collectAsState(initial = MediaUri.Missing)
	val displayThumbnailUri = thumbnailUri

    val backgroundColor = if (displayThumbnailUri is MediaUri.Missing) {
        colors.surfaceVariant
    } else {
        colors.surface
    }

    Box(
        modifier = Modifier
            .fillMaxWidth()
            .aspectRatio(9f / 16f)
            .padding(1.dp)
            .clip(RoundedCornerShape(4.dp))
            .background(backgroundColor)
            .clickable(onClick = onClick)
            .alpha(mediaAlpha(displayThumbnailUri))
            .pointerInput(item.videoId) {
                var dragAccumulator = 0f
                detectHorizontalDragGestures(
                    onDragEnd = {
                        if (dragAccumulator < swipeThresholdPx) onSwipeLeft()
                        dragAccumulator = 0f
                    },
                    onDragCancel = { dragAccumulator = 0f },
                    onHorizontalDrag = { _, dragAmount ->
                        dragAccumulator += dragAmount
                    },
                )
            },
    ) {
        val viewedAlpha = if (item.isViewed) 0.70f else 1f
        MediaCellArtwork(
            thumbnailUri = displayThumbnailUri,
            contentDescription = stringResource(R.string.content_description_moment_thumbnail),
            artworkAlpha = viewedAlpha,
        )

        val normalizedHandle = normalizeHandle(item.authorHandle)
        val authorLabel = displayLabel(
            primary = item.authorDisplayName,
            handle = normalizedHandle,
            fallback = stripPlatformPrefix(item.channelId),
        )
        if (authorLabel.isNotEmpty()) {
            // Non-channel-scoped grids (Moments All) keep author identity at the
            // top-left so users can tell whose post they're scrubbing past.
            Row(
                modifier = Modifier
                    .align(Alignment.TopStart)
                    .fillMaxWidth()
                    .background(
                        Brush.verticalGradient(
                            colors = listOf(Color.Black.copy(alpha = 0.55f), Color.Transparent),
                        ),
                    )
                    .padding(horizontal = 6.dp, vertical = 4.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(4.dp),
            ) {
                Avatar(channelId = item.channelId, size = 16.dp)
                Text(
                    text = authorLabel,
                    style = MaterialTheme.typography.labelSmall.copy(fontWeight = FontWeight.Medium),
                    color = Color.White,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }

        // Shared overlays — same shape across all 3 vertical grids (Moments All,
        // TikTok/Instagram channel, Bookmarks). The faded thumbnail already signals
        // "not cached", so the cloud-download badge is intentionally omitted here.
        MediaTypeBadge(mediaTypeFor(item.mediaKind, item.slideCount))
        TimestampBadge(item.publishedAt)
    }
}
