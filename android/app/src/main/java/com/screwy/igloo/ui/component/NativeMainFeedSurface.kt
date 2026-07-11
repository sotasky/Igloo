// SPDX-License-Identifier: AGPL-3.0-only
// RecyclerView list/header/video behavior is adapted from Flare's AGPL-3.0 timeline patterns.

package com.screwy.igloo.ui.component

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ArrowUpward
import androidx.compose.material.icons.filled.KeyboardArrowUp
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.SmallFloatingActionButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.graphics.Color as ComposeColor
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.compose.ui.zIndex
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.recyclerview.widget.LinearLayoutManager
import coil3.ImageLoader
import com.screwy.igloo.R
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.entity.FeedRow
import com.screwy.igloo.data.entity.ThreadedFeedRow
import com.screwy.igloo.feed.FeedMediaGridModel
import com.screwy.igloo.feed.SocialPostModel
import com.screwy.igloo.media.MediaResolvers
import com.screwy.igloo.net.IglooHostProvider
import com.screwy.igloo.net.auth.AuthTokenProvider
import com.screwy.igloo.ui.UiState
import com.screwy.igloo.ui.nav.ApplyOverlayChrome
import com.screwy.igloo.ui.nav.OverlayChromeState
import com.screwy.igloo.ui.theme.iglooColors
import kotlinx.coroutines.delay
import org.koin.compose.koinInject

data class NewPostPoster(
    val channelId: String,
    val contentDescription: String,
)

internal const val NativeChannelHeaderBannerHeightDp = 176
internal const val NativeChannelHeaderBannerFrameHeightDp = NativeChannelHeaderBannerHeightDp
internal const val NativeChannelHeaderAvatarSizeDp = 108
internal const val NativeChannelHeaderAvatarOverlapDp = NativeChannelHeaderAvatarSizeDp / 2
internal const val NativeChannelHeaderInlineAvatarSizeDp = 60
internal const val NativeChannelHeaderActionRowHeightDp = NativeChannelHeaderAvatarOverlapDp + 6
internal const val NativeChannelHeaderFollowHeightDp = 44
internal const val NativeChannelHeaderIconButtonSizeDp = 40
internal const val NativeChannelHeaderNameTextSp = 27f
internal const val NativeChannelHeaderBioTextSp = 16f
internal const val NativeChannelHeaderMetaTextSp = 17f

internal enum class NativeFeedPrimaryAction {
    Share,
    Like,
    Bookmark,
}

internal val NativeFeedPrimaryActions = listOf(
    NativeFeedPrimaryAction.Share,
    NativeFeedPrimaryAction.Like,
    NativeFeedPrimaryAction.Bookmark,
)

internal const val NativeFeedBodyCollapsedLines = 15
internal const val NativeFeedParentBodyCollapsedLines = 9
internal const val NativeFeedQuoteCollapsedLines = 2

