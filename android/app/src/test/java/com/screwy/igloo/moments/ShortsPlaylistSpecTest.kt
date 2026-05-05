package com.screwy.igloo.moments

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class ShortsPlaylistSpecTest {
    @Test
    fun decodeRootPlaylistsNormalizesBlankId() {
        assertEquals(
            ShortsPlaylistSpec(type = ShortsPlaylistType.Moments, playlistId = ShortsPlaylistSpec.RootPlaylistId),
            ShortsPlaylistSpec.decode("moments", ""),
        )
        assertEquals(
            ShortsPlaylistSpec(type = ShortsPlaylistType.AllMoments, playlistId = ShortsPlaylistSpec.RootPlaylistId),
            ShortsPlaylistSpec.decode("all_moments", "   "),
        )
        assertEquals(
            ShortsPlaylistSpec(type = ShortsPlaylistType.Bookmarks, playlistId = ShortsPlaylistSpec.RootPlaylistId),
            ShortsPlaylistSpec.decode("bookmarks", null),
        )
        assertEquals(
            ShortsPlaylistSpec(type = ShortsPlaylistType.StoryTray, playlistId = ShortsPlaylistSpec.RootPlaylistId),
            ShortsPlaylistSpec.decode("stories", "tiktok_ignored"),
        )
    }

    @Test
    fun decodeChannelRequiresChannelId() {
        assertEquals(
            ShortsPlaylistSpec(type = ShortsPlaylistType.Channel, playlistId = "tiktok_creator"),
            ShortsPlaylistSpec.decode("channel", " tiktok_creator "),
        )
        assertNull(ShortsPlaylistSpec.decode("channel", ""))
    }

    @Test
    fun routePartsUseStableRootId() {
        assertEquals("moments", ShortsPlaylistSpec.moments().routePlaylistType)
        assertEquals(ShortsPlaylistSpec.RootPlaylistId, ShortsPlaylistSpec.moments().routePlaylistId)
        assertEquals("all_moments", ShortsPlaylistSpec.allMoments().routePlaylistType)
        assertEquals(ShortsPlaylistSpec.RootPlaylistId, ShortsPlaylistSpec.bookmarks().routePlaylistId)
        assertEquals("channel", ShortsPlaylistSpec.channel("instagram_a")?.routePlaylistType)
        assertEquals("instagram_a", ShortsPlaylistSpec.channel("instagram_a")?.routePlaylistId)
        assertEquals("stories", ShortsPlaylistSpec.storyTray().routePlaylistType)
        assertEquals(ShortsPlaylistSpec.RootPlaylistId, ShortsPlaylistSpec.storyTray().routePlaylistId)
    }

    @Test
    fun startIndexFallsBackToZeroWhenRequestedVideoIsMissing() {
        assertEquals(1, shortsStartIndex(listOf("a", "b", "c"), "b"))
        assertEquals(0, shortsStartIndex(listOf("a", "b", "c"), "missing"))
        assertEquals(0, shortsStartIndex(emptyList<String>(), "b"))
    }
}
