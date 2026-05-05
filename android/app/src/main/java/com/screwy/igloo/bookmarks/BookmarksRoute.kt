package com.screwy.igloo.bookmarks

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items as gridItems
import androidx.compose.foundation.lazy.grid.rememberLazyGridState
import androidx.compose.foundation.lazy.items
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
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavController
import com.screwy.igloo.R
import com.screwy.igloo.data.stripPlatformPrefix
import com.screwy.igloo.data.entity.BookmarkItem
import com.screwy.igloo.media.MediaResolvers
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.ui.UiStateSwitch
import com.screwy.igloo.ui.component.Avatar
import com.screwy.igloo.ui.component.BookmarkSheet
import com.screwy.igloo.ui.component.MediaCellArtwork
import com.screwy.igloo.ui.component.MediaTypeBadge
import com.screwy.igloo.ui.component.ScrollToBottomFab
import com.screwy.igloo.ui.component.ScrollToTopFab
import com.screwy.igloo.ui.component.TimestampBadge
import com.screwy.igloo.ui.component.displayLabel
import com.screwy.igloo.ui.component.displayMediaCellThumbnail
import com.screwy.igloo.ui.component.normalizeHandle
import com.screwy.igloo.ui.component.scrollArrowVisibility
import com.screwy.igloo.ui.nav.IglooNavigationSource
import com.screwy.igloo.ui.nav.rememberIglooNavigator
import com.screwy.igloo.ui.theme.iglooColors
import kotlinx.coroutines.launch
import org.koin.androidx.compose.koinViewModel
import org.koin.compose.koinInject

/**
 * Bookmarks tab: a 3-column vertical grid with a category-chip filter row on top.
 * Each tile shows the post thumbnail with a small avatar bug and opens playable
 * items in the shared in-place moments-style overlay.
 */
@Composable
fun BookmarksRoute(
    navController: NavController,
    modifier: Modifier = Modifier,
) {
    val vm: BookmarksViewModel = koinViewModel()
    val scope = rememberCoroutineScope()
    val baseUrlProvider: ServerBaseUrlProvider = koinInject()
    val baseUrl = baseUrlProvider.baseUrl()
    val items by vm.items.collectAsStateWithLifecycle()
    val categories by vm.categories.collectAsStateWithLifecycle()
    val bookmarkCategories by vm.bookmarkCategories.collectAsStateWithLifecycle()
    val counts by vm.counts.collectAsStateWithLifecycle()
    val selectedCategory by vm.selectedCategory.collectAsStateWithLifecycle()
    val uiState by vm.uiState.collectAsStateWithLifecycle()
    val pendingBookmark by vm.pendingBookmark.collectAsStateWithLifecycle()
    val gridState = rememberLazyGridState()
    val navigator = rememberIglooNavigator(navController)

    val scrollFabsEnabled = items.isNotEmpty() && pendingBookmark == null
    val scrollArrows by remember(items.size, scrollFabsEnabled) {
        derivedStateOf {
            scrollArrowVisibility(
                showScrollFabs = scrollFabsEnabled,
                itemCount = items.size,
                visibleItemCount = gridState.layoutInfo.visibleItemsInfo.size,
                firstVisibleItemIndex = gridState.firstVisibleItemIndex,
                firstVisibleItemScrollOffset = gridState.firstVisibleItemScrollOffset,
            )
        }
    }

    UiStateSwitch(state = uiState, modifier = modifier) {
        Box(modifier = Modifier.fillMaxSize()) {
            LazyVerticalGrid(
                columns = GridCells.Fixed(3),
                state = gridState,
                modifier = Modifier.fillMaxSize(),
                contentPadding = PaddingValues(horizontal = 8.dp, vertical = 8.dp),
                horizontalArrangement = Arrangement.spacedBy(6.dp),
                verticalArrangement = Arrangement.spacedBy(6.dp),
            ) {
                // Chip row spans the full 3-col width so the grid scrolls with its filter.
                item(span = { GridItemSpan(3) }) {
                    CategoryChipRow(
                        categories = categories,
                        counts = counts,
                        selected = selectedCategory,
                        onSelect = { vm.selectCategory(it) },
                    )
                }
                gridItems(
                    items = items,
                    key = { it.bookmark.videoId },
                ) { item ->
                    BookmarkTile(
                        item = item,
                        authorDisplayName = bookmarkAuthorDisplayName(item),
                        authorHandle = bookmarkAuthorHandle(item, bookmarkChannelId(item)),
                        onAuthorClick = { channelId ->
                            navigator.openChannel(channelId, IglooNavigationSource.Bookmarks)
                        },
                        onClick = {
                            if (opensBookmarkInMomentsOverlay(item)) {
                                navigator.openShorts(
                                    playlistType = "bookmarks",
                                    playlistId = "_",
                                    videoId = item.bookmark.videoId,
                                    source = IglooNavigationSource.Bookmarks,
                                    posterUri = item.initialThumbnailUri(baseUrl),
                                )
                            }
                        },
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
                        scope.launch { gridState.scrollToItem(items.lastIndex + 1) }
                    },
                    modifier = Modifier
                        .align(Alignment.BottomEnd)
                        .padding(12.dp),
                )
            }

        }
    }

    pendingBookmark?.let { target ->
        BookmarkSheet(
            target = target,
            categories = bookmarkCategories,
            onConfirm = vm::confirmBookmark,
            onRemove = vm::removePendingBookmark,
            onDismiss = vm::dismissBookmarkSheet,
            onCreateCategory = vm::createCategory,
        )
    }
}

