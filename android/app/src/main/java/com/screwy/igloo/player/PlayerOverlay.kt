package com.screwy.igloo.player

import android.content.Context
import android.media.AudioManager
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.VolumeOff
import androidx.compose.material.icons.automirrored.filled.VolumeUp
import androidx.compose.material.icons.filled.ClosedCaption
import androidx.compose.material.icons.filled.ClosedCaptionDisabled
import androidx.compose.material.icons.filled.Fullscreen
import androidx.compose.material.icons.filled.FullscreenExit
import androidx.compose.material.icons.filled.Pause
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.SkipNext
import androidx.compose.material.icons.filled.SkipPrevious
import androidx.compose.material.icons.filled.Speed
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Slider
import androidx.compose.material3.Text
import androidx.compose.runtime.getValue
import androidx.compose.ui.res.stringResource
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.media3.exoplayer.ExoPlayer
import com.screwy.igloo.R
import com.screwy.igloo.data.entity.SponsorBlockSegmentEntity
import com.screwy.igloo.ui.component.formatDuration
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive

/**
 * Player transport overlay. The route owns visibility so single taps can be
 * handled by [PlayerGestures] without the overlay swallowing them.
 */
@Composable
fun PlayerOverlay(
    player: ExoPlayer,
    title: String,
    onBack: () -> Unit,
    onPreviousVideo: (() -> Unit)?,
    onNextVideo: (() -> Unit)?,
    segments: List<SponsorBlockSegmentEntity>,
    showSubtitles: Boolean,
    onToggleSubtitles: () -> Unit,
    isFullscreen: Boolean,
    onToggleFullscreen: () -> Unit,
    controlsVisible: Boolean,
    onControlsVisibleChange: (Boolean) -> Unit,
    previewSpritePath: String? = null,
    previewTrackJsonPath: String? = null,
    modifier: Modifier = Modifier,
) {
    val backLabel = stringResource(R.string.action_back)
    val previousVideoLabel = stringResource(R.string.player_previous_video)
    val nextVideoLabel = stringResource(R.string.player_next_video)
    val playLabel = stringResource(R.string.action_play)
    val pauseLabel = stringResource(R.string.action_pause)
    val showSubtitlesLabel = stringResource(R.string.player_show_subtitles)
    val hideSubtitlesLabel = stringResource(R.string.player_hide_subtitles)
    val volumeLabel = stringResource(R.string.player_volume)
    val playbackSpeedLabel = stringResource(R.string.player_playback_speed)
    val enterFullscreenLabel = stringResource(R.string.action_enter_fullscreen)
    val exitFullscreenLabel = stringResource(R.string.action_exit_fullscreen)

    val context = LocalContext.current
    val audioManager = remember(context) {
        context.getSystemService(Context.AUDIO_SERVICE) as? AudioManager
    }
    var isPlaying by remember { mutableStateOf(player.isPlaying) }
    var positionMs by remember { mutableLongStateOf(0L) }
    var durationMs by remember { mutableLongStateOf(0L) }
    var speed by remember { mutableFloatStateOf(player.playbackParameters.speed) }
    var speedMenuOpen by remember { mutableStateOf(false) }
    var volumeMenuOpen by remember { mutableStateOf(false) }
    var volumeFraction by remember { mutableFloatStateOf(readVolumeFraction(audioManager)) }
    var interactionTick by remember { mutableLongStateOf(0L) }
    var isScrubbing by remember { mutableStateOf(false) }
    var scrubTargetMs by remember { mutableLongStateOf(0L) }

    fun keepVisible() {
        interactionTick = System.currentTimeMillis()
        onControlsVisibleChange(true)
    }

    LaunchedEffect(player) {
        while (isActive) {
            isPlaying = player.isPlaying
            positionMs = player.currentPosition.coerceAtLeast(0L)
            durationMs = player.duration.coerceAtLeast(0L)
            speed = player.playbackParameters.speed
            volumeFraction = readVolumeFraction(audioManager)
            delay(250L)
        }
    }

    LaunchedEffect(controlsVisible, interactionTick, isScrubbing) {
        if (controlsVisible && !isScrubbing) {
            delay(3_000L)
            if (!isScrubbing) onControlsVisibleChange(false)
        }
    }

    val shownPositionMs = if (isScrubbing) scrubTargetMs else positionMs

    Box(modifier = modifier.fillMaxSize()) {
        if (!controlsVisible) return@Box

        Box(
            modifier = Modifier
                .fillMaxSize()
                .background(Color.Black.copy(alpha = 0.35f)),
        ) {
            Row(
                modifier = Modifier
                    .align(Alignment.TopStart)
                    .fillMaxWidth()
                    .statusBarsPadding()
                    .padding(horizontal = 8.dp, vertical = 8.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                IconButton(
                    onClick = {
                        onBack()
                        keepVisible()
                    },
                ) {
                    Icon(
                        imageVector = Icons.AutoMirrored.Filled.ArrowBack,
                        contentDescription = backLabel,
                        tint = Color.White,
                    )
                }
                Text(
                    text = title,
                    color = Color.White,
                    style = MaterialTheme.typography.titleMedium,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f),
                )
            }

            Row(
                modifier = Modifier.align(Alignment.Center),
                horizontalArrangement = Arrangement.spacedBy(20.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                TransportButton(
                    icon = Icons.Default.SkipPrevious,
                    contentDescription = previousVideoLabel,
                    enabled = onPreviousVideo != null,
                    onClick = {
                        onPreviousVideo?.invoke()
                        keepVisible()
                    },
                )
                TransportButton(
                    icon = if (isPlaying) Icons.Default.Pause else Icons.Default.PlayArrow,
                    contentDescription = if (isPlaying) pauseLabel else playLabel,
                    iconSize = 44.dp,
                    buttonSize = 64.dp,
                    enabled = true,
                    onClick = {
                        if (player.isPlaying) player.pause() else player.play()
                        keepVisible()
                    },
                )
                TransportButton(
                    icon = Icons.Default.SkipNext,
                    contentDescription = nextVideoLabel,
                    enabled = onNextVideo != null,
                    onClick = {
                        onNextVideo?.invoke()
                        keepVisible()
                    },
                )
            }

            Column(
                modifier = Modifier
                    .align(Alignment.BottomStart)
                    .fillMaxWidth()
                    .padding(horizontal = 12.dp, vertical = 6.dp),
                verticalArrangement = Arrangement.spacedBy(4.dp),
            ) {
                ScrubberWithSegments(
                    positionMs = positionMs,
                    durationMs = durationMs,
                    segments = segments,
                    previewSpritePath = previewSpritePath,
                    previewTrackJsonPath = previewTrackJsonPath,
                    onSeekTo = { target ->
                        player.seekTo(target)
                        keepVisible()
                    },
                    onScrubStart = { target ->
                        isScrubbing = true
                        scrubTargetMs = target
                        keepVisible()
                    },
                    onScrubUpdate = { target ->
                        isScrubbing = true
                        scrubTargetMs = target
                        keepVisible()
                    },
                    onScrubEnd = { target ->
                        scrubTargetMs = target
                        isScrubbing = false
                        keepVisible()
                    },
                )
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Text(
                        text = formatDuration(shownPositionMs),
                        style = MaterialTheme.typography.labelSmall,
                        color = Color.White,
                    )
                    Spacer(modifier = Modifier.width(4.dp))
                    Text(
                        text = "/ ${formatDuration(durationMs)}",
                        style = MaterialTheme.typography.labelSmall,
                        color = Color.White.copy(alpha = 0.7f),
                    )
                    Spacer(modifier = Modifier.weight(1f))
                    IconButton(
                        onClick = {
                            onToggleSubtitles()
                            keepVisible()
                        },
                    ) {
                        Icon(
                            imageVector = if (showSubtitles) Icons.Default.ClosedCaption
                            else Icons.Default.ClosedCaptionDisabled,
                            contentDescription = if (showSubtitles) hideSubtitlesLabel else showSubtitlesLabel,
                            tint = Color.White,
                        )
                    }
                    Box {
                        IconButton(
                            onClick = {
                                volumeMenuOpen = true
                                keepVisible()
                            },
                        ) {
                            Icon(
                                imageVector = if (volumeFraction <= 0.01f) {
                                    Icons.AutoMirrored.Filled.VolumeOff
                                } else {
                                    Icons.AutoMirrored.Filled.VolumeUp
                                },
                                contentDescription = volumeLabel,
                                tint = Color.White,
                            )
                        }
                        DropdownMenu(
                            expanded = volumeMenuOpen,
                            onDismissRequest = { volumeMenuOpen = false },
                        ) {
                            Column(
                                modifier = Modifier
                                    .width(180.dp)
                                    .padding(horizontal = 14.dp, vertical = 8.dp),
                                verticalArrangement = Arrangement.spacedBy(4.dp),
                            ) {
                                Text(
                                    text = stringResource(
                                        R.string.player_volume_percent,
                                        ((volumeFraction * 100).toInt()).coerceIn(0, 100),
                                    ),
                                    style = MaterialTheme.typography.labelMedium,
                                )
                                Slider(
                                    value = volumeFraction.coerceIn(0f, 1f),
                                    onValueChange = { value ->
                                        setVolumeFraction(audioManager, value)
                                        volumeFraction = readVolumeFraction(audioManager)
                                        keepVisible()
                                    },
                                )
                            }
                        }
                    }
                    Box {
                        IconButton(
                            onClick = {
                                speedMenuOpen = true
                                keepVisible()
                            },
                        ) {
                            Icon(
                                imageVector = Icons.Default.Speed,
                                contentDescription = playbackSpeedLabel,
                                tint = Color.White,
                            )
                        }
                        DropdownMenu(
                            expanded = speedMenuOpen,
                            onDismissRequest = { speedMenuOpen = false },
                        ) {
                            SPEED_CHOICES.forEach { choice ->
                                DropdownMenuItem(
                                    text = { Text("${choice}×") },
                                    onClick = {
                                        player.setPlaybackSpeed(choice)
                                        speed = choice
                                        speedMenuOpen = false
                                        keepVisible()
                                    },
                                )
                            }
                        }
                    }
                    IconButton(
                        onClick = {
                            onToggleFullscreen()
                            keepVisible()
                        },
                    ) {
                        Icon(
                            imageVector = if (isFullscreen) Icons.Default.FullscreenExit
                            else Icons.Default.Fullscreen,
                            contentDescription = if (isFullscreen) exitFullscreenLabel else enterFullscreenLabel,
                            tint = Color.White,
                        )
                    }
                }
                @Suppress("UNUSED_EXPRESSION") speed
            }
        }
    }
}

