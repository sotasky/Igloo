package com.screwy.igloo.feed

import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavController
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.ui.component.NativeFeedSurface
import com.screwy.igloo.ui.nav.IglooNavigationSource
import com.screwy.igloo.ui.nav.rememberIglooNavigator
import org.koin.androidx.compose.koinViewModel
import org.koin.compose.koinInject

/** Default bottom-nav tab for posts. */
@Composable
fun FeedRoute(
    navController: NavController,
    modifier: Modifier = Modifier,
) {
    val vm: FeedViewModel = koinViewModel()
    val rows by vm.rows.collectAsStateWithLifecycle()
    val isRefreshing by vm.isRefreshing.collectAsStateWithLifecycle()
    val uiState by vm.uiState.collectAsStateWithLifecycle()
    val pendingBookmark by vm.pendingBookmark.collectAsStateWithLifecycle()
    val categories by vm.bookmarkCategories.collectAsStateWithLifecycle()
    val mutedChannelIds by vm.mutedChannelIds.collectAsStateWithLifecycle()
    val newPostsAvailable by vm.newPostsAvailable.collectAsStateWithLifecycle()
    val newPostPosters by vm.newPostPosters.collectAsStateWithLifecycle()
    val mediaModels by vm.mediaModels.collectAsStateWithLifecycle()
    val baseUrlProvider: ServerBaseUrlProvider = koinInject()
    val baseUrl = baseUrlProvider.baseUrl()
    val navigator = rememberIglooNavigator(navController)

    NativeFeedSurface(
        rows = rows,
        uiState = uiState,
        isRefreshing = isRefreshing,
        newPostsAvailable = newPostsAvailable,
        newPostPosters = newPostPosters,
        pendingBookmark = pendingBookmark,
        bookmarkCategories = categories,
        mutedChannelIds = mutedChannelIds,
        mediaModels = mediaModels,
        onRefresh = vm::refresh,
        onNewPostsClick = vm::showNewPosts,
        onChannelClick = { channelId ->
            navigator.openChannel(channelId, IglooNavigationSource.Feed)
        },
        onProfileOpen = { post ->
            navigator.openChannel(
                channelId = post.author.channelId,
                source = IglooNavigationSource.Feed,
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
                source = IglooNavigationSource.Feed,
            )
        },
        onSeenReached = vm::markSeen,
        onConfirmBookmark = vm::confirmBookmark,
        onRemoveBookmark = vm::removePendingBookmark,
        onDismissBookmarkSheet = vm::dismissBookmarkSheet,
        onCreateCategory = vm::createCategory,
        onMediaRowsChanged = vm::setMediaModelRows,
        onRowClick = { row ->
            if (row.item.isReply) {
                navigator.openThread(row.item.tweetId, IglooNavigationSource.Feed)
            }
        },
        onQuoteOpen = { tweetId ->
            navigator.openThread(tweetId, IglooNavigationSource.Feed)
        },
        modifier = modifier,
    )
}
