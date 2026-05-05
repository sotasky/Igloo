package com.screwy.igloo.settings

import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavController
import com.screwy.igloo.media.CacheStats
import com.screwy.igloo.R
import com.screwy.igloo.settings.components.RetentionRow
import com.screwy.igloo.settings.components.SectionDescription
import com.screwy.igloo.settings.components.SectionHeader
import com.screwy.igloo.settings.components.SettingsSubScreen
import com.screwy.igloo.settings.components.SettingsSwitchRow
import com.screwy.igloo.settings.components.StoriesWindowRow
import com.screwy.igloo.settings.components.SyncIntervalRow
import com.screwy.igloo.settings.components.TextActionRow
import com.screwy.igloo.ui.UiEffect
import com.screwy.igloo.ui.UiEffects
import com.screwy.igloo.ui.theme.iglooColors
import org.koin.androidx.compose.koinViewModel
import org.koin.compose.koinInject

/**
 * Combined data-lifecycle screen — sync cadence, retention windows, and cache
 * usage all live here because they're three views of the same question
 * ("how much of the server do I keep on this device, and how often do I refresh
 * it"). Splitting them into separate hub entries felt redundant.
 */
@Composable
fun StorageRoute(
    navController: NavController,
    modifier: Modifier = Modifier,
) {
    val storageVm: StorageViewModel = koinViewModel()
    val uiEffects: UiEffects = koinInject()

    val syncEnabled by storageVm.syncEnabled.collectAsStateWithLifecycle()
    val syncWifiOnly by storageVm.syncWifiOnly.collectAsStateWithLifecycle()
    val syncInterval by storageVm.syncIntervalMinutes.collectAsStateWithLifecycle()
    val rYt by storageVm.retentionDaysYoutube.collectAsStateWithLifecycle()
    val rMm by storageVm.retentionDaysMoments.collectAsStateWithLifecycle()
    val rFd by storageVm.retentionDaysFeed.collectAsStateWithLifecycle()
    val storiesWindowHours by storageVm.storiesWindowHours.collectAsStateWithLifecycle()
    val stats by storageVm.stats.collectAsStateWithLifecycle()

    SettingsSubScreen(
        title = stringResource(R.string.settings_storage_sync),
        onBack = { navController.popBackStack() },
        modifier = modifier,
    ) {
        SectionHeader(stringResource(R.string.settings_section_sync))
        SectionDescription(stringResource(R.string.settings_sync_description))
        SettingsSwitchRow(
            label = stringResource(R.string.settings_background_sync),
            checked = syncEnabled,
            onToggle = storageVm::setSyncEnabled,
        )
        SettingsSwitchRow(
            label = stringResource(R.string.settings_wifi_only),
            checked = syncWifiOnly,
            onToggle = storageVm::setSyncWifiOnly,
            dimWhenOff = true,
        )
        SyncIntervalRow(
            selected = syncInterval,
            onSelect = storageVm::setSyncIntervalMinutes,
        )
        val syncing = stringResource(R.string.settings_syncing)
        TextActionRow(label = stringResource(R.string.action_sync_now)) {
            storageVm.triggerSyncNow()
            uiEffects.emit(UiEffect.Toast(syncing))
        }

        SectionHeader(stringResource(R.string.settings_section_retention))
        SectionDescription(stringResource(R.string.settings_retention_help))
        RetentionRow(stringResource(R.string.platform_youtube), rYt, storageVm::setRetentionDaysYoutube)
        RetentionRow(stringResource(R.string.nav_moments), rMm, storageVm::setRetentionDaysMoments)
        RetentionRow(stringResource(R.string.platform_x), rFd, storageVm::setRetentionDaysFeed)
        StoriesWindowRow(storiesWindowHours, storageVm::setStoriesWindowHours)

        SectionHeader(stringResource(R.string.settings_section_cache))
        if (stats.isEmpty()) {
            SectionDescription(stringResource(R.string.settings_no_cached_media))
        } else {
            stats.forEach { row -> StatsRow(row) }
        }
        TextActionRow(label = stringResource(R.string.action_clear_all_cache)) { storageVm.clearCache(null) }
        TextActionRow(label = stringResource(R.string.action_clear_youtube_cache)) { storageVm.clearCache("youtube_videos") }
        TextActionRow(label = stringResource(R.string.action_clear_moments_cache)) { storageVm.clearCache("shorts_videos") }

        SectionHeader(stringResource(R.string.settings_section_export))
        SectionDescription(stringResource(R.string.settings_export_help))
        val exportUnavailable = stringResource(R.string.settings_export_unavailable)
        TextActionRow(label = stringResource(R.string.action_export_data)) {
            uiEffects.emit(UiEffect.Toast(exportUnavailable))
        }
    }
}

@Composable
private fun StatsRow(row: CacheStats) {
    Row(
        modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = formatCacheBucketLabel(row.bucket),
            style = MaterialTheme.typography.bodyLarge,
            color = MaterialTheme.iglooColors.onSurface,
            modifier = Modifier.weight(1f),
        )
        Text(
            text = formatBytes(row.bytes),
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.iglooColors.onSurfaceMuted,
        )
    }
}

@Composable
internal fun formatCacheBucketLabel(bucket: String): String = when (bucket) {
    "feed_items" -> stringResource(cacheBucketLabelResource(bucket) ?: R.string.cache_feed_items)
    "youtube_videos" -> stringResource(R.string.platform_youtube)
    else -> cacheBucketLabelResource(bucket)?.let { stringResource(it) } ?: humanizeCacheBucket(bucket)
}

internal fun cacheBucketLabelResource(bucket: String): Int? = when (bucket) {
    "feed_items" -> R.string.cache_feed_items
    "shorts_videos" -> R.string.nav_moments
    "twitter_media" -> R.string.cache_x_media
    "avatars" -> R.string.cache_avatars
    "banners" -> R.string.cache_banners
    "subtitles" -> R.string.cache_subtitles
    else -> null
}

internal fun humanizeCacheBucket(bucket: String): String =
    bucket
        .split('_')
        .filter { it.isNotBlank() }
        .joinToString(" ") { part -> part.replaceFirstChar { it.uppercaseChar() } }

private fun formatBytes(bytes: Long): String {
    if (bytes < 1024) return "$bytes B"
    val kb = bytes / 1024.0
    if (kb < 1024) return String.format("%.0f KB", kb)
    val mb = kb / 1024.0
    if (mb < 1024) return String.format("%.1f MB", mb)
    val gb = mb / 1024.0
    return String.format("%.2f GB", gb)
}
