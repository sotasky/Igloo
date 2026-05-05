package com.screwy.igloo.moments

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.screwy.igloo.R
import com.screwy.igloo.ui.component.Avatar
import com.screwy.igloo.ui.component.MomentsGrid
import com.screwy.igloo.ui.component.MomentThumbnailItem
import com.screwy.igloo.ui.component.storyRingBorder
import com.screwy.igloo.ui.theme.iglooColors

/**
 * Secondary `all-moments` route.
 * `all-moments`. Renders a compact "← All" header above the 3-column grid so a
 * back-nav affordance is always visible in addition to the predictive back gesture.
 */
@Composable
fun AllMomentsRoute(
    items: List<MomentThumbnailItem>,
    initialIndex: Int,
    onMomentClick: (videoId: String) -> Unit,
    onChannelClick: (channelId: String) -> Unit,
    storyChannels: List<MomentsViewModel.StoryChannelUiItem>,
    onStoryClick: (channelId: String, firstVideoId: String) -> Unit,
    activeTab: String,
    onTabSelected: (String) -> Unit,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(modifier = modifier.fillMaxSize()) {
        AllMomentsHeader(
            activeTab = activeTab,
            onTabSelected = onTabSelected,
            onBack = onBack,
        )
        if (activeTab == "stories") {
            StoryChannelList(
                rows = storyChannels,
                onStoryClick = onStoryClick,
                modifier = Modifier.fillMaxSize(),
            )
        } else {
            MomentsGrid(
                items = items,
                initialIndex = initialIndex,
                onItemClick = { videoId, _ -> onMomentClick(videoId) },
                onSwipeLeftOnItem = onChannelClick,
                showScrollFabs = true,
                modifier = Modifier.fillMaxSize(),
            )
        }
    }
}

@Composable
private fun AllMomentsHeader(
    activeTab: String,
    onTabSelected: (String) -> Unit,
    onBack: () -> Unit,
) {
    val allLabel = stringResource(R.string.moments_label_all)
    val followingLabel = stringResource(R.string.action_following)
    val storiesLabel = stringResource(R.string.shorts_tab_stories)
    val colors = MaterialTheme.iglooColors
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .background(colors.surface)
            .padding(start = 8.dp, end = 8.dp, top = 16.dp, bottom = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Box(
            modifier = Modifier
                .size(40.dp)
                .clickable(onClick = onBack),
            contentAlignment = Alignment.Center,
        ) {
            Icon(
                imageVector = Icons.AutoMirrored.Filled.ArrowBack,
                contentDescription = stringResource(R.string.action_back),
                tint = colors.onSurface,
            )
        }
        Text(
            text = allLabel,
            style = MaterialTheme.typography.titleMedium.copy(fontWeight = FontWeight.SemiBold),
            color = colors.onSurface,
        )
        Row(
            modifier = Modifier
                .clip(RoundedCornerShape(999.dp))
                .background(colors.surfaceElevated)
                .border(1.dp, colors.border, RoundedCornerShape(999.dp))
                .padding(3.dp),
            horizontalArrangement = Arrangement.spacedBy(2.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            AllMomentsTabPill("all", allLabel, activeTab == "all", onTabSelected)
            AllMomentsTabPill("following", followingLabel, activeTab == "following", onTabSelected)
            AllMomentsTabPill("stories", storiesLabel, activeTab == "stories", onTabSelected)
        }
    }
}

@Composable
private fun AllMomentsTabPill(
    tab: String,
    label: String,
    active: Boolean,
    onTabSelected: (String) -> Unit,
) {
    val colors = MaterialTheme.iglooColors
    Box(
        modifier = Modifier
            .clip(RoundedCornerShape(999.dp))
            .background(if (active) colors.primary else colors.surfaceElevated)
            .clickable { onTabSelected(tab) }
            .padding(horizontal = 12.dp, vertical = 6.dp),
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.labelMedium.copy(fontWeight = FontWeight.SemiBold),
            color = if (active) colors.onPrimary else colors.onSurface,
        )
    }
}

@Composable
private fun StoryChannelList(
    rows: List<MomentsViewModel.StoryChannelUiItem>,
    onStoryClick: (channelId: String, firstVideoId: String) -> Unit,
    modifier: Modifier = Modifier,
) {
    val colors = MaterialTheme.iglooColors
    if (rows.isEmpty()) {
        Box(modifier = modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
            Text(
                text = stringResource(R.string.stories_empty),
                style = MaterialTheme.typography.bodyLarge,
                color = colors.onSurfaceMuted,
            )
        }
        return
    }
    LazyColumn(modifier = modifier.padding(vertical = 8.dp)) {
        items(rows, key = { it.channelId }) { row ->
            StoryChannelRow(row = row, onStoryClick = onStoryClick)
        }
    }
}

@Composable
private fun StoryChannelRow(
    row: MomentsViewModel.StoryChannelUiItem,
    onStoryClick: (channelId: String, firstVideoId: String) -> Unit,
) {
    val colors = MaterialTheme.iglooColors
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(enabled = row.startVideoId.isNotBlank()) {
                onStoryClick(row.channelId, row.startVideoId)
            }
            .padding(horizontal = 16.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Avatar(
            channelId = row.channelId,
            size = 48.dp,
            modifier = Modifier.storyRingBorder(row.ringState, colors),
            showPendingBadge = false,
        )
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = row.displayName,
                style = MaterialTheme.typography.bodyLarge.copy(fontWeight = FontWeight.SemiBold),
                color = colors.onSurface,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (row.handle.isNotBlank()) {
                Text(
                    text = row.handle,
                    style = MaterialTheme.typography.bodyMedium,
                    color = colors.onSurfaceMuted,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        Text(
            text = if (row.count == 1) {
                stringResource(R.string.stories_count_one)
            } else {
                stringResource(R.string.stories_count_many, row.count)
            },
            style = MaterialTheme.typography.labelMedium.copy(fontWeight = FontWeight.SemiBold),
            color = colors.onSurfaceMuted,
        )
    }
}
