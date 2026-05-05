package com.screwy.igloo.ui.component

import androidx.compose.ui.graphics.Color
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Pure-function tests for [annotateMentionsAndUrls].
 */
class AtMentionTextTest {

    private val mention = Color(0xFFFF0000)
    private val url = Color(0xFF00FF00)

    @Test
    fun single_mention_produces_one_annotation_with_handle_item() {
        val annotated = annotateMentionsAndUrls("@alice hi", mention, url)

        val all = annotated.getStringAnnotations(0, annotated.length)
        assertEquals(1, all.size)
        assertEquals("mention", all[0].tag)
        assertEquals("alice", all[0].item)
        // Range covers "@alice" (6 chars).
        assertEquals(0, all[0].start)
        assertEquals("@alice".length, all[0].end)
    }

    @Test
    fun mention_and_url_produce_disjoint_annotations() {
        val text = "hello @bob and http://x.com"
        val annotated = annotateMentionsAndUrls(text, mention, url)

        val all = annotated.getStringAnnotations(0, annotated.length)
        val mentions = all.filter { it.tag == "mention" }
        val urls = all.filter { it.tag == "url" }

        assertEquals(1, mentions.size)
        assertEquals("bob", mentions[0].item)

        assertEquals(1, urls.size)
        assertEquals("http://x.com", urls[0].item)

        // Disjoint ranges: the mention ends at or before the URL begins.
        val m = mentions[0]
        val u = urls[0]
        assertTrue("mention and url ranges overlap", m.end <= u.start || u.end <= m.start)
    }

    @Test
    fun plain_text_produces_zero_annotations() {
        val annotated = annotateMentionsAndUrls("no matches here", mention, url)
        assertEquals(0, annotated.getStringAnnotations(0, annotated.length).size)
    }

    @Test
    fun link_hit_returns_null_for_plain_caption_text() {
        val annotated = annotateMentionsAndUrls("plain #caption text", mention, url)

        assertEquals(null, annotatedTextLinkAt(annotated, 2))
    }

    @Test
    fun link_hit_returns_mention_at_tapped_offset() {
        val annotated = annotateMentionsAndUrls("hello @alice", mention, url)

        val hit = annotatedTextLinkAt(annotated, "hello @a".lastIndex)

        assertEquals("mention", hit?.tag)
        assertEquals("alice", hit?.item)
    }

    @Test
    fun bare_at_sign_alone_produces_zero_annotations() {
        val annotated = annotateMentionsAndUrls("@", mention, url)
        assertEquals(0, annotated.getStringAnnotations(0, annotated.length).size)
    }

    @Test
    fun adjacent_mentions_produce_two_annotations() {
        val annotated = annotateMentionsAndUrls("@a@b", mention, url)
        val all = annotated.getStringAnnotations(0, annotated.length)
        val items = all.filter { it.tag == "mention" }.map { it.item }
        assertEquals(listOf("a", "b"), items)
    }

    @Test
    fun dotted_and_hyphenated_mentions_are_annotated_without_trailing_punctuation() {
        val annotated = annotateMentionsAndUrls(
            "Talk to @creator.name-youtube, then @second_handle.",
            mention,
            url,
        )
        val mentions = annotated.getStringAnnotations(0, annotated.length)
            .filter { it.tag == "mention" }
            .map { it.item }

        assertEquals(listOf("creator.name-youtube", "second_handle"), mentions)
    }

    // --- annotateUrls (URL-only annotator used by LinkifyText) ---

    @Test
    fun annotateUrls_plain_text_zero_annotations() {
        val annotated = annotateUrls("plain text", url)
        assertEquals(0, annotated.getStringAnnotations(0, annotated.length).size)
    }

    @Test
    fun annotateUrls_single_url_one_annotation() {
        val annotated = annotateUrls("visit http://x.com now", url)
        val all = annotated.getStringAnnotations(0, annotated.length)
        assertEquals(1, all.size)
        assertEquals("url", all[0].tag)
        assertEquals("http://x.com", all[0].item)
    }

    @Test
    fun annotateUrls_two_urls_two_annotations() {
        val annotated = annotateUrls("two links http://a.com and https://b.org", url)
        val all = annotated.getStringAnnotations(0, annotated.length)
        assertEquals(2, all.size)
        val items = all.map { it.item }
        assertEquals(listOf("http://a.com", "https://b.org"), items)
    }
}
