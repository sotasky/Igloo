package com.screwy.igloo.player

import com.screwy.igloo.media.MediaUri
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test
import java.io.File

class PlaybackCoordinatorTest {

    @Test
    fun bindLocalSourceStopsOldSourceAndStartsPlayback() {
        val player = FakePlaybackPlayer()
        val coordinator = PlaybackCoordinator()
        coordinator.bind(
            player = player,
            source = PlaybackSource(
                mediaUri = MediaUri.Local(File("/tmp/video.mp4")),
                resumeMs = 1_250L,
            ),
        )

        assertEquals(
            listOf(
                "stop",
                "set:file:/tmp/video.mp4",
                "prepare",
                "seek:1250",
                "playWhenReady:true",
            ),
            player.events,
        )
    }

    @Test
    fun bindRemoteSourceUsesRemoteUrl() {
        val player = FakePlaybackPlayer()

        PlaybackCoordinator().bind(
            player = player,
            source = PlaybackSource(
                mediaUri = MediaUri.Remote("https://example.test/video.mp4"),
            ),
        )

        assertEquals("set:https://example.test/video.mp4", player.events[1])
        assertFalse(player.events.any { it.startsWith("seek:") })
    }

    @Test
    fun missingSourceDoesNotTouchPlayer() {
        val player = FakePlaybackPlayer()
        PlaybackCoordinator().bind(
            player = player,
            source = PlaybackSource(
                mediaUri = MediaUri.Missing,
            ),
        )

        assertEquals(emptyList<String>(), player.events)
    }

    private class FakePlaybackPlayer : PlaybackPlayer {
        val events = mutableListOf<String>()

        override fun stop() {
            events += "stop"
        }

        override fun setMediaItem(uri: String) {
            events += "set:$uri"
        }

        override fun prepare() {
            events += "prepare"
        }

        override fun seekTo(positionMs: Long) {
            events += "seek:$positionMs"
        }

        override fun setPlayWhenReady(playWhenReady: Boolean) {
            events += "playWhenReady:$playWhenReady"
        }
    }
}
