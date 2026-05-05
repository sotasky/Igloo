package com.screwy.igloo.ui.nav

import androidx.compose.animation.EnterTransition
import androidx.compose.animation.ExitTransition
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.navigation.NavBackStackEntry
import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navDeepLink
import androidx.navigation.navigation
import com.screwy.igloo.auth.AuthRepo
import com.screwy.igloo.auth.LoginRoute
import com.screwy.igloo.bookmarks.BookmarksRoute
import com.screwy.igloo.channel.ChannelRoute
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.feed.FeedRoute
import com.screwy.igloo.liked.LikedRoute
import com.screwy.igloo.logs.LogFilter
import com.screwy.igloo.logs.LogsRoute
import com.screwy.igloo.media.MediaRoute
import com.screwy.igloo.moments.AllMomentsHost
import com.screwy.igloo.moments.MomentsRoute
import com.screwy.igloo.moments.ShortsRoute
import com.screwy.igloo.player.PlayerRoute
import com.screwy.igloo.settings.AccountRoute
import com.screwy.igloo.settings.FeedRoute as FeedSettingsRoute
import com.screwy.igloo.settings.PlaybackRoute
import com.screwy.igloo.settings.SettingsHubRoute
import com.screwy.igloo.settings.SponsorBlockRoute
import com.screwy.igloo.settings.StorageRoute
import com.screwy.igloo.settings.ThemeRoute
import com.screwy.igloo.thread.ThreadRoute
import com.screwy.igloo.ui.UiEffect
import com.screwy.igloo.ui.UiEffects
import com.screwy.igloo.videos.VideosRoute
import org.koin.compose.koinInject

/**
 * Root NavHost. Moments sits in a nested `moments-graph` so the Moments VM is
 * scoped to `{moments, all-moments}` and resume cursor state is shared across
 * the two destinations.
 *
 * Deep links: `igloo://channel/{id}`, `igloo://youtube/{id}`, `igloo://tt/{id}`,
 * `igloo://ig/{id}`, `igloo://tw/{id}`.
 */
