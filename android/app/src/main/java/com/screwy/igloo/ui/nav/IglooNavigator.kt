package com.screwy.igloo.ui.nav

import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.navigation.NavController
import com.screwy.igloo.media.MediaUri

enum class IglooNavigationSource {
    Feed,
    Bookmarks,
    Liked,
    Channel,
    Videos,
    Moments,
    AllMoments,
    MediaViewer,
    Player,
    Thread,
    Drawer,
    Settings,
    Resolver,
}

enum class IglooDestination {
    Liked,
    Settings,
    Logs,
    AllMoments,
    ThemeSettings,
    PlaybackSettings,
    FeedSettings,
    SponsorBlockSettings,
    StorageSettings,
    AccountSettings,
}

sealed interface IglooNavigationIntent {
    val source: IglooNavigationSource

    data class OpenChannel(
        val channelId: String?,
        override val source: IglooNavigationSource,
        val originItemId: String? = null,
        val snapshot: ProfileOpenSnapshot? = null,
    ) : IglooNavigationIntent

    data class OpenVideo(
        val videoId: String?,
        override val source: IglooNavigationSource,
        val posterUri: MediaUri = MediaUri.Missing,
    ) : IglooNavigationIntent

    data class OpenShorts(
        val playlistType: String?,
        val playlistId: String?,
        val videoId: String?,
        override val source: IglooNavigationSource,
        val posterUri: MediaUri = MediaUri.Missing,
    ) : IglooNavigationIntent

    data class OpenMedia(
        val ownerKind: String?,
        val ownerId: String?,
        val index: Int,
        override val source: IglooNavigationSource,
        val posterUri: MediaUri = MediaUri.Missing,
        val snapshot: MediaOpenSnapshot? = null,
    ) : IglooNavigationIntent

    data class OpenThread(
        val tweetId: String?,
        override val source: IglooNavigationSource,
    ) : IglooNavigationIntent

    data class OpenDestination(
        val destination: IglooDestination,
        override val source: IglooNavigationSource,
    ) : IglooNavigationIntent
}

data class IglooNavigationTarget(
    val route: String,
    val channelId: String? = null,
    val playlistType: String? = null,
    val playlistId: String? = null,
    val videoId: String? = null,
    val mediaOwnerKind: String? = null,
    val mediaOwnerId: String? = null,
    val mediaIndex: Int? = null,
    val tweetId: String? = null,
    val fullscreenTransition: FullscreenMediaTransition? = null,
    val mediaOpenSnapshot: MediaOpenSnapshot? = null,
    val profileOpenSnapshot: ProfileOpenSnapshot? = null,
)

object IglooNavigation {
    fun routeForChannel(
        channelId: String?,
        source: IglooNavigationSource,
        originItemId: String? = null,
    ): String? = targetFor(
        IglooNavigationIntent.OpenChannel(
            channelId = channelId,
            source = source,
            originItemId = originItemId,
        ),
    )?.route