@Composable
internal fun NativeFeedSurface(
    rows: List<ThreadedFeedRow>,
    uiState: UiState<Unit>,
    isRefreshing: Boolean,
    newPostsAvailable: Boolean = false,
    newPostPosters: List<NewPostPoster> = emptyList(),
    pendingBookmark: BookmarkTarget?,
    bookmarkCategories: List<BookmarkCategoryDisplay>,
    mutedChannelIds: Set<String>,
    mediaModels: Map<String, FeedMediaGridModel> = emptyMap(),
    onRefresh: () -> Unit,
    onNewPostsClick: () -> Unit = onRefresh,
    onChannelClick: (channelId: String) -> Unit,
    onMentionClick: (handle: String) -> Unit,
    onLikeToggle: (tweetId: String, newValue: Boolean) -> Unit,
    onBookmarkToggle: (FeedRow) -> Unit,
    onFollowToggle: (channelId: String, newValue: Boolean) -> Unit,
    onStarToggle: (channelId: String, newValue: Boolean) -> Unit,
    onMuteToggle: (channelId: String, newValue: Boolean) -> Unit,
    onMediaOpen: (FeedRow, mediaIndex: Int, visibleMediaModel: FeedMediaGridModel) -> Unit,
    onSeenReached: (tweetIds: List<String>) -> Unit,
    onConfirmBookmark: (BookmarkPayload) -> Unit,
    onRemoveBookmark: () -> Unit,
    onDismissBookmarkSheet: () -> Unit,
    onCreateCategory: (name: String) -> Unit,
    modifier: Modifier = Modifier,
    emptyMessageRes: Int = R.string.feed_empty_posts,
    channelHeader: ChannelProfileHeaderUiModel? = null,
    onHeaderFollowToggle: (newValue: Boolean) -> Unit = {},
    onHeaderStarToggle: (newValue: Boolean) -> Unit = {},
    onHeaderRefresh: () -> Unit = onRefresh,
    onHeaderOpenInPlatform: () -> Unit = {},
    onMediaRowsChanged: (List<FeedRow>) -> Unit = {},
    onRowClick: (FeedRow) -> Unit = {},
    onQuoteOpen: (tweetId: String) -> Unit = {},
    onProfileOpen: (SocialPostModel) -> Unit = { post -> onChannelClick(post.author.channelId) },
) {
    val context = LocalContext.current
    val imageLoader: ImageLoader = koinInject()
    val authTokens: AuthTokenProvider = koinInject()
    val iglooHostProvider: IglooHostProvider = koinInject()
    val mediaResolvers: MediaResolvers = koinInject()
    val prefs: PreferencesRepo = koinInject()
    val useEmbedFriendlyShareLinks by prefs.shareEmbedFriendlyLinks()
        .collectAsStateWithLifecycle(initialValue = PreferencesRepo.Defaults.SHARE_EMBED_FRIENDLY_LINKS)
    val lifecycleOwner = LocalLifecycleOwner.current
    val colors = nativeFeedColors()
    var pendingUnfollowChannelId by remember { mutableStateOf<String?>(null) }
    var pendingMuteAction by remember { mutableStateOf<FeedMuteMenuAction?>(null) }
    val currentCallbacks by rememberUpdatedState(
        NativeFeedCallbacks(
            mutedChannelIds = mutedChannelIds,
            onRefresh = onRefresh,
            onProfileOpen = onProfileOpen,
            onMentionClick = onMentionClick,
            onLikeToggle = onLikeToggle,
            onBookmarkToggle = onBookmarkToggle,
            onFollowToggle = onFollowToggle,
            onStarToggle = onStarToggle,
            onMuteToggle = onMuteToggle,
            onMediaOpen = onMediaOpen,
            onSeenReached = onSeenReached,
            onMediaRowsChanged = onMediaRowsChanged,
            onRowClick = onRowClick,
            onQuoteOpen = onQuoteOpen,
            onRequestUnfollowConfirmation = { pendingUnfollowChannelId = it },
            onRequestMuteConfirmation = { pendingMuteAction = it },
            onHeaderFollowToggle = onHeaderFollowToggle,
            onHeaderStarToggle = onHeaderStarToggle,
            onHeaderRefresh = onHeaderRefresh,
            onHeaderOpenInPlatform = onHeaderOpenInPlatform,
            useEmbedFriendlyShareLinks = useEmbedFriendlyShareLinks,
        )
    )
    val seenBatcher = remember(onSeenReached) { SeenBatcher(onSeenReached) }
    var showScrollToTop by remember { mutableStateOf(false) }
    var scrollAnchorRowId by rememberSaveable { mutableStateOf<String?>(null) }
    var scrollAnchorOffsetPx by rememberSaveable { mutableStateOf(0) }
    val controller = remember(context, imageLoader, authTokens, iglooHostProvider, mediaResolvers, seenBatcher) {
        NativeMainFeedController(
            context = context,
            imageLoader = imageLoader,
            authTokens = authTokens,
            iglooHostProvider = iglooHostProvider,
            mediaResolvers = mediaResolvers,
            colors = colors,
            callbacks = currentCallbacks,
            seenBatcher = seenBatcher,
            onScrollToTopVisibility = { showScrollToTop = it },
            initialScrollAnchor = NativeFeedScrollAnchor(
                rowId = scrollAnchorRowId,
                offsetPx = scrollAnchorOffsetPx,
            ),
            onScrollAnchorChanged = { anchor ->
                scrollAnchorRowId = anchor.rowId
                scrollAnchorOffsetPx = anchor.offsetPx
            },
        )
    }
    val showNewPostsPill = newPostsAvailable && pendingBookmark == null

    ApplyOverlayChrome(
        if (pendingBookmark != null) OverlayChromeState.HideTopBar else OverlayChromeState.None,
    )

    LaunchedEffect(seenBatcher) {
        while (true) {
            delay(3_000L)
            seenBatcher.flush()
        }
    }

    LaunchedEffect(rows) {
        val mediaRows = rows
            .take(16)
            .flatMap { threaded -> threaded.chain + threaded.row }
        onMediaRowsChanged(mediaRows)
    }

    DisposableEffect(lifecycleOwner, controller, seenBatcher) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_STOP) {
                controller.pauseVideo()
            }
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose {
            lifecycleOwner.lifecycle.removeObserver(observer)
            seenBatcher.flush()
            controller.release()
        }
    }

    Box(modifier = modifier.fillMaxSize()) {
            when {
                uiState is UiState.Loading && rows.isEmpty() -> Box(
                    modifier = Modifier.fillMaxSize(),
                    contentAlignment = Alignment.TopCenter,
                ) {
                    LinearProgressIndicator()
                }

                uiState is UiState.Empty && rows.isEmpty() -> Box(
                    modifier = Modifier.fillMaxSize(),
                    contentAlignment = Alignment.Center,
                ) {
                    Text(
                        text = stringResource(emptyMessageRes),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.iglooColors.onSurfaceMuted,
                    )
                }

                else -> AndroidView(
                    factory = { controller.rootView },
                    update = {
                        controller.update(
                            rows = rows,
                            channelHeader = channelHeader,
                            mediaModels = mediaModels,
                            colors = colors,
                            callbacks = currentCallbacks,
                            isRefreshing = isRefreshing,
                        )
                    },
                    modifier = Modifier
                        .fillMaxSize()
                        .background(
                            if (channelHeader != null) {
                                MaterialTheme.iglooColors.surface
                            } else {
                                MaterialTheme.iglooColors.background
                            },
                        ),
                )
            }

            AnimatedVisibility(
                visible = showNewPostsPill,
                enter = fadeIn(),
                exit = fadeOut(),
                modifier = Modifier
                    .align(Alignment.TopCenter)
                    .padding(top = 12.dp),
            ) {
                NativeNewPostsPill(
                    onClick = {
                        onNewPostsClick()
                        controller.scrollToTop()
                    },
                    posters = newPostPosters,
                )
            }

            AnimatedVisibility(
                visible = showScrollToTop && pendingBookmark == null,
                enter = fadeIn(),
                exit = fadeOut(),
                modifier = Modifier
                    .align(Alignment.BottomEnd)
                    .padding(16.dp),
            ) {
                SmallFloatingActionButton(
                    onClick = { controller.scrollToTop() },
                    containerColor = MaterialTheme.colorScheme.primaryContainer,
                    contentColor = MaterialTheme.colorScheme.onPrimaryContainer,
                ) {
                    Icon(
                        Icons.Default.KeyboardArrowUp,
                        contentDescription = stringResource(R.string.a11y_scroll_to_top),
                    )
                }
            }

            pendingBookmark?.let { target ->
                BookmarkSheet(
                    target = target,
                    categories = bookmarkCategories,
                    onConfirm = onConfirmBookmark,
                    onRemove = onRemoveBookmark,
                    onDismiss = onDismissBookmarkSheet,
                    onCreateCategory = onCreateCategory,
                )
            }

            pendingUnfollowChannelId?.let { channelId ->
                AlertDialog(
                    onDismissRequest = { pendingUnfollowChannelId = null },
                    title = { Text(stringResource(R.string.confirm_unfollow_account_title)) },
                    text = { Text(stringResource(R.string.confirm_unfollow_account_body)) },
                    confirmButton = {
                        TextButton(
                            onClick = {
                                pendingUnfollowChannelId = null
                                onFollowToggle(channelId, false)
                            },
                        ) {
                            Text(stringResource(R.string.action_unfollow))
                        }
                    },
                    dismissButton = {
                        TextButton(onClick = { pendingUnfollowChannelId = null }) {
                            Text(stringResource(R.string.action_cancel))
                        }
                    },
                )
            }

            pendingMuteAction?.let { action ->
                AlertDialog(
                    onDismissRequest = { pendingMuteAction = null },
                    title = { Text(stringResource(R.string.feed_mute_confirm_title, action.handle)) },
                    text = { Text(stringResource(R.string.feed_mute_confirm_body)) },
                    confirmButton = {
                        TextButton(
                            onClick = {
                                pendingMuteAction = null
                                onMuteToggle(action.channelId, true)
                            },
                        ) {
                            Text(stringResource(R.string.action_mute))
                        }
                    },
                    dismissButton = {
                        TextButton(onClick = { pendingMuteAction = null }) {
                            Text(stringResource(R.string.action_cancel))
                        }
                    },
                )
            }
    }
}

