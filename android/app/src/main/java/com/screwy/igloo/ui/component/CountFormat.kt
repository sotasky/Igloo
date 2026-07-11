package com.screwy.igloo.ui.component

import java.util.Locale

internal fun compactCount(value: Long): String {
    val (scale, suffix) = when {
        value >= 1_000_000_000L -> 1_000_000_000.0 to "B"
        value >= 1_000_000L -> 1_000_000.0 to "M"
        value >= 1_000L -> 1_000.0 to "K"
        else -> return value.toString()
    }
    val scaled = value / scale
    val number =
        if (scaled >= 100) {
            String.format(Locale.ROOT, "%.0f", scaled)
        } else {
            String.format(Locale.ROOT, "%.1f", scaled).removeSuffix(".0")
        }
    return number + suffix
}
