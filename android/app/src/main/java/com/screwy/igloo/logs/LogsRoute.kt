package com.screwy.igloo.logs

import android.content.ClipData
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.ContentCopy
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.ClipEntry
import androidx.compose.ui.platform.LocalClipboard
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavController
import com.screwy.igloo.R
import com.screwy.igloo.ui.UiEffect
import com.screwy.igloo.ui.UiEffects
import com.screwy.igloo.ui.component.ScreenHeader
import com.screwy.igloo.ui.theme.iglooColors
import kotlinx.coroutines.launch
import org.koin.androidx.compose.koinViewModel
import org.koin.compose.koinInject

/**
 * Logs inspector route. Top bar (close / title / copy / refresh), six-chip
 * filter row (All / Errors / Sync / Outbox / Media / Debug), and a list of
 * [LogRowCard]s that render each event as a human sentence with expandable
 * field details.
 */
@Composable
fun LogsRoute(
    navController: NavController? = null,
    modifier: Modifier = Modifier,
    initialFilter: LogFilter = LogFilter.All,
) {
    val vm: LogsViewModel = koinViewModel()
    val rows by vm.filteredRows.collectAsStateWithLifecycle()
    val filter by vm.filter.collectAsStateWithLifecycle()
    val clipboard = LocalClipboard.current
    val scope = rememberCoroutineScope()
    val uiEffects: UiEffects = koinInject()
    val logsCopied = stringResource(R.string.logs_copied)
    val logsClipboardLabel = stringResource(R.string.logs_clipboard_label)

    LaunchedEffect(initialFilter) {
        vm.setFilter(initialFilter)
    }

    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = PaddingValues(bottom = 16.dp),
    ) {
        item(key = "logs_header") {
            // Header is part of the scroll content so it naturally disappears
            // as the log list moves, matching the shared settings header.
            ScreenHeader(
                title = stringResource(R.string.logs_title),
                navigationIcon = {
                    IconButton(onClick = { navController?.popBackStack() }) {
                        Icon(
                            imageVector = Icons.Default.Close,
                            contentDescription = stringResource(R.string.action_close_logs),
                        )
                    }
                },
                actions = {
                    IconButton(onClick = {
                        val text = rows.joinToString("\n") { it.toPlainTextLine() }
                        scope.launch {
                            clipboard.setClipEntry(
                                ClipEntry(ClipData.newPlainText(logsClipboardLabel, text)),
                            )
                            uiEffects.emit(UiEffect.Toast(logsCopied))
                        }
                    }) {
                        Icon(
                            imageVector = Icons.Default.ContentCopy,
                            contentDescription = stringResource(R.string.action_copy_logs),
                            tint = MaterialTheme.iglooColors.onSurfaceMuted,
                        )
                    }
                    IconButton(onClick = { vm.refresh() }) {
                        Icon(
                            imageVector = Icons.Default.Refresh,
                            contentDescription = stringResource(R.string.action_refresh_logs),
                            tint = MaterialTheme.iglooColors.onSurfaceMuted,
                        )
                    }
                },
            )
        }

        item(key = "logs_filters") {
            LazyRow(
                modifier = Modifier.fillMaxWidth(),
                contentPadding = PaddingValues(horizontal = 12.dp, vertical = 4.dp),
                horizontalArrangement = Arrangement.spacedBy(6.dp),
            ) {
                items(items = LogFilter.entries, key = { it.name }) { f ->
                    LogFilterChip(
                        label = stringResource(f.labelRes),
                        selected = f == filter,
                        onClick = { vm.setFilter(f) },
                    )
                }
            }
        }

        if (rows.isEmpty()) {
            item(key = "logs_empty") {
                Box(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(24.dp),
                    contentAlignment = Alignment.Center,
                ) {
                    Text(
                        text = stringResource(R.string.logs_empty_filter),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.iglooColors.onSurfaceMuted,
                    )
                }
            }
        } else {
            items(items = rows, key = { it.id }) { row -> LogRowCard(row) }
        }
    }
}

/** Outlined chip; selected state fills with accent-muted and tints the label. */
@Composable
private fun LogFilterChip(label: String, selected: Boolean, onClick: () -> Unit) {
    val colors = MaterialTheme.iglooColors
    Box(
        modifier = Modifier
            .clip(RoundedCornerShape(10.dp))
            .background(if (selected) colors.primaryMuted else Color.Transparent)
            .border(
                width = 1.dp,
                color = if (selected) colors.primary else colors.borderSubtle,
                shape = RoundedCornerShape(10.dp),
            )
            .clickable(onClick = onClick)
            .padding(horizontal = 10.dp, vertical = 8.dp),
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodyMedium,
            color = if (selected) colors.primary else colors.onSurfaceMuted,
            maxLines = 1,
            softWrap = false,
        )
    }
}
