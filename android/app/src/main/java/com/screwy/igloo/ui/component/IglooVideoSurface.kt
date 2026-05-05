package com.screwy.igloo.ui.component

import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.viewinterop.AndroidView
import androidx.media3.common.Player
import androidx.media3.common.VideoSize
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.ui.AspectRatioFrameLayout
import androidx.media3.ui.PlayerView

internal fun videoResizeModeFor(
    width: Int,
    height: Int,
    wideThreshold: Float = 0.85f,
): Int {
    if (width <= 0 || height <= 0) return AspectRatioFrameLayout.RESIZE_MODE_ZOOM
    val aspect = width.toFloat() / height.toFloat()
    return if (aspect > wideThreshold) {
        AspectRatioFrameLayout.RESIZE_MODE_FIT
    } else {
        AspectRatioFrameLayout.RESIZE_MODE_ZOOM
    }
}

@Composable
internal fun IglooVideoSurface(
    player: ExoPlayer,
    modifier: Modifier = Modifier,
    resizeMode: Int? = null,
) {
    val context = LocalContext.current
    var resolvedResizeMode by remember(player, resizeMode) {
        mutableStateOf(resizeMode ?: videoResizeModeFor(player.videoSize.width, player.videoSize.height))
    }

    DisposableEffect(player, resizeMode) {
        if (resizeMode != null) {
            resolvedResizeMode = resizeMode
            return@DisposableEffect onDispose { }
        }
        resolvedResizeMode = videoResizeModeFor(player.videoSize.width, player.videoSize.height)
        val listener = object : Player.Listener {
            override fun onVideoSizeChanged(videoSize: VideoSize) {
                resolvedResizeMode = videoResizeModeFor(videoSize.width, videoSize.height)
            }
        }
        player.addListener(listener)
        onDispose { player.removeListener(listener) }
    }

    val playerView = remember {
        PlayerView(context).apply {
            useController = false
            setEnableComposeSurfaceSyncWorkaround(true)
            setShutterBackgroundColor(android.graphics.Color.BLACK)
        }
    }

    AndroidView(
        factory = { playerView },
        update = { view ->
            if (view.player !== player) view.player = player
            view.useController = false
            view.setEnableComposeSurfaceSyncWorkaround(true)
            view.resizeMode = resolvedResizeMode
        },
        modifier = modifier,
    )

    DisposableEffect(player) {
        onDispose {
            playerView.player = null
        }
    }
}
