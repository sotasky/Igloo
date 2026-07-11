package com.screwy.igloo.ui.nav

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class IglooNavigatorTest {

    @Test
    fun targetForOpenChannelTrimsAndBuildsChannelRoute() {
        val target = IglooNavigation.targetFor(
            IglooNavigationIntent.OpenChannel(
                channelId = "  tiktok_creator  ",
                source = IglooNavigationSource.Moments,
                originItemId = "video-1",
            ),
        )

        assertEquals(
            IglooNavigationTarget(
                route = "channel/tiktok_creator",
                channelId = "tiktok_creator",
            ),
            target,
        )
    }

    @Test
    fun targetForOpenChannelCarriesWarmProfileSnapshot() {
        val snapshot = ProfileOpenSnapshot(
            channelId = "twitter_alice",
            displayName = "Alice",
            handle = "alice",
            platform = "twitter",
            isFollowed = true,
			isStarred = false,
        )
        val target = IglooNavigation.targetFor(
            IglooNavigationIntent.OpenChannel(
                channelId = "twitter_alice",
                source = IglooNavigationSource.Feed,
                originItemId = "tweet-1",
                snapshot = snapshot,
            ),
        )

        assertEquals("channel/twitter_alice", target?.route)
        assertEquals(snapshot, target?.profileOpenSnapshot)
    }

    @Test
    fun targetForOpenVideoBuildsPlayerRoute() {
        val target = IglooNavigation.targetFor(
            IglooNavigationIntent.OpenVideo(
                videoId = " video/id ",
                source = IglooNavigationSource.Videos,
            ),
        )

        assertEquals("player/video%2Fid", target?.route)
        assertEquals("video/id", target?.videoId)
    }

    @Test
    fun targetForOpenShortsBuildsMomentsRoute() {
        val target = IglooNavigation.targetFor(
            IglooNavigationIntent.OpenShorts(
                playlistType = " channel ",
                playlistId = " tiktok_creator ",
                videoId = " video/id ",
                source = IglooNavigationSource.Channel,
            ),
        )

        assertEquals("shorts/channel/tiktok_creator/video%2Fid", target?.route)
        assertEquals("channel", target?.playlistType)
        assertEquals("tiktok_creator", target?.playlistId)
        assertEquals("video/id", target?.videoId)
    }

    @Test
    fun targetForOpenMediaBuildsFullscreenRoute() {
        val target = IglooNavigation.targetFor(
            IglooNavigationIntent.OpenMedia(
                ownerKind = " tweet ",
                ownerId = " status/123 ",
                index = 2,
                source = IglooNavigationSource.Feed,
            ),
        )

        assertEquals("media/tweet/status%2F123/2", target?.route)
        assertEquals("tweet", target?.mediaOwnerKind)
        assertEquals("status/123", target?.mediaOwnerId)
        assertEquals(2, target?.mediaIndex)
    }

    @Test
    fun targetForOpenThreadBuildsThreadRoute() {
        val target = IglooNavigation.targetFor(
            IglooNavigationIntent.OpenThread(
                tweetId = "status/123",
                source = IglooNavigationSource.Feed,
            ),
        )

        assertEquals("tweet/status%2F123", target?.route)
        assertEquals("status/123", target?.tweetId)
    }

    @Test
    fun targetForOpenDestinationUsesRegisteredRoute() {
        assertEquals(
            RouteRegistry.Settings.route,
            IglooNavigation.targetFor(
                IglooNavigationIntent.OpenDestination(IglooDestination.Settings, IglooNavigationSource.Drawer),
            )?.route,
        )
        assertEquals(
            RouteRegistry.Logs.route,
            IglooNavigation.targetFor(
                IglooNavigationIntent.OpenDestination(IglooDestination.Logs, IglooNavigationSource.Drawer),
            )?.route,
        )
        assertEquals(
            RouteRegistry.AllMoments.route,
            IglooNavigation.targetFor(
                IglooNavigationIntent.OpenDestination(IglooDestination.AllMoments, IglooNavigationSource.Moments),
            )?.route,
        )
    }

    @Test
    fun targetForRejectsBlankDynamicIds() {
        assertNull(
            IglooNavigation.targetFor(
                IglooNavigationIntent.OpenChannel(" ", IglooNavigationSource.Feed),
            ),
        )
        assertNull(
            IglooNavigation.targetFor(
                IglooNavigationIntent.OpenVideo(" ", IglooNavigationSource.Videos),
            ),
        )
        assertNull(
            IglooNavigation.targetFor(
                IglooNavigationIntent.OpenShorts("channel", "creator", " ", IglooNavigationSource.Channel),
            ),
        )
        assertNull(
            IglooNavigation.targetFor(
                IglooNavigationIntent.OpenMedia("tweet", " ", 0, IglooNavigationSource.Feed),
            ),
        )
        assertNull(
            IglooNavigation.targetFor(
                IglooNavigationIntent.OpenThread("", IglooNavigationSource.Thread),
            ),
        )
    }

    @Test
    fun shouldNavigateSkipsCurrentDynamicAndStaticRoutes() {
        assertFalse(
            IglooNavigation.shouldNavigate(
                currentRoute = RouteRegistry.Channel.route,
                currentArguments = mapOf("channel_id" to "twitter_account"),
                intent = IglooNavigationIntent.OpenChannel("twitter_account", IglooNavigationSource.Channel),
            ),
        )
        assertFalse(
            IglooNavigation.shouldNavigate(
                currentRoute = RouteRegistry.Player.route,
                currentArguments = mapOf("video_id" to "video-1"),
                intent = IglooNavigationIntent.OpenVideo("video-1", IglooNavigationSource.Player),
            ),
        )
        assertFalse(
            IglooNavigation.shouldNavigate(
                currentRoute = RouteRegistry.Thread.route,
                currentArguments = mapOf("tweet_id" to "tweet-1"),
                intent = IglooNavigationIntent.OpenThread("tweet-1", IglooNavigationSource.Thread),
            ),
        )
        assertFalse(
            IglooNavigation.shouldNavigate(
                currentRoute = RouteRegistry.Settings.route,
                currentArguments = emptyMap<String, String?>(),
                intent = IglooNavigationIntent.OpenDestination(IglooDestination.Settings, IglooNavigationSource.Drawer),
            ),
        )
        assertTrue(
            IglooNavigation.shouldNavigate(
                currentRoute = RouteRegistry.Videos.route,
                currentArguments = emptyMap<String, String?>(),
                intent = IglooNavigationIntent.OpenVideo("video-1", IglooNavigationSource.Videos),
            ),
        )
    }

    @Test
    fun dynamicSameRouteNavigationDoesNotReuseTopEntry() {
        val channelIntent = IglooNavigationIntent.OpenChannel(
            "twitter_bob",
            IglooNavigationSource.Channel,
        )
        assertTrue(
            IglooNavigation.shouldNavigate(
                currentRoute = RouteRegistry.Channel.route,
                currentArguments = mapOf("channel_id" to "twitter_alice"),
                intent = channelIntent,
            ),
        )
        assertFalse(
            IglooNavigation.shouldLaunchSingleTop(
                currentRoute = RouteRegistry.Channel.route,
                intent = channelIntent,
            ),
        )
        assertTrue(
            IglooNavigation.shouldLaunchSingleTop(
                currentRoute = RouteRegistry.Feed.route,
                intent = channelIntent,
            ),
        )
    }
}
