package com.screwy.igloo.ui.nav

import com.screwy.igloo.R
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertSame
import org.junit.Test

class RouteRegistryTest {

    @Test
    fun allRoutesAreUniqueAndInventoried() {
        val routes = RouteRegistry.routes.map { it.route }

        assertEquals(routes.distinct(), routes)
        listOf(
            "login",
            "feed",
            "videos",
            "moments",
            "all-moments",
            "bookmarks",
            "liked",
            "liked/downloaded",
            "channel/{channel_id}",
            "shorts/{playlist_type}/{playlist_id}/{video_id}",
            "media/{owner_kind}/{owner_id}/{index}",
            "player/{video_id}",
            "tweet/{tweet_id}",
            "settings",
            "settings/theme",
            "settings/playback",
            "settings/feed",
            "settings/sponsorblock",
            "settings/storage",
            "settings/account",
            "logs",
            "logs/outbox",
        ).forEach { route ->
            assertNotNull("missing route spec for $route", RouteRegistry.find(route))
        }
    }

    @Test
    fun routeChromeComesFromRegistry() {
        RouteRegistry.routes.forEach { spec ->
            assertSame(spec.chrome, routeChromePolicyFor(spec.route))
        }
    }

    @Test
    fun threadUsesCentralScaffoldWithoutCompactDrawerOrBottomNav() {
        val thread = RouteRegistry.Thread

        assertEquals(TopChrome.ScrollAwayAppBar, thread.chrome.topChrome)
        assertEquals(BottomChrome.Hidden, thread.chrome.bottomChrome)
        assertEquals(DrawerChrome.Disabled, thread.chrome.drawerChrome)
        assertEquals(DrawerChrome.Enabled, thread.chrome.wideDrawerChrome)
        assertEquals(TopBarTitle.Resource(R.string.label_thread), thread.chrome.topBarTitle)
    }

    @Test
    fun deepLinksAreOwnedByTheDestinationSpec() {
        assertEquals(
            listOf("igloo://channel/{channel_id}"),
            RouteRegistry.require("channel/{channel_id}").deepLinks,
        )
        assertEquals(
            listOf("igloo://youtube/{video_id}", "igloo://tt/{video_id}", "igloo://ig/{video_id}"),
            RouteRegistry.require("player/{video_id}").deepLinks,
        )
        assertEquals(
            listOf("igloo://tw/{tweet_id}"),
            RouteRegistry.require("tweet/{tweet_id}").deepLinks,
        )
    }

    @Test
    fun dynamicRoutesEncodePathSegments() {
        assertEquals("channel/twitter_user%2Fwith%20space", RouteRegistry.channelRoute("twitter_user/with space"))
        assertEquals(
            "shorts/channel/tiktok_creator/video%2Fid",
            RouteRegistry.shortsRoute("channel", "tiktok_creator", "video/id"),
        )
        assertEquals(
            "media/tweet/status%2F123/2",
            RouteRegistry.mediaRoute("tweet", "status/123", 2),
        )
        assertEquals("player/video%2Fid%3Fv%3D1", RouteRegistry.playerRoute("video/id?v=1"))
        assertEquals("tweet/status%2F123%23reply", RouteRegistry.threadRoute("status/123#reply"))
    }

    @Test
    fun fallbackRouteUsesFeedPolicy() {
        assertSame(RouteRegistry.Feed.chrome, routeChromePolicyFor("unknown-route"))
        assertSame(RouteRegistry.Feed.chrome, routeChromePolicyFor(null))
    }
}
