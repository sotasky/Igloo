package com.screwy.igloo.player

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.style.TextDecoration
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class PlayerLinkedTextTest {

    @Test
    fun annotate_links_mentions_and_timestamps() {
        val annotated =
            annotatePlayerLinkedText(
                text = "Hi @alice see https://example.com at 1:23",
                linkColor = Color.Red,
            )

        val annotations = annotated.getStringAnnotations(0, annotated.length)
        assertEquals(3, annotations.size)
        assertEquals(TAG_MENTION, annotations[0].tag)
        assertEquals("alice", annotations[0].item)
        assertEquals(TAG_URL, annotations[1].tag)
        assertEquals("https://example.com", annotations[1].item)
        assertEquals(TAG_TIMESTAMP, annotations[2].tag)
        assertEquals("83000", annotations[2].item)
    }

    @Test
    fun timestamp_inside_url_is_not_annotated_twice() {
        val annotated =
            annotatePlayerLinkedText(
                text = "https://example.com/watch?v=1:23",
                linkColor = Color.Red,
            )

        val annotations = annotated.getStringAnnotations(0, annotated.length)
        assertEquals(1, annotations.size)
        assertEquals(TAG_URL, annotations.single().tag)
    }

    @Test
    fun youtube_comment_patreon_link_is_clickable_and_themed() {
        val annotated =
            annotatePlayerLinkedText(
                text = "If it matters to you: https://www.patreon.com/c/thehatedone",
                linkColor = Color.Red,
            )

        val url = annotated.getStringAnnotations(0, annotated.length).single { it.tag == TAG_URL }
        assertEquals("https://www.patreon.com/c/thehatedone", url.item)
        val style =
            annotated.spanStyles
                .single {
                    it.start == "If it matters to you: ".length && it.end == annotated.length
                }
                .item
        assertEquals(Color.Red, style.color)
        assertEquals(TextDecoration.Underline, style.textDecoration)
    }

    @Test
    fun scheme_less_comment_links_get_https_without_trailing_punctuation() {
        val annotated =
            annotatePlayerLinkedText(
                text = "support me at patreon.com/example.",
                linkColor = Color.Red,
            )

        val url = annotated.getStringAnnotations(0, annotated.length).single { it.tag == TAG_URL }
        assertEquals("https://patreon.com/example", url.item)
        assertEquals("patreon.com/example", annotated.text.substring(url.start, url.end))
    }

    @Test
    fun email_domains_are_not_annotated_as_urls() {
        val annotated =
            annotatePlayerLinkedText(text = "mail support@example.com", linkColor = Color.Red)

        assertTrue(annotated.getStringAnnotations(0, annotated.length).none { it.tag == TAG_URL })
    }

    @Test
    fun mention_parser_supports_dots_and_hyphens_without_trailing_punctuation() {
        val annotated =
            annotatePlayerLinkedText(
                text = "See @creator.name-youtube, then @second_handle.",
                linkColor = Color.Red,
            )

        val mentions =
            annotated.getStringAnnotations(0, annotated.length).filter { it.tag == TAG_MENTION }

        assertEquals(listOf("creator.name-youtube", "second_handle"), mentions.map { it.item })
    }

    @Test
    fun parse_metadata_counts_reads_server_labels() {
        val counts =
            parseVideoMetadataCounts(
                """{"view_count":182191,"view_count_label":"182K","like_count":9051,"like_count_label":"9.1K"}"""
            )

        assertEquals(182_191L, counts.viewCount)
        assertEquals("182K", counts.viewCountLabel)
        assertEquals(9_051L, counts.likeCount)
        assertEquals("9.1K", counts.likeCountLabel)
    }

    @Test
    fun parse_metadata_counts_ignores_non_primitive_labels() {
        val counts =
            parseVideoMetadataCounts(
                """{"view_count":"x","view_count_label":{},"like_count":"y","like_count_label":["bad"]}"""
            )

        assertEquals(null, counts.viewCount)
        assertEquals(null, counts.viewCountLabel)
        assertEquals(null, counts.likeCount)
        assertEquals(null, counts.likeCountLabel)
    }

    @Test
    fun comment_initial_uses_first_non_space_character() {
        assertEquals("A", commentInitial("  alice"))
        assertEquals("?", commentInitial("   "))
    }

    @Test
    fun youtube_comment_author_ids_normalize_to_channel_ids() {
        assertEquals("youtube_UCabc123", youtubeCommentAuthorChannelId("UCabc123"))
        assertEquals("youtube_UCabc123", youtubeCommentAuthorChannelId(" youtube_UCabc123 "))
        assertEquals(null, youtubeCommentAuthorChannelId("@handle"))
    }
}
