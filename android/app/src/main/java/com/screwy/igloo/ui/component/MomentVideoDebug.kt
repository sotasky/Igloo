package com.screwy.igloo.ui.component

import android.media.MediaFormat
import android.os.SystemClock
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.media3.common.Format
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.analytics.AnalyticsListener
import androidx.media3.exoplayer.source.LoadEventInfo
import androidx.media3.exoplayer.source.MediaLoadData
import androidx.media3.exoplayer.video.VideoFrameMetadataListener
import com.screwy.igloo.log.Logger
import com.screwy.igloo.media.MediaUri
import java.io.IOException
import java.util.concurrent.atomic.AtomicLong
import kotlinx.coroutines.delay

@Composable
internal fun MomentVideoDebugTelemetry(
    pageIndex: Int,
    item: MomentItem,
    streamUri: MediaUri,
    player: ExoPlayer,
    loadedKey: String?,
    isActive: Boolean,
    shouldPrepare: Boolean,
    logger: Logger,
) {
    val renderedFrameCount = remember(item.videoId) { AtomicLong(0L) }
    val lastRenderedFrameAtMs = remember(item.videoId) { AtomicLong(0L) }

    DisposableEffect(player, item.videoId, streamUri) {
        if (!logger.debugEnabled()) {
            onDispose {}
        } else {
            renderedFrameCount.set(0L)
            lastRenderedFrameAtMs.set(0L)
            val frameListener = VideoFrameMetadataListener { _: Long, releaseTimeNs: Long, _: Format, _: MediaFormat? ->
                renderedFrameCount.incrementAndGet()
                lastRenderedFrameAtMs.set(SystemClock.elapsedRealtime())
                if (releaseTimeNs <= 0L) return@VideoFrameMetadataListener
            }
            val analyticsListener = object : AnalyticsListener {
                override fun onRenderedFirstFrame(
                    eventTime: AnalyticsListener.EventTime,
                    output: Any,
                    renderTimeMs: Long,
                ) {
                    logger.debugMoment("moments_player_analytics_first_frame") {
                        momentVideoDebugFields(
                            item = item,
                            pageIndex = pageIndex,
                            streamUri = streamUri,
                            player = player,
                            loadedKey = loadedKey,
                            targetLoadKey = loadedKey,
                        ) + mapOf(
                            "event_position_ms" to eventTime.currentPlaybackPositionMs,
                            "render_time_ms" to renderTimeMs,
                            "frame_count" to renderedFrameCount.get(),
                        )
                    }
                }

                override fun onDroppedVideoFrames(
                    eventTime: AnalyticsListener.EventTime,
                    droppedFrames: Int,
                    elapsedMs: Long,
                ) {
                    logger.debugMoment("moments_player_dropped_frames") {
                        momentVideoDebugFields(
                            item = item,
                            pageIndex = pageIndex,
                            streamUri = streamUri,
                            player = player,
                            loadedKey = loadedKey,
                            targetLoadKey = loadedKey,
                        ) + mapOf(
                            "dropped_frames" to droppedFrames,
                            "elapsed_ms" to elapsedMs,
                            "event_position_ms" to eventTime.currentPlaybackPositionMs,
                        )
                    }
                }

                override fun onVideoFrameProcessingOffset(
                    eventTime: AnalyticsListener.EventTime,
                    totalProcessingOffsetUs: Long,
                    frameCount: Int,
                ) {
                    if (frameCount <= 0) return
                    val averageOffsetUs = totalProcessingOffsetUs / frameCount
                    if (averageOffsetUs < 20_000L) return
                    logger.debugMoment("moments_player_frame_processing_slow") {
                        momentVideoDebugFields(
                            item = item,
                            pageIndex = pageIndex,
                            streamUri = streamUri,
                            player = player,
                            loadedKey = loadedKey,
                            targetLoadKey = loadedKey,
                        ) + mapOf(
                            "average_offset_us" to averageOffsetUs,
                            "frame_count" to frameCount,
                            "event_position_ms" to eventTime.currentPlaybackPositionMs,
                        )
                    }
                }

                override fun onPlayerError(
                    eventTime: AnalyticsListener.EventTime,
                    error: PlaybackException,
                ) {
                    logger.error(
                        "moments_player_error",
                        momentVideoDebugFields(
                            item = item,
                            pageIndex = pageIndex,
                            streamUri = streamUri,
                            player = player,
                            loadedKey = loadedKey,
                            targetLoadKey = loadedKey,
                        ) + mapOf("event_position_ms" to eventTime.currentPlaybackPositionMs),
                        error,
                    )
                }

                override fun onLoadError(
                    eventTime: AnalyticsListener.EventTime,
                    loadEventInfo: LoadEventInfo,
                    mediaLoadData: MediaLoadData,
                    error: IOException,
                    wasCanceled: Boolean,
                ) {
                    logger.error(
                        "moments_player_load_error",
                        momentVideoDebugFields(
                            item = item,
                            pageIndex = pageIndex,
                            streamUri = streamUri,
                            player = player,
                            loadedKey = loadedKey,
                            targetLoadKey = loadedKey,
                        ) + mapOf(
                            "event_position_ms" to eventTime.currentPlaybackPositionMs,
                            "uri" to loadEventInfo.uri.toString(),
                            "was_canceled" to wasCanceled,
                            "data_type" to mediaLoadData.dataType,
                            "track_type" to mediaLoadData.trackType,
                        ),
                        error,
                    )
                }
            }
            player.setVideoFrameMetadataListener(frameListener)
            player.addAnalyticsListener(analyticsListener)
            onDispose {
                player.removeAnalyticsListener(analyticsListener)
                player.clearVideoFrameMetadataListener(frameListener)
            }
        }
    }

    LaunchedEffect(isActive, shouldPrepare, player, item.videoId, loadedKey) {
        val loadedAtStart = loadedKey
        if (!logger.debugEnabled() || !isActive || !shouldPrepare || loadedAtStart == null) {
            return@LaunchedEffect
        }
        val startedAtMs = SystemClock.elapsedRealtime()
        val startingFrameCount = renderedFrameCount.get()
        var lastFrameCount = startingFrameCount
        var lastPositionMs = player.currentPosition
        var firstFrameLogged = false
        var playbackLogged = false
        var lastFreezeLogAtMs = 0L
        logger.debugMoment("moments_player_active_start") {
            momentVideoDebugFields(
                item = item,
                pageIndex = pageIndex,
                streamUri = streamUri,
                player = player,
                loadedKey = loadedAtStart,
                targetLoadKey = loadedAtStart,
            )
        }
        while (true) {
            delay(100L)
            val now = SystemClock.elapsedRealtime()
            val frameCount = renderedFrameCount.get()
            if (!firstFrameLogged && frameCount > startingFrameCount) {
                firstFrameLogged = true
                logger.debugMoment("moments_player_first_frame_latency") {
                    momentVideoDebugFields(
                        item = item,
                        pageIndex = pageIndex,
                        streamUri = streamUri,
                        player = player,
                        loadedKey = loadedAtStart,
                        targetLoadKey = loadedAtStart,
                    ) + mapOf(
                        "latency_ms" to (now - startedAtMs),
                        "frame_count" to frameCount,
                    )
                }
            }
            if (!playbackLogged && player.isPlaying && player.currentPosition > 0L) {
                playbackLogged = true
                logger.debugMoment("moments_player_playback_started") {
                    momentVideoDebugFields(
                        item = item,
                        pageIndex = pageIndex,
                        streamUri = streamUri,
                        player = player,
                        loadedKey = loadedAtStart,
                        targetLoadKey = loadedAtStart,
                    ) + mapOf(
                        "latency_ms" to (now - startedAtMs),
                        "frame_count" to frameCount,
                    )
                }
            }
            if (frameCount != lastFrameCount) {
                lastFrameCount = frameCount
                lastPositionMs = player.currentPosition
                continue
            }
            val lastFrameAtMs = lastRenderedFrameAtMs.get()
            val noFrameForMs = if (lastFrameAtMs > 0L) now - lastFrameAtMs else now - startedAtMs
            val positionAdvancedMs = player.currentPosition - lastPositionMs
            if (
                player.isPlaying &&
                firstFrameLogged &&
                noFrameForMs >= 650L &&
                positionAdvancedMs >= 500L &&
                now - lastFreezeLogAtMs >= 1_500L
            ) {
                lastFreezeLogAtMs = now
                logger.debugMoment("moments_player_video_freeze_detected") {
                    momentVideoDebugFields(
                        item = item,
                        pageIndex = pageIndex,
                        streamUri = streamUri,
                        player = player,
                        loadedKey = loadedAtStart,
                        targetLoadKey = loadedAtStart,
                    ) + mapOf(
                        "no_frame_for_ms" to noFrameForMs,
                        "position_advanced_ms" to positionAdvancedMs,
                        "frame_count" to frameCount,
                    )
                }
            }
        }
    }
}

