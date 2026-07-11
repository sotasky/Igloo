package com.screwy.igloo.bookmarks

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items as gridItems
import androidx.compose.foundation.lazy.grid.rememberLazyGridState
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ArrowDropDown
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
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
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.ui.UiStateSwitch
import com.screwy.igloo.ui.component.Avatar
import com.screwy.igloo.ui.component.BookmarkSheet
import com.screwy.igloo.ui.component.MediaCellArtwork
import com.screwy.igloo.ui.component.MediaTypeBadge
import com.screwy.igloo.ui.component.ScrollToBottomFab
import com.screwy.igloo.ui.component.ScrollToTopFab
import com.screwy.igloo.ui.component.TimestampBadge
import com.screwy.igloo.ui.component.displayLabel
import com.screwy.igloo.ui.component.normalizeHandle
import com.screwy.igloo.ui.component.scrollArrowVisibility
import com.screwy.igloo.ui.nav.IglooNavigationSource
import com.screwy.igloo.ui.nav.WideVerticalGridMinCellWidthDp
import com.screwy.igloo.ui.nav.rememberIglooAdaptiveLayout
import com.screwy.igloo.ui.nav.rememberIglooNavigator
import com.screwy.igloo.ui.theme.iglooColors
import kotlinx.coroutines.launch
import org.koin.androidx.compose.koinViewModel
import org.koin.compose.koinInject

