package com.screwy.igloo.moments

import androidx.activity.compose.BackHandler
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideOutHorizontally
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.zIndex
import com.screwy.igloo.R
import com.screwy.igloo.data.entity.StoryChannelItem
import com.screwy.igloo.data.stripPlatformPrefix
import com.screwy.igloo.ui.component.Avatar
import com.screwy.igloo.ui.component.StoryRingState
import com.screwy.igloo.ui.component.storyRingBorder
import com.screwy.igloo.ui.component.storyRingState
import com.screwy.igloo.ui.theme.iglooColors

internal data class StoryTrayItem(
    val channelId: String,
    val displayName: String,
    val handle: String,
    val count: Int,
    val startVideoId: String,
    val ringState: StoryRingState,
)

internal fun StoryChannelItem.toStoryTrayItem(): StoryTrayItem {
    val handle = channelSourceId?.takeIf { it.isNotBlank() } ?: stripPlatformPrefix(channelId)
    return StoryTrayItem(
        channelId = channelId,
        displayName = channelName?.takeIf { it.isNotBlank() } ?: handle.ifBlank { channelId },
        handle = if (handle.isNotBlank()) "@$handle" else "",
        count = storyCount,
        startVideoId = firstUnseenVideoId.takeIf { it.isNotBlank() } ?: firstVideoId,
        ringState = storyRingState(storyCount, unseenCount),
    )
}

internal fun MomentsViewModel.StoryChannelUiItem.toStoryTrayItem(): StoryTrayItem =
    StoryTrayItem(
        channelId = channelId,
        displayName = displayName,
        handle = handle,
        count = count,
        startVideoId = startVideoId,
        ringState = ringState,
    )

@Composable
internal fun StoryTray(
    visible: Boolean,
    rows: List<StoryTrayItem>,
    onDismiss: () -> Unit,
    onStoryClick: (channelId: String, firstVideoId: String) -> Unit,
    modifier: Modifier = Modifier,
) {
    BackHandler(enabled = visible, onBack = onDismiss)
    AnimatedVisibility(
        visible = visible,
        enter = slideInHorizontally(initialOffsetX = { it }),
        exit = slideOutHorizontally(targetOffsetX = { it }),
        modifier = modifier.zIndex(12f),
    ) {
        val colors = MaterialTheme.iglooColors
        Surface(
            modifier = Modifier
                .fillMaxHeight()
                .widthIn(min = 280.dp, max = 360.dp),
            color = colors.surface.copy(alpha = 0.96f),
            tonalElevation = 8.dp,
            shadowElevation = 12.dp,
        ) {
            Column(modifier = Modifier.fillMaxSize()) {
                Text(
                    text = stringResource(R.string.shorts_tab_stories),
                    modifier = Modifier.padding(horizontal = 18.dp, vertical = 16.dp),
                    color = colors.onSurface,
                    style = MaterialTheme.typography.titleMedium.copy(fontWeight = FontWeight.Bold),
                )
                if (rows.isEmpty()) {
                    Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                        Text(
                            text = stringResource(R.string.stories_empty),
                            color = colors.onSurfaceMuted,
                            style = MaterialTheme.typography.bodyMedium,
                        )
                    }
                } else {
                    LazyColumn(modifier = Modifier.fillMaxSize()) {
                        items(rows, key = { it.channelId }) { row ->
                            StoryTrayRow(row = row, onStoryClick = onStoryClick)
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun StoryTrayRow(
    row: StoryTrayItem,
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
    ) {
        Avatar(
            channelId = row.channelId,
            size = 46.dp,
            modifier = Modifier.storyRingBorder(row.ringState, colors),
            showPendingBadge = false,
        )
        Column(
            modifier = Modifier
                .weight(1f)
                .padding(horizontal = 12.dp),
        ) {
            Text(
                text = row.displayName,
                color = colors.onSurface,
                style = MaterialTheme.typography.bodyMedium.copy(fontWeight = FontWeight.SemiBold),
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (row.handle.isNotBlank()) {
                Text(
                    text = row.handle,
                    color = colors.onSurfaceMuted,
                    style = MaterialTheme.typography.bodySmall,
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
            color = colors.onSurfaceMuted,
            style = MaterialTheme.typography.labelSmall.copy(fontWeight = FontWeight.SemiBold),
        )
    }
}
