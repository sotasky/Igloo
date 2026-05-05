package com.screwy.igloo.sync

/**
 * Inbound delta streams. Each has its own endpoint, cursor key in the `cursors`
 * table, and failure mode.
 *
 * The reconciler iterates streams in `priority` order on every pass so a slow YouTube
 * pull can't starve a fast channels update. Practical default order is channels
 * -> feed -> shorts -> youtube.
 */
enum class SyncStream(
    val cursorKey: String,
    val priority: Int,
) {
    Channels(cursorKey = "channels", priority = 0),
    Feed(cursorKey = "feed", priority = 1),
    Shorts(cursorKey = "shorts", priority = 2),
    Youtube(cursorKey = "youtube_videos", priority = 3);

    companion object {
        val ALL: Set<SyncStream> = values().toSet()
    }
}