@Composable
private fun nativeFeedColors(): NativeFeedColors {
    val colors = MaterialTheme.iglooColors
    return NativeFeedColors(
        background = colors.background.toArgb(),
        surface = colors.surface.toArgb(),
        surfaceElevated = colors.surfaceElevated.toArgb(),
        surfaceHighest = colors.surfaceHighest.toArgb(),
        surfaceVariant = colors.surfaceVariant.toArgb(),
        onSurface = colors.onSurface.toArgb(),
        onSurfaceMuted = colors.onSurfaceMuted.toArgb(),
        onSurfaceFaint = colors.onSurfaceFaint.toArgb(),
        onSurfaceHandle = colors.onSurfaceHandle.toArgb(),
        border = colors.border.toArgb(),
        borderSubtle = colors.borderSubtle.toArgb(),
        primary = colors.primary.toArgb(),
        onPrimary = colors.onPrimary.toArgb(),
    )
}

internal data class NativeFeedColors(
    val background: Int,
    val surface: Int,
    val surfaceElevated: Int,
    val surfaceHighest: Int,
    val surfaceVariant: Int,
    val onSurface: Int,
    val onSurfaceMuted: Int,
    val onSurfaceFaint: Int,
    val onSurfaceHandle: Int,
    val border: Int,
    val borderSubtle: Int,
    val primary: Int,
    val onPrimary: Int,
)

