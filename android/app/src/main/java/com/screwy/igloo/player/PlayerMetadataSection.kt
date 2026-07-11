package com.screwy.igloo.player

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Bookmark
import androidx.compose.material.icons.filled.BookmarkBorder
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.PersonRemove
import androidx.compose.material.icons.filled.Share
import androidx.compose.material.icons.filled.Star
import androidx.compose.material.icons.filled.StarBorder
import androidx.compose.material.icons.filled.ThumbUp
import androidx.compose.material.icons.filled.Visibility
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.res.stringResource
import com.screwy.igloo.R
import com.screwy.igloo.data.Dearrow
import com.screwy.igloo.data.stripPlatformPrefix
import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.VideoEntity
import com.screwy.igloo.ui.component.Avatar
import com.screwy.igloo.ui.component.localizedRelativeTime
import com.screwy.igloo.ui.theme.iglooColors

@Composable
internal fun VideoMetaBlock(
    video: VideoEntity?,
    dearrowMode: String = "off",
    channel: ChannelEntity?,
    metadataCounts: VideoMetadataCounts,
    isBookmarked: Boolean,
    isFollowed: Boolean,
    isStarred: Boolean,
    hasLocalMedia: Boolean,
    onChannelClick: (channelId: String) -> Unit,
    shareEnabled: Boolean = true,
    onShare: () -> Unit,
    onBookmark: () -> Unit,
    onToggleStar: () -> Unit,
    onUnfollow: () -> Unit,
    onDeleteLocal: () -> Unit,
    onMentionClick: (String) -> Unit,
    onUrlClick: (String) -> Unit,
    onTimestampClick: (Long) -> Unit,
) {
    val colors = MaterialTheme.iglooColors
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 16.dp, vertical = 12.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Text(
            text = Dearrow.resolveTitle(
                dearrowMode,
                video?.title,
                video?.dearrowTitle,
                video?.dearrowTitleCasual,
            ),
            style = MaterialTheme.typography.titleMedium.copy(fontWeight = FontWeight.SemiBold),
            color = colors.onSurface,
            maxLines = 2,
        )

        Row(
            modifier = Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            val cid = video?.channelId ?: channel?.channelId
            val channelLabel = channel?.name?.trim()?.takeIf { it.isNotBlank() }
                ?: cid?.let(::stripPlatformPrefix)
                ?: ""
            ChannelAvatar(
                channelId = cid,
                label = channelLabel,
                size = 40.dp,
                onClick = cid?.let { { onChannelClick(it) } },
            )
            Row(
                modifier = Modifier.weight(1f),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                Text(
                    text = channelLabel,
                    style = MaterialTheme.typography.bodyMedium.copy(fontWeight = FontWeight.SemiBold),
                    color = colors.primary,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = if (cid != null) {
                        Modifier.weight(3f, fill = false).clickable { onChannelClick(cid) }
                    } else {
                        Modifier.weight(3f, fill = false)
                    },
                )
                if (isFollowed) {
                    Icon(
                        imageVector = if (isStarred) Icons.Default.Star else Icons.Default.StarBorder,
                        contentDescription = if (isStarred) {
                            stringResource(R.string.action_unstar_channel)
                        } else {
                            stringResource(R.string.action_star_channel)
                        },
                        tint = if (isStarred) colors.primary else colors.onSurfaceMuted,
                        modifier = Modifier
                            .size(22.dp)
                            .clickable(onClick = onToggleStar),
                    )
                    Icon(
                        imageVector = Icons.Default.PersonRemove,
                        contentDescription = stringResource(R.string.action_unfollow),
                        tint = colors.onSurfaceMuted,
                        modifier = Modifier
                            .size(22.dp)
                            .clickable(onClick = onUnfollow),
                    )
                }
                Spacer(modifier = Modifier.weight(1f))
            }
            val publishedAt = video?.publishedAt ?: 0L
            if (publishedAt > 0L) {
                Text(
                    text = localizedRelativeTime(publishedAt),
                    style = MaterialTheme.typography.labelMedium,
                    color = colors.onSurfaceMuted,
                )
            }
        }

        StatsRow(
            metadataCounts = metadataCounts,
            isBookmarked = isBookmarked,
            hasLocalMedia = hasLocalMedia,
            shareEnabled = shareEnabled,
            onShare = onShare,
            onBookmark = onBookmark,
            onDeleteLocal = onDeleteLocal,
        )

        val body = video?.description.orEmpty()
        if (body.isNotBlank()) {
            DescriptionCard(
                body = body,
                onMentionClick = onMentionClick,
                onUrlClick = onUrlClick,
                onTimestampClick = onTimestampClick,
            )
        }
    }
}

