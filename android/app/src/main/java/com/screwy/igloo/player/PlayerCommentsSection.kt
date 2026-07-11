package com.screwy.igloo.player

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ThumbUp
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.screwy.igloo.R
import com.screwy.igloo.data.entity.VideoCommentEntity
import com.screwy.igloo.ui.component.Avatar
import com.screwy.igloo.ui.component.localizedRelativeTime
import com.screwy.igloo.ui.theme.iglooColors

@Composable
internal fun CommentsHeader(
    isRefreshing: Boolean,
    onRefresh: () -> Unit,
) {
    val colors = MaterialTheme.iglooColors
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 16.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.SpaceBetween,
    ) {
        val commentsLabel = stringResource(R.string.player_comments_heading)
        val refreshingLabel = stringResource(R.string.action_refreshing)
        val refreshLabel = stringResource(R.string.action_refresh)
        Text(
            text = commentsLabel,
            style = MaterialTheme.typography.titleSmall.copy(fontWeight = FontWeight.SemiBold),
            color = colors.onSurface,
        )
        OutlinedButton(
            onClick = onRefresh,
            enabled = !isRefreshing,
            border = BorderStroke(1.dp, colors.onSurfaceFaint.copy(alpha = 0.4f)),
        ) {
            Text(
                text = if (isRefreshing) refreshingLabel else refreshLabel,
                style = MaterialTheme.typography.labelSmall,
                color = colors.onSurfaceMuted,
            )
        }
    }
}

@Composable
internal fun CommentsEmptyState(isRefreshing: Boolean) {
    val colors = MaterialTheme.iglooColors
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 16.dp, vertical = 20.dp),
        contentAlignment = Alignment.Center,
    ) {
        if (isRefreshing) {
            CircularProgressIndicator(strokeWidth = 2.dp, modifier = Modifier.size(24.dp))
        } else {
            Text(
                text = stringResource(R.string.player_comments_empty),
                style = MaterialTheme.typography.bodyMedium,
                color = colors.onSurfaceMuted,
            )
        }
    }
}

@Composable
internal fun CommentRow(
    comment: VideoCommentEntity,
    threadDepth: Int,
    replyToAuthor: String?,
    isCreator: Boolean,
    onMentionClick: (String) -> Unit,
    onUrlClick: (String) -> Unit,
    onTimestampClick: (Long) -> Unit,
) {
    val colors = MaterialTheme.iglooColors
    val normalizedDepth = threadDepth.coerceAtLeast(0)
    val startPad = 12.dp + (normalizedDepth.coerceAtMost(2) * 28).dp
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(start = startPad, end = 12.dp, top = 8.dp, bottom = 8.dp),
        verticalAlignment = Alignment.Top,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        val avatarSize = if (normalizedDepth > 0) 28.dp else 32.dp
        val authorChannelId = youtubeCommentAuthorChannelId(comment.authorId)
        if (authorChannelId != null) {
			Avatar(
				channelId = authorChannelId,
				size = avatarSize,
				showPendingBadge = false,
				assetOwnerKind = "comment_author",
			)
        } else {
            CommentAvatarFallback(
                name = comment.authorName,
                size = avatarSize,
            )
        }
        Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(6.dp),
            ) {
                if (isCreator) {
                    Surface(
                        shape = CircleShape,
                        color = colors.surfaceVariant,
                    ) {
                        Text(
                            text = comment.authorName ?: "—",
                            style = MaterialTheme.typography.labelMedium,
                            color = colors.onSurface,
                            fontWeight = FontWeight.SemiBold,
                            modifier = Modifier.padding(horizontal = 8.dp, vertical = 2.dp),
                        )
                    }
                } else {
                    Text(
                        text = comment.authorName ?: "—",
                        style = MaterialTheme.typography.labelMedium,
                        color = colors.onSurface,
                        fontWeight = FontWeight.SemiBold,
                    )
                }
                if (replyToAuthor != null) {
                    Text(
                        text = "@$replyToAuthor",
                        style = MaterialTheme.typography.labelSmall,
                        color = colors.onSurfaceFaint,
                    )
                }
                Spacer(modifier = Modifier.weight(1f))
                Text(
                    text = localizedRelativeTime(comment.publishedAt, useWeeks = false),
                    style = MaterialTheme.typography.labelSmall,
                    color = colors.onSurfaceFaint,
                )
            }
            PlayerLinkedText(
                text = comment.text.orEmpty(),
                onMentionClick = onMentionClick,
                onUrlClick = onUrlClick,
                onTimestampClick = onTimestampClick,
                style = MaterialTheme.typography.bodySmall,
            )
            val likeCountLabel = commentLikeCountLabel(comment)
            if (likeCountLabel != null) {
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(4.dp),
                ) {
                    Icon(
                        imageVector = Icons.Default.ThumbUp,
                        contentDescription = null,
                        tint = colors.onSurfaceFaint,
                        modifier = Modifier.size(12.dp),
                    )
                    Text(
                        text = likeCountLabel,
                        style = MaterialTheme.typography.labelSmall,
                        color = colors.onSurfaceFaint,
                    )
                }
            }
        }
    }
}

@Composable
internal fun CommentAvatarFallback(name: String?, size: Dp = 32.dp) {
    val colors = MaterialTheme.iglooColors
    Box(
        modifier = Modifier
            .size(size)
            .clip(CircleShape)
            .background(colors.surfaceVariant),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = commentInitial(name),
            style = MaterialTheme.typography.labelMedium.copy(fontWeight = FontWeight.SemiBold),
            color = colors.onSurfaceMuted,
        )
    }
}