@Composable
internal fun NativeNewPostsPill(
    posters: List<NewPostPoster>,
    onClick: () -> Unit,
) {
    val colors = MaterialTheme.iglooColors
    val shape = RoundedCornerShape(999.dp)
    Box(
        modifier = Modifier
            .shadow(
                elevation = 14.dp,
                shape = shape,
                ambientColor = ComposeColor.Black.copy(alpha = 0.35f),
                spotColor = ComposeColor.Black.copy(alpha = 0.35f),
            )
            .clip(shape)
            .background(colors.primary)
            .border(1.dp, colors.primary, shape)
            .clickable(onClick = onClick)
            .padding(start = 10.dp, top = 8.dp, end = 20.dp, bottom = 8.dp),
        contentAlignment = Alignment.Center,
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(
                imageVector = Icons.Default.ArrowUpward,
                contentDescription = null,
                tint = colors.onPrimary,
                modifier = Modifier.size(18.dp),
            )
            if (posters.isNotEmpty()) {
                NewPostsAvatarStack(
                    posters = posters.take(3),
                    modifier = Modifier.padding(start = 10.dp),
                )
                Text(
                    text = stringResource(R.string.feed_new_posts_posted),
                    color = colors.onPrimary,
                    fontWeight = FontWeight.SemiBold,
                    modifier = Modifier.padding(start = 10.dp),
                )
            } else {
                Text(
                    text = stringResource(R.string.feed_new_posts),
                    color = colors.onPrimary,
                    fontWeight = FontWeight.SemiBold,
                    modifier = Modifier.padding(start = 10.dp),
                )
            }
        }
    }
}

