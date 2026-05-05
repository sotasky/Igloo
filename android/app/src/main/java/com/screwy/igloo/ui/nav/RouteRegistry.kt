package com.screwy.igloo.ui.nav

import com.screwy.igloo.R
import java.net.URLEncoder
import java.nio.charset.StandardCharsets

data class IglooRouteSpec(
    val route: String,
    val chrome: ChromeSpec,
    val deepLinks: List<String> = emptyList(),
)

object RouteRegistry {
    const val MomentsGraphRoute = "moments-graph"

    val Login = IglooRouteSpec(
        route = "login",
        chrome = RouteChromePolicy(
            topChrome = TopChrome.FullscreenMedia,
            bottomChrome = BottomChrome.Hidden,
            drawerChrome = DrawerChrome.Disabled,
        ),
    )

    val Feed = IglooRouteSpec(
        route = "feed",
        chrome = feedStylePolicy(topBarTitle = TopBarTitle.Resource(R.string.nav_feed)),
    )

    val Videos = IglooRouteSpec(
        route = "videos",
        chrome = feedStylePolicy(topBarTitle = TopBarTitle.Resource(R.string.nav_videos)),
    )

    val Moments = IglooRouteSpec(
        route = "moments",
        chrome = momentsPolicy(),
    )

    val AllMoments = IglooRouteSpec(
        route = "all-moments",
        chrome = momentsPolicy(),
    )

    val Bookmarks = IglooRouteSpec(
        route = "bookmarks",
        chrome = feedStylePolicy(topBarTitle = TopBarTitle.Resource(R.string.nav_bookmarks)),
    )

    val Liked = IglooRouteSpec(
        route = "liked",
        chrome = feedStylePolicy(topBarTitle = TopBarTitle.Resource(R.string.nav_liked)),
    )

    val Channel = IglooRouteSpec(
        route = "channel/{channel_id}",
        chrome = feedStylePolicy(topBarTitle = TopBarTitle.Channel),
        deepLinks = listOf("igloo://channel/{channel_id}"),
    )

    val Shorts = IglooRouteSpec(
        route = "shorts/{playlist_type}/{playlist_id}/{video_id}",
        chrome = momentsPolicy(),
        deepLinks = listOf("igloo://shorts/{playlist_type}/{playlist_id}/{video_id}"),
    )

    val Media = IglooRouteSpec(
        route = "media/{owner_kind}/{owner_id}/{index}",
        chrome = fullscreenMediaPolicy(),
    )

    val Player = IglooRouteSpec(
        route = "player/{video_id}",
        chrome = RouteChromePolicy(
            topChrome = TopChrome.PinnedMediaGuard,
            bottomChrome = BottomChrome.Hidden,
            drawerChrome = DrawerChrome.Disabled,
        ),
        deepLinks = listOf(
            "igloo://youtube/{video_id}",
            "igloo://tt/{video_id}",
            "igloo://ig/{video_id}",
        ),
    )

    val Thread = IglooRouteSpec(
        route = "tweet/{tweet_id}",
        chrome = RouteChromePolicy(
            topChrome = TopChrome.ScrollAwayAppBar,
            bottomChrome = BottomChrome.Hidden,
            drawerChrome = DrawerChrome.Disabled,
            topBarTitle = TopBarTitle.Resource(R.string.label_thread),
        ),
        deepLinks = listOf("igloo://tw/{tweet_id}"),
    )

    val Settings = IglooRouteSpec(
        route = "settings",
        chrome = scrollContentHeaderPolicy(),
    )

    val ThemeSettings = IglooRouteSpec(
        route = "settings/theme",
        chrome = feedStylePolicy(topBarTitle = TopBarTitle.Resource(R.string.label_theme)),
    )

    val PlaybackSettings = settingsChild("settings/playback")
    val FeedSettings = settingsChild("settings/feed")
    val SponsorBlockSettings = settingsChild("settings/sponsorblock")
    val StorageSettings = settingsChild("settings/storage")
    val AccountSettings = settingsChild("settings/account")
    val Logs = settingsChild(route = "logs")
    val OutboxLogs = settingsChild(route = "logs/outbox")

    val routes: List<IglooRouteSpec> = listOf(
        Login,
        Feed,
        Videos,
        Moments,
        AllMoments,
        Bookmarks,
        Liked,
        Channel,
        Shorts,
        Media,
        Player,
        Thread,
        Settings,
        ThemeSettings,
        PlaybackSettings,
        FeedSettings,
        SponsorBlockSettings,
        StorageSettings,
        AccountSettings,
        Logs,
        OutboxLogs,
    )

    private val routesByPattern: Map<String, IglooRouteSpec> = routes.associateBy { it.route }

    init {
        require(routesByPattern.size == routes.size) { "RouteRegistry contains duplicate route patterns" }
    }

    fun find(route: String?): IglooRouteSpec? = route?.let(routesByPattern::get)

    fun require(route: String): IglooRouteSpec =
        routesByPattern[route] ?: error("RouteRegistry does not contain route '$route'")

    fun channelRoute(channelId: String): String = "channel/${encodePathSegment(channelId)}"

    fun shortsRoute(playlistType: String, playlistId: String, videoId: String): String =
        "shorts/${encodePathSegment(playlistType)}/${encodePathSegment(playlistId)}/${encodePathSegment(videoId)}"

    fun mediaRoute(ownerKind: String, ownerId: String, index: Int): String =
        "media/${encodePathSegment(ownerKind)}/${encodePathSegment(ownerId)}/${index.coerceAtLeast(0)}"

    fun playerRoute(videoId: String): String = "player/${encodePathSegment(videoId)}"

    fun threadRoute(tweetId: String): String = "tweet/${encodePathSegment(tweetId)}"

    fun chromeFor(route: String?): ChromeSpec = find(route)?.chrome ?: Feed.chrome

    private fun settingsChild(route: String) = IglooRouteSpec(
        route = route,
        chrome = scrollContentHeaderPolicy(),
    )
}

private fun encodePathSegment(value: String): String =
    URLEncoder.encode(value, StandardCharsets.UTF_8.toString()).replace("+", "%20")

private fun feedStylePolicy(topBarTitle: TopBarTitle = TopBarTitle.None): RouteChromePolicy =
    RouteChromePolicy(
        topChrome = TopChrome.ScrollAwayAppBar,
        bottomChrome = BottomChrome.Visible,
        drawerChrome = DrawerChrome.Enabled,
        topBarTitle = topBarTitle,
    )

private fun momentsPolicy(): RouteChromePolicy =
    RouteChromePolicy(
        topChrome = TopChrome.ContentOwnsTopInset,
        bottomChrome = BottomChrome.Visible,
        drawerChrome = DrawerChrome.Enabled,
    )

private fun fullscreenMediaPolicy(): RouteChromePolicy =
    RouteChromePolicy(
        topChrome = TopChrome.FullscreenMedia,
        bottomChrome = BottomChrome.Hidden,
        drawerChrome = DrawerChrome.Disabled,
    )

private fun scrollContentHeaderPolicy(): RouteChromePolicy =
    RouteChromePolicy(
        topChrome = TopChrome.ScrollContentHeader,
        bottomChrome = BottomChrome.Visible,
        drawerChrome = DrawerChrome.Enabled,
    )
