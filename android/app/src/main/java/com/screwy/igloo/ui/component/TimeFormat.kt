package com.screwy.igloo.ui.component

import java.util.concurrent.TimeUnit

/**
 * Shared time-formatting helpers for media grids.
 * Pure functions so unit tests can pin the grid duration-badge + watch-progress
 * formatting without spinning up Compose.
 */

/**
 * Formats a duration in milliseconds as "m:ss" (or "h:mm:ss" for >= 1h).
 *  - 0L            -> "0:00"
 *  - 45_000L       -> "0:45"
 *  - 754_000L      -> "12:34"
 *  - 3_726_000L    -> "1:02:06"
 */
internal fun formatDuration(ms: Long): String {
    val safe = ms.coerceAtLeast(0)
    val totalSeconds = TimeUnit.MILLISECONDS.toSeconds(safe)
    val hours = totalSeconds / 3600
    val minutes = (totalSeconds % 3600) / 60
    val seconds = totalSeconds % 60
    return if (hours > 0) {
        "%d:%02d:%02d".format(hours, minutes, seconds)
    } else {
        "%d:%02d".format(minutes, seconds)
    }
}

/**
 * Watch-progress fraction for the thumbnail progress bar on [VideoGrid] cells.
 * Returns 0f..1f (inclusive), clamped; 0f when [duration] is zero or negative,
 * or when [position] is null.
 */
internal fun progressFraction(position: Double?, duration: Double?): Float {
    if (position == null || duration == null || duration <= 0.0) return 0f
    val raw = (position / duration).toFloat()
    return raw.coerceIn(0f, 1f)
}