@Composable
private fun StatsRow(
    metadataCounts: VideoMetadataCounts,
    isBookmarked: Boolean,
    hasLocalMedia: Boolean,
    shareEnabled: Boolean,
    onShare: () -> Unit,
    onBookmark: () -> Unit,
    onDeleteLocal: () -> Unit,
) {
    val colors = MaterialTheme.iglooColors
    Surface(
        modifier = Modifier.fillMaxWidth(),
        shape = RoundedCornerShape(12.dp),
        color = colors.surfaceVariant,
        contentColor = colors.onSurface,
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = 12.dp, vertical = 10.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(14.dp),
        ) {
            Row(
                modifier = Modifier.weight(1f),
                horizontalArrangement = Arrangement.spacedBy(12.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                metadataCounts.viewCountLabel?.let { label ->
                    StatIconChip(
                        icon = Icons.Default.Visibility,
                        label = label,
                        color = colors.onSurfaceMuted,
                    )
                }
                metadataCounts.likeCountLabel?.let { label ->
                    StatIconChip(
                        icon = Icons.Default.ThumbUp,
                        label = label,
                        color = colors.onSurfaceMuted,
                    )
                }
            }
            IconButton(onClick = onShare, enabled = shareEnabled) {
                Icon(
                    imageVector = Icons.Default.Share,
                    contentDescription = stringResource(R.string.action_share),
                    tint = colors.onSurfaceMuted,
                )
            }
            IconButton(onClick = onBookmark) {
                Icon(
                    imageVector = if (isBookmarked) Icons.Default.Bookmark else Icons.Default.BookmarkBorder,
                    contentDescription = if (isBookmarked) {
                        stringResource(R.string.action_remove_bookmark)
                    } else {
                        stringResource(R.string.action_bookmark)
                    },
                    tint = if (isBookmarked) colors.primary else colors.onSurfaceMuted,
                )
            }
            IconButton(onClick = onDeleteLocal, enabled = hasLocalMedia) {
                Icon(
                    imageVector = Icons.Default.Delete,
                    contentDescription = stringResource(R.string.action_delete_local_media),
                    tint = if (hasLocalMedia) colors.onSurfaceMuted else colors.onSurfaceFaint,
                )
            }
        }
    }
}

@Composable
private fun DescriptionCard(
    body: String,
    onMentionClick: (String) -> Unit,
    onUrlClick: (String) -> Unit,
    onTimestampClick: (Long) -> Unit,
) {
    val colors = MaterialTheme.iglooColors
    var expanded by remember(body) { mutableStateOf(false) }
    Surface(
        modifier = Modifier.fillMaxWidth(),
        shape = RoundedCornerShape(12.dp),
        color = colors.surfaceVariant,
        contentColor = colors.onSurface,
    ) {
        Column(
            modifier = Modifier.padding(horizontal = 12.dp, vertical = 10.dp),
            verticalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            PlayerLinkedText(
                text = body,
                onMentionClick = onMentionClick,
                onUrlClick = onUrlClick,
                onTimestampClick = onTimestampClick,
                style = MaterialTheme.typography.bodySmall,
                maxLines = if (expanded) Int.MAX_VALUE else 4,
            )
            Text(
                text = if (expanded) {
                    stringResource(R.string.action_show_less)
                } else {
                    stringResource(R.string.action_show_more)
                },
                style = MaterialTheme.typography.labelMedium,
                color = colors.primary,
                modifier = Modifier
                    .fillMaxWidth()
                    .clickable { expanded = !expanded }
                    .padding(vertical = 2.dp),
            )
        }
    }
}

@Composable
private fun ChannelAvatar(
    channelId: String?,
    label: String,
    size: Dp,
    onClick: (() -> Unit)?,
) {
    val resolvedChannelId = channelId?.takeIf { it.isNotBlank() }
    if (resolvedChannelId != null) {
        Avatar(
            channelId = resolvedChannelId,
            size = size,
            onClick = onClick,
        )
    } else {
        CommentAvatarFallback(name = label, size = size)
    }
}

@Composable
private fun StatIconChip(
    icon: ImageVector,
    label: String,
    color: Color,
) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        Icon(
            imageVector = icon,
            contentDescription = null,
            tint = color,
            modifier = Modifier.size(16.dp),
        )
        Text(
            text = label,
            style = MaterialTheme.typography.bodySmall,
            color = color,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}
