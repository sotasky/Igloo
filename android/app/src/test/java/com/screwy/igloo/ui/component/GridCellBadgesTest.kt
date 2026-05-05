package com.screwy.igloo.ui.component

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * Pure-function tests for [mediaTypeFor] — the shared media-kind classifier used
 * by all three vertical thumbnail grids (Moments All, TikTok/Instagram channel,
 * Bookmarks). The rules must hold across both data sources:
 *
 *   - `videos.media_kind` + `slide_count` (TikTok / Instagram / YouTube)
 *   - tweet feed items (only `slideCount` from descriptor count, no media_kind)
 *
 * "slideCount > 1 wins" must fire first so multi-photo tweets — which carry no
 * `media_kind` value — still resolve to [MediaType.Slideshow].
 */
class GridCellBadgesTest {

    @Test
    fun video_kind_resolves_to_video() {
        assertEquals(MediaType.Video, mediaTypeFor(mediaKind = "video", slideCount = 0))
    }

    @Test
    fun unknown_kind_with_zero_slides_defaults_to_video() {
        // Catches the "videos.media_kind is NULL" path for legacy YouTube rows.
        assertEquals(MediaType.Video, mediaTypeFor(mediaKind = null, slideCount = 0))
    }

    @Test
    fun slideshow_kind_resolves_to_slideshow() {
        assertEquals(MediaType.Slideshow, mediaTypeFor(mediaKind = "slideshow", slideCount = 5))
    }

    @Test
    fun multi_descriptor_tweet_with_no_kind_resolves_to_slideshow() {
        // Twitter multi-photo tweets have no media_kind field — count must win.
        assertEquals(MediaType.Slideshow, mediaTypeFor(mediaKind = null, slideCount = 4))
    }

    @Test
    fun image_kind_resolves_to_image() {
        assertEquals(MediaType.Image, mediaTypeFor(mediaKind = "image", slideCount = 1))
    }

    @Test
    fun photo_kind_resolves_to_image() {
        // Twitter feed descriptors store "photo" rather than "image".
        assertEquals(MediaType.Image, mediaTypeFor(mediaKind = "photo", slideCount = 1))
    }

    @Test
    fun mediakind_lookup_is_case_insensitive_and_trimmed() {
        assertEquals(MediaType.Slideshow, mediaTypeFor(mediaKind = "  SLIDESHOW ", slideCount = 0))
        assertEquals(MediaType.Image, mediaTypeFor(mediaKind = "Image", slideCount = 0))
    }
}
