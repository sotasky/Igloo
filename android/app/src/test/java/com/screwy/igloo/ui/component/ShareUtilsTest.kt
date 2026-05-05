package com.screwy.igloo.ui.component

import org.junit.Assert.assertEquals
import org.junit.Test

class ShareUtilsTest {
    @Test fun toShareUrl_preservesOriginalUrlByDefault() {
        assertEquals(
            "https://www.instagram.com/reel/ABC123/",
            toShareUrl("https://www.instagram.com/reel/ABC123/"),
        )
        assertEquals(
            "https://x.com/user/status/123",
            toShareUrl("https://x.com/user/status/123"),
        )
    }

    @Test fun toShareUrl_leavesInstagramAloneWhenEnabled() {
        assertEquals(
            "https://www.instagram.com/reel/ABC123/",
            toShareUrl("https://www.instagram.com/reel/ABC123/", useEmbedFriendlySite = true),
        )
        assertEquals(
            "http://instagram.com/p/DEF456/",
            toShareUrl("http://instagram.com/p/DEF456/", useEmbedFriendlySite = true),
        )
    }

    @Test fun toShareUrl_rewritesTwitterWhenEnabled() {
        assertEquals(
            "https://fxtwitter.com/user/status/123",
            toShareUrl("https://x.com/user/status/123", useEmbedFriendlySite = true),
        )
    }

    @Test fun toShareUrl_rewritesTikTokWhenEnabled() {
        assertEquals(
            "https://tnktok.com/@user/video/123",
            toShareUrl("https://www.tiktok.com/@user/video/123", useEmbedFriendlySite = true),
        )
    }

    @Test fun toShareUrl_leavesYouTubeAloneWhenEnabled() {
        assertEquals(
            "https://www.youtube.com/watch?v=abc",
            toShareUrl("https://www.youtube.com/watch?v=abc", useEmbedFriendlySite = true),
        )
    }
}
