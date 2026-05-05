package com.screwy.igloo.ui.nav

import androidx.annotation.StringRes

/**
 * Central route chrome inventory for the Android app shell.
 *
 * This table is intentionally descriptive before it is clever: it preserves the
 * current route-specific exceptions while moving them out of ad hoc route
 * string checks in MainScaffold and individual screens.
 */
data class RouteChromePolicy(
    val topChrome: TopChrome,
    val bottomChrome: BottomChrome,
    val drawerChrome: DrawerChrome,
    val topBarTitle: TopBarTitle = TopBarTitle.None,
) {
    val usesScaffoldTopBar: Boolean
        get() = topChrome == TopChrome.ScrollAwayAppBar

    val showsBottomNav: Boolean
        get() = bottomChrome == BottomChrome.Visible
}

sealed class TopChrome {
    /**
     * The scaffold owns a Material top app bar with enter-always scroll
     * behavior. Feed-style content starts below the app bar and the bar
     * disappears while scrolling.
     */
    object ScrollAwayAppBar : TopChrome()

    /**
     * The screen renders its own safe-area-aware header as scroll content.
     * Settings/logs use this shape so their header naturally scrolls away with
     * the page.
     */
    object ScrollContentHeader : TopChrome()

    /**
     * The screen is chromeless at the scaffold layer and owns its top safe
     * inset. Moments uses this so media controls can sit near the top without a
     * persistent app bar.
     */
    object ContentOwnsTopInset : TopChrome()

    /**
     * A media route has a pinned guard above normal-view media. The YouTube
     * player uses this for its non-fullscreen video-at-top layout.
     */
    object PinnedMediaGuard : TopChrome()

    /**
     * The route draws fullscreen media and owns every edge.
     */
    object FullscreenMedia : TopChrome()

}

enum class BottomChrome {
    Visible,
    Hidden,
}

enum class DrawerChrome {
    Enabled,
    Disabled,
}

sealed class TopBarTitle {
    object None : TopBarTitle()
    data class Static(val value: String) : TopBarTitle()
    data class Resource(@param:StringRes val id: Int) : TopBarTitle()
    object Channel : TopBarTitle()
}

enum class OverlayChromeState(
    val hidesScaffoldTopBar: Boolean,
    val hidesBottomNav: Boolean,
) {
    None(hidesScaffoldTopBar = false, hidesBottomNav = false),
    HideTopBar(hidesScaffoldTopBar = true, hidesBottomNav = false),
    FullscreenMedia(hidesScaffoldTopBar = true, hidesBottomNav = true),
}

typealias ChromeSpec = RouteChromePolicy

fun routeChromePolicyFor(route: String?): RouteChromePolicy = RouteRegistry.chromeFor(route)
