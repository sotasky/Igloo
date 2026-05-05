package com.screwy.igloo.settings.components

import androidx.annotation.StringRes
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.SwitchDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.res.stringArrayResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.screwy.igloo.R
import com.screwy.igloo.settings.SponsorBlockSettings
import com.screwy.igloo.ui.nav.RouteRegistry
import com.screwy.igloo.ui.theme.iglooColors

// ─── Option tables shared across sub-screens ────────────────────────────────

internal val retentionOptions = listOf(0, 1, 2, 3, 7, 14, 30, 60, 90)

@Composable
internal fun retentionDaysLabel(days: Int): String = when {
    days <= 0 -> stringResource(R.string.option_off)
    days == 1 -> stringResource(R.string.settings_retention_one_day)
    else -> stringResource(R.string.settings_retention_days, days)
}

internal val syncIntervalOptions = listOf(30, 60, 120, 240)
internal val storiesWindowHourOptions = listOf(24, 48, 72, 168)

@Composable
internal fun syncIntervalLabel(minutes: Int): String =
    if (minutes > 0 && minutes % 60 == 0) {
        stringResource(R.string.settings_sync_hours, minutes / 60)
    } else {
        stringResource(R.string.settings_sync_minutes, minutes)
    }

@Composable
internal fun storiesWindowHoursLabel(hours: Int): String =
    stringResource(R.string.settings_sync_hours, hours)

// Starting-page options shown in the Feed section. Values mirror the NavHost
// route registry and the allow-list in PreferencesRepo.Defaults.VALID_STARTING_PAGES.
private data class StartingPageOption(
    val key: String,
    @param:StringRes val labelRes: Int,
)

private val startingPageOptions = listOf(
    StartingPageOption(RouteRegistry.Feed.route, R.string.nav_feed),
    StartingPageOption(RouteRegistry.Videos.route, R.string.nav_videos),
    StartingPageOption(RouteRegistry.Moments.route, R.string.nav_moments),
    StartingPageOption(RouteRegistry.Bookmarks.route, R.string.nav_bookmarks),
    StartingPageOption(RouteRegistry.Liked.route, R.string.nav_liked),
)

@Composable
private fun startingPageLabel(value: String): String =
    stringResource(startingPageOptions.firstOrNull { it.key == value }?.labelRes ?: R.string.nav_feed)

private data class LanguageOption(
    val tag: String,
    val label: String,
)

// ─── Section primitives ─────────────────────────────────────────────────────

@Composable
internal fun SectionHeader(title: String) {
    val colors = MaterialTheme.iglooColors
    Column {
        Spacer(Modifier.height(8.dp))
        Text(
            text = title,
            style = MaterialTheme.typography.labelMedium,
            color = colors.onSurfaceMuted,
            fontWeight = FontWeight.SemiBold,
            modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp),
        )
        HorizontalDivider(
            color = colors.borderSubtle,
            modifier = Modifier.padding(horizontal = 16.dp),
        )
        Spacer(Modifier.height(8.dp))
    }
}

@Composable
internal fun SectionDescription(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.bodyMedium,
        color = MaterialTheme.iglooColors.onSurfaceMuted,
        modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp),
    )
}

// ─── Retention row ─────────────────────────────────────────────────────────

@Composable
internal fun RetentionRow(
    label: String,
    value: Int,
    onSelect: (Int) -> Unit,
) {
    val colors = MaterialTheme.iglooColors
    var expanded by remember { mutableStateOf(false) }

    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable { expanded = true }
            .padding(horizontal = 16.dp, vertical = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodyLarge,
            color = colors.onSurface,
            modifier = Modifier.weight(1f),
        )
        Box {
            Box(
                modifier = Modifier
                    .clip(RoundedCornerShape(10.dp))
                    .background(colors.surfaceElevated)
                    .clickable { expanded = true }
                    .padding(horizontal = 14.dp, vertical = 6.dp),
            ) {
                Text(
                    text = retentionDaysLabel(value),
                    style = MaterialTheme.typography.bodyMedium,
                    color = colors.onSurface,
                )
            }
            DropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
                retentionOptions.forEach { opt ->
                    DropdownMenuItem(
                        text = { Text(retentionDaysLabel(opt)) },
                        onClick = {
                            onSelect(opt)
                            expanded = false
                        },
                    )
                }
            }
        }
    }
}

