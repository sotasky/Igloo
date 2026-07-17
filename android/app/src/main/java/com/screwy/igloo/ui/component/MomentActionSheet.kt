package com.screwy.igloo.ui.component

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import com.screwy.igloo.R
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.platformKeyFromChannelId
import com.screwy.igloo.data.stripPlatformPrefix
import org.koin.compose.koinInject

internal data class MomentActionAvailability(
    val canToggleReposts: Boolean,
    val canToggleMute: Boolean,
    val canUnfollowReposter: Boolean,
)

internal fun momentActionAvailability(item: MomentItem): MomentActionAvailability {
    val isRepost = item.repostIntroduced && !item.reposterChannelId.isNullOrBlank()
    return MomentActionAvailability(
        canToggleReposts = isRepost,
        canToggleMute = isRepost && !item.isAuthorFollowed,
        canUnfollowReposter = isRepost,
    )
}

internal fun momentUnfollowTarget(item: MomentItem): String? =
    item.reposterChannelId?.takeIf { momentActionAvailability(item).canUnfollowReposter }

/** A display target for the account affected by a moment action. */
internal data class MomentActionAccountLabels(
    val reposter: String,
    val author: String,
)

/**
 * Keeps action labels meaningful across TikTok, Instagram, and X identifiers.
 *
 * The preferred values come from the prepared profile row. If that row has not arrived yet,
 * use a readable platform identifier rather than losing the account-specific action entirely.
 */
internal fun momentActionAccountLabels(item: MomentItem): MomentActionAccountLabels {
    val reposterId = item.reposterChannelId.orEmpty().trim()
    val reposter =
        item.repostAuthorLabel?.trim()?.takeIf { it.isNotBlank() }
            ?: momentAccountHandleLabel(
                platform = platformKeyFromChannelId(reposterId),
                raw = stripPlatformPrefix(reposterId),
            )
            ?: stripPlatformPrefix(reposterId)
    val author =
        momentAccountHandleLabel(
            platform = platformKeyFromChannelId(item.channelId),
            raw = item.authorHandle,
        )
            ?: item.authorDisplayName?.trim()?.takeIf { it.isNotBlank() }
            ?: momentAccountHandleLabel(
                platform = platformKeyFromChannelId(item.channelId),
                raw = stripPlatformPrefix(item.channelId),
            )
            ?: stripPlatformPrefix(item.channelId)
    return MomentActionAccountLabels(reposter = reposter, author = author)
}

private fun momentAccountHandleLabel(platform: String, raw: String?): String? =
    platformHandleCandidate(platform, raw).takeIf { it.isNotBlank() }?.let { "@$it" }

internal fun momentUnfollowAuthorLabel(item: MomentItem): String =
    item.authorDisplayName?.trim()?.takeIf { it.isNotBlank() }
        ?: item.authorHandle.trim().takeIf { it.isNotBlank() }
        ?: item.channelId

@Composable
@OptIn(ExperimentalMaterial3Api::class)
internal fun MomentActionSheet(
    item: MomentItem,
    onDismissRequest: () -> Unit,
    onRepostsEnabledChanged: (channelId: String, enabled: Boolean) -> Unit,
    onChannelMutedChanged: (channelId: String, muted: Boolean) -> Unit,
    onUnfollowChannel: (channelId: String) -> Unit,
) {
    val actions = momentActionAvailability(item)
    if (!actions.canToggleReposts && !actions.canToggleMute && !actions.canUnfollowReposter) return

    val db: IglooDatabase = koinInject()
    val reposterChannelId = item.reposterChannelId.orEmpty()
    val unfollowChannelId = momentUnfollowTarget(item)
    val reposterSetting by
        db.channelSettingDao()
            .getByIdFlow(reposterChannelId)
            .collectAsState(initial = null)
    val mutedAuthor by
        db.mutedChannelDao()
            .getByIdFlow(item.channelId)
            .collectAsState(initial = null)
    val repostsEnabled = reposterSetting?.includeReposts != 0
    val authorMuted = mutedAuthor != null
    val accountLabels = momentActionAccountLabels(item)
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = false)
    var showUnfollowConfirmation by remember(item.videoId, reposterChannelId) { mutableStateOf(false) }

    ModalBottomSheet(
        onDismissRequest = onDismissRequest,
        sheetState = sheetState,
    ) {
        // Do not let this context menu claim player-sized vertical space. With the partial sheet
        // state above, it wraps to compact rows on phones as well.
        Column(modifier = Modifier.fillMaxWidth().padding(bottom = 12.dp)) {
            if (actions.canToggleReposts) {
                MomentActionRow(
                    label =
                        stringResource(
                            if (repostsEnabled) R.string.action_turn_off_reposts_for_account
                            else R.string.action_turn_on_reposts_for_account,
                            accountLabels.reposter,
                        ),
                    onClick = { onRepostsEnabledChanged(reposterChannelId, !repostsEnabled) },
                )
            }
            if (actions.canToggleMute) {
                MomentActionRow(
                    label =
                        stringResource(
                            if (authorMuted) R.string.action_unmute_account_label
                            else R.string.action_mute_account_label,
                            accountLabels.author,
                        ),
                    onClick = { onChannelMutedChanged(item.channelId, !authorMuted) },
                )
            }
            if (unfollowChannelId != null) {
                MomentActionRow(
                    label = stringResource(R.string.action_unfollow_account_label, accountLabels.reposter),
                    onClick = { showUnfollowConfirmation = true },
                )
            }
        }
    }
    if (showUnfollowConfirmation) {
        MomentUnfollowConfirmation(
            accountLabel = accountLabels.reposter,
            onDismissRequest = { showUnfollowConfirmation = false },
            onConfirm = {
                showUnfollowConfirmation = false
                onDismissRequest()
                unfollowChannelId?.let(onUnfollowChannel)
            },
        )
    }
}

@Composable
internal fun MomentUnfollowConfirmation(
    accountLabel: String,
    onDismissRequest: () -> Unit,
    onConfirm: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismissRequest,
        title = { Text(stringResource(R.string.confirm_unfollow_account_title)) },
        text = { Text(stringResource(R.string.confirm_unfollow_channel_delete_media_body, accountLabel)) },
        confirmButton = {
            TextButton(onClick = onConfirm) {
                Text(stringResource(R.string.action_unfollow))
            }
        },
        dismissButton = {
            TextButton(onClick = onDismissRequest) {
                Text(stringResource(R.string.action_cancel))
            }
        },
    )
}

@Composable
private fun MomentActionRow(label: String, onClick: () -> Unit) {
    Text(
        text = label,
        style = MaterialTheme.typography.bodyLarge,
        modifier =
            Modifier
                .fillMaxWidth()
                .heightIn(min = 48.dp)
                .clickable(onClick = onClick)
                .padding(horizontal = 24.dp, vertical = 8.dp),
    )
}
