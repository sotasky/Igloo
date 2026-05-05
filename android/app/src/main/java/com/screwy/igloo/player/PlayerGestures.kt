package com.screwy.igloo.player

import android.app.Activity
import android.content.Context
import android.media.AudioManager
import androidx.compose.foundation.gestures.detectDragGestures
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.onSizeChanged
import androidx.media3.exoplayer.ExoPlayer
import kotlin.math.roundToInt

/**
 * Gesture layer for the player surface.
 *
 * Owns the pointer-input pipeline that translates touches into ExoPlayer calls:
 *  - double-tap L/R halves → 10s skip back/forward.
 *  - vertical drag L/R halves → brightness / volume.
 *  - horizontal drag → scrubber seek with preview tooltip, committed on release.
 *  - long-press → temporary 2× speed, restored on release.
 *
 * Pure math lives in [skipBackwardMs], [skipForwardMs], [seekFromHorizontalDrag]
 * so the tests don't need to instantiate ExoPlayer or Compose.
 */
/** Skip-back clamp at zero. */
internal fun skipBackwardMs(currentMs: Long): Long =
    (currentMs - SKIP_STEP_MS).coerceAtLeast(0L)

/**
 * Skip-forward clamp at duration. If [currentMs] already exceeds [durationMs]
 * (defensive: racy updates on a just-swapped video), the clamp still returns
 * `currentMs` rather than reeling the position backwards.
 */
internal fun skipForwardMs(currentMs: Long, durationMs: Long): Long =
    (currentMs + SKIP_STEP_MS).coerceAtMost(durationMs.coerceAtLeast(currentMs))

/**
 * Maps a horizontal drag (in pixels, across [widthPx]) to an absolute target
 * position. One full-width sweep = full duration; fractional sweeps scale
 * linearly. Clamps to `[0, durationMs]`. Returns `currentMs` unchanged on
 * zero-width / zero-duration surfaces (defensive: division-by-zero would
 * propagate as NaN).
 */
internal fun seekFromHorizontalDrag(
    currentMs: Long,
    dragPx: Float,
    widthPx: Float,
    durationMs: Long,
): Long {
    if (widthPx <= 0f || durationMs <= 0L) return currentMs
    val delta = (dragPx / widthPx) * durationMs.toDouble()
    val target = currentMs + delta.toLong()
    return target.coerceIn(0L, durationMs)
}

internal fun adjustedLevelFromVerticalDrag(
    startLevel: Float,
    dragPx: Float,
    heightPx: Float,
): Float {
    if (heightPx <= 0f) return startLevel.coerceIn(0f, 1f)
    val effectiveHeight = (heightPx * VERTICAL_LEVEL_DRAG_FRACTION).coerceAtLeast(1f)
    return (startLevel - (dragPx / effectiveHeight)).coerceIn(0f, 1f)
}

internal fun volumeIndexForFraction(fraction: Float, maxVolume: Int): Int {
    val max = maxVolume.coerceAtLeast(1)
    return (fraction.coerceIn(0f, 1f) * max).roundToInt().coerceIn(0, max)
}

private const val SKIP_STEP_MS: Long = 10_000L
private const val BOOST_SPEED: Float = 2.0f
private const val NORMAL_SPEED: Float = 1.0f
private const val MIN_BRIGHTNESS: Float = 0.05f
private const val VERTICAL_LEVEL_DRAG_FRACTION: Float = 0.62f

/**
 * Gesture overlay — sits above the video surface and below the transport
 * controls. Double-tap skips, long-press speed-boosts, horizontal drag scrubs
 * (commit-on-release), vertical drag adjusts brightness/volume.
 */