@Composable
internal fun StoriesWindowRow(
    value: Int,
    onSelect: (Int) -> Unit,
) {
    val colors = MaterialTheme.iglooColors
    var expanded by remember { mutableStateOf(false) }

    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable { expanded = true }
            .padding(horizontal = 16.dp, vertical = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = stringResource(R.string.settings_stories_window_hours),
            style = MaterialTheme.typography.bodyLarge,
            color = colors.onSurface,
            modifier = Modifier.weight(1f),
        )
        Box {
            Box(
                modifier = Modifier
                    .clip(RoundedCornerShape(10.dp))
                    .background(colors.surfaceElevated)
                    .clickable { expanded = true }
                    .padding(horizontal = 14.dp, vertical = 6.dp),
            ) {
                Text(
                    text = storiesWindowHoursLabel(value),
                    style = MaterialTheme.typography.bodyMedium,
                    color = colors.onSurface,
                )
            }
            DropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
                storiesWindowHourOptions.forEach { opt ->
                    DropdownMenuItem(
                        text = { Text(storiesWindowHoursLabel(opt)) },
                        onClick = {
                            onSelect(opt)
                            expanded = false
                        },
                    )
                }
            }
        }
    }
}

// ─── Starting page row ─────────────────────────────────────────────────────

@Composable
internal fun StartingPageRow(
    value: String,
    onSelect: (String) -> Unit,
) {
    val colors = MaterialTheme.iglooColors
    var expanded by remember { mutableStateOf(false) }

    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable { expanded = true }
            .padding(horizontal = 16.dp, vertical = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = stringResource(R.string.settings_starting_page),
            style = MaterialTheme.typography.bodyLarge,
            color = colors.onSurface,
            modifier = Modifier.weight(1f),
        )
        Box {
            Box(
                modifier = Modifier
                    .clip(RoundedCornerShape(10.dp))
                    .background(colors.surfaceElevated)
                    .clickable { expanded = true }
                    .padding(horizontal = 14.dp, vertical = 6.dp),
            ) {
                Text(
                    text = startingPageLabel(value),
                    style = MaterialTheme.typography.bodyMedium,
                    color = colors.onSurface,
                )
            }
            DropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
                startingPageOptions.forEach { option ->
                    val label = stringResource(option.labelRes)
                    DropdownMenuItem(
                        text = { Text(label) },
                        onClick = {
                            onSelect(option.key)
                            expanded = false
                        },
                    )
                }
            }
        }
    }
}

@Composable
internal fun MomentsDefaultTabRow(
    value: String,
    onSelect: (String) -> Unit,
) {
    val colors = MaterialTheme.iglooColors
    var expanded by remember { mutableStateOf(false) }
    val allLabel = stringResource(R.string.settings_moments_default_tab_all)
    val followingLabel = stringResource(R.string.settings_moments_default_tab_following)
    val storiesLabel = stringResource(R.string.shorts_tab_stories)
    val options = listOf("all" to allLabel, "following" to followingLabel, "stories" to storiesLabel)

    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable { expanded = true }
            .padding(horizontal = 16.dp, vertical = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = stringResource(R.string.settings_moments_default_tab),
            style = MaterialTheme.typography.bodyLarge,
            color = colors.onSurface,
            modifier = Modifier.weight(1f),
        )
        Box {
            Box(
                modifier = Modifier
                    .clip(RoundedCornerShape(10.dp))
                    .background(colors.surfaceElevated)
                    .clickable { expanded = true }
                    .padding(horizontal = 14.dp, vertical = 6.dp),
            ) {
                Text(
                    text = options.firstOrNull { it.first == value }?.second ?: allLabel,
                    style = MaterialTheme.typography.bodyMedium,
                    color = colors.onSurface,
                )
            }
            DropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
                options.forEach { (key, label) ->
                    DropdownMenuItem(
                        text = { Text(label) },
                        onClick = {
                            onSelect(key)
                            expanded = false
                        },
                    )
                }
            }
        }
    }
}

