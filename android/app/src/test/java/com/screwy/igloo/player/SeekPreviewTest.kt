package com.screwy.igloo.player

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class SeekPreviewTest {

    @Test fun parsePreviewTrackJsonUsesServerCues() {
        val cues = parsePreviewTrackJson(
            """
            {
              "version": 1,
              "duration_ms": 30000,
              "tile_width": 160,
              "tile_height": 90,
              "columns": 5,
              "cues": [
                {"start_ms":0,"end_ms":15000,"x":0,"y":0,"w":160,"h":90},
                {"start_ms":15000,"end_ms":30000,"x":160,"y":0,"w":160,"h":90}
              ]
            }
            """.trimIndent(),
        )

        assertEquals(2, cues.size)
        assertEquals(PreviewCue(startMs = 0, endMs = 15_000, x = 0, y = 0, width = 160, height = 90), cues[0])
        assertEquals(PreviewCue(startMs = 15_000, endMs = 30_000, x = 160, y = 0, width = 160, height = 90), cues[1])
        assertEquals(cues[1], findPreviewCue(cues, 20_000))
    }

    @Test fun parsePreviewTrackJsonDoesNotAcceptLegacyVtt() {
        val cues = parsePreviewTrackJson(
            """
            WEBVTT

            00:00:00.000 --> 00:00:15.000
            /api/media/preview-sprite/video#xywh=0,0,160,90
            """.trimIndent(),
        )

        assertTrue(cues.isEmpty())
    }
}
