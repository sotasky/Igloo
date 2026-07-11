package com.screwy.igloo.settings

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavController
import com.screwy.igloo.data.entity.MutedChannelDisplay
import com.screwy.igloo.R
import com.screwy.igloo.settings.components.SectionHeader
import com.screwy.igloo.settings.components.SettingsSubScreen
import com.screwy.igloo.settings.components.SettingsSwitchRow
import com.screwy.igloo.settings.components.TextActionRow
import com.screwy.igloo.ui.theme.iglooColors
import org.koin.androidx.compose.koinViewModel

@Composable
fun FeedRoute(
    navController: NavController,
    modifier: Modifier = Modifier,
) {
    val settingsVm: FeedSettingsViewModel = koinViewModel()
    val mutedVm: MutedAccountsViewModel = koinViewModel()

    val includeReposts by settingsVm.includeReposts.collectAsStateWithLifecycle()
    val mediaOnly by settingsVm.mediaOnly.collectAsStateWithLifecycle()
    val mutedChannels by mutedVm.channels.collectAsStateWithLifecycle()
    var pendingUnmute by remember { mutableStateOf<MutedChannelDisplay?>(null) }
    var confirmClearMuted by remember { mutableStateOf(false) }

    SettingsSubScreen(
        title = stringResource(R.string.nav_feed),
        onBack = { navController.popBackStack() },
        modifier = modifier,
    ) {
        SettingsSwitchRow(
            label = stringResource(R.string.settings_feed_include_reposts),
            checked = includeReposts,
            onToggle = settingsVm::setIncludeReposts,
        )
        SettingsSwitchRow(
            label = stringResource(R.string.settings_media_only_x),
            checked = mediaOnly,
            onToggle = settingsVm::setMediaOnly,
        )

        SectionHeader(stringResource(R.string.settings_section_muted_accounts))
        if (mutedChannels.isEmpty()) {
            Text(
                text = stringResource(R.string.settings_no_muted_accounts),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.iglooColors.onSurfaceMuted,
                modifier = Modifier.padding(horizontal = 16.dp, vertical = 12.dp),
            )
        } else {
            mutedChannels.forEach { channel ->
                MutedAccountRow(label = channel.label(), onUnmute = { pendingUnmute = channel })
            }
            TextActionRow(label = stringResource(R.string.action_clear_muted_accounts)) { confirmClearMuted = true }
        }
    }

    pendingUnmute?.let { channel ->
        val label = channel.label()
        AlertDialog(
            onDismissRequest = { pendingUnmute = null },
            title = { Text(stringResource(R.string.settings_unmute_title, label)) },
            text = { Text(stringResource(R.string.settings_unmute_help)) },
            confirmButton = {
                TextButton(
                    onClick = {
                        pendingUnmute = null
                        mutedVm.unmute(channel.muted.channelId)
                    },
                ) {
                    Text(stringResource(R.string.action_unmute_account))
                }
            },
            dismissButton = {
                TextButton(onClick = { pendingUnmute = null }) {
                    Text(stringResource(R.string.action_cancel))
                }
            },
        )
    }

    if (confirmClearMuted) {
        AlertDialog(
            onDismissRequest = { confirmClearMuted = false },
            title = { Text(stringResource(R.string.settings_clear_muted_title)) },
            text = { Text(stringResource(R.string.settings_clear_muted_help)) },
            confirmButton = {
                TextButton(
                    onClick = {
                        confirmClearMuted = false
                        mutedVm.clearAll()
                    },
                ) {
                    Text(stringResource(R.string.action_clear))
                }
            },
            dismissButton = {
                TextButton(onClick = { confirmClearMuted = false }) {
                    Text(stringResource(R.string.action_cancel))
                }
            },
        )
    }
}

@Composable
private fun MutedAccountRow(label: String, onUnmute: () -> Unit) {
    val colors = MaterialTheme.iglooColors
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 16.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.SpaceBetween,
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodyLarge,
            color = colors.onSurface,
        )
        IconButton(onClick = onUnmute) {
            Icon(
                imageVector = Icons.Default.Close,
                contentDescription = stringResource(R.string.settings_unmute_title, label),
                tint = colors.onSurfaceMuted,
            )
        }
    }
}

private fun MutedChannelDisplay.label(): String =
    handle?.trim()?.removePrefix("@")?.takeIf(String::isNotBlank)?.let { "@$it" }
        ?: displayName?.trim()?.takeIf(String::isNotBlank)
        ?: muted.channelId