// ─── App language row ──────────────────────────────────────────────────────

@Composable
internal fun LanguageRow(
    value: String,
    onSelect: (String) -> Unit,
) {
    val colors = MaterialTheme.iglooColors
    val systemLabel = stringResource(R.string.language_system)
    val tags = stringArrayResource(R.array.supported_locale_tags)
    val labels = stringArrayResource(R.array.supported_locale_labels)
    val options = buildList {
        add(LanguageOption("", systemLabel))
        tags.zip(labels).forEach { (tag, label) -> add(LanguageOption(tag, label)) }
        if (value.isNotBlank() && none { it.tag == value }) {
            add(LanguageOption(value, value))
        }
    }
    val selectedLabel = options.firstOrNull { it.tag == value }?.label ?: systemLabel
    var expanded by remember { mutableStateOf(false) }

    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable { expanded = true }
            .padding(horizontal = 16.dp, vertical = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = stringResource(R.string.settings_ui_language),
            style = MaterialTheme.typography.bodyLarge,
            color = colors.onSurface,
            modifier = Modifier.weight(1f),
        )
        Box {
            Box(
                modifier = Modifier
                    .clip(RoundedCornerShape(10.dp))
                    .background(colors.surfaceElevated)
                    .clickable { expanded = true }
                    .padding(horizontal = 14.dp, vertical = 6.dp),
            ) {
                Text(
                    text = selectedLabel,
                    style = MaterialTheme.typography.bodyMedium,
                    color = colors.onSurface,
                )
            }
            DropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
                options.forEach { option ->
                    DropdownMenuItem(
                        text = { Text(option.label) },
                        onClick = {
                            onSelect(option.tag)
                            expanded = false
                        },
                    )
                }
            }
        }
    }
}

// ─── Sync interval chips ───────────────────────────────────────────────────

@Composable
internal fun SyncIntervalRow(selected: Int, onSelect: (Int) -> Unit) {
    val colors = MaterialTheme.iglooColors
    Column {
        Text(
            text = stringResource(R.string.settings_sync_interval),
            style = MaterialTheme.typography.bodyLarge,
            color = colors.onSurface,
            modifier = Modifier.padding(horizontal = 16.dp, vertical = 12.dp),
        )
        Row(
            modifier = Modifier.padding(horizontal = 16.dp),
            horizontalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            syncIntervalOptions.forEach { opt ->
                val isSelected = opt == selected
                Box(
                    modifier = Modifier
                        .clip(RoundedCornerShape(10.dp))
                        .background(if (isSelected) colors.surfaceElevated else androidx.compose.ui.graphics.Color.Transparent)
                        .border(
                            width = 1.dp,
                            color = if (isSelected) colors.surfaceElevated else colors.borderSubtle,
                            shape = RoundedCornerShape(10.dp),
                        )
                        .clickable { onSelect(opt) }
                        .padding(horizontal = 18.dp, vertical = 10.dp),
                ) {
                    Text(
                        text = syncIntervalLabel(opt),
                        style = MaterialTheme.typography.bodyMedium,
                        color = if (isSelected) colors.onSurface else colors.onSurfaceMuted,
                    )
                }
            }
        }
    }
}

// ─── SponsorBlock 3-chip segmented row ─────────────────────────────────────

@Composable
internal fun SponsorBlockRow(
    label: String,
    value: String,
    onSelect: (String) -> Unit,
) {
    val colors = MaterialTheme.iglooColors
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 16.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodyLarge,
            color = colors.onSurface,
            modifier = Modifier.weight(1f),
        )
        Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            SponsorBlockChip(
                labelRes = R.string.option_off,
                selected = value == SponsorBlockSettings.SB_OFF,
            ) { onSelect(SponsorBlockSettings.SB_OFF) }
            SponsorBlockChip(
                labelRes = R.string.settings_sponsorblock_silent,
                selected = value == SponsorBlockSettings.SB_SILENT,
            ) { onSelect(SponsorBlockSettings.SB_SILENT) }
            SponsorBlockChip(
                labelRes = R.string.settings_sponsorblock_ask,
                selected = value == SponsorBlockSettings.SB_ASK,
            ) { onSelect(SponsorBlockSettings.SB_ASK) }
        }
    }
}