    fun targetFor(intent: IglooNavigationIntent): IglooNavigationTarget? =
        when (intent) {
            is IglooNavigationIntent.OpenChannel -> {
                val channelId = normalizedId(intent.channelId) ?: return null
                IglooNavigationTarget(
                    route = RouteRegistry.channelRoute(channelId),
                    channelId = channelId,
                    profileOpenSnapshot = intent.snapshot,
                )
            }
            is IglooNavigationIntent.OpenVideo -> {
                val videoId = normalizedId(intent.videoId) ?: return null
                IglooNavigationTarget(
                    route = RouteRegistry.playerRoute(videoId),
                    videoId = videoId,
                    fullscreenTransition = intent.posterUri
                        .takeUnless { it is MediaUri.Missing }
                        ?.let { FullscreenMediaTransition(mediaId = videoId, posterUri = it) },
                )
            }
            is IglooNavigationIntent.OpenShorts -> {
                val playlistType = normalizedId(intent.playlistType) ?: return null
                val playlistId = normalizedId(intent.playlistId) ?: return null
                val videoId = normalizedId(intent.videoId) ?: return null
                IglooNavigationTarget(
                    route = RouteRegistry.shortsRoute(playlistType, playlistId, videoId),
                    playlistType = playlistType,
                    playlistId = playlistId,
                    videoId = videoId,
                    fullscreenTransition = intent.posterUri
                        .takeUnless { it is MediaUri.Missing }
                        ?.let { FullscreenMediaTransition(mediaId = videoId, posterUri = it) },
                )
            }
            is IglooNavigationIntent.OpenMedia -> {
                val ownerKind = normalizedId(intent.ownerKind) ?: return null
                val ownerId = normalizedId(intent.ownerId) ?: return null
                val index = intent.index.coerceAtLeast(0)
                IglooNavigationTarget(
                    route = RouteRegistry.mediaRoute(ownerKind, ownerId, index),
                    mediaOwnerKind = ownerKind,
                    mediaOwnerId = ownerId,
                    mediaIndex = index,
                    fullscreenTransition = intent.posterUri
                        .takeUnless { it is MediaUri.Missing }
                        ?.let { FullscreenMediaTransition(mediaId = ownerId, posterUri = it) },
                    mediaOpenSnapshot = intent.snapshot,
                )
            }
            is IglooNavigationIntent.OpenThread -> {
                val tweetId = normalizedId(intent.tweetId) ?: return null
                IglooNavigationTarget(
                    route = RouteRegistry.threadRoute(tweetId),
                    tweetId = tweetId,
                )
            }
            is IglooNavigationIntent.OpenDestination -> {
                IglooNavigationTarget(route = routeForDestination(intent.destination))
            }
        }

    fun shouldNavigate(
        currentRoute: String?,
        currentArguments: Map<String, String?>,
        intent: IglooNavigationIntent,
    ): Boolean {
        val target = targetFor(intent) ?: return false
        return when (intent) {
            is IglooNavigationIntent.OpenChannel ->
                currentRoute != RouteRegistry.Channel.route ||
                    currentArguments["channel_id"]?.trim() != target.channelId
            is IglooNavigationIntent.OpenVideo ->
                currentRoute != RouteRegistry.Player.route ||
                    currentArguments["video_id"]?.trim() != target.videoId
            is IglooNavigationIntent.OpenShorts ->
                currentRoute != RouteRegistry.Shorts.route ||
                    currentArguments["playlist_type"]?.trim() != target.playlistType ||
                    currentArguments["playlist_id"]?.trim() != target.playlistId ||
                    currentArguments["video_id"]?.trim() != target.videoId
            is IglooNavigationIntent.OpenMedia ->
                currentRoute != RouteRegistry.Media.route ||
                    currentArguments["owner_kind"]?.trim() != target.mediaOwnerKind ||
                    currentArguments["owner_id"]?.trim() != target.mediaOwnerId ||
                    currentArguments["index"]?.toIntOrNull() != target.mediaIndex
            is IglooNavigationIntent.OpenThread ->
                currentRoute != RouteRegistry.Thread.route ||
                    currentArguments["tweet_id"]?.trim() != target.tweetId
            is IglooNavigationIntent.OpenDestination ->
                currentRoute != target.route
        }
    }

    fun shouldLaunchSingleTop(
        currentRoute: String?,
        intent: IglooNavigationIntent,
    ): Boolean {
        targetFor(intent) ?: return false
        return currentRoute != routePatternFor(intent)
    }

    private fun routePatternFor(intent: IglooNavigationIntent): String =
        when (intent) {
            is IglooNavigationIntent.OpenChannel -> RouteRegistry.Channel.route
            is IglooNavigationIntent.OpenVideo -> RouteRegistry.Player.route
            is IglooNavigationIntent.OpenShorts -> RouteRegistry.Shorts.route
            is IglooNavigationIntent.OpenMedia -> RouteRegistry.Media.route
            is IglooNavigationIntent.OpenThread -> RouteRegistry.Thread.route
            is IglooNavigationIntent.OpenDestination -> routeForDestination(intent.destination)
        }

