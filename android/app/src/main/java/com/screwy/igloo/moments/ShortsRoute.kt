package com.screwy.igloo.moments

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
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
import com.screwy.igloo.ui.nav.IglooNavigationSource
import com.screwy.igloo.ui.nav.rememberIglooNavigator
import kotlinx.coroutines.launch
import org.koin.androidx.compose.koinViewModel
import org.koin.compose.koinInject
import org.koin.core.parameter.parametersOf

@Composable
fun ShortsRoute(
    playlistType: String,
    playlistId: String,
    videoId: String,
    navController: NavController,
    modifier: Modifier = Modifier,
) {
    val spec = remember(playlistType, playlistId) {
        ShortsPlaylistSpec.decode(playlistType, playlistId) ?: ShortsPlaylistSpec.allMoments()
    }
    val vm: ShortsRouteViewModel = koinViewModel(
        parameters = { parametersOf(spec, videoId) },
    )
    val items by vm.items.collectAsStateWithLifecycle()
    val startIndex by vm.startIndex.collectAsStateWithLifecycle()
    val uiState by vm.uiState.collectAsStateWithLifecycle()
    val autoplayEnabled by vm.autoplayEnabled.collectAsStateWithLifecycle()
    val muted by vm.muted.collectAsStateWithLifecycle()
    val pendingBookmark by vm.pendingBookmark.collectAsStateWithLifecycle()
    val pendingMomentActions by vm.pendingMomentActions.collectAsStateWithLifecycle()
    val categories by vm.bookmarkCategories.collectAsStateWithLifecycle()
    val currentVideoId by vm.currentVideoId.collectAsStateWithLifecycle()
    val storyChannels by vm.storyChannels.collectAsStateWithLifecycle()
    val prefs: PreferencesRepo = koinInject()
    val useEmbedFriendlyShareLinks by prefs.shareEmbedFriendlyLinks()
        .collectAsStateWithLifecycle(initialValue = PreferencesRepo.Defaults.SHARE_EMBED_FRIENDLY_LINKS)
    var showStoryTray by remember { mutableStateOf(false) }
    val navigator = rememberIglooNavigator(navController)
    val context = LocalContext.current
    val activeMomentsTab = when (spec.type) {
        ShortsPlaylistType.Moments -> "following"
        ShortsPlaylistType.AllMoments -> "all"
        else -> null
    }
    val storyPlaybackMode = spec.type == ShortsPlaylistType.Story || spec.type == ShortsPlaylistType.StoryTray

    Box(
        modifier = modifier
            .fillMaxSize()
            .background(Color.Black),
    ) {
        UiStateSwitch(state = uiState, modifier = Modifier.fillMaxSize()) {
            MomentsPlayer(
                items = items,
                startIndex = startIndex,
                startVideoId = currentVideoId,
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
                    sharePlainText(context, item.canonicalUrl, useEmbedFriendlyShareLinks)
                },
                onMentionClick = vm::resolveMentionAndNavigate,
                onSwipeLeftToChannel = { cid ->
                    navigator.openChannel(cid, IglooNavigationSource.Moments)
                },
                onOpenAllMomentsGrid = { navController.popBackStack() },
                onEndReached = {
                    if (storyPlaybackMode) {
                        navController.popBackStack()
                    }
                },
                forceAutoSwipe = storyPlaybackMode,
                exitOnEnd = storyPlaybackMode,
                storyCrossProfileAdvance = spec.type == ShortsPlaylistType.StoryTray,
                activeTab = activeMomentsTab,
                onTabSelected = if (activeMomentsTab == null) {
                    null
                } else {
                    { tab ->
                        if (tab == "stories") {
                            showStoryTray = true
                        } else {
                            val nextType = if (tab == "following") {
                                ShortsPlaylistType.Moments.routeValue
                            } else {
                                ShortsPlaylistType.AllMoments.routeValue
                            }
                            navigator.openShorts(
                                playlistType = nextType,
                                playlistId = ShortsPlaylistSpec.RootPlaylistId,
                                videoId = currentVideoId.ifBlank { videoId },
                                source = IglooNavigationSource.Moments,
                            )
                        }
                    }
                },
                modifier = Modifier.fillMaxSize(),
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
