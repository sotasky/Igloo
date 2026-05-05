package com.screwy.igloo.player

import androidx.media3.exoplayer.ExoPlayer
import com.screwy.igloo.media.MediaUri

data class PlaybackSource(
    val mediaUri: MediaUri,
    val resumeMs: Long = 0L,
)

interface PlaybackPlayer {
    fun stop()
    fun setMediaItem(uri: String)
    fun prepare()
    fun seekTo(positionMs: Long)
    fun setPlayWhenReady(playWhenReady: Boolean)
}

class PlaybackCoordinator {
    fun bind(
        player: PlaybackPlayer,
        source: PlaybackSource,
    ) {
        val uri = when (val mediaUri = source.mediaUri) {
            is MediaUri.Local -> mediaUri.file.toURI().toString()
            is MediaUri.Remote -> mediaUri.url
            is MediaUri.Missing -> return
        }

        player.stop()
        player.setMediaItem(uri)
        player.prepare()
        if (source.resumeMs > 0L) {
            player.seekTo(source.resumeMs)
        }
        player.setPlayWhenReady(true)
    }
}

class ExoPlayerPlaybackPlayer(
    private val player: ExoPlayer,
) : PlaybackPlayer {
    override fun stop() {
        player.stop()
    }

    override fun setMediaItem(uri: String) {
        player.setMediaItem(androidx.media3.common.MediaItem.fromUri(uri))
    }

    override fun prepare() {
        player.prepare()
    }

    override fun seekTo(positionMs: Long) {
        player.seekTo(positionMs)
    }

    override fun setPlayWhenReady(playWhenReady: Boolean) {
        player.playWhenReady = playWhenReady
    }
}
