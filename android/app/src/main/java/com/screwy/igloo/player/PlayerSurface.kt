package com.screwy.igloo.player

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.ui.PlayerView
import coil3.compose.AsyncImage
import com.screwy.igloo.data.entity.SponsorBlockSegmentEntity
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.ui.component.rememberRemoteImageModel
import com.screwy.igloo.ui.theme.iglooColors

internal enum class PlayerSurfaceMode {
    Inline,
    Fullscreen,
}

internal data class PlayerLevelFeedback(
    val label: String,
    val level: Float,
    val nonce: Long,
)

@Composable
internal fun PlayerSurface(
    mode: PlayerSurfaceMode,
    player: ExoPlayer,
    posterUri: MediaUri,
    streamUri: MediaUri,
    title: String,
    onBack: () -> Unit,
    onPreviousVideo: (() -> Unit)?,
    onNextVideo: (() -> Unit)?,
    segments: List<SponsorBlockSegmentEntity>,
    showSubtitles: Boolean,
    onToggleSubtitles: () -> Unit,
    onToggleFullscreen: () -> Unit,
    controlsVisible: Boolean,
    onControlsVisibleChange: (Boolean) -> Unit,
    previewSpritePath: String?,
    previewTrackJsonPath: String?,
    subtitlePath: String?,
    currentPositionMs: () -> Long,
    sponsorBlockSkipSegment: SponsorBlockUiSegment?,
    sponsorBlockAutoSkipMessage: String?,
    onSkipSponsorBlock: (SponsorBlockUiSegment) -> Unit,
    levelFeedback: PlayerLevelFeedback?,
    onBrightnessChange: (Float) -> Unit,
    onVolumeChange: (Float) -> Unit,
    modifier: Modifier = Modifier,
) {
    val fullscreen = mode == PlayerSurfaceMode.Fullscreen
    val subtitleBottomPadding = when {
        fullscreen && controlsVisible -> 68.dp
        controlsVisible -> 56.dp
        else -> 16.dp
    }
    val sponsorBlockBottomPadding = if (fullscreen) 72.dp else 56.dp

    Box(
        modifier = modifier.background(Color.Black),
    ) {
        PlayerPoster(thumbnailUri = posterUri, modifier = Modifier.fillMaxSize())
        if (streamUri !is MediaUri.Missing) {
            VideoSurface(player = player, modifier = Modifier.fillMaxSize())
        }
        PlayerGestures(
            player = player,
            modifier = Modifier.fillMaxSize(),
            onTap = { onControlsVisibleChange(!controlsVisible) },
            onScrubStart = { onControlsVisibleChange(true) },
            onScrubUpdate = { onControlsVisibleChange(true) },
            onScrubEnd = { onControlsVisibleChange(true) },
            onBrightnessChange = onBrightnessChange,
            onVolumeChange = onVolumeChange,
        )
        PlayerOverlay(
            player = player,
            title = title,
            onBack = onBack,
            onPreviousVideo = onPreviousVideo,
            onNextVideo = onNextVideo,
            segments = segments,
            showSubtitles = showSubtitles,
            onToggleSubtitles = onToggleSubtitles,
            isFullscreen = fullscreen,
            onToggleFullscreen = onToggleFullscreen,
            controlsVisible = controlsVisible,
            onControlsVisibleChange = onControlsVisibleChange,
            previewSpritePath = previewSpritePath,
            previewTrackJsonPath = previewTrackJsonPath,
            modifier = Modifier.fillMaxSize(),
        )
        SubtitleOverlay(
            subtitlePath = subtitlePath,
            currentPositionMs = currentPositionMs,
            modifier = Modifier.align(Alignment.BottomCenter).fillMaxWidth(),
            visible = showSubtitles,
            bottomPadding = subtitleBottomPadding,
        )
        SponsorBlockSkipButton(
            segment = sponsorBlockSkipSegment,
            bottomPadding = sponsorBlockBottomPadding,
            onSkip = onSkipSponsorBlock,
            modifier = Modifier.align(Alignment.BottomEnd),
        )
        SponsorBlockAutoSkipNotification(
            message = sponsorBlockAutoSkipMessage,
            bottomPadding = sponsorBlockBottomPadding,
            modifier = Modifier.align(Alignment.BottomCenter),
        )
        PlayerLevelFeedbackOverlay(
            feedback = levelFeedback,
            modifier = Modifier.align(Alignment.Center),
        )
    }
}

@Composable
private fun VideoSurface(player: ExoPlayer, modifier: Modifier = Modifier) {
    val context = LocalContext.current
    val playerView = remember {
        PlayerView(context).apply {
            useController = false
            setShutterBackgroundColor(android.graphics.Color.TRANSPARENT)
        }
    }
    AndroidView(
        factory = { playerView },
        update = { view ->
            if (view.player !== player) view.player = player
        },
        modifier = modifier,
    )
    DisposableEffect(player) {
        onDispose {
            playerView.player = null
        }
    }
}

@Composable
private fun PlayerLevelFeedbackOverlay(
    feedback: PlayerLevelFeedback?,
    modifier: Modifier = Modifier,
) {
    val shown = feedback ?: return
    Surface(
        modifier = modifier.size(112.dp),
        shape = CircleShape,
        color = Color.Black.copy(alpha = 0.72f),
        contentColor = Color.White,
    ) {
        Box(contentAlignment = Alignment.Center) {
            CircularProgressIndicator(
                progress = { shown.level.coerceIn(0f, 1f) },
                color = MaterialTheme.colorScheme.primary,
                trackColor = Color.White.copy(alpha = 0.22f),
                modifier = Modifier.fillMaxSize().padding(8.dp),
            )
            Column(
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.spacedBy(2.dp),
            ) {
                Text(
                    text = shown.label,
                    style = MaterialTheme.typography.labelSmall,
                    color = Color.White.copy(alpha = 0.78f),
                )
                Text(
                    text = "${(shown.level * 100).toInt().coerceIn(0, 100)}%",
                    style = MaterialTheme.typography.titleMedium.copy(fontWeight = FontWeight.SemiBold),
                    color = Color.White,
                )
            }
        }
    }
}

@Composable
private fun PlayerPoster(thumbnailUri: MediaUri, modifier: Modifier = Modifier) {
    val colors = MaterialTheme.iglooColors
    Box(
        modifier = modifier.background(Color.Black),
        contentAlignment = Alignment.Center,
    ) {
        when (thumbnailUri) {
            is MediaUri.Local -> AsyncImage(
                model = thumbnailUri.file,
                contentDescription = null,
                modifier = Modifier.fillMaxSize(),
            )
            is MediaUri.Remote -> AsyncImage(
                model = rememberRemoteImageModel(thumbnailUri.url),
                contentDescription = null,
                modifier = Modifier.fillMaxSize(),
            )
            is MediaUri.Missing -> CircularProgressIndicator(color = colors.onSurfaceFaint)
        }
    }
}
