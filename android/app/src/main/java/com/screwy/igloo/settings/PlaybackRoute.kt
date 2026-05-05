package com.screwy.igloo.settings

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavController
import com.screwy.igloo.R
import com.screwy.igloo.settings.components.SectionHeader
import com.screwy.igloo.settings.components.SettingsSubScreen
import com.screwy.igloo.settings.components.SettingsSwitchRow
import com.screwy.igloo.ui.theme.iglooColors
import org.koin.androidx.compose.koinViewModel

private val speedOptions = listOf("0.5x", "0.75x", "1x", "1.25x", "1.5x", "2x")

@Composable
fun PlaybackRoute(
    navController: NavController,
    modifier: Modifier = Modifier,
) {
    val vm: PlaybackSettingsViewModel = koinViewModel()
    val autoplay by vm.autoplay.collectAsStateWithLifecycle()
    val muteDefault by vm.muteDefault.collectAsStateWithLifecycle()
    val speed by vm.playbackSpeedDefault.collectAsStateWithLifecycle()

    SettingsSubScreen(
        title = stringResource(R.string.settings_playback),
        onBack = { navController.popBackStack() },
        modifier = modifier,
    ) {
        SettingsSwitchRow(
            label = stringResource(R.string.settings_autoplay),
            checked = autoplay,
            onToggle = vm::setAutoplay,
        )
        SettingsSwitchRow(
            label = stringResource(R.string.settings_mute_by_default),
            checked = muteDefault,
            onToggle = vm::setMuteDefault,
        )
        SectionHeader(stringResource(R.string.settings_default_speed))
        Row(
            modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            speedOptions.forEach { opt -> SpeedChip(opt, opt == speed, vm::setPlaybackSpeedDefault) }
        }
    }
}

@Composable
private fun SpeedChip(label: String, selected: Boolean, onSelect: (String) -> Unit) {
    val colors = MaterialTheme.iglooColors
    Box(
        modifier = Modifier
            .clip(RoundedCornerShape(8.dp))
            .background(if (selected) colors.surfaceElevated else Color.Transparent)
            .border(
                width = 1.dp,
                color = if (selected) colors.surfaceElevated else colors.borderSubtle,
                shape = RoundedCornerShape(8.dp),
            )
            .clickable { onSelect(label) }
            .padding(horizontal = 14.dp, vertical = 8.dp),
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodyMedium,
            color = if (selected) colors.onSurface else colors.onSurfaceMuted,
        )
    }
}