@Composable
internal fun SponsorBlockChip(
    @StringRes labelRes: Int,
    selected: Boolean,
    onClick: () -> Unit,
) {
    val colors = MaterialTheme.iglooColors
    val label = stringResource(labelRes)
    Box(
        modifier = Modifier
            .clip(RoundedCornerShape(8.dp))
            .background(if (selected) colors.surfaceElevated else androidx.compose.ui.graphics.Color.Transparent)
            .border(
                width = 1.dp,
                color = if (selected) colors.surfaceElevated else colors.borderSubtle,
                shape = RoundedCornerShape(8.dp),
            )
            .clickable(onClick = onClick)
            .padding(horizontal = 14.dp, vertical = 8.dp),
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodyMedium,
            color = if (selected) colors.onSurface else colors.onSurfaceMuted,
            textAlign = TextAlign.Center,
        )
    }
}

// ─── DeArrow 3-chip segmented row ──────────────────────────────────────────

@Composable
internal fun DearrowModeRow(
    value: String,
    onSelect: (String) -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 16.dp, vertical = 6.dp),
        horizontalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        DearrowChip(
            labelRes = R.string.option_off,
            selected = value == "off",
        ) { onSelect("off") }
        DearrowChip(
            labelRes = R.string.settings_dearrow_community,
            selected = value == "default",
        ) { onSelect("default") }
        DearrowChip(
            labelRes = R.string.settings_dearrow_casual,
            selected = value == "casual",
        ) { onSelect("casual") }
    }
}

@Composable
internal fun DearrowChip(
    @StringRes labelRes: Int,
    selected: Boolean,
    onClick: () -> Unit,
) {
    val colors = MaterialTheme.iglooColors
    val label = stringResource(labelRes)
    Box(
        modifier = Modifier
            .clip(RoundedCornerShape(8.dp))
            .background(if (selected) colors.surfaceElevated else androidx.compose.ui.graphics.Color.Transparent)
            .border(
                width = 1.dp,
                color = if (selected) colors.surfaceElevated else colors.borderSubtle,
                shape = RoundedCornerShape(8.dp),
            )
            .clickable(onClick = onClick)
            .padding(horizontal = 14.dp, vertical = 8.dp),
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodyMedium,
            color = if (selected) colors.onSurface else colors.onSurfaceMuted,
            textAlign = TextAlign.Center,
        )
    }
}

// ─── Switch + text-action row primitives ──────────────────────────────────

@Composable
internal fun SettingsSwitchRow(
    label: String,
    checked: Boolean,
    onToggle: (Boolean) -> Unit,
    dimWhenOff: Boolean = false,
) {
    val colors = MaterialTheme.iglooColors
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable { onToggle(!checked) }
            .padding(horizontal = 16.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.SpaceBetween,
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodyLarge,
            color = colors.onSurface,
            modifier = Modifier.weight(1f),
        )
        val switchColors = if (dimWhenOff && !checked) {
            SwitchDefaults.colors(
                uncheckedTrackColor = colors.surfaceElevated,
                uncheckedThumbColor = colors.onSurfaceMuted,
                uncheckedBorderColor = colors.borderSubtle,
            )
        } else {
            SwitchDefaults.colors(
                checkedTrackColor = colors.primary,
                checkedThumbColor = colors.onPrimary,
            )
        }
        Switch(
            checked = checked,
            onCheckedChange = onToggle,
            colors = switchColors,
        )
    }
}

@Composable
internal fun TextActionRow(label: String, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(horizontal = 16.dp, vertical = 14.dp),
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodyLarge,
            color = MaterialTheme.iglooColors.onSurface,
        )
    }
}