/**
 * Bookmarks tab: vertical thumbnail grid with a category-chip filter row on top.
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
    val items by vm.items.collectAsStateWithLifecycle()
    val categories by vm.categories.collectAsStateWithLifecycle()
    val bookmarkCategories by vm.bookmarkCategories.collectAsStateWithLifecycle()
    val counts by vm.counts.collectAsStateWithLifecycle()
    val labelCounts by vm.labelCounts.collectAsStateWithLifecycle()
    val selectedFilter by vm.selectedFilter.collectAsStateWithLifecycle()
    val uiState by vm.uiState.collectAsStateWithLifecycle()
    val pendingBookmark by vm.pendingBookmark.collectAsStateWithLifecycle()
    val gridState = rememberLazyGridState()
    val navigator = rememberIglooNavigator(navController)
    val adaptiveLayout = rememberIglooAdaptiveLayout()
    val gridCells = if (adaptiveLayout.isWide) {
        GridCells.Adaptive(WideVerticalGridMinCellWidthDp.dp)
    } else {
        GridCells.Fixed(3)
    }
    var labelPopupOpen by remember { mutableStateOf(false) }

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
                columns = gridCells,
                state = gridState,
                modifier = Modifier.fillMaxSize(),
                contentPadding = PaddingValues(horizontal = 8.dp, vertical = 8.dp),
                horizontalArrangement = Arrangement.spacedBy(6.dp),
                verticalArrangement = Arrangement.spacedBy(6.dp),
            ) {
                // Chip row spans the full 3-col width so the grid scrolls with its filter.
                item(span = { GridItemSpan(maxLineSpan) }) {
                    CategoryChipRow(
                        categories = categories,
                        counts = counts,
                        selected = selectedFilter,
                        onSelectAll = vm::selectAll,
                        onSelectCategory = { vm.selectCategory(it) },
                        onOpenLabels = { labelPopupOpen = true },
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
                                    playlistId = bookmarkPlaylistId(selectedFilter),
                                    videoId = item.bookmark.videoId,
                                    source = IglooNavigationSource.Bookmarks,
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

            if (labelPopupOpen) {
                BookmarkLabelPopup(
                    labels = labelCounts,
                    onSelect = { filter ->
                        when (filter) {
                            BookmarkFilter.All -> vm.selectAll()
                            is BookmarkFilter.Category -> vm.selectCategory(filter.categoryId)
                            is BookmarkFilter.Label -> vm.selectLabel(filter.label)
                            BookmarkFilter.NoLabel -> vm.selectNoLabel()
                        }
                        labelPopupOpen = false
                    },
                    onDismiss = { labelPopupOpen = false },
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
    selected: BookmarkFilter,
    onSelectAll: () -> Unit,
    onSelectCategory: (Long) -> Unit,
    onOpenLabels: () -> Unit,
) {
    val totalCount = counts.values.sum()
    val allLabel = stringResource(R.string.moments_label_all)
    val labelChipLabel = when (selected) {
        is BookmarkFilter.Label -> selected.label
        BookmarkFilter.NoLabel -> stringResource(R.string.bookmark_filter_no_label)
        else -> stringResource(R.string.bookmark_filter_labels)
    }
    val labelSelected = selected is BookmarkFilter.Label || selected == BookmarkFilter.NoLabel
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
                selected = selected == BookmarkFilter.All,
                onClick = onSelectAll,
            )
        }
        items(
            items = categories,
            key = { it.categoryId },
        ) { cat ->
            val count = counts[cat.categoryId] ?: 0
            CategoryChip(
                label = if (count > 0) "${cat.name} ($count)" else cat.name,
                selected = selected == BookmarkFilter.Category(cat.categoryId),
                onClick = { onSelectCategory(cat.categoryId) },
            )
        }
        item(key = "__labels__") {
            CategoryChip(
                label = labelChipLabel,
                selected = labelSelected,
                onClick = onOpenLabels,
                trailingIcon = {
                    Icon(
                        imageVector = Icons.Default.ArrowDropDown,
                        contentDescription = null,
                        modifier = Modifier.size(18.dp),
                    )
                },
            )
        }
    }
}

@Composable
private fun CategoryChip(
    label: String,
    selected: Boolean,
    onClick: () -> Unit,
    trailingIcon: (@Composable () -> Unit)? = null,
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
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(2.dp),
        ) {
            Text(
                text = label,
                style = MaterialTheme.typography.labelMedium,
                color = if (selected) colors.primary else colors.onSurface,
                fontWeight = if (selected) FontWeight.SemiBold else FontWeight.Normal,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            trailingIcon?.invoke()
        }
    }
}

@Composable
private fun BookmarkLabelPopup(
    labels: List<BookmarkLabelCount>,
    onSelect: (BookmarkFilter) -> Unit,
    onDismiss: () -> Unit,
) {
    val colors = MaterialTheme.iglooColors
    var query by remember { mutableStateOf("") }
    val noLabelText = stringResource(R.string.bookmark_filter_no_label)
    val filteredLabels = remember(labels, query, noLabelText) {
        filterBookmarkLabelCounts(labels, query, noLabelText)
    }
    val backdropInteraction = remember { MutableInteractionSource() }
    val panelInteraction = remember { MutableInteractionSource() }

    BackHandler(onBack = onDismiss)

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(colors.overlayDim.copy(alpha = 0.28f))
            .clickable(
                interactionSource = backdropInteraction,
                indication = null,
                onClick = onDismiss,
            )
            .padding(horizontal = 16.dp, vertical = 72.dp),
    ) {
        Surface(
            color = colors.surface,
            shape = RoundedCornerShape(16.dp),
            tonalElevation = 8.dp,
            shadowElevation = 12.dp,
            modifier = Modifier
                .align(Alignment.TopCenter)
                .widthIn(max = 420.dp)
                .fillMaxWidth()
                .clickable(
                    interactionSource = panelInteraction,
                    indication = null,
                    onClick = {},
                ),
        ) {
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(14.dp),
                verticalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                OutlinedTextField(
                    value = query,
                    onValueChange = { query = it },
                    placeholder = { Text(stringResource(R.string.bookmark_filter_search_labels)) },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                LazyColumn(
                    modifier = Modifier.heightIn(max = 360.dp),
                ) {
                    items(
                        items = filteredLabels,
                        key = { it.label ?: "__no_label__" },
                    ) { row ->
                        BookmarkLabelRow(
                            row = row,
                            onClick = {
                                val label = row.label
                                if (label == null) {
                                    onSelect(BookmarkFilter.NoLabel)
                                } else {
                                    onSelect(BookmarkFilter.Label(label))
                                }
                            },
                        )
                    }
                    if (filteredLabels.isEmpty()) {
                        item(key = "__empty__") {
                            Text(
                                text = stringResource(R.string.bookmark_filter_no_labels_found),
                                style = MaterialTheme.typography.bodyMedium,
                                color = colors.onSurfaceMuted,
                                modifier = Modifier.padding(horizontal = 4.dp, vertical = 12.dp),
                            )
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun BookmarkLabelRow(
    row: BookmarkLabelCount,
    onClick: () -> Unit,
) {
    val colors = MaterialTheme.iglooColors
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(8.dp))
            .clickable(onClick = onClick)
            .padding(horizontal = 10.dp, vertical = 11.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        Text(
            text = row.label ?: stringResource(R.string.bookmark_filter_no_label),
            style = MaterialTheme.typography.bodyMedium,
            color = colors.onSurface,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
        Text(
            text = row.count.toString(),
            style = MaterialTheme.typography.bodyMedium,
            color = colors.onSurfaceMuted,
            fontWeight = FontWeight.Medium,
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

    val ownerKind = bookmarkOwnerKind(item)
    val mediaOwnerId = bookmarkMediaOwnerId(item)
    val channelId = item.resolvedChannelId
	val thumbUri by resolvers.thumbnailForPostFlow(mediaOwnerId, ownerKind)
		.collectAsState(initial = MediaUri.Missing)
	val displayThumbUri = thumbUri

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
