package com.screwy.igloo.liked

import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavController
import com.screwy.igloo.R
import com.screwy.igloo.feed.buildProfileOpenSnapshot
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.ui.component.NativeFeedSurface
import com.screwy.igloo.ui.nav.IglooNavigationSource
import com.screwy.igloo.ui.nav.rememberIglooNavigator
import org.koin.androidx.compose.koinViewModel
import org.koin.compose.koinInject

/**
 * Liked posts drawer shortcut backed by the liked-only DAO query.
 */
@Composable
fun LikedRoute(
    navController: NavController,
    modifier: Modifier = Modifier,
) {
    val vm: LikedViewModel = koinViewModel()
    val rows by vm.rows.collectAsStateWithLifecycle()
    val isRefreshing by vm.isRefreshing.collectAsStateWithLifecycle()
    val uiState by vm.uiState.collectAsStateWithLifecycle()
    val pendingBookmark by vm.pendingBookmark.collectAsStateWithLifecycle()
    val categories by vm.bookmarkCategories.collectAsStateWithLifecycle()
    val mutedChannelIds by vm.mutedChannelIds.collectAsStateWithLifecycle()
    val mediaModels by vm.mediaModels.collectAsStateWithLifecycle()
    val baseUrlProvider: ServerBaseUrlProvider = koinInject()
    val baseUrl = baseUrlProvider.baseUrl()
    val navigator = rememberIglooNavigator(navController)

    NativeFeedSurface(
        rows = rows,
        uiState = uiState,
        isRefreshing = isRefreshing,
        pendingBookmark = pendingBookmark,
        bookmarkCategories = categories,
        mutedChannelIds = mutedChannelIds,
        mediaModels = mediaModels,
        onRefresh = vm::refresh,
        onChannelClick = { channelId ->
            navigator.openChannel(channelId, IglooNavigationSource.Liked)
        },
        onProfileOpen = { post ->
            navigator.openChannel(
                channelId = post.author.channelId,
                source = IglooNavigationSource.Liked,
                originItemId = post.row.item.tweetId,
				snapshot = buildProfileOpenSnapshot(post),
            )
        },
        onMentionClick = vm::resolveMentionAndNavigate,
        onLikeToggle = vm::toggleLike,
        onBookmarkToggle = vm::toggleBookmark,
        onFollowToggle = vm::toggleFollow,
        onStarToggle = vm::toggleStar,
        onMuteToggle = vm::toggleMute,
        onMediaOpen = { row, mediaIndex, _ ->
            navigator.openMedia(
                ownerKind = "tweet",
                ownerId = row.item.tweetId,
                index = mediaIndex,
                source = IglooNavigationSource.Liked,
            )
        },
        onSeenReached = vm::markSeen,
        onConfirmBookmark = vm::confirmBookmark,
        onRemoveBookmark = vm::removePendingBookmark,
        onDismissBookmarkSheet = vm::dismissBookmarkSheet,
        onCreateCategory = vm::createCategory,
        onMediaRowsChanged = vm::setMediaModelRows,
        onQuoteOpen = { tweetId ->
            navigator.openThread(tweetId, IglooNavigationSource.Liked)
        },
        emptyMessageRes = R.string.feed_empty_liked,
        modifier = modifier,
    )
}
