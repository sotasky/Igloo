package com.screwy.igloo.ui.component

import com.screwy.igloo.data.entity.ChannelDisplay
import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.displayOrName
import com.screwy.igloo.ui.nav.IglooDestination
import com.screwy.igloo.ui.nav.RouteRegistry
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class AppDrawerTest {

    @Test
    fun profile_handle_is_shown_next_to_distinct_display_name() {
        val row = channel(
            name = "Readable Creator",
            platform = "twitter",
            channelId = "twitter_creator",
            handle = "creator",
        )

        assertEquals("creator", drawerAccountHandle(row))
    }

    @Test
    fun matching_display_name_still_shows_explicit_handle() {
        val row = channel(
            name = "creator",
            platform = "twitter",
            channelId = "twitter_creator",
            handle = "creator",
        )

        assertEquals("creator", drawerAccountHandle(row))
    }

    @Test
    fun handle_like_profile_name_uses_distinct_channel_name_for_label() {
        val row = channel(
            name = "Readable Creator",
            platform = "twitter",
            channelId = "twitter_creator",
            handle = "creator",
            displayName = "@creator",
        )

        assertEquals("Readable Creator", row.displayOrName)
    }

    @Test
    fun handle_first_platforms_can_fall_back_to_source_id() {
        val row = channel(
            name = "Readable Creator",
            platform = "tiktok",
            channelId = "tiktok_creator",
            sourceId = "creator",
        )

        assertEquals("creator", drawerAccountHandle(row))
    }

    @Test
    fun tiktok_internal_source_id_is_not_rendered_as_handle() {
        val row = channel(
            name = "Readable Creator",
            platform = "tiktok",
            channelId = "tiktok_7000000000000000001",
            sourceId = "7000000000000000001",
        )

        assertEquals("", drawerAccountHandle(row))
    }

    @Test
    fun tiktok_internal_source_id_does_not_guess_from_name() {
        val row = channel(
            name = "creator_one",
            platform = "tiktok",
            channelId = "tiktok_7000000000000000001",
            sourceId = "7000000000000000001",
        )

        assertEquals("", drawerAccountHandle(row))
    }

    @Test
    fun youtube_source_id_is_not_rendered_as_handle() {
        val row = channel(
            name = "Readable Channel",
            platform = "youtube",
            channelId = "youtube_UCAbCdEfGhIjKlMnOpQrStUv",
            sourceId = "UCAbCdEfGhIjKlMnOpQrStUv",
        )

        assertEquals("", drawerAccountHandle(row))
    }

    @Test
    fun drawer_destination_selection_tracks_registered_routes() {
        assertTrue(drawerDestinationSelected(RouteRegistry.Feed.route, IglooDestination.Feed))
        assertTrue(drawerDestinationSelected(RouteRegistry.Videos.route, IglooDestination.Videos))
        assertTrue(drawerDestinationSelected(RouteRegistry.Moments.route, IglooDestination.Moments))
        assertTrue(drawerDestinationSelected(RouteRegistry.Shorts.route, IglooDestination.Moments))
        assertTrue(drawerDestinationSelected(RouteRegistry.Bookmarks.route, IglooDestination.Bookmarks))
        assertTrue(drawerDestinationSelected(RouteRegistry.Liked.route, IglooDestination.Liked))
        assertTrue(drawerDestinationSelected(RouteRegistry.PlaybackSettings.route, IglooDestination.Settings))
        assertTrue(drawerDestinationSelected(RouteRegistry.OutboxLogs.route, IglooDestination.Logs))
        assertFalse(drawerDestinationSelected(RouteRegistry.Videos.route, IglooDestination.Liked))
    }

    @Test
    fun compact_drawer_keeps_liked_while_wide_drawer_shows_all_primary_destinations() {
        assertEquals(
            listOf(IglooDestination.Liked),
            drawerPrimaryDestinations(widePrimaryNavigation = false),
        )
        assertEquals(
            listOf(
                IglooDestination.Feed,
                IglooDestination.Videos,
                IglooDestination.Moments,
                IglooDestination.Bookmarks,
                IglooDestination.Liked,
            ),
            drawerPrimaryDestinations(widePrimaryNavigation = true),
        )
    }

    @Test
    fun drawer_channel_selection_only_matches_current_channel_route() {
        assertTrue(
            drawerChannelSelected(
                currentRoute = RouteRegistry.Channel.route,
                currentChannelId = "tiktok_creator",
                rowChannelId = "tiktok_creator",
            ),
        )
        assertFalse(
            drawerChannelSelected(
                currentRoute = RouteRegistry.Videos.route,
                currentChannelId = "tiktok_creator",
                rowChannelId = "tiktok_creator",
            ),
        )
        assertFalse(
            drawerChannelSelected(
                currentRoute = RouteRegistry.Channel.route,
                currentChannelId = "tiktok_other",
                rowChannelId = "tiktok_creator",
            ),
        )
    }

    private fun channel(
        name: String,
        platform: String,
        channelId: String,
        sourceId: String? = null,
        handle: String? = null,
        displayName: String? = null,
    ): ChannelDisplay =
        ChannelDisplay(
            channel = ChannelEntity(
                channelId = channelId,
                sourceId = sourceId,
                name = name,
                platform = platform,
            ),
            isStarred = 0,
            isFollowed = 1,
            handle = handle,
            displayName = displayName,
        )
}
