package com.screwy.igloo.ui.nav

import com.screwy.igloo.R
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test

class RouteChromePolicyTest {

    @Test
    fun feedStyleRoutesUseScrollAwayScaffoldChrome() {
        listOf("feed", "videos", "bookmarks", "liked", "channel/{channel_id}").forEach { route ->
            val policy = routeChromePolicyFor(route)

            assertSame(TopChrome.ScrollAwayAppBar, policy.topChrome)
            assertTrue(policy.usesScaffoldTopBar)
            assertEquals(BottomChrome.Visible, policy.bottomChrome)
            assertEquals(DrawerChrome.Enabled, policy.drawerChrome)
        }
    }

    @Test
    fun feedStyleRoutesDeclareTheirAppBarTitles() {
        assertEquals(TopBarTitle.Resource(R.string.nav_feed), routeChromePolicyFor("feed").topBarTitle)
        assertEquals(TopBarTitle.Resource(R.string.nav_videos), routeChromePolicyFor("videos").topBarTitle)
        assertEquals(TopBarTitle.Resource(R.string.nav_bookmarks), routeChromePolicyFor("bookmarks").topBarTitle)
        assertEquals(TopBarTitle.Resource(R.string.nav_liked), routeChromePolicyFor("liked").topBarTitle)
        assertEquals(TopBarTitle.Resource(R.string.label_theme), routeChromePolicyFor("settings/theme").topBarTitle)
        assertSame(TopBarTitle.Channel, routeChromePolicyFor("channel/{channel_id}").topBarTitle)
    }

    @Test
    fun momentsRoutesKeepChromelessContentOwnedInsetsWithBottomNav() {
        listOf("moments", "all-moments", "shorts/{playlist_type}/{playlist_id}/{video_id}").forEach { route ->
            val policy = routeChromePolicyFor(route)

            assertSame(TopChrome.ContentOwnsTopInset, policy.topChrome)
            assertFalse(policy.usesScaffoldTopBar)
            assertEquals(BottomChrome.Visible, policy.bottomChrome)
            assertEquals(DrawerChrome.Enabled, policy.drawerChrome)
        }
    }

    @Test
    fun settingsAndLogsKeepScrollContentHeaders() {
        listOf(
            "settings",
            "settings/playback",
            "settings/feed",
            "settings/sponsorblock",
            "settings/storage",
            "settings/account",
            "logs",
            "logs/outbox",
        ).forEach { route ->
            val policy = routeChromePolicyFor(route)

            assertSame(TopChrome.ScrollContentHeader, policy.topChrome)
            assertFalse(policy.usesScaffoldTopBar)
        }
    }

    @Test
    fun themePreservesExistingScaffoldTopBarException() {
        val policy = routeChromePolicyFor("settings/theme")

        assertSame(TopChrome.ScrollAwayAppBar, policy.topChrome)
        assertTrue(policy.usesScaffoldTopBar)
    }

    @Test
    fun mediaRoutesAndThreadAreExplicitlyInventoried() {
        val media = routeChromePolicyFor("media/{owner_kind}/{owner_id}/{index}")
        assertSame(TopChrome.FullscreenMedia, media.topChrome)
        assertEquals(BottomChrome.Hidden, media.bottomChrome)
        assertEquals(DrawerChrome.Disabled, media.drawerChrome)

        val player = routeChromePolicyFor("player/{video_id}")
        assertSame(TopChrome.PinnedMediaGuard, player.topChrome)
        assertEquals(BottomChrome.Hidden, player.bottomChrome)
        assertEquals(DrawerChrome.Disabled, player.drawerChrome)

        val thread = routeChromePolicyFor("tweet/{tweet_id}")
        assertSame(TopChrome.ScrollAwayAppBar, thread.topChrome)
        assertEquals(TopBarTitle.Resource(R.string.label_thread), thread.topBarTitle)
        assertEquals(BottomChrome.Hidden, thread.bottomChrome)
        assertEquals(DrawerChrome.Disabled, thread.drawerChrome)
    }
}
