package com.screwy.igloo.moments

import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavController
import com.screwy.igloo.ui.UiStateSwitch
import com.screwy.igloo.ui.nav.IglooNavigationSource
import com.screwy.igloo.ui.nav.rememberIglooNavigator
import org.koin.androidx.compose.koinViewModel
import org.koin.compose.koinInject

/**
 * Hosts [AllMomentsRoute] against the nav-graph-scoped [MomentsViewModel]. Kept
 * separate from `AllMomentsRoute` so the composable stays pure (testable with a
 * canned items list) while this host owns the wiring concerns.
 *
 * Tap-to-resume flow: grid cell → `vm.selectResumeVideoId(videoId)` (writes the
 * MomentsCursor outbox kind, which in turn updates prefs + re-fires [startIndex]),
 * then `popBackStack()` back to the player. The player recomposes with the new
 * startIndex so the tapped video is the active page.
 */
@Composable
fun AllMomentsHost(
    navController: NavController,
    modifier: Modifier = Modifier,
) {
    val backStackEntry = rememberMomentsGraphBackStackEntry(navController) ?: return
    val vm: MomentsViewModel = koinViewModel(viewModelStoreOwner = backStackEntry)

    val items by vm.items.collectAsStateWithLifecycle()
    val startIndex by vm.startIndex.collectAsStateWithLifecycle()
    val uiState by vm.uiState.collectAsStateWithLifecycle()
    val activeTab by vm.activeTab.collectAsStateWithLifecycle()
    val storyChannels by vm.storyChannels.collectAsStateWithLifecycle()
    val navigator = rememberIglooNavigator(navController)

    UiStateSwitch(state = uiState, modifier = modifier) {
        AllMomentsRoute(
            items = items,
            initialIndex = startIndex,
            onMomentClick = { videoId ->
				navigator.openShorts(
                    playlistType = if (activeTab == "following") {
                        ShortsPlaylistType.Moments.routeValue
                    } else {
                        ShortsPlaylistType.AllMoments.routeValue
                    },
                    playlistId = ShortsPlaylistSpec.RootPlaylistId,
                    videoId = videoId,
                    source = IglooNavigationSource.AllMoments,
				)
            },
            onChannelClick = { cid ->
                navigator.openChannel(cid, IglooNavigationSource.AllMoments)
            },
            storyChannels = storyChannels,
            onStoryClick = { _, firstVideoId ->
                navigator.openShorts(
                    playlistType = ShortsPlaylistType.StoryTray.routeValue,
                    playlistId = ShortsPlaylistSpec.RootPlaylistId,
                    videoId = firstVideoId,
                    source = IglooNavigationSource.AllMoments,
                )
            },
            activeTab = activeTab,
            onTabSelected = vm::setActiveTab,
            onBack = { navController.popBackStack() },
        )
    }
}