internal inline fun Logger.debugMoment(
    event: String,
    fields: () -> Map<String, Any?>,
) {
    if (!debugEnabled()) return
    debug(event, fields())
}

internal fun momentVideoDebugFields(
    item: MomentItem,
    pageIndex: Int,
    streamUri: MediaUri,
    player: ExoPlayer,
    loadedKey: String?,
    targetLoadKey: String?,
    seedPositionMs: Long = 0L,
): Map<String, Any?> = mapOf(
    "page" to pageIndex,
    "video_id" to item.videoId,
    "channel_id" to item.channelId,
    "media_kind" to item.mediaKind.orEmpty(),
    "stream_kind" to streamUri.momentsDebugKind(),
    "seed_position_ms" to seedPositionMs,
    "loaded_key_video_id" to (momentStreamLoadKeyVideoId(loadedKey) ?: ""),
    "target_key_video_id" to (momentStreamLoadKeyVideoId(targetLoadKey) ?: ""),
    "current_media_id" to (player.currentMediaItem?.mediaId ?: ""),
    "player_state" to player.playbackState.momentPlayerStateDebugName(),
    "player_play_when_ready" to player.playWhenReady,
    "player_is_playing" to player.isPlaying,
    "player_position_ms" to player.currentPosition,
    "player_media_item_count" to player.mediaItemCount,
)

internal fun MediaUri.momentsDebugKind(): String = when (this) {
    is MediaUri.Local -> "local"
    is MediaUri.Remote -> "remote"
    MediaUri.Missing -> "missing"
}

internal fun Int.momentPlayerStateDebugName(): String = when (this) {
    Player.STATE_IDLE -> "idle"
    Player.STATE_BUFFERING -> "buffering"
    Player.STATE_READY -> "ready"
    Player.STATE_ENDED -> "ended"
    else -> toString()
}
