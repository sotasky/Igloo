package com.screwy.igloo.player

import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.media3.common.Player
import androidx.media3.exoplayer.ExoPlayer
import com.screwy.igloo.R
import com.screwy.igloo.data.entity.SponsorBlockSegmentEntity
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive

internal class SponsorBlockPlaybackController(
    private val seekTo: (Long) -> Unit,
    private val skippedMessage: (String) -> String,
    private val nowMs: () -> Long = System::currentTimeMillis,
) {
    var skipSegment by mutableStateOf<SponsorBlockUiSegment?>(null)
        private set
    var autoSkipMessage by mutableStateOf<String?>(null)
        private set

    private var activeKey: String? = null
    private var lastSeekAtMs by mutableLongStateOf(0L)
    private var lastSeekPositionMs by mutableStateOf<Long?>(null)
    private var manualSegmentKey: String? = null

    fun reset() {
        skipSegment = null
        autoSkipMessage = null
        activeKey = null
        manualSegmentKey = null
    }

    fun onSeek(positionMs: Long) {
        lastSeekAtMs = nowMs()
        lastSeekPositionMs = positionMs
        manualSegmentKey = null
    }

    fun onTick(
        isPlaying: Boolean,
        positionMs: Long,
        segments: List<SponsorBlockUiSegment>,
    ) {
        if (!isPlaying || segments.isEmpty()) return
        val segment = segments.firstOrNull { seg ->
            positionMs >= seg.startMs && positionMs < seg.endMs - 300L
        }
        if (segment == null) {
            if (activeKey != null) {
                activeKey = null
                skipSegment = null
            }
            manualSegmentKey = null
            return
        }
        if (lastSeekPositionMs?.let { it >= segment.startMs && it < segment.endMs } == true) {
            manualSegmentKey = segment.key
        }
        if (segment.key == activeKey) return
        activeKey = segment.key
        when (segment.mode) {
            SponsorBlockModeAsk -> skipSegment = segment
            SponsorBlockModeSilent -> {
                val wasRecentSeek = nowMs() - lastSeekAtMs < 1_000L
                if (wasRecentSeek || manualSegmentKey == segment.key) {
                    skipSegment = segment
                } else {
                    seekTo(segment.endMs)
                    skipSegment = null
                    activeKey = null
                    autoSkipMessage = skippedMessage(segment.category)
                }
            }
        }
    }

    fun skip(segment: SponsorBlockUiSegment) {
        seekTo(segment.endMs)
        skipSegment = null
        activeKey = null
        autoSkipMessage = skippedMessage(segment.category)
    }

    fun clearAutoSkipMessage() {
        autoSkipMessage = null
    }
}

internal data class SponsorBlockPlaybackState(
    val visibleSegments: List<SponsorBlockSegmentEntity>,
    val skipSegment: SponsorBlockUiSegment?,
    val autoSkipMessage: String?,
    val onSkip: (SponsorBlockUiSegment) -> Unit,
)

@Composable
internal fun rememberSponsorBlockPlaybackState(
    videoId: String,
    player: ExoPlayer,
    segments: List<SponsorBlockSegmentEntity>,
    modes: Map<String, String>,
): SponsorBlockPlaybackState {
    val context = LocalContext.current
    val activeSegments = remember(segments, modes) {
        buildSponsorBlockUiSegments(segments, modes)
    }
    val visibleSegments = remember(activeSegments) {
        activeSegments.map { it.source }
    }
    val controller = remember(videoId, player) {
        SponsorBlockPlaybackController(
            seekTo = player::seekTo,
            skippedMessage = { category ->
                context.getString(
                    R.string.sponsorblock_segment_skipped,
                    context.getString(sponsorBlockLabelRes(category)),
                )
            },
        )
    }

    DisposableEffect(player, controller) {
        val listener = object : Player.Listener {
            override fun onPositionDiscontinuity(
                oldPosition: Player.PositionInfo,
                newPosition: Player.PositionInfo,
                reason: Int,
            ) {
                if (reason == Player.DISCONTINUITY_REASON_SEEK) {
                    controller.onSeek(newPosition.positionMs)
                }
            }
        }
        player.addListener(listener)
        onDispose {
            player.removeListener(listener)
        }
    }

    LaunchedEffect(activeSegments, player, videoId, controller) {
        controller.reset()
        while (isActive) {
            delay(500L)
            controller.onTick(
                isPlaying = player.isPlaying,
                positionMs = player.currentPosition,
                segments = activeSegments,
            )
        }
    }
    LaunchedEffect(controller.autoSkipMessage) {
        if (controller.autoSkipMessage != null) {
            delay(2_000L)
            controller.clearAutoSkipMessage()
        }
    }

    return SponsorBlockPlaybackState(
        visibleSegments = visibleSegments,
        skipSegment = controller.skipSegment,
        autoSkipMessage = controller.autoSkipMessage,
        onSkip = controller::skip,
    )
}