@Composable
private fun NewPostsAvatarStack(
    posters: List<NewPostPoster>,
    modifier: Modifier = Modifier,
) {
    val colors = MaterialTheme.iglooColors
    val avatarSize = 32.dp
    val overlap = 10.dp
    val step = avatarSize - overlap
    Box(
        modifier = modifier
            .width(avatarSize + step * (posters.size - 1).coerceAtLeast(0))
            .height(avatarSize),
    ) {
        posters.forEachIndexed { index, poster ->
            Box(
                modifier = Modifier
                    .offset(x = step * index)
                    .zIndex(index.toFloat())
                    .size(avatarSize)
                    .clip(CircleShape)
                    .background(colors.primary)
                    .border(2.dp, colors.primary, CircleShape),
            ) {
                Avatar(
                    channelId = poster.channelId,
                    size = avatarSize,
                    modifier = Modifier.size(avatarSize),
                    fadeWhenRemoteOffline = false,
                    showPendingBadge = false,
                )
            }
        }
    }
}

internal data class NativeFeedCallbacks(
    val mutedChannelIds: Set<String>,
    val onRefresh: () -> Unit,
    val onProfileOpen: (SocialPostModel) -> Unit,
    val onMentionClick: (handle: String) -> Unit,
    val onLikeToggle: (tweetId: String, newValue: Boolean) -> Unit,
    val onBookmarkToggle: (FeedRow) -> Unit,
    val onFollowToggle: (channelId: String, newValue: Boolean) -> Unit,
    val onStarToggle: (channelId: String, newValue: Boolean) -> Unit,
    val onMuteToggle: (channelId: String, newValue: Boolean) -> Unit,
    val onMediaOpen: (FeedRow, mediaIndex: Int, visibleMediaModel: FeedMediaGridModel) -> Unit,
    val onSeenReached: (tweetIds: List<String>) -> Unit,
    val onMediaRowsChanged: (List<FeedRow>) -> Unit,
    val onRowClick: (FeedRow) -> Unit,
    val onQuoteOpen: (tweetId: String) -> Unit,
    val onRequestUnfollowConfirmation: (channelId: String) -> Unit,
    val onRequestMuteConfirmation: (FeedMuteMenuAction) -> Unit,
    val onHeaderFollowToggle: (newValue: Boolean) -> Unit,
    val onHeaderStarToggle: (newValue: Boolean) -> Unit,
    val onHeaderRefresh: () -> Unit,
    val onHeaderOpenInPlatform: () -> Unit,
    val useEmbedFriendlyShareLinks: Boolean,
)

internal data class NativeTranslationPill(
    val sourceLangLabel: String,
    val active: Boolean,
    val enabled: Boolean,
)

internal data class NativeFeedMenuItem(
    val label: String,
    val danger: Boolean = false,
    val action: () -> Unit,
)

internal data class NativeFeedScrollAnchor(
    val rowId: String?,
    val offsetPx: Int,
)

internal fun nativeFeedScrollAnchor(
    items: List<NativeFeedAdapterItem>,
    layoutManager: LinearLayoutManager,
): NativeFeedScrollAnchor {
    val adapterIndex = layoutManager.findFirstVisibleItemPosition()
    val item = items.getOrNull(adapterIndex) as? NativeFeedAdapterItem.Post
    val view = layoutManager.findViewByPosition(adapterIndex)
    return NativeFeedScrollAnchor(
        rowId = item?.id,
        offsetPx = view?.top ?: 0,
    )
}

internal fun nativeFeedRestoreAdapterIndex(
    items: List<NativeFeedAdapterItem>,
    anchor: NativeFeedScrollAnchor,
): Int? {
    val rowId = anchor.rowId ?: return null
    return items.indexOfFirst { it.id == rowId }.takeIf { it >= 0 }
}

internal sealed class NativeFeedAdapterItem {
    abstract val id: String

    data class Header(
        val header: ChannelProfileHeaderUiModel,
    ) : NativeFeedAdapterItem() {
        override val id: String = "header:${header.channelId}"
    }

    data class Post(
        val threaded: ThreadedFeedRow,
        val post: SocialPostModel,
        val chainPosts: List<SocialPostModel> = emptyList(),
    ) : NativeFeedAdapterItem() {
        override val id: String = post.stableKey
    }
}