@Composable
fun PlayerGestures(
    player: ExoPlayer,
    modifier: Modifier = Modifier,
    onTap: () -> Unit = {},
    onScrubStart: () -> Unit = {},
    onScrubUpdate: (targetMs: Long) -> Unit = { _ -> },
    onScrubEnd: (targetMs: Long) -> Unit = { _ -> },
    onBrightnessChange: (level: Float) -> Unit = { _ -> },
    onVolumeChange: (level: Float) -> Unit = { _ -> },
) {
    val context = LocalContext.current
    val activity = context.findActivity()
    val audioManager = remember(context) {
        context.getSystemService(Context.AUDIO_SERVICE) as? AudioManager
    }
    val surfaceWidthPx = remember { mutableStateOf(0f) }
    val surfaceHeightPx = remember { mutableStateOf(0f) }
    val scrubTarget = remember { mutableStateOf(0L) }
    val scrubbing = remember { mutableStateOf(false) }
    val dragMode = remember { mutableStateOf<PlayerDragMode?>(null) }
    val verticalDragPx = remember { mutableFloatStateOf(0f) }
    val verticalStartLevel = remember { mutableFloatStateOf(0f) }

    Box(
        modifier = modifier
            .fillMaxSize()
            .onSizeChanged {
                surfaceWidthPx.value = it.width.toFloat()
                surfaceHeightPx.value = it.height.toFloat()
            }
            .pointerInput(player) {
                detectTapGestures(
                    onTap = { onTap() },
                    onDoubleTap = { offset ->
                        val widthPx = surfaceWidthPx.value
                        val isLeft = widthPx > 0f && offset.x < widthPx / 2f
                        val current = player.currentPosition
                        val duration = player.duration.coerceAtLeast(0L)
                        val target = if (isLeft) skipBackwardMs(current)
                                     else skipForwardMs(current, duration)
                        player.seekTo(target)
                    },
                    onLongPress = {
                        player.setPlaybackSpeed(BOOST_SPEED)
                    },
                    onPress = {
                        // Wait for release — if it was a long-press the speed is
                        // already boosted; restore on release (success or cancel).
                        tryAwaitRelease()
                        if (player.playbackParameters.speed != NORMAL_SPEED) {
                            player.setPlaybackSpeed(NORMAL_SPEED)
                        }
                    },
                )
            }
            .pointerInput(player) {
                detectDragGestures(
                    onDragStart = {
                        scrubTarget.value = player.currentPosition
                        scrubbing.value = false
                        dragMode.value = null
                        verticalDragPx.floatValue = 0f
                        verticalStartLevel.floatValue = 0f
                    },
                    onDrag = { change, dragAmount ->
                        change.consume()
                        val mode = dragMode.value ?: run {
                            val widthPx = surfaceWidthPx.value
                            val isLeft = widthPx > 0f && change.position.x < widthPx / 2f
                            if (kotlin.math.abs(dragAmount.x) >= kotlin.math.abs(dragAmount.y)) {
                                PlayerDragMode.Scrub
                            } else if (isLeft) {
                                PlayerDragMode.Brightness
                            } else {
                                PlayerDragMode.Volume
                            }.also { dragMode.value = it }
                        }
                        when (mode) {
                            PlayerDragMode.Scrub -> {
                                if (!scrubbing.value) {
                                    scrubbing.value = true
                                    onScrubStart()
                                }
                                val widthPx = surfaceWidthPx.value
                                val duration = player.duration.coerceAtLeast(0L)
                                scrubTarget.value = seekFromHorizontalDrag(
                                    currentMs = scrubTarget.value,
                                    dragPx = dragAmount.x,
                                    widthPx = widthPx,
                                    durationMs = duration,
                                )
                                onScrubUpdate(scrubTarget.value)
                            }
                            PlayerDragMode.Brightness -> {
                                if (verticalDragPx.floatValue == 0f) {
                                    verticalStartLevel.floatValue = readBrightness(activity)
                                }
                                verticalDragPx.floatValue += dragAmount.y
                                val level = adjustedLevelFromVerticalDrag(
                                    startLevel = verticalStartLevel.floatValue,
                                    dragPx = verticalDragPx.floatValue,
                                    heightPx = surfaceHeightPx.value,
                                ).coerceAtLeast(MIN_BRIGHTNESS)
                                setBrightness(activity, level)?.let(onBrightnessChange)
                            }
                            PlayerDragMode.Volume -> {
                                if (verticalDragPx.floatValue == 0f) {
                                    verticalStartLevel.floatValue = readVolumeFraction(audioManager)
                                }
                                verticalDragPx.floatValue += dragAmount.y
                                val level = adjustedLevelFromVerticalDrag(
                                    startLevel = verticalStartLevel.floatValue,
                                    dragPx = verticalDragPx.floatValue,
                                    heightPx = surfaceHeightPx.value,
                                )
                                setVolumeFraction(audioManager, level)?.let(onVolumeChange)
                            }
                        }
                    },
                    onDragEnd = {
                        if (scrubbing.value) {
                            player.seekTo(scrubTarget.value)
                            onScrubEnd(scrubTarget.value)
                            scrubbing.value = false
                        }
                        dragMode.value = null
                        verticalDragPx.floatValue = 0f
                    },
                    onDragCancel = {
                        scrubbing.value = false
                        dragMode.value = null
                        verticalDragPx.floatValue = 0f
                    },
                )
            }
    )
}

private enum class PlayerDragMode {
    Scrub,
    Brightness,
    Volume,
}

private fun readBrightness(activity: Activity?): Float {
    val attrs = activity?.window?.attributes
    return if (attrs != null && attrs.screenBrightness > 0f) attrs.screenBrightness else 0.5f
}

private fun setBrightness(activity: Activity?, level: Float): Float? {
    val window = activity?.window ?: return null
    val attrs = window.attributes
    attrs.screenBrightness = level.coerceIn(MIN_BRIGHTNESS, 1f)
    window.attributes = attrs
    return attrs.screenBrightness
}

private fun readVolumeFraction(audioManager: AudioManager?): Float {
    val audio = audioManager ?: return 0.5f
    val maxVolume = audio.getStreamMaxVolume(AudioManager.STREAM_MUSIC).coerceAtLeast(1)
    return audio.getStreamVolume(AudioManager.STREAM_MUSIC).toFloat() / maxVolume.toFloat()
}

private fun setVolumeFraction(audioManager: AudioManager?, level: Float): Float? {
    val audio = audioManager ?: return null
    val maxVolume = audio.getStreamMaxVolume(AudioManager.STREAM_MUSIC).coerceAtLeast(1)
    val current = audio.getStreamVolume(AudioManager.STREAM_MUSIC)
    val target = volumeIndexForFraction(level, maxVolume)
    if (target != current) {
        audio.setStreamVolume(AudioManager.STREAM_MUSIC, target, 0)
    }
    return target.toFloat() / maxVolume.toFloat()
}

private tailrec fun Context.findActivity(): Activity? = when (this) {
    is Activity -> this
    is android.content.ContextWrapper -> baseContext.findActivity()
    else -> null
}
