package com.screwy.igloo.player

import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.gestures.awaitTouchSlopOrCancellation
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.unit.dp
import com.screwy.igloo.data.entity.SponsorBlockSegmentEntity

@Composable
internal fun ScrubberWithSegments(
    positionMs: Long,
    durationMs: Long,
    segments: List<SponsorBlockSegmentEntity>,
    previewSpritePath: String?,
    previewTrackJsonPath: String?,
    onSeekTo: (Long) -> Unit,
    onScrubStart: (Long) -> Unit,
    onScrubUpdate: (Long) -> Unit,
    onScrubEnd: (Long) -> Unit,
) {
    val accentColor = MaterialTheme.colorScheme.primary
    val duration = durationMs.coerceAtLeast(1L)
    val density = LocalDensity.current
    var isDragging by remember { mutableStateOf(false) }
    var dragFraction by remember { mutableFloatStateOf(0f) }
    var barWidthPx by remember { mutableIntStateOf(1) }
    val shownFraction = if (isDragging) {
        dragFraction
    } else {
        scrubberFractionForPosition(positionMs, duration)
    }

    Box(
        modifier = Modifier
            .fillMaxWidth()
            .height(if (isDragging) 86.dp else 24.dp)
            .onSizeChanged { barWidthPx = it.width.coerceAtLeast(1) }
            .pointerInput(durationMs, segments) {
                awaitEachGesture {
                    val down = awaitFirstDown(requireUnconsumed = false)
                    down.consume()
                    dragFraction = scrubberFractionForX(down.position.x, barWidthPx)
                    onScrubStart(scrubberTargetMs(dragFraction, duration))
                    val downSegment = scrubberSegmentAtFraction(
                        segments = segments,
                        fraction = dragFraction,
                        durationMs = duration,
                    )
                    val slop = awaitTouchSlopOrCancellation(down.id) { change, _ ->
                        change.consume()
                    }
                    if (slop == null) {
                        val target = scrubberTapTargetMs(
                            fraction = dragFraction,
                            durationMs = duration,
                            downSegment = downSegment,
                        )
                        onSeekTo(target)
                        onScrubEnd(target)
                        return@awaitEachGesture
                    }

                    isDragging = true
                    dragFraction = scrubberFractionForX(slop.position.x, barWidthPx)
                    onScrubUpdate(scrubberTargetMs(dragFraction, duration))
                    val pointerId = slop.id
                    while (true) {
                        val event = awaitPointerEvent()
                        val change = event.changes.firstOrNull { it.id == pointerId } ?: break
                        if (!change.pressed) break
                        change.consume()
                        dragFraction = scrubberFractionForX(change.position.x, barWidthPx)
                        onScrubUpdate(scrubberTargetMs(dragFraction, duration))
                    }
                    val target = scrubberTargetMs(dragFraction, duration)
                    onSeekTo(target)
                    onScrubEnd(target)
                    isDragging = false
                }
            },
    ) {
        if (isDragging) {
            SeekPreview(
                targetMs = scrubberTargetMs(dragFraction, duration),
                visible = true,
                previewSpritePath = previewSpritePath,
                previewTrackJsonPath = previewTrackJsonPath,
                modifier = Modifier
                    .align(Alignment.TopStart)
                    .offset(
                        x = with(density) {
                            trackThumbOffsetPx(
                                shownFraction = dragFraction,
                                barWidthPx = barWidthPx,
                                thumbWidthPx = 96.dp.toPx(),
                            ).toDp()
                        },
                    ),
            )
        }
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .height(3.dp)
                .align(if (isDragging) Alignment.BottomCenter else Alignment.Center)
                .background(Color.White.copy(alpha = 0.35f)),
        )
        Box(
            modifier = Modifier
                .fillMaxWidth(shownFraction.coerceIn(0f, 1f))
                .height(3.dp)
                .align(if (isDragging) Alignment.BottomStart else Alignment.CenterStart)
                .background(accentColor),
        ) {
        }
        segments.forEach { seg ->
            val (startFraction, widthFraction) = sponsorSegmentFractions(
                startTimeSec = seg.startTime,
                endTimeSec = seg.endTime,
                durationMs = duration,
            )
            if (widthFraction <= 0f) return@forEach
            val offsetDp = with(density) { (startFraction * barWidthPx.toFloat()).toDp() }
            val widthDp = with(density) { (widthFraction * barWidthPx.toFloat()).coerceAtLeast(2f).toDp() }
            Box(
                modifier = Modifier
                    .offset(x = offsetDp)
                    .width(widthDp)
                    .height(3.dp)
                    .align(if (isDragging) Alignment.BottomStart else Alignment.CenterStart)
                    .background(sponsorBlockColorFor(seg.category).copy(alpha = 0.85f)),
            )
        }
        Box(
            modifier = Modifier
                .align(if (isDragging) Alignment.BottomStart else Alignment.CenterStart)
                .offset(
                    x = with(density) {
                        trackThumbOffsetPx(
                            shownFraction = shownFraction,
                            barWidthPx = barWidthPx,
                            thumbWidthPx = 10.dp.toPx(),
                        ).toDp()
                    },
                ),
        ) {
            Box(
                modifier = Modifier
                    .size(10.dp)
                    .background(accentColor, CircleShape),
            )
        }
    }
}

