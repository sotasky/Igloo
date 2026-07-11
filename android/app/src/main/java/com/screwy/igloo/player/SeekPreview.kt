package com.screwy.igloo.player

import android.graphics.BitmapFactory
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.dp
import com.screwy.igloo.ui.component.formatDuration
import java.io.File
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.json.Json

@Composable
fun SeekPreview(
    targetMs: Long,
    visible: Boolean,
    previewSpritePath: String? = null,
    previewTrackJsonPath: String? = null,
    modifier: Modifier = Modifier,
    width: Dp = 96.dp,
    height: Dp = 54.dp,
) {
    if (!visible) return
    val bitmap by
        produceState<androidx.compose.ui.graphics.ImageBitmap?>(null, previewSpritePath) {
            value = null
            value =
                withContext(Dispatchers.IO) {
                    previewSpritePath?.let(BitmapFactory::decodeFile)?.asImageBitmap()
                }
        }
    val trackCues by
        produceState(emptyList<PreviewCue>(), previewTrackJsonPath) {
            value = emptyList()
            value =
                withContext(Dispatchers.IO) {
                    previewTrackJsonPath
                        ?.let { path -> runCatching { File(path).readText() }.getOrNull() }
                        ?.let(::parsePreviewTrackJson)
                        .orEmpty()
                }
        }
    val cue = remember(targetMs, trackCues) { findPreviewCue(trackCues, targetMs) }
    Box(
        modifier =
            modifier
                .clip(RoundedCornerShape(6.dp))
                .background(Color.Black.copy(alpha = 0.82f))
                .padding(4.dp),
        contentAlignment = Alignment.Center,
    ) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            val previewBitmap = bitmap
            if (previewBitmap != null && cue != null) {
                Canvas(
                    modifier =
                        Modifier.size(width = width, height = height).clip(RoundedCornerShape(4.dp))
                ) {
                    drawImage(
                        image = previewBitmap,
                        srcOffset = IntOffset(cue.x, cue.y),
                        srcSize = IntSize(cue.width, cue.height),
                        dstOffset = IntOffset.Zero,
                        dstSize = IntSize(size.width.toInt(), size.height.toInt()),
                    )
                }
            }
            Text(
                text = formatDuration(targetMs),
                style = MaterialTheme.typography.labelMedium,
                color = Color.White,
            )
        }
    }
}

internal data class PreviewCue(
    val startMs: Long,
    val endMs: Long,
    val x: Int,
    val y: Int,
    val width: Int,
    val height: Int,
)

internal fun findPreviewCue(cues: List<PreviewCue>, targetMs: Long): PreviewCue? =
    cues.firstOrNull { targetMs >= it.startMs && targetMs < it.endMs }
        ?: cues.lastOrNull()?.takeIf { targetMs >= it.startMs }

internal fun parsePreviewTrackJson(text: String): List<PreviewCue> {
    val track =
        runCatching { previewTrackJson.decodeFromString<PreviewTrackJson>(text) }.getOrNull()
            ?: return emptyList()
    if (track.version != 1 || track.tileWidth <= 0 || track.tileHeight <= 0 || track.columns <= 0) {
        return emptyList()
    }
    return track.cues.mapNotNull { cue ->
        if (cue.endMs <= cue.startMs || cue.w <= 0 || cue.h <= 0) return@mapNotNull null
        PreviewCue(
            startMs = cue.startMs,
            endMs = cue.endMs,
            x = cue.x,
            y = cue.y,
            width = cue.w,
            height = cue.h,
        )
    }
}

private val previewTrackJson = Json {
    ignoreUnknownKeys = true
    coerceInputValues = true
}

@Serializable
private data class PreviewTrackJson(
    val version: Int = 0,
    @SerialName("duration_ms") val durationMs: Long = 0,
    @SerialName("tile_width") val tileWidth: Int = 0,
    @SerialName("tile_height") val tileHeight: Int = 0,
    val columns: Int = 0,
    val cues: List<PreviewCueJson> = emptyList(),
)

@Serializable
private data class PreviewCueJson(
    @SerialName("start_ms") val startMs: Long = 0,
    @SerialName("end_ms") val endMs: Long = 0,
    val x: Int = 0,
    val y: Int = 0,
    val w: Int = 0,
    val h: Int = 0,
)
