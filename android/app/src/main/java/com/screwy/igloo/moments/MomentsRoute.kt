package com.screwy.igloo.moments

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavController
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.ui.UiStateSwitch
import com.screwy.igloo.ui.component.BookmarkSheet
import com.screwy.igloo.ui.component.MomentActionSheet
import com.screwy.igloo.ui.component.MomentsPlayer
import com.screwy.igloo.ui.component.sharePlainText
import com.screwy.igloo.ui.nav.IglooDestination
import com.screwy.igloo.ui.nav.IglooNavigationSource
import com.screwy.igloo.ui.nav.rememberIglooNavigator
import kotlinx.coroutines.launch
import org.koin.androidx.compose.koinViewModel
import org.koin.compose.koinInject

/**
 * Moments tab: TikTok-style vertical video pager.
 *
 * Shares a nav-graph-scoped [MomentsViewModel] with [AllMomentsHost]: both routes
 * resolve the VM against the `moments-graph` back-stack entry so seeding the
 * resume cursor from the grid (via [MomentsViewModel.selectResumeVideoId]) flows
 * straight into the player's [MomentsViewModel.startIndex].
 */
@Composable
fun MomentsRoute(
    navController: NavController,
    modifier: Modifier = Modifier,
) {
    val backStackEntry = rememberMomentsGraphBackStackEntry(navController) ?: return
    val vm: MomentsViewModel = koinViewModel(viewModelStoreOwner = backStackEntry)

    val items by vm.playerItems.collectAsStateWithLifecycle()
    val startIndex by vm.startIndex.collectAsStateWithLifecycle()
    val startVideoId by vm.startVideoId.collectAsStateWithLifecycle()
    val autoplayEnabled by vm.autoplayEnabled.collectAsStateWithLifecycle()
    val muted by vm.muted.collectAsStateWithLifecycle()
    val uiState by vm.playerUiState.collectAsStateWithLifecycle()
    val pendingBookmark by vm.pendingBookmark.collectAsStateWithLifecycle()
    val pendingMomentActions by vm.pendingMomentActions.collectAsStateWithLifecycle()
    val categories by vm.bookmarkCategories.collectAsStateWithLifecycle()
    val activeTab by vm.activeTab.collectAsStateWithLifecycle()
    val storyChannels by vm.storyChannels.collectAsStateWithLifecycle()
    val prefs: PreferencesRepo = koinInject()
    val useEmbedFriendlyShareLinks by prefs.shareEmbedFriendlyLinks()
        .collectAsStateWithLifecycle(initialValue = PreferencesRepo.Defaults.SHARE_EMBED_FRIENDLY_LINKS)
    var showStoryTray by remember { mutableStateOf(false) }
    val playerActiveTab = if (activeTab == "stories") "all" else activeTab

    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val navigator = rememberIglooNavigator(navController)

    Box(
        modifier = modifier
            .fillMaxSize()
            .background(Color.Black),
    ) {
        UiStateSwitch(state = uiState, modifier = Modifier.fillMaxSize()) {
            MomentsPlayer(
                items = items,
                startIndex = startIndex,
                startVideoId = startVideoId,
                autoSwipeDefault = autoplayEnabled,
                muteDefault = muted,
                onAutoSwipeChanged = vm::setAutoplayEnabled,
                onMuteChanged = vm::setMuted,
                onIndexChange = vm::onIndexChange,
                onViewEvent = vm::onViewEvent,
                onChannelClick = { cid ->
                    navigator.openChannel(cid, IglooNavigationSource.Moments)
                },
                onStoryClick = { cid, firstVideoId ->
                    navigator.openShorts(
                        playlistType = ShortsPlaylistType.Story.routeValue,
                        playlistId = cid,
                        videoId = firstVideoId,
                        source = IglooNavigationSource.Moments,
                    )
                },
                onBookmarkToggle = vm::toggleBookmark,
                onRequestBookmarkSheet = vm::requestBookmarkSheet,
                onFollowChannel = vm::followChannel,
                onUnfollowChannel = vm::unfollowChannel,
                onRequestMomentActions = vm::requestMomentActions,
                onShare = { item ->
                    scope.launch { sharePlainText(context, item.canonicalUrl, useEmbedFriendlyShareLinks) }
                },
                onMentionClick = vm::resolveMentionAndNavigate,
                onSwipeLeftToChannel = { cid ->
                    navigator.openChannel(cid, IglooNavigationSource.Moments)
                },
                onOpenAllMomentsGrid = {
                    navigator.openDestination(IglooDestination.AllMoments, IglooNavigationSource.Moments)
                },
                onEndReached = vm::notifyUpToDate,
                activeTab = playerActiveTab,
                onTabSelected = { tab ->
                    if (tab == "stories") {
                        showStoryTray = true
                    } else {
                        vm.setActiveTab(tab)
                    }
                },
            )
        }
        StoryTray(
            visible = showStoryTray,
            rows = storyChannels.map { it.toStoryTrayItem() },
            onDismiss = { showStoryTray = false },
            onStoryClick = { _, firstVideoId ->
                showStoryTray = false
                navigator.openShorts(
                    playlistType = ShortsPlaylistType.StoryTray.routeValue,
                    playlistId = ShortsPlaylistSpec.RootPlaylistId,
                    videoId = firstVideoId,
                    source = IglooNavigationSource.Moments,
                )
            },
            modifier = Modifier.align(Alignment.CenterEnd),
        )
    }

    pendingBookmark?.let { target ->
        BookmarkSheet(
            target = target,
            categories = categories,
            onConfirm = vm::confirmBookmark,
            onRemove = vm::removePendingBookmark,
            onDismiss = vm::dismissBookmarkSheet,
            onCreateCategory = vm::createCategory,
        )
    }
    pendingMomentActions?.let { item ->
        MomentActionSheet(
            item = item,
            onDismissRequest = vm::dismissMomentActions,
            onRepostsEnabledChanged = vm::setRepostsEnabled,
            onChannelMutedChanged = vm::setChannelMuted,
            onUnfollowChannel = vm::unfollowChannel,
        )
    }
}
