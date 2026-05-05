package com.screwy.igloo.thread

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavController
import com.screwy.igloo.R
import com.screwy.igloo.data.entity.ThreadedFeedRow
import com.screwy.igloo.feed.buildFeedMediaOpenSnapshot
import com.screwy.igloo.feed.buildProfileOpenSnapshot
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.ui.UiState
import com.screwy.igloo.ui.component.NativeFeedSurface
import com.screwy.igloo.ui.nav.IglooNavigationSource
import com.screwy.igloo.ui.nav.rememberIglooNavigator
import com.screwy.igloo.ui.theme.iglooColors
import org.koin.androidx.compose.koinViewModel
import org.koin.compose.koinInject

@Composable
fun ThreadRoute(
    tweetId: String,
    navController: NavController,
    modifier: Modifier = Modifier,
) {
    val vm: ThreadViewModel = koinViewModel()
    val chain by vm.chain.collectAsStateWithLifecycle()
    val pendingBookmark by vm.pendingBookmark.collectAsStateWithLifecycle()
    val categories by vm.bookmarkCategories.collectAsStateWithLifecycle()
    val mutedHandles by vm.mutedHandles.collectAsStateWithLifecycle()
    val mediaModels by vm.mediaModels.collectAsStateWithLifecycle()
    val baseUrlProvider: ServerBaseUrlProvider = koinInject()
    val baseUrl = baseUrlProvider.baseUrl()
    val navigator = rememberIglooNavigator(navController)
    val threadRows = remember(chain) {
        chain.map { row ->
            ThreadedFeedRow(
                row = row,
                chain = emptyList(),
            )
        }
    }

    LaunchedEffect(tweetId) {
        vm.load(tweetId)
    }

    Box(modifier = modifier.fillMaxSize()) {
        if (threadRows.isEmpty()) {
            Box(
                modifier = Modifier
                    .fillMaxSize()
                    .padding(24.dp),
                contentAlignment = Alignment.Center,
            ) {
                Text(
                    text = stringResource(R.string.thread_unavailable),
                    color = MaterialTheme.iglooColors.onSurfaceMuted,
                    style = MaterialTheme.typography.bodyMedium,
                )
            }
        } else {
            NativeFeedSurface(
                rows = threadRows,
                uiState = UiState.Data(Unit),
                isRefreshing = false,
                pendingBookmark = pendingBookmark,
                bookmarkCategories = categories,
                mutedHandles = mutedHandles,
                mediaModels = mediaModels,
                onRefresh = { vm.load(tweetId) },
                onChannelClick = { channelId ->
                    navigator.openChannel(channelId, IglooNavigationSource.Thread)
                },
                onRowClick = { row ->
                    if (row.item.isReply && row.item.tweetId != tweetId) {
                        navigator.openThread(row.item.tweetId, IglooNavigationSource.Thread)
                    }
                },
                onProfileOpen = { post ->
                    navigator.openChannel(
                        channelId = post.author.channelId,
                        source = IglooNavigationSource.Thread,
                        originItemId = post.row.item.tweetId,
                        snapshot = buildProfileOpenSnapshot(post, baseUrl),
                    )
                },
                onMentionClick = vm::resolveMentionAndNavigate,
                onLikeToggle = vm::toggleLike,
                onBookmarkToggle = vm::toggleBookmark,
                onFollowToggle = vm::toggleFollow,
                onStarToggle = vm::toggleStar,
                onMuteToggle = vm::toggleMute,
                onMediaOpen = { row, mediaIndex, visibleMediaModel ->
                    val snapshot = buildFeedMediaOpenSnapshot(
                        row = row,
                        mediaIndex = mediaIndex,
                        mediaModels = mediaModels,
                        visibleMediaModel = visibleMediaModel,
                    )
                    navigator.openMedia(
                        ownerKind = "tweet",
                        ownerId = row.item.tweetId,
                        index = mediaIndex,
                        source = IglooNavigationSource.Thread,
                        posterUri = snapshot.posterUri,
                        snapshot = snapshot,
                    )
                },
                onSeenReached = {},
                onConfirmBookmark = vm::confirmBookmark,
                onRemoveBookmark = vm::removePendingBookmark,
                onDismissBookmarkSheet = vm::dismissBookmarkSheet,
                onCreateCategory = vm::createCategory,
                onWarmMediaRows = vm::warmMediaModels,
                onQuoteOpen = { quoteTweetId ->
                    navigator.openThread(quoteTweetId, IglooNavigationSource.Thread)
                },
            )
        }
    }
}