private val SPEED_CHOICES = listOf(0.5f, 0.75f, 1.0f, 1.25f, 1.5f, 2.0f)

private fun readVolumeFraction(audioManager: AudioManager?): Float {
    val audio = audioManager ?: return 0f
    val max = audio.getStreamMaxVolume(AudioManager.STREAM_MUSIC).coerceAtLeast(1)
    return (audio.getStreamVolume(AudioManager.STREAM_MUSIC).toFloat() / max.toFloat()).coerceIn(0f, 1f)
}

private fun setVolumeFraction(audioManager: AudioManager?, value: Float) {
    val audio = audioManager ?: return
    val max = audio.getStreamMaxVolume(AudioManager.STREAM_MUSIC).coerceAtLeast(1)
    audio.setStreamVolume(
        AudioManager.STREAM_MUSIC,
        (value.coerceIn(0f, 1f) * max).toInt().coerceIn(0, max),
        0,
    )
}

@Composable
private fun TransportButton(
    icon: androidx.compose.ui.graphics.vector.ImageVector,
    contentDescription: String,
    enabled: Boolean,
    onClick: () -> Unit,
    buttonSize: Dp = 52.dp,
    iconSize: Dp = 32.dp,
) {
    IconButton(
        onClick = onClick,
        enabled = enabled,
        modifier = Modifier.size(buttonSize),
    ) {
        Icon(
            imageVector = icon,
            contentDescription = contentDescription,
            tint = Color.White.copy(alpha = if (enabled) 1f else 0.35f),
            modifier = Modifier.size(iconSize),
        )
    }
}