/** All + one chip per category, showing counts. Horizontal scroll for overflow. */
@Composable
private fun CategoryChipRow(
    categories: List<com.screwy.igloo.data.entity.BookmarkCategoryEntity>,
    counts: Map<Long, Int>,
    selected: Long?,
    onSelect: (Long?) -> Unit,
) {
    val totalCount = counts.values.sum()
    val allLabel = stringResource(R.string.moments_label_all)
    LazyRow(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 6.dp),
        contentPadding = PaddingValues(horizontal = 4.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        item(key = "__all__") {
            CategoryChip(
                label = if (totalCount > 0) {
                    stringResource(R.string.bookmarks_all_with_count, totalCount)
                } else {
                    allLabel
                },
                selected = selected == null,
                onClick = { onSelect(null) },
            )
        }
        items(
            items = categories,
            key = { it.categoryId },
        ) { cat ->
            val count = counts[cat.categoryId] ?: 0
            CategoryChip(
                label = if (count > 0) "${cat.name} ($count)" else cat.name,
                selected = selected == cat.categoryId,
                onClick = { onSelect(cat.categoryId) },
            )
        }
    }
}

@Composable
private fun CategoryChip(
    label: String,
    selected: Boolean,
    onClick: () -> Unit,
) {
    val colors = MaterialTheme.iglooColors
    val bg = if (selected) colors.primary.copy(alpha = 0.18f) else Color.Transparent
    val border = if (selected) colors.primary else colors.onSurfaceFaint
    Box(
        modifier = Modifier
            .clip(RoundedCornerShape(18.dp))
            .background(bg)
            .border(1.dp, border, RoundedCornerShape(18.dp))
            .clickable(onClick = onClick)
            .padding(horizontal = 14.dp, vertical = 8.dp),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.labelMedium,
            color = if (selected) colors.primary else colors.onSurface,
            fontWeight = if (selected) FontWeight.SemiBold else FontWeight.Normal,
        )
    }
}

/**
 * One grid cell — portrait tile, small avatar bug top-left, [MediaTypeBadge]
 * bottom-left, [TimestampBadge] bottom-right. Matches MomentsGrid / AllMomentsRoute /
 * TikTok-channel cell shape so the three vertical grids look identical to the user.
 */
@Composable
private fun BookmarkTile(
    item: BookmarkItem,
    authorDisplayName: String?,
    authorHandle: String,
    onAuthorClick: (String) -> Unit,
    onClick: () -> Unit,
) {
    val colors = MaterialTheme.iglooColors
    val resolvers: MediaResolvers = koinInject()
    val baseUrlProvider: ServerBaseUrlProvider = koinInject()

    val ownerKind = bookmarkOwnerKind(item)
    val channelId = item.resolvedChannelId
    val fallbackThumbUri = remember(item, ownerKind) {
        item.initialThumbnailUri(baseUrlProvider.baseUrl())
    }

    val thumbUri by resolvers.thumbnailForPostFlow(item.bookmark.videoId, ownerKind)
        .collectAsState(initial = fallbackThumbUri)
    val displayThumbUri = displayMediaCellThumbnail(thumbUri, fallbackThumbUri)

    Box(
        modifier = Modifier
            .fillMaxWidth()
            .aspectRatio(9f / 16f)
            .clip(RoundedCornerShape(8.dp))
            .background(colors.surfaceVariant)
            .clickable(onClick = onClick),
    ) {
        MediaCellArtwork(
            thumbnailUri = displayThumbUri,
            contentDescription = null,
        )

        val normalizedHandle = normalizeHandle(authorHandle)
        val authorLabel = displayLabel(
            primary = authorDisplayName,
            handle = normalizedHandle,
            fallback = channelId?.let(::stripPlatformPrefix),
        )
        if (channelId != null && authorLabel.isNotBlank()) {
            Row(
                modifier = Modifier
                    .align(Alignment.TopStart)
                    .fillMaxWidth()
                    .clickable { onAuthorClick(channelId) }
                    .background(
                        Brush.verticalGradient(
                            colors = listOf(Color.Black.copy(alpha = 0.55f), Color.Transparent),
                        ),
                    )
                    .padding(horizontal = 6.dp, vertical = 4.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(4.dp),
            ) {
                Avatar(channelId = channelId, size = 16.dp)
                Text(
                    text = authorLabel,
                    style = MaterialTheme.typography.labelSmall.copy(fontWeight = FontWeight.Medium),
                    color = Color.White,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }

        // Shared overlays — same shape across all 3 vertical grids.
        MediaTypeBadge(bookmarkMediaType(item))
        TimestampBadge(bookmarkPublishedAt(item))
    }
}