@Composable
fun AppNavHost() {
    val navController = rememberNavController()
    val authRepo: AuthRepo = koinInject()
    val uiEffects: UiEffects = koinInject()
    // PreferencesRepo is DB-backed — resolve only when logged in, otherwise
    // Koin would trigger DatabaseHolder.requireCurrent() before a session exists.
    val startDestination = if (!authRepo.canOpenLocalSessionSync()) {
        RouteRegistry.Login.route
    } else {
        val prefs: PreferencesRepo = koinInject()
        val pref = prefs.startingPageSync()
        if (pref in PreferencesRepo.Defaults.VALID_STARTING_PAGES) pref else RouteRegistry.Feed.route
    }

    LaunchedEffect(navController, uiEffects) {
        uiEffects.flow.collect { effect ->
            when (effect) {
                is UiEffect.NavigateTo -> navController.navigate(effect.route)
                UiEffect.RequireLogin -> {
                    navController.navigate(RouteRegistry.Login.route) {
                        popUpTo(navController.graph.id) { inclusive = true }
                    }
                }
                is UiEffect.Toast,
                is UiEffect.ToastRes,
                is UiEffect.DialogError -> Unit
            }
        }
    }

    NavHost(
        navController = navController,
        startDestination = startDestination,
        enterTransition = { EnterTransition.None },
        exitTransition = { ExitTransition.None },
        popEnterTransition = { EnterTransition.None },
        popExitTransition = { ExitTransition.None },
    ) {
        directDestination(RouteRegistry.Login) { LoginRoute(navController) }

        scaffoldDestination(navController, RouteRegistry.Feed) { FeedRoute(navController) }
        scaffoldDestination(navController, RouteRegistry.Videos) { VideosRoute(navController) }

        navigation(route = RouteRegistry.MomentsGraphRoute, startDestination = RouteRegistry.Moments.route) {
            scaffoldDestination(navController, RouteRegistry.Moments) { MomentsRoute(navController) }
            scaffoldDestination(navController, RouteRegistry.AllMoments) { AllMomentsHost(navController) }
        }

        scaffoldDestination(navController, RouteRegistry.Bookmarks) {
            BookmarksRoute(navController = navController)
        }
        scaffoldDestination(navController, RouteRegistry.Liked) { LikedRoute(navController) }

        scaffoldDestination(navController, RouteRegistry.Channel) { entry ->
            val channelId = entry.arguments!!.getString("channel_id")!!
            val initialSnapshot = remember(entry) {
                navController.consumeProfileOpenSnapshotFromPrevious()
            }
            ChannelRoute(
                channelId = channelId,
                navController = navController,
                initialSnapshot = initialSnapshot,
            )
        }

        scaffoldDestination(navController, RouteRegistry.Shorts) { entry ->
            ShortsRoute(
                playlistType = entry.arguments!!.getString("playlist_type")!!,
                playlistId = entry.arguments!!.getString("playlist_id")!!,
                videoId = entry.arguments!!.getString("video_id")!!,
                navController = navController,
            )
        }

        directDestination(RouteRegistry.Media) { entry ->
            val initialSnapshot = remember(entry) {
                navController.consumeMediaOpenSnapshotFromPrevious()
            }
            MediaRoute(
                ownerKind = entry.arguments!!.getString("owner_kind")!!,
                ownerId = entry.arguments!!.getString("owner_id")!!,
                index = entry.arguments!!.getString("index")!!.toIntOrNull() ?: 0,
                navController = navController,
                initialSnapshot = initialSnapshot,
            )
        }

        directDestination(RouteRegistry.Player) { entry ->
            val videoId = entry.arguments!!.getString("video_id")!!
            PlayerRoute(videoId = videoId, navController = navController)
        }

        scaffoldDestination(navController, RouteRegistry.Thread) { entry ->
            val tweetId = entry.arguments!!.getString("tweet_id")!!
            ThreadRoute(tweetId = tweetId, navController = navController)
        }

        scaffoldDestination(navController, RouteRegistry.Settings) { SettingsHubRoute(navController) }
        scaffoldDestination(navController, RouteRegistry.ThemeSettings) { ThemeRoute() }
        scaffoldDestination(navController, RouteRegistry.PlaybackSettings) { PlaybackRoute(navController) }
        scaffoldDestination(navController, RouteRegistry.FeedSettings) { FeedSettingsRoute(navController) }
        scaffoldDestination(navController, RouteRegistry.SponsorBlockSettings) { SponsorBlockRoute(navController) }
        scaffoldDestination(navController, RouteRegistry.StorageSettings) { StorageRoute(navController) }
        scaffoldDestination(navController, RouteRegistry.AccountSettings) { AccountRoute(navController) }
        scaffoldDestination(navController, RouteRegistry.Logs) { LogsRoute(navController = navController) }
        scaffoldDestination(navController, RouteRegistry.OutboxLogs) {
            LogsRoute(navController = navController, initialFilter = LogFilter.Outbox)
        }
    }
}

private fun NavGraphBuilder.scaffoldDestination(
    navController: NavController,
    spec: IglooRouteSpec,
    content: @Composable (NavBackStackEntry) -> Unit,
) {
    composable(
        route = spec.route,
        deepLinks = spec.deepLinks.toNavDeepLinks(),
    ) { entry ->
        MainScaffold(navController = navController) {
            content(entry)
        }
    }
}

private fun NavGraphBuilder.directDestination(
    spec: IglooRouteSpec,
    content: @Composable (NavBackStackEntry) -> Unit,
) {
    composable(
        route = spec.route,
        deepLinks = spec.deepLinks.toNavDeepLinks(),
    ) { entry ->
        content(entry)
    }
}

private fun List<String>.toNavDeepLinks() = map { pattern ->
    navDeepLink { uriPattern = pattern }
}
