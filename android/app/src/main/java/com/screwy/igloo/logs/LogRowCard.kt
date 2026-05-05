package com.screwy.igloo.logs

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.screwy.igloo.ui.theme.iglooColors

/**
 * Two-line log card replacing the monospace one-liner. Line 1: LEVEL + SUBSYSTEM
 * pills + HH:mm:ss. Line 2: human sentence from [EventDictionary], with a caret
 * that expands a key/value grid.
 */
@Composable
internal fun LogRowCard(row: LogRowDisplay) {
    val colors = MaterialTheme.iglooColors
    val isError = row.level == "error"
    val template = EventDictionary[row.event]
    val sentence = template?.render(row.fields) ?: row.event
    val hasExpandable = row.fields.isNotEmpty()
    var expanded by remember(row.id) { mutableStateOf(false) }

    Column(
        modifier = Modifier
            .fillMaxWidth()
            .background(
                if (isError) colors.error.copy(alpha = 0.06f) else Color.Transparent
            )
            .clickable(enabled = hasExpandable) { expanded = !expanded }
            .padding(horizontal = 14.dp, vertical = 10.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            LevelPill(level = row.level, stream = row.stream)
            SubsystemPill(subsystem = row.subsystem)
            Text(
                text = formatLogTimestamp(row.timestampMs),
                style = MaterialTheme.typography.bodySmall,
                color = colors.onSurfaceMuted,
                fontFamily = FontFamily.Monospace,
                modifier = Modifier.weight(1f).padding(start = 8.dp),
            )
        }
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = sentence,
                style = MaterialTheme.typography.bodyMedium,
                color = colors.onSurface,
                modifier = Modifier.weight(1f).padding(top = 3.dp),
            )
            if (hasExpandable) {
                Text(
                    text = if (expanded) " ⏷" else " ⏵",
                    style = MaterialTheme.typography.bodySmall,
                    color = colors.onSurfaceMuted,
                )
            }
        }
        if (expanded && hasExpandable) {
            val shown = template?.expandFields?.takeIf { it.isNotEmpty() }?.let { keys ->
                keys.mapNotNull { k -> row.fields[k]?.let { k to it } }
            } ?: row.fields.entries.map { it.key to it.value }
            FieldsGrid(shown)
        }
    }
}

@Composable
private fun LevelPill(level: String?, stream: String) {
    val colors = MaterialTheme.iglooColors
    val (text, bg, fg) = when {
        level == "error"  -> Triple("ERROR", colors.error.copy(alpha = 0.22f), colors.error)
        stream == "debug" -> Triple("DEBUG", colors.surfaceElevated, colors.onSurfaceMuted)
        else              -> Triple((level ?: "info").uppercase(), colors.primary.copy(alpha = 0.18f), colors.primary)
    }
    Pill(text, bg, fg)
}

@Composable
private fun SubsystemPill(subsystem: Subsystem) {
    val colors = MaterialTheme.iglooColors
    val (bg, fg) = when (subsystem) {
        Subsystem.Sync   -> colors.success.copy(alpha = 0.18f) to colors.success
        Subsystem.Outbox -> colors.warning.copy(alpha = 0.18f) to colors.warning
        Subsystem.Media  -> colors.info.copy(alpha = 0.18f)    to colors.info
        Subsystem.App    -> colors.info.copy(alpha = 0.18f)    to colors.info
        Subsystem.Other  -> colors.surfaceElevated             to colors.onSurfaceMuted
    }
    Pill(subsystem.label.uppercase(), bg, fg)
}

@Composable
private fun Pill(text: String, bg: Color, fg: Color) {
    Text(
        text = text,
        fontSize = 10.sp,
        fontWeight = FontWeight.SemiBold,
        color = fg,
        modifier = Modifier
            .padding(end = 6.dp)
            .clip(RoundedCornerShape(5.dp))
            .background(bg)
            .padding(horizontal = 7.dp, vertical = 2.dp),
    )
}

@Composable
private fun FieldsGrid(fields: List<Pair<String, String>>) {
    val colors = MaterialTheme.iglooColors
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(top = 6.dp)
            .clip(RoundedCornerShape(6.dp))
            .background(colors.background)
            .padding(horizontal = 8.dp, vertical = 6.dp),
    ) {
        fields.forEach { (k, v) ->
            Row(modifier = Modifier.fillMaxWidth().padding(vertical = 1.dp)) {
                Text(
                    text = k,
                    fontSize = 11.sp,
                    color = colors.onSurfaceMuted,
                    fontFamily = FontFamily.Monospace,
                    modifier = Modifier.padding(end = 10.dp),
                )
                Text(
                    text = displayFieldValue(v),
                    fontSize = 11.sp,
                    color = colors.onSurface,
                    fontFamily = FontFamily.Monospace,
                )
            }
        }
    }
}

internal fun displayFieldValue(value: String): String {
    if (value.length <= FIELD_VALUE_LIMIT) return value
    return value.take(FIELD_VALUE_LIMIT) + "\n..."
}

private const val FIELD_VALUE_LIMIT = 1_200
