package com.screwy.igloo.player

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * VTT parser tests for [parseVtt]. The parser is best-effort — well-formed
 * cues land; malformed cues are silently skipped.
 */
class SubtitleOverlayTest {

    @Test
    fun parses_basic_three_cue_file() {
        val vtt = """
            WEBVTT

            00:00:01.000 --> 00:00:04.000
            Hello world

            00:00:05.000 --> 00:00:07.500
            Second line

            00:00:10.000 --> 00:00:12.000
            Third line
        """.trimIndent()

        val cues = parseVtt(vtt)
        assertEquals(3, cues.size)
        assertEquals(1_000L, cues[0].startMs)
        assertEquals(4_000L, cues[0].endMs)
        assertEquals("Hello world", cues[0].text)
        assertEquals(5_000L, cues[1].startMs)
        assertEquals(7_500L, cues[1].endMs)
        assertEquals("Second line", cues[1].text)
        assertEquals(10_000L, cues[2].startMs)
        assertEquals(12_000L, cues[2].endMs)
    }

    @Test
    fun empty_string_returns_empty_list() {
        assertEquals(emptyList<SubtitleCue>(), parseVtt(""))
    }

    @Test
    fun blank_string_returns_empty_list() {
        assertEquals(emptyList<SubtitleCue>(), parseVtt("   \n   "))
    }

    @Test
    fun malformed_timing_line_is_skipped() {
        val vtt = """
            WEBVTT

            not:a:timestamp --> also:bad
            This cue should be skipped

            00:00:02.000 --> 00:00:04.000
            Valid cue
        """.trimIndent()

        val cues = parseVtt(vtt)
        assertEquals(1, cues.size)
        assertEquals("Valid cue", cues[0].text)
    }

    @Test
    fun accepts_mmss_shorthand_without_hour_field() {
        val vtt = """
            WEBVTT

            01:30.000 --> 01:32.000
            Short form
        """.trimIndent()

        val cues = parseVtt(vtt)
        assertEquals(1, cues.size)
        assertEquals(90_000L, cues[0].startMs)
        assertEquals(92_000L, cues[0].endMs)
    }

    @Test
    fun multiline_cue_text_is_joined_with_newlines() {
        val vtt = """
            WEBVTT

            00:00:01.000 --> 00:00:05.000
            Line one
            Line two
        """.trimIndent()

        val cues = parseVtt(vtt)
        assertEquals(1, cues.size)
        assertEquals("Line one\nLine two", cues[0].text)
    }

    @Test
    fun cue_with_inline_settings_after_end_time_still_parses() {
        val vtt = """
            WEBVTT

            00:00:01.000 --> 00:00:04.000 line:84% align:middle
            Caption
        """.trimIndent()

        val cues = parseVtt(vtt)
        assertEquals(1, cues.size)
        assertEquals(1_000L, cues[0].startMs)
        assertEquals(4_000L, cues[0].endMs)
        assertEquals("Caption", cues[0].text)
    }

    @Test
    fun notes_and_cue_identifiers_are_ignored() {
        val vtt = """
            WEBVTT

            NOTE this is a comment block

            cue-1
            00:00:01.000 --> 00:00:02.000
            First

            cue-2
            00:00:03.000 --> 00:00:04.000
            Second
        """.trimIndent()

        val cues = parseVtt(vtt)
        assertEquals(2, cues.size)
        assertEquals("First", cues[0].text)
        assertEquals("Second", cues[1].text)
    }

    @Test
    fun srt_style_comma_decimal_is_accepted() {
        val vtt = """
            00:00:01,500 --> 00:00:03,750
            Commas ok
        """.trimIndent()

        val cues = parseVtt(vtt)
        assertEquals(1, cues.size)
        assertEquals(1_500L, cues[0].startMs)
        assertEquals(3_750L, cues[0].endMs)
    }

    @Test
    fun empty_body_cues_are_dropped() {
        val vtt = """
            WEBVTT

            00:00:01.000 --> 00:00:02.000

            00:00:03.000 --> 00:00:04.000
            Real cue
        """.trimIndent()

        val cues = parseVtt(vtt)
        assertEquals(1, cues.size)
        assertTrue(cues[0].text == "Real cue")
    }
}