internal fun scrubberFractionForX(xPx: Float, barWidthPx: Int): Float =
    (xPx / barWidthPx.coerceAtLeast(1).toFloat()).coerceIn(0f, 1f)

internal fun scrubberFractionForPosition(positionMs: Long, durationMs: Long): Float =
    (positionMs.toFloat() / durationMs.coerceAtLeast(1L).toFloat()).coerceIn(0f, 1f)

internal fun scrubberTargetMs(fraction: Float, durationMs: Long): Long =
    (fraction.coerceIn(0f, 1f) * durationMs.coerceAtLeast(1L).toFloat()).toLong()

internal fun scrubberTapTargetMs(
    fraction: Float,
    durationMs: Long,
    downSegment: SponsorBlockSegmentEntity?,
): Long =
    downSegment?.let { (it.endTime * 1000.0).toLong() }
        ?: scrubberTargetMs(fraction, durationMs)

internal fun scrubberSegmentAtFraction(
    segments: List<SponsorBlockSegmentEntity>,
    fraction: Float,
    durationMs: Long,
): SponsorBlockSegmentEntity? {
    if (durationMs <= 0L) return null
    return segments.firstOrNull { seg ->
        val (startFraction, widthFraction) = sponsorSegmentFractions(
            startTimeSec = seg.startTime,
            endTimeSec = seg.endTime,
            durationMs = durationMs,
        )
        fraction >= startFraction && fraction <= startFraction + widthFraction
    }
}

internal fun sponsorSegmentFractions(
    startTimeSec: Double,
    endTimeSec: Double,
    durationMs: Long,
): Pair<Float, Float> {
    if (durationMs <= 0L) return 0f to 0f
    val startMs = (startTimeSec * 1000.0).toLong().coerceAtLeast(0L)
    val endMs = (endTimeSec * 1000.0).toLong().coerceAtLeast(startMs)
    val startFraction = (startMs.toFloat() / durationMs.toFloat()).coerceIn(0f, 1f)
    val widthFraction = ((endMs - startMs).toFloat() / durationMs.toFloat()).coerceAtLeast(0f)
    return startFraction to widthFraction.coerceAtMost(1f - startFraction)
}

internal fun trackThumbOffsetPx(
    shownFraction: Float,
    barWidthPx: Int,
    thumbWidthPx: Float,
): Float {
    val clamped = shownFraction.coerceIn(0f, 1f)
    val maxOffset = (barWidthPx - thumbWidthPx).coerceAtLeast(0f)
    return ((clamped * barWidthPx) - (thumbWidthPx / 2f)).coerceIn(0f, maxOffset)
}

private fun sponsorBlockColorFor(category: String): Color = when (category.lowercase()) {
    "sponsor" -> Color(0xFF00D400)
    "selfpromo" -> Color(0xFFFFFF00)
    "interaction" -> Color(0xFF00FFFF)
    "intro" -> Color(0xFF00FFFF)
    "outro" -> Color(0xFF0202ED)
    "preview" -> Color(0xFF008FD6)
    "music_offtopic" -> Color(0xFFFF9900)
    "filler" -> Color(0xFF7E7E7E)
    else -> Color(0xFFAA00AA)
}
