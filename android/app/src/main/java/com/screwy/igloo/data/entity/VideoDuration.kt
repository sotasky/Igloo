package com.screwy.igloo.data.entity

/**
 * Server/store contract: `videos.duration` is persisted in whole seconds.
 * UI surfaces render milliseconds, so convert at the edge rather than
 * sprinkling `* 1000` across routes.
 */
fun VideoEntity.durationMs(): Long = (duration ?: 0L).coerceAtLeast(0L) * 1000L
