package com.screwy.igloo.player

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import java.io.File

/**
 * Renders the active VTT cue as a centered caption below the video surface.
 *
 * VTT parsing is in-memory on first composition. Expected file sizes are small
 * (~KB) so we don't bother with a streaming parser. Malformed cues are skipped
 * rather than failing the whole file.
 */

/** One subtitle cue — start/end in ms, text already whitespace-normalized. */
internal data class SubtitleCue(
    val startMs: Long,
    val endMs: Long,
    val text: String,
)

@Composable
fun SubtitleOverlay(
    subtitlePath: String?,
    currentPositionMs: () -> Long,
    modifier: Modifier = Modifier,
    visible: Boolean = true,
    bottomPadding: Dp = 12.dp,
) {
    if (!visible || subtitlePath == null) return
    val cues = remember(subtitlePath) {
        runCatching { File(subtitlePath).readText() }
            .map { parseVtt(it) }
            .getOrDefault(emptyList())
    }
    if (cues.isEmpty()) return

    // Re-sample the position every 200ms — finer than the eye perceives,
    // coarser than per-frame so it doesn't thrash recomposition.
    var posMs by remember { mutableStateOf(0L) }
    LaunchedEffect(subtitlePath) {
        while (isActive) {
            posMs = currentPositionMs()
            delay(200L)
        }
    }

    val active by remember(cues) {
        derivedStateOf {
            cues.firstOrNull { posMs in it.startMs..it.endMs }
        }
    }

    val current = active ?: return

    Box(
        modifier = modifier.padding(bottom = bottomPadding, start = 12.dp, end = 12.dp),
        contentAlignment = Alignment.Center,
    ) {
        Box(
            modifier = Modifier
                .clip(RoundedCornerShape(4.dp))
                .background(Color.Black.copy(alpha = 0.72f))
                .padding(horizontal = 10.dp, vertical = 4.dp),
        ) {
            Text(
                text = current.text,
                color = Color.White,
                style = MaterialTheme.typography.bodyMedium,
                textAlign = TextAlign.Center,
            )
        }
    }
}

/**
 * Parse a VTT file into a cue list. Skips the `WEBVTT` header, blank separator
 * lines, cue IDs, and anything that doesn't match the
 * `HH:MM:SS.mmm --> HH:MM:SS.mmm` timing line. Any cue whose timing line fails
 * to parse is dropped silently — graceful degradation per the task direction.
 *
 * Accepts both `HH:MM:SS.mmm` and `MM:SS.mmm` — VTT lets the hour field be
 * omitted for cues under an hour. `,` is accepted in place of `.` as a
 * pragmatic concession to SRT-flavored files.
 */
internal fun parseVtt(content: String): List<SubtitleCue> {
    return parseVttCueBlocks(content)
        .mapNotNull { cue ->
            if (cue.lines.isEmpty()) {
                null
            } else {
                SubtitleCue(
                    startMs = cue.startMs,
                    endMs = cue.endMs,
                    text = cue.lines.joinToString("\n"),
                )
            }
        }
}