    private fun routeForDestination(destination: IglooDestination): String =
        when (destination) {
            IglooDestination.Liked -> RouteRegistry.Liked.route
            IglooDestination.Settings -> RouteRegistry.Settings.route
            IglooDestination.Logs -> RouteRegistry.Logs.route
            IglooDestination.AllMoments -> RouteRegistry.AllMoments.route
            IglooDestination.ThemeSettings -> RouteRegistry.ThemeSettings.route
            IglooDestination.PlaybackSettings -> RouteRegistry.PlaybackSettings.route
            IglooDestination.FeedSettings -> RouteRegistry.FeedSettings.route
            IglooDestination.SponsorBlockSettings -> RouteRegistry.SponsorBlockSettings.route
            IglooDestination.StorageSettings -> RouteRegistry.StorageSettings.route
            IglooDestination.AccountSettings -> RouteRegistry.AccountSettings.route
        }

    private fun normalizedId(value: String?): String? =
        value?.trim()?.takeIf { it.isNotEmpty() }
}

class IglooNavigator internal constructor(
    private val navController: NavController,
    private val beforeNavigate: () -> Unit = {},
) {
    fun openChannel(
        channelId: String?,
        source: IglooNavigationSource,
        originItemId: String? = null,
        snapshot: ProfileOpenSnapshot? = null,
    ) {
        open(IglooNavigationIntent.OpenChannel(channelId, source, originItemId, snapshot))
    }

    fun openVideo(
        videoId: String?,
        source: IglooNavigationSource,
        posterUri: MediaUri = MediaUri.Missing,
    ) {
        open(IglooNavigationIntent.OpenVideo(videoId, source, posterUri))
    }

    fun openShorts(
        playlistType: String?,
        playlistId: String?,
        videoId: String?,
        source: IglooNavigationSource,
        posterUri: MediaUri = MediaUri.Missing,
    ) {
        open(IglooNavigationIntent.OpenShorts(playlistType, playlistId, videoId, source, posterUri))
    }

    fun openMedia(
        ownerKind: String?,
        ownerId: String?,
        index: Int,
        source: IglooNavigationSource,
        posterUri: MediaUri = MediaUri.Missing,
        snapshot: MediaOpenSnapshot? = null,
    ) {
        open(IglooNavigationIntent.OpenMedia(ownerKind, ownerId, index, source, posterUri, snapshot))
    }

    fun openThread(tweetId: String?, source: IglooNavigationSource) {
        open(IglooNavigationIntent.OpenThread(tweetId, source))
    }

    fun openDestination(destination: IglooDestination, source: IglooNavigationSource) {
        open(IglooNavigationIntent.OpenDestination(destination, source))
    }

    fun open(intent: IglooNavigationIntent) {
        val target = IglooNavigation.targetFor(intent) ?: return
        val currentEntry = navController.currentBackStackEntry
        val currentRoute = currentEntry?.destination?.route
        beforeNavigate()
        if (!IglooNavigation.shouldNavigate(
                currentRoute = currentRoute,
                currentArguments = mapOf(
                    "channel_id" to currentEntry?.arguments?.getString("channel_id"),
                    "playlist_type" to currentEntry?.arguments?.getString("playlist_type"),
                    "playlist_id" to currentEntry?.arguments?.getString("playlist_id"),
                    "video_id" to currentEntry?.arguments?.getString("video_id"),
                    "owner_kind" to currentEntry?.arguments?.getString("owner_kind"),
                    "owner_id" to currentEntry?.arguments?.getString("owner_id"),
                    "index" to currentEntry?.arguments?.getString("index"),
                    "tweet_id" to currentEntry?.arguments?.getString("tweet_id"),
                ),
                intent = intent,
            )
        ) {
            return
        }
        target.fullscreenTransition?.let(navController::prepareFullscreenMediaTransitionForNext)
        target.mediaOpenSnapshot?.let(navController::prepareMediaOpenSnapshotForNext)
        target.profileOpenSnapshot?.let(navController::prepareProfileOpenSnapshotForNext)
        val launchSingleTop = IglooNavigation.shouldLaunchSingleTop(currentRoute, intent)
        navController.navigate(target.route) {
            this.launchSingleTop = launchSingleTop
            restoreState = launchSingleTop
        }
    }
}

@Composable
fun rememberIglooNavigator(
    navController: NavController,
    beforeNavigate: () -> Unit = {},
): IglooNavigator {
    val currentBeforeNavigate = rememberUpdatedState(beforeNavigate)
    return remember(navController) {
        IglooNavigator(
            navController = navController,
            beforeNavigate = { currentBeforeNavigate.value() },
        )
    }
}
