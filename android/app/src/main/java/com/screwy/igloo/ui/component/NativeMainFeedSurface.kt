// SPDX-License-Identifier: AGPL-3.0-only
// RecyclerView list/header/video behavior is adapted from Flare's AGPL-3.0 timeline patterns.
package com.screwy.igloo.ui.component

import android.content.Context
import android.graphics.BitmapFactory
import android.graphics.Color
import android.graphics.Rect
import android.graphics.Typeface
import android.graphics.drawable.ColorDrawable
import android.graphics.drawable.GradientDrawable
import android.text.SpannableString
import android.text.Spanned
import android.text.TextPaint
import android.text.TextUtils
import android.text.method.LinkMovementMethod
import android.text.style.ClickableSpan
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.FrameLayout
import android.widget.ImageView
import android.widget.ImageButton
import android.widget.LinearLayout
import android.widget.PopupWindow
import android.widget.TextView
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.Row
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
import androidx.media3.common.MediaItem as Media3Item
import androidx.media3.common.Player
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.ui.AspectRatioFrameLayout
import androidx.media3.ui.PlayerView
import androidx.recyclerview.widget.DiffUtil
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.ListAdapter
import androidx.recyclerview.widget.RecyclerView
import androidx.swiperefreshlayout.widget.SwipeRefreshLayout
import coil3.ImageLoader
import coil3.target.ImageViewTarget
import com.screwy.igloo.R
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.data.entity.FeedRow
import com.screwy.igloo.data.entity.ThreadedFeedRow
import com.screwy.igloo.feed.FeedMediaCellModel
import com.screwy.igloo.feed.FeedMediaGridModel
import com.screwy.igloo.feed.SocialPostModel
import com.screwy.igloo.feed.buildSocialPostModel
import com.screwy.igloo.media.MediaResolvers
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.net.IglooHostProvider
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.net.auth.AuthTokenProvider
import com.screwy.igloo.player.buildIglooPlayer
import com.screwy.igloo.ui.UiState
import com.screwy.igloo.ui.nav.ApplyOverlayChrome
import com.screwy.igloo.ui.nav.OverlayChromeState
import com.screwy.igloo.ui.theme.iglooColors
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.koin.compose.koinInject
import java.util.Locale
import kotlin.math.abs
import androidx.compose.ui.graphics.Color as ComposeColor

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

private const val NativeFeedBodyCollapsedLines = 15
private const val NativeFeedQuoteCollapsedLines = 2

@Composable
internal fun NativeFeedSurface(
    rows: List<ThreadedFeedRow>,
    uiState: UiState<Unit>,
    isRefreshing: Boolean,
    newPostsAvailable: Boolean = false,
    newPostPosters: List<NewPostPoster> = emptyList(),
    pendingBookmark: BookmarkTarget?,
    bookmarkCategories: List<BookmarkCategoryDisplay>,
    mutedHandles: Set<String>,
    mediaModels: Map<String, FeedMediaGridModel> = emptyMap(),
    onRefresh: () -> Unit,
    onNewPostsClick: () -> Unit = onRefresh,
    onChannelClick: (channelId: String) -> Unit,
    onMentionClick: (handle: String) -> Unit,
    onLikeToggle: (tweetId: String, newValue: Boolean) -> Unit,
    onBookmarkToggle: (FeedRow) -> Unit,
    onFollowToggle: (channelId: String, newValue: Boolean) -> Unit,
    onStarToggle: (channelId: String, newValue: Boolean) -> Unit,
    onMuteToggle: (handle: String, newValue: Boolean) -> Unit,
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
    onWarmMediaRows: (List<FeedRow>) -> Unit = {},
    onRowClick: (FeedRow) -> Unit = {},
    onQuoteOpen: (tweetId: String) -> Unit = {},
    onProfileOpen: (SocialPostModel) -> Unit = { post -> onChannelClick(post.author.channelId) },
) {
    val context = LocalContext.current
    val imageLoader: ImageLoader = koinInject()
    val authTokens: AuthTokenProvider = koinInject()
    val iglooHostProvider: IglooHostProvider = koinInject()
    val baseUrlProvider: ServerBaseUrlProvider = koinInject()
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
            mutedHandles = mutedHandles,
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
            onWarmMediaRows = onWarmMediaRows,
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
            baseUrl = baseUrlProvider.baseUrl(),
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
        val warmRows = rows
            .take(16)
            .flatMap { threaded -> threaded.chain + threaded.row }
        if (warmRows.isNotEmpty()) onWarmMediaRows(warmRows)
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
                            baseUrl = baseUrlProvider.baseUrl(),
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
                                onMuteToggle(action.handle, true)
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

private data class NativeFeedColors(
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

private data class NativeFeedCallbacks(
    val mutedHandles: Set<String>,
    val onRefresh: () -> Unit,
    val onProfileOpen: (SocialPostModel) -> Unit,
    val onMentionClick: (handle: String) -> Unit,
    val onLikeToggle: (tweetId: String, newValue: Boolean) -> Unit,
    val onBookmarkToggle: (FeedRow) -> Unit,
    val onFollowToggle: (channelId: String, newValue: Boolean) -> Unit,
    val onStarToggle: (channelId: String, newValue: Boolean) -> Unit,
    val onMuteToggle: (handle: String, newValue: Boolean) -> Unit,
    val onMediaOpen: (FeedRow, mediaIndex: Int, visibleMediaModel: FeedMediaGridModel) -> Unit,
    val onSeenReached: (tweetIds: List<String>) -> Unit,
    val onWarmMediaRows: (List<FeedRow>) -> Unit,
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
    val sourceLangCode: String,
    val active: Boolean,
    val enabled: Boolean,
)

private data class NativeFeedMenuItem(
    val label: String,
    val danger: Boolean = false,
    val action: () -> Unit,
)

internal data class NativeFeedScrollAnchor(
    val rowId: String?,
    val offsetPx: Int,
)

private fun nativeFeedScrollAnchor(
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
    ) : NativeFeedAdapterItem() {
        override val id: String = post.stableKey
    }
}

private class NativeMainFeedController(
    private val context: Context,
    private val imageLoader: ImageLoader,
    private val authTokens: AuthTokenProvider,
    private val iglooHostProvider: IglooHostProvider,
    private val mediaResolvers: MediaResolvers,
    private var colors: NativeFeedColors,
    private var baseUrl: String,
    private var callbacks: NativeFeedCallbacks,
    seenBatcher: SeenBatcher,
    private val onScrollToTopVisibility: (Boolean) -> Unit,
    private val initialScrollAnchor: NativeFeedScrollAnchor,
    private val onScrollAnchorChanged: (NativeFeedScrollAnchor) -> Unit,
) {
    private val scopeJob = SupervisorJob()
    private val scope = CoroutineScope(scopeJob + Dispatchers.Main.immediate)
    private val layoutManager = LinearLayoutManager(context)
    private val seenTracker = PassedFeedRowsTracker(seenBatcher)
    private val inlineVideoManager = NativeInlineVideoManager(
        player = buildIglooPlayer(context, authTokens, iglooHostProvider),
    )
    private var pendingInitialScrollAnchor: NativeFeedScrollAnchor? =
        initialScrollAnchor.takeIf { it.rowId != null }
    private val adapter = NativeFeedAdapter(
        imageLoader = imageLoader,
        authTokens = authTokens,
        iglooHostProvider = iglooHostProvider,
        mediaResolvers = mediaResolvers,
        scope = scope,
        getColors = { colors },
        getBaseUrl = { baseUrl },
        getCallbacks = { callbacks },
        inlineVideoManager = inlineVideoManager,
    )
    val recyclerView: RecyclerView = RecyclerView(context).apply {
        setBackgroundColor(colors.background)
        this.layoutManager = this@NativeMainFeedController.layoutManager
        adapter = this@NativeMainFeedController.adapter
        itemAnimator = null
        setHasFixedSize(false)
        clipToPadding = false
        setPadding(0, dp(2), 0, dp(2))
        recycledViewPool.setMaxRecycledViews(NativeFeedAdapter.ViewTypePost, 12)
        addOnScrollListener(object : RecyclerView.OnScrollListener() {
            override fun onScrolled(recyclerView: RecyclerView, dx: Int, dy: Int) {
                onViewportChanged()
            }

            override fun onScrollStateChanged(recyclerView: RecyclerView, newState: Int) {
                onViewportChanged()
            }
        })
    }
    val rootView: SwipeRefreshLayout = SwipeRefreshLayout(context).apply {
        setColorSchemeColors(colors.primary)
        setProgressBackgroundColorSchemeColor(colors.surfaceElevated)
        addView(
            recyclerView,
            ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT,
            ),
        )
        setOnRefreshListener { callbacks.onRefresh() }
    }

    fun update(
        rows: List<ThreadedFeedRow>,
        channelHeader: ChannelProfileHeaderUiModel?,
        mediaModels: Map<String, FeedMediaGridModel>,
        colors: NativeFeedColors,
        baseUrl: String,
        callbacks: NativeFeedCallbacks,
        isRefreshing: Boolean,
    ) {
        this.colors = colors
        this.baseUrl = baseUrl
        this.callbacks = callbacks
        rootView.setColorSchemeColors(colors.primary)
        rootView.setProgressBackgroundColorSchemeColor(colors.surfaceElevated)
        rootView.isRefreshing = isRefreshing
        recyclerView.setBackgroundColor(if (channelHeader != null) colors.surface else colors.background)
        rootView.setBackgroundColor(if (channelHeader != null) colors.surface else colors.background)
        adapter.submitList(buildList {
            channelHeader?.let { add(NativeFeedAdapterItem.Header(it)) }
            rows.forEach { threaded ->
                add(
                    NativeFeedAdapterItem.Post(
                        threaded = threaded,
                        post = buildSocialPostModel(threaded.row, mediaModels),
                    )
                )
            }
        }) {
            recyclerView.post {
                restoreInitialScrollAnchorIfNeeded()
                onViewportChanged()
            }
        }
    }

    fun scrollToTop() {
        recyclerView.stopScroll()
        layoutManager.scrollToPositionWithOffset(0, 0)
        onViewportChanged()
    }

    fun pauseVideo() {
        inlineVideoManager.pause()
    }

    fun release() {
        scopeJob.cancel()
        inlineVideoManager.release()
    }

    private fun onViewportChanged() {
        onScrollAnchorChanged(nativeFeedScrollAnchor(adapter.currentList, layoutManager))
        val firstVisible = layoutManager.findFirstVisibleItemPosition().coerceAtLeast(0)
        val firstVisiblePost = firstVisiblePostIndex(firstVisible).coerceAtLeast(0)
        seenTracker.onViewportChanged(
            rowIds = adapter.postItems().map { it.id },
            firstVisibleIndex = firstVisiblePost,
        )
        onScrollToTopVisibility(firstVisiblePost > 5)
        warmNearVisibleRows(firstVisiblePost)
        inlineVideoManager.selectFrom(recyclerView)
    }

    private fun restoreInitialScrollAnchorIfNeeded() {
        val anchor = pendingInitialScrollAnchor ?: return
        val adapterIndex = nativeFeedRestoreAdapterIndex(adapter.currentList, anchor) ?: return
        pendingInitialScrollAnchor = null
        recyclerView.stopScroll()
        layoutManager.scrollToPositionWithOffset(adapterIndex, anchor.offsetPx)
    }

    private fun firstVisiblePostIndex(firstVisibleAdapterIndex: Int): Int {
        val lastVisible = layoutManager.findLastVisibleItemPosition().coerceAtLeast(firstVisibleAdapterIndex)
        for (index in firstVisibleAdapterIndex..lastVisible) {
            adapter.postIndexForAdapterIndex(index)?.let { return it }
        }
        return adapter.postIndexForAdapterIndex(firstVisibleAdapterIndex) ?: 0
    }

    private fun warmNearVisibleRows(firstVisiblePost: Int) {
        val lastVisibleAdapter = layoutManager.findLastVisibleItemPosition()
        val lastVisiblePost = (0..lastVisibleAdapter.coerceAtLeast(0))
            .mapNotNull { adapter.postIndexForAdapterIndex(it) }
            .lastOrNull()
            ?: firstVisiblePost
        val posts = adapter.postItems()
        val start = (firstVisiblePost - 2).coerceAtLeast(0)
        val end = (lastVisiblePost + 4).coerceAtMost(posts.lastIndex)
        if (end < start) return
        val rows = (start..end)
            .flatMap { index ->
                posts.getOrNull(index)
                    ?.threaded
                    ?.let { threaded -> threaded.chain + threaded.row }
                    .orEmpty()
            }
        if (rows.isNotEmpty()) callbacks.onWarmMediaRows(rows)
    }
}

private class NativeFeedAdapter(
    private val imageLoader: ImageLoader,
    private val authTokens: AuthTokenProvider,
    private val iglooHostProvider: IglooHostProvider,
    private val mediaResolvers: MediaResolvers,
    private val scope: CoroutineScope,
    private val getColors: () -> NativeFeedColors,
    private val getBaseUrl: () -> String,
    private val getCallbacks: () -> NativeFeedCallbacks,
    private val inlineVideoManager: NativeInlineVideoManager,
) : ListAdapter<NativeFeedAdapterItem, RecyclerView.ViewHolder>(Diff) {

    init {
        setHasStableIds(true)
    }

    override fun getItemViewType(position: Int): Int = when (getItem(position)) {
        is NativeFeedAdapterItem.Header -> ViewTypeHeader
        is NativeFeedAdapterItem.Post -> ViewTypePost
    }

    override fun getItemId(position: Int): Long =
        stableItemId(getItem(position).id)

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): RecyclerView.ViewHolder =
        when (viewType) {
            ViewTypeHeader -> NativeFeedChannelHeaderViewHolder(
                context = parent.context,
                imageLoader = imageLoader,
                authTokens = authTokens,
                iglooHostProvider = iglooHostProvider,
                mediaResolvers = mediaResolvers,
                scope = scope,
                getColors = getColors,
                getBaseUrl = getBaseUrl,
                getCallbacks = getCallbacks,
            )
            else -> NativeFeedViewHolder(
                context = parent.context,
                imageLoader = imageLoader,
                authTokens = authTokens,
                iglooHostProvider = iglooHostProvider,
                mediaResolvers = mediaResolvers,
                scope = scope,
                getColors = getColors,
                getBaseUrl = getBaseUrl,
                getCallbacks = getCallbacks,
                inlineVideoManager = inlineVideoManager,
            )
        }

    override fun onBindViewHolder(holder: RecyclerView.ViewHolder, position: Int) {
        when (val item = getItem(position)) {
            is NativeFeedAdapterItem.Header -> (holder as NativeFeedChannelHeaderViewHolder).bind(item.header)
            is NativeFeedAdapterItem.Post -> (holder as NativeFeedViewHolder).bind(item)
        }
    }

    override fun onViewRecycled(holder: RecyclerView.ViewHolder) {
        when (holder) {
            is NativeFeedViewHolder -> holder.recycle()
            is NativeFeedChannelHeaderViewHolder -> holder.recycle()
        }
        super.onViewRecycled(holder)
    }

    fun postItems(): List<NativeFeedAdapterItem.Post> =
        currentList.filterIsInstance<NativeFeedAdapterItem.Post>()

    fun postIndexForAdapterIndex(adapterIndex: Int): Int? {
        if (adapterIndex !in currentList.indices) return null
        if (currentList[adapterIndex] !is NativeFeedAdapterItem.Post) return null
        return currentList.take(adapterIndex + 1).count { it is NativeFeedAdapterItem.Post } - 1
    }

    companion object {
        const val ViewTypeHeader = 0
        const val ViewTypePost = 1

        private val Diff = object : DiffUtil.ItemCallback<NativeFeedAdapterItem>() {
            override fun areItemsTheSame(
                oldItem: NativeFeedAdapterItem,
                newItem: NativeFeedAdapterItem,
            ): Boolean = oldItem.id == newItem.id

            override fun areContentsTheSame(
                oldItem: NativeFeedAdapterItem,
                newItem: NativeFeedAdapterItem,
            ): Boolean = oldItem == newItem
        }
    }
}

private class NativeFeedChannelHeaderViewHolder(
    context: Context,
    private val imageLoader: ImageLoader,
    private val authTokens: AuthTokenProvider,
    private val iglooHostProvider: IglooHostProvider,
    private val mediaResolvers: MediaResolvers,
    private val scope: CoroutineScope,
    private val getColors: () -> NativeFeedColors,
    private val getBaseUrl: () -> String,
    private val getCallbacks: () -> NativeFeedCallbacks,
) : RecyclerView.ViewHolder(NativeFeedChannelHeaderViews(context).root) {
    private val views: NativeFeedChannelHeaderViews = itemView.tag as NativeFeedChannelHeaderViews
    private var avatarJob: Job? = null
    private var bannerJob: Job? = null

    fun bind(header: ChannelProfileHeaderUiModel) {
        val colors = getColors()
        val callbacks = getCallbacks()
        views.applyColors(colors)

        views.bannerFrame.visibility = if (header.hasBannerSlot) {
            View.VISIBLE
        } else {
            View.GONE
        }
        if (views.bannerFrame.visibility == View.VISIBLE) {
            loadBanner(header)
            views.bannerAvatar.visibility = View.VISIBLE
            views.inlineAvatar.visibility = View.GONE
            loadAvatar(views.bannerAvatar, header)
        } else {
            bannerJob?.cancel()
            views.banner.setImageDrawable(null)
            views.bannerAvatar.visibility = View.GONE
            views.inlineAvatar.visibility = View.VISIBLE
            loadAvatar(views.inlineAvatar, header)
        }

        views.name.text = header.displayName
        views.verified.visibility = if (header.isVerified) View.VISIBLE else View.GONE
        views.handle.text = header.handle.takeIf { it.isNotBlank() }?.let { "@$it" }.orEmpty()
        views.handle.visibility = if (views.handle.text.isNullOrBlank()) View.GONE else View.VISIBLE
        bindHeaderBio(header, colors, callbacks)
        val linkColor = colors.channelProfileHeaderLinkColor(header.linkColorRole)
        views.website.text = header.website
        views.website.visibility = if (header.website.isBlank()) View.GONE else View.VISIBLE
        views.website.setTextColor(linkColor)
        views.website.setOnClickListener {
            openExternalUrl(views.root.context, header.website)
        }
        views.stats.text = header.stats.joinToString("    ")
        views.stats.visibility = if (views.stats.text.isNullOrBlank()) View.GONE else View.VISIBLE

        views.follow.text = views.root.context.getString(
            if (header.isFollowed) R.string.action_following else R.string.action_follow
        )
        views.follow.setTextColor(if (header.isFollowed) colors.onSurface else colors.onPrimary)
        views.follow.background = if (header.isFollowed) {
            roundedStroke(colors.surface, colors.primary, dp(1), dp(999))
        } else {
            roundedFill(colors.primary, dp(999))
        }
        views.follow.setOnClickListener { callbacks.onHeaderFollowToggle(!header.isFollowed) }

        views.star.setImageResource(
            if (header.isStarred) R.drawable.ic_channel_star_24 else R.drawable.ic_channel_star_border_24,
        )
        views.star.setColorFilter(if (header.isStarred) colors.primary else colors.onSurfaceMuted)
        views.star.contentDescription = views.root.context.getString(
            if (header.isStarred) R.string.action_unstar_channel else R.string.action_star_channel,
        )
        views.star.setOnClickListener { callbacks.onHeaderStarToggle(!header.isStarred) }

        views.menu.setImageResource(R.drawable.ic_channel_more_horiz_24)
        views.menu.setColorFilter(colors.onSurfaceMuted)
        views.menu.contentDescription = views.root.context.getString(R.string.action_more)
        views.menu.setOnClickListener { showHeaderMenu(header) }
    }

    private fun bindHeaderBio(
        header: ChannelProfileHeaderUiModel,
        colors: NativeFeedColors,
        callbacks: NativeFeedCallbacks,
    ) {
        val rawBio = when {
            header.isProtected -> header.protectedText
            else -> header.bio
        }
        views.bio.visibility = if (rawBio.isBlank()) View.GONE else View.VISIBLE
        if (rawBio.isBlank()) {
            views.bio.text = ""
            views.bio.movementMethod = null
            return
        }

        views.bio.setTextColor(if (header.isProtected) colors.onSurfaceMuted else colors.onSurface)
        if (header.isProtected) {
            views.bio.text = rawBio
            views.bio.movementMethod = null
            return
        }

        val linkColor = colors.channelProfileHeaderLinkColor(header.linkColorRole)
        views.bio.setLinkTextColor(linkColor)
        views.bio.highlightColor = Color.TRANSPARENT
        views.bio.movementMethod = LinkMovementMethod.getInstance()
        views.bio.text = clickableText(
            raw = rawBio,
            linkColor = linkColor,
            onMentionClick = callbacks.onMentionClick,
            onUrlClick = { url -> openExternalUrl(views.bio.context, url) },
        )
    }

    fun recycle() {
        avatarJob?.cancel()
        bannerJob?.cancel()
        views.banner.setImageDrawable(null)
        views.bannerAvatar.setImageDrawable(null)
        views.inlineAvatar.setImageDrawable(null)
    }

    private fun showHeaderMenu(header: ChannelProfileHeaderUiModel) {
        val callbacks = getCallbacks()
        val context = views.root.context
        val items = buildList {
            add(
                NativeFeedMenuItem(
                    label = context.getString(
                        if (header.isFollowed) R.string.action_unfollow_account else R.string.action_follow_account,
                    ),
                    action = { callbacks.onHeaderFollowToggle(!header.isFollowed) },
                ),
            )
            add(
                NativeFeedMenuItem(
                    label = context.getString(R.string.action_refresh_channel),
                    action = callbacks.onHeaderRefresh,
                ),
            )
            if (!header.platformUrl.isNullOrBlank()) {
                add(
                    NativeFeedMenuItem(
                        label = context.getString(R.string.action_open_in, header.openLabel),
                        action = callbacks.onHeaderOpenInPlatform,
                    ),
                )
            }
        }
        showNativeFeedPopup(
            anchor = views.menu,
            colors = getColors(),
            items = items,
        )
    }

    private fun loadAvatar(imageView: ImageView, header: ChannelProfileHeaderUiModel) {
        val requestKey = "channel-avatar:${header.channelId}:${header.initialAvatarUri}"
        imageView.tag = requestKey
        avatarJob?.cancel()
        val colors = getColors()
        val isBannerAvatar = imageView == views.bannerAvatar
        imageView.setPadding(0, 0, 0, 0)
        imageView.elevation = if (isBannerAvatar) dp(6).toFloat() else 0f
        imageView.translationZ = if (isBannerAvatar) dp(6).toFloat() else 0f
        imageView.background = roundedFill(colors.surfaceVariant, dp(999))
        avatarJob = scope.launch {
            val resolved = withContext(Dispatchers.IO) {
                mediaResolvers.avatarForChannel(header.channelId)
            }
            if (imageView.tag != requestKey) return@launch
            loadImage(
                imageView = imageView,
                uri = resolved.takeUnless { it is MediaUri.Missing } ?: header.initialAvatarUri,
                widthPx = imageView.layoutParams?.width ?: dp(92),
                heightPx = imageView.layoutParams?.height ?: dp(92),
            )
        }
    }

    private fun loadBanner(header: ChannelProfileHeaderUiModel) {
        val requestKey = "channel-banner:${header.channelId}:${header.initialBannerUri}:${header.fallbackBannerUrl}"
        views.banner.tag = requestKey
        bannerJob?.cancel()
        views.banner.setImageDrawable(null)
        bannerJob = scope.launch {
            val resolved = withContext(Dispatchers.IO) {
                mediaResolvers.bannerForChannel(header.channelId)
            }
            if (views.banner.tag != requestKey) return@launch
            val fallback = header.initialBannerUri.takeUnless { it is MediaUri.Missing }
                ?: header.fallbackBannerUrl?.let { remoteUriFor(it, getBaseUrl()) }
                ?: MediaUri.Missing
            loadImage(
                imageView = views.banner,
                uri = resolved.takeUnless { it is MediaUri.Missing } ?: fallback,
                widthPx = views.banner.resources.displayMetrics.widthPixels,
                heightPx = dp(NativeChannelHeaderBannerHeightDp),
            )
        }
    }

    private fun loadImage(imageView: ImageView, uri: MediaUri, widthPx: Int, heightPx: Int) {
        val request = buildMediaImageRequest(
            context = imageView.context,
            uri = uri,
            bearerToken = authTokens.bearerTokenSync(),
            iglooHost = iglooHostProvider.hostSync(),
            widthPx = widthPx,
            heightPx = heightPx,
        )?.newBuilder(imageView.context)
            ?.target(ImageViewTarget(imageView))
            ?.build()
        if (request != null) imageLoader.enqueue(request)
    }
}

private class NativeFeedViewHolder(
    context: Context,
    private val imageLoader: ImageLoader,
    private val authTokens: AuthTokenProvider,
    private val iglooHostProvider: IglooHostProvider,
    private val mediaResolvers: MediaResolvers,
    private val scope: CoroutineScope,
    private val getColors: () -> NativeFeedColors,
    private val getBaseUrl: () -> String,
    private val getCallbacks: () -> NativeFeedCallbacks,
    private val inlineVideoManager: NativeInlineVideoManager,
) : RecyclerView.ViewHolder(NativeFeedCardViews(context).root) {
    private val views: NativeFeedCardViews = itemView.tag as NativeFeedCardViews
    private val videoSlots = mutableListOf<NativeVideoSlot>()
    private val avatarJobs = mutableMapOf<ImageView, Job>()
    private var boundRow: NativeFeedAdapterItem.Post? = null
    private var showTranslatedBody = true
    private var bodyExpanded = false

    fun bind(adapterRow: NativeFeedAdapterItem.Post) {
        val previousId = boundRow?.id
        if (previousId != adapterRow.id) {
            showTranslatedBody = true
            bodyExpanded = false
        }
        boundRow = adapterRow
        videoSlots.forEach { inlineVideoManager.detachSlot(it.key) }
        videoSlots.clear()

        val colors = getColors()
        val callbacks = getCallbacks()
        val post = adapterRow.post
        val row = adapterRow.threaded.row
        val item = row.item
        val shareUrl = feedShareUrl(item).trim()
        val bodyTranslation = item.bodyTranslation?.takeIf { it.isNotBlank() }
        val quoteTranslation = item.quoteTranslation?.takeIf { it.isNotBlank() }
        val hasTranslation = bodyTranslation != null || quoteTranslation != null
        val bodyText = if (showTranslatedBody && bodyTranslation != null) {
            bodyTranslation
        } else {
            item.bodyText.orEmpty()
        }
        val translationPill = nativeTranslationPillFor(
            item = item,
            active = showTranslatedBody && hasTranslation,
            enabled = hasTranslation,
        )

        views.applyColors(colors)
        views.root.setOnClickListener { callbacks.onRowClick(row) }
        views.root.setOnLongClickListener {
            showMenu(row, post)
            true
        }

        bindRetweeter(item, callbacks, colors)
        bindHeader(
            header = views.header,
            channelId = item.channelId.orEmpty(),
            explicitAvatarUrl = item.authorAvatarUrl,
            displayName = post.author.displayName,
            handle = post.author.handle,
            timestamp = localizedRelativeTime(views.root.context, item.publishedAt),
            showFollow = item.channelId?.isNotBlank() == true,
            isFollowed = row.channelIsFollowed == 1,
            colors = colors,
            translation = translationPill,
            onClick = {
                if (post.author.channelId.isNotBlank()) {
                    callbacks.onProfileOpen(post)
                } else if (item.authorHandle.isNotBlank()) {
                    callbacks.onMentionClick(item.authorHandle)
                }
            },
            onFollowClick = {
                val channelId = item.channelId?.takeIf { it.isNotBlank() } ?: return@bindHeader
                if (row.channelIsFollowed == 1) {
                    callbacks.onRequestUnfollowConfirmation(channelId)
                } else {
                    callbacks.onFollowToggle(channelId, true)
                }
            },
            onTranslationClick = {
                if (hasTranslation) {
                    showTranslatedBody = !showTranslatedBody
                    bind(adapterRow)
                }
            },
        )

        bindReply(item, callbacks, colors)
        bindBody(
            textView = views.body,
            moreView = views.showMore,
            text = bodyText,
            colors = colors,
            callbacks = callbacks,
        )
        bindMediaGrid(
            container = views.media,
            ownerKeyPrefix = item.tweetId,
            row = row,
            grid = post.media.grid,
            mediaIndexOffset = 0,
            colors = colors,
            callbacks = callbacks,
        )
        bindQuote(row, post, quoteTranslation, colors, callbacks)
        bindActions(row, post, shareUrl, colors, callbacks)
    }

    fun recycle() {
        videoSlots.forEach { inlineVideoManager.detachSlot(it.key) }
        videoSlots.clear()
        cancelAvatarJobs()
        views.media.removeAllViews()
        views.quoteMedia.removeAllViews()
        boundRow = null
    }

    fun videoSlotsForSelection(): List<NativeVideoSlot> = videoSlots

    private fun bindRetweeter(
        item: FeedItemEntity,
        callbacks: NativeFeedCallbacks,
        colors: NativeFeedColors,
    ) {
        if (!item.isRetweet) {
            views.retweeter.visibility = View.GONE
            views.retweeter.setCompoundDrawablesWithIntrinsicBounds(null, null, null, null)
            return
        }
        val context = views.root.context
        val handle = item.retweetedByHandle ?: item.sourceHandle ?: ""
        val label = item.retweetedByDisplayName?.takeIf { it.isNotBlank() }
            ?: normalizeHandle(handle).takeIf { it.isNotBlank() }
        val icon = context.getDrawable(R.drawable.ic_feed_repost_24)?.mutate()?.apply {
            setTint(colors.onSurfaceMuted)
            setBounds(0, 0, dp(16), dp(16))
        }
        views.retweeter.visibility = View.VISIBLE
        views.retweeter.text = if (label.isNullOrBlank()) {
            context.getString(R.string.feed_reposted_someone)
        } else {
            context.getString(R.string.feed_reposted_single, label)
        }
        views.retweeter.setCompoundDrawables(icon, null, null, null)
        views.retweeter.compoundDrawablePadding = dp(4)
        views.retweeter.setTextColor(colors.onSurfaceMuted)
        views.retweeter.setOnClickListener {
            handle.takeIf { it.isNotBlank() }?.let(callbacks.onMentionClick)
        }
    }

    private fun bindReply(
        item: FeedItemEntity,
        callbacks: NativeFeedCallbacks,
        colors: NativeFeedColors,
    ) {
        val replyHandle = normalizeHandle(item.replyToHandle)
        if (replyHandle.isBlank()) {
            views.reply.visibility = View.GONE
            return
        }
        views.reply.visibility = View.VISIBLE
        views.reply.text = views.root.context.getString(R.string.feed_replying_to, replyHandle)
        views.reply.setTextColor(colors.primary)
        views.reply.setOnClickListener { boundRow?.threaded?.row?.let(callbacks.onRowClick) }
    }

    private fun bindBody(
        textView: TextView,
        moreView: TextView,
        text: String,
        colors: NativeFeedColors,
        callbacks: NativeFeedCallbacks,
    ) {
        if (text.isBlank()) {
            textView.visibility = View.GONE
            moreView.visibility = View.GONE
            return
        }
        textView.visibility = View.VISIBLE
        bindMentionText(textView, text, colors, callbacks)
        val shouldClamp = nativeShouldClampBody(text)
        textView.maxLines = if (bodyExpanded || !shouldClamp) Int.MAX_VALUE else NativeFeedBodyCollapsedLines
        textView.ellipsize = if (bodyExpanded || !shouldClamp) null else TextUtils.TruncateAt.END
        moreView.visibility = if (shouldClamp) View.VISIBLE else View.GONE
        moreView.text = textView.context.getString(
            if (bodyExpanded) R.string.action_show_less else R.string.action_read_more,
        )
        moreView.setTextColor(colors.primary)
        moreView.setOnClickListener {
            bodyExpanded = !bodyExpanded
            textView.maxLines = if (bodyExpanded) Int.MAX_VALUE else NativeFeedBodyCollapsedLines
            textView.ellipsize = if (bodyExpanded) null else TextUtils.TruncateAt.END
            moreView.text = textView.context.getString(
                if (bodyExpanded) R.string.action_show_less else R.string.action_read_more,
            )
        }
    }

    private fun bindQuote(
        row: FeedRow,
        post: SocialPostModel,
        quoteTranslation: String?,
        colors: NativeFeedColors,
        callbacks: NativeFeedCallbacks,
    ) {
        val item = row.item
        val quoteId = item.quoteTweetId?.trim().orEmpty()
        if (quoteId.isBlank()) {
            views.quote.visibility = View.GONE
            views.quote.setOnClickListener(null)
            views.quoteBody.setOnClickListener(null)
            return
        }
        views.quote.visibility = View.VISIBLE
        views.quote.background = roundedStroke(colors.surfaceElevated, colors.borderSubtle, dp(1), dp(8))
        views.quote.setOnClickListener { callbacks.onQuoteOpen(quoteId) }
        val quoteHandle = normalizeHandle(item.quoteAuthorHandle)
            .ifBlank { displayNameLooksLikeHandle(item.quoteAuthorDisplayName) }
        val quoteDisplay = displayLabel(
            primary = item.quoteAuthorDisplayName,
            fallback = null,
            handle = quoteHandle,
        )
        val quoteChannelId = row.quoteChannelId?.takeIf { it.isNotBlank() } ?: "twitter_${quoteHandle.lowercase()}"
        val quoteTimestamp = item.quotePublishedAt
            .takeIf { it > 0L }
            ?.let { localizedRelativeTime(views.root.context, it) }
            .orEmpty()
        val followTarget = feedQuoteFollowTarget(row)

        bindHeader(
            header = views.quoteHeader,
            channelId = quoteChannelId,
            explicitAvatarUrl = item.quoteAuthorAvatarUrl,
            displayName = quoteDisplay,
            handle = quoteHandle,
            timestamp = quoteTimestamp,
            showFollow = followTarget != null,
            isFollowed = false,
            colors = colors,
            onClick = { if (quoteHandle.isNotBlank()) callbacks.onMentionClick(quoteHandle) },
            onFollowClick = { followTarget?.let { callbacks.onFollowToggle(it.channelId, true) } },
        )

        val quoteBody = if (showTranslatedBody && quoteTranslation != null) {
            quoteTranslation
        } else {
            item.quoteBodyText.orEmpty()
        }
        if (quoteBody.isBlank()) {
            views.quoteBody.visibility = View.GONE
            views.quoteBody.setOnClickListener(null)
        } else {
            views.quoteBody.visibility = View.VISIBLE
            views.quoteBody.setTextColor(colors.onSurface)
            views.quoteBody.movementMethod = null
            views.quoteBody.text = quoteBody
            views.quoteBody.maxLines = NativeFeedQuoteCollapsedLines
            views.quoteBody.ellipsize = TextUtils.TruncateAt.END
            views.quoteBody.setOnClickListener { callbacks.onQuoteOpen(quoteId) }
        }
        val parentCount = post.media.grid.mediaCount
        val quoteMedia = post.quoteMedia?.grid
        if (quoteMedia == null || quoteMedia.mediaCount == 0) {
            views.quoteMedia.visibility = View.GONE
            views.quoteMedia.removeAllViews()
        } else {
            views.quoteMedia.visibility = View.VISIBLE
            bindMediaGrid(
                container = views.quoteMedia,
                ownerKeyPrefix = item.quoteTweetId.orEmpty(),
                row = row,
                grid = quoteMedia,
                mediaIndexOffset = parentCount,
                colors = colors,
                callbacks = callbacks,
            )
        }
    }

    private fun bindActions(
        row: FeedRow,
        post: SocialPostModel,
        shareUrl: String,
        colors: NativeFeedColors,
        callbacks: NativeFeedCallbacks,
    ) {
        val canOpenExternal = shareUrl.isNotBlank()
        views.actions.removeAllViews()
        NativeFeedPrimaryActions.forEach { action ->
            val button = actionIconButton(views.root.context, colors)
            button.contentDescription = action.contentDescription(views.root.context, post)
            button.isEnabled = when (action) {
                NativeFeedPrimaryAction.Share -> canOpenExternal
                NativeFeedPrimaryAction.Like,
                NativeFeedPrimaryAction.Bookmark -> true
            }
            val selected = when (action) {
                NativeFeedPrimaryAction.Like -> post.actions.isLiked
                NativeFeedPrimaryAction.Bookmark -> post.actions.isBookmarked
                else -> false
            }
            button.setImageResource(action.iconRes(selected))
            button.setColorFilter(
                when {
                    !button.isEnabled -> colors.onSurfaceFaint
                    selected -> colors.primary
                    else -> colors.onSurfaceMuted
                }
            )
            button.setOnClickListener {
                when (action) {
                    NativeFeedPrimaryAction.Share -> sharePlainText(
                        views.root.context,
                        shareUrl,
                        callbacks.useEmbedFriendlyShareLinks,
                    )
                    NativeFeedPrimaryAction.Like -> callbacks.onLikeToggle(row.item.tweetId, row.isLiked == 0)
                    NativeFeedPrimaryAction.Bookmark -> callbacks.onBookmarkToggle(row)
                }
            }
            views.actions.addView(button)
        }
        views.menu.setImageResource(R.drawable.ic_feed_more_vert_24)
        views.menu.setColorFilter(colors.onSurfaceMuted)
        views.menu.contentDescription = views.root.context.getString(R.string.action_more)
        views.menu.setOnClickListener { showMenu(row, post, shareUrl) }
    }

    private fun showMenu(row: FeedRow, post: SocialPostModel, shareUrl: String = feedShareUrl(row.item).trim()) {
        val callbacks = getCallbacks()
        val context = views.root.context
        val items = mutableListOf<NativeFeedMenuItem>()
        val channelId = row.item.channelId?.trim().orEmpty()
        if (shareUrl.isNotBlank()) {
            items += NativeFeedMenuItem(
                label = context.getString(R.string.action_open_on_x),
                action = {
                    openExternalUrl(context, shareUrl)
                },
            )
        }
        if (channelId.isNotBlank()) {
            items += NativeFeedMenuItem(
                label = context.getString(
                    if (row.channelIsStarred == 1) R.string.action_unstar_channel else R.string.action_star_channel,
                ),
                action = {
                    callbacks.onStarToggle(channelId, row.channelIsStarred == 0)
                },
            )
        }
        feedMuteMenuActions(row, callbacks.mutedHandles).forEach { action ->
            items += NativeFeedMenuItem(
                label = context.getString(
                    if (action.isMuted) R.string.action_unmute_account_handle else R.string.action_mute_account_handle,
                    action.handle,
                ),
                action = {
                    if (action.isMuted) {
                        callbacks.onMuteToggle(action.handle, false)
                    } else {
                        callbacks.onRequestMuteConfirmation(action)
                    }
                },
            )
        }
        if (items.isEmpty()) {
            items += NativeFeedMenuItem(
                label = context.getString(R.string.feed_open_thread),
                action = {
                    callbacks.onRowClick(post.row)
                },
            )
        }
        showNativeFeedPopup(views.menu, getColors(), items)
    }

    private fun bindHeader(
        header: NativeIdentityHeaderViews,
        channelId: String,
        explicitAvatarUrl: String?,
        displayName: String,
        handle: String,
        timestamp: String,
        showFollow: Boolean,
        isFollowed: Boolean,
        colors: NativeFeedColors,
        translation: NativeTranslationPill? = null,
        onClick: () -> Unit,
        onFollowClick: () -> Unit,
        onTranslationClick: () -> Unit = {},
    ) {
        header.root.setOnClickListener { onClick() }
        header.avatar.setOnClickListener { onClick() }
        loadAvatar(header.avatar, channelId, explicitAvatarUrl)
        header.name.text = displayName.ifBlank { handle }
        header.name.setTextColor(colors.onSurface)
        val normalizedHandle = normalizeHandle(handle)
        header.meta.text = when {
            normalizedHandle.isNotBlank() && timestamp.isNotBlank() -> "@$normalizedHandle · $timestamp"
            normalizedHandle.isNotBlank() -> "@$normalizedHandle"
            else -> timestamp
        }
        header.meta.setTextColor(colors.onSurfaceHandle)
        bindTranslationPill(header, translation, colors, onTranslationClick)
        header.follow.visibility = if (showFollow) View.VISIBLE else View.GONE
        header.follow.text = views.root.context.getString(
            if (isFollowed) R.string.action_following else R.string.action_follow,
        )
        header.follow.setTextColor(if (isFollowed) colors.onSurface else colors.onPrimary)
        header.follow.background = roundedFill(if (isFollowed) colors.surfaceHighest else colors.primary, dp(999))
        header.follow.setOnClickListener { onFollowClick() }
    }

    private fun bindTranslationPill(
        header: NativeIdentityHeaderViews,
        translation: NativeTranslationPill?,
        colors: NativeFeedColors,
        onTranslationClick: () -> Unit,
    ) {
        if (translation == null) {
            header.translate.visibility = View.GONE
            header.translate.setOnClickListener(null)
            return
        }
        header.translate.visibility = View.VISIBLE
        header.translate.isEnabled = translation.enabled
        header.translate.alpha = if (translation.enabled) 1f else 0.65f
        header.translate.contentDescription = header.root.context.getString(R.string.settings_auto_translate)
        header.translateIcon.setColorFilter(if (translation.active) colors.primary else colors.onSurfaceMuted)
        header.translateLabel.text = if (translation.active) translation.sourceLangCode else ""
        header.translateLabel.visibility = if (translation.active && translation.sourceLangCode.isNotBlank()) {
            View.VISIBLE
        } else {
            View.GONE
        }
        header.translateLabel.setTextColor(colors.primary)
        header.translate.setOnClickListener {
            if (translation.enabled) onTranslationClick()
        }
    }

    private fun bindMentionText(
        textView: TextView,
        text: String,
        colors: NativeFeedColors,
        callbacks: NativeFeedCallbacks,
    ) {
        textView.setTextColor(colors.onSurface)
        textView.setLinkTextColor(colors.primary)
        textView.highlightColor = Color.TRANSPARENT
        textView.movementMethod = LinkMovementMethod.getInstance()
        textView.text = clickableText(
            raw = text,
            linkColor = colors.primary,
            onMentionClick = callbacks.onMentionClick,
            onUrlClick = { url -> openExternalUrl(textView.context, url) },
        )
    }

    private fun bindMediaGrid(
        container: LinearLayout,
        ownerKeyPrefix: String,
        row: FeedRow,
        grid: FeedMediaGridModel,
        mediaIndexOffset: Int,
        colors: NativeFeedColors,
        callbacks: NativeFeedCallbacks,
    ) {
        container.removeAllViews()
        if (grid.mediaCount == 0) {
            container.visibility = View.GONE
            return
        }
        container.visibility = View.VISIBLE
        if (grid.mediaCount == 1) {
            val cell = grid.cells.first()
            val aspect = nativeStableSingleMediaAspectRatio(cell)
            val dimensions = nativeSingleMediaDimensions(container.context, aspect)
            val frame = FrameLayout(container.context).apply {
                setBackgroundColor(Color.TRANSPARENT)
                clipToOutline = true
                background = roundedFill(Color.TRANSPARENT, dp(8))
            }
            frame.layoutParams = LinearLayout.LayoutParams(
                dimensions.widthPx,
                dimensions.heightPx,
            ).apply { gravity = Gravity.START }
            bindMediaCell(
                parent = frame,
                ownerKey = "$ownerKeyPrefix:0",
                row = row,
                grid = grid,
                cell = cell,
                mediaIndex = mediaIndexOffset,
                isSingle = true,
                colors = colors,
                callbacks = callbacks,
            )
            container.addView(frame)
        } else {
            val context = container.context
            val gridWidth = nativeMediaGridWidthPx(context)
            val gap = dp(2)
            val displayCells = grid.cells.take(4)
            fun frameFor(index: Int, cell: FeedMediaCellModel): FrameLayout {
                val frame = FrameLayout(container.context).apply {
                    setBackgroundColor(colors.surface)
                    clipToOutline = true
                    background = roundedFill(colors.surface, dp(8))
                }
                bindMediaCell(
                    parent = frame,
                    ownerKey = "$ownerKeyPrefix:$index",
                    row = row,
                    grid = grid,
                    cell = cell,
                    mediaIndex = mediaIndexOffset + index,
                    colors = colors,
                    callbacks = callbacks,
                )
                if (index == 3 && grid.mediaCount > 4) {
                    frame.addView(
                        TextView(context).apply {
                            text = "+${grid.mediaCount - 4}"
                            textSize = 24f
                            setTypeface(typeface, Typeface.BOLD)
                            setTextColor(Color.WHITE)
                            gravity = Gravity.CENTER
                            background = GradientDrawable().apply {
                                setColor(Color.argb(145, 0, 0, 0))
                            }
                        },
                        FrameLayout.LayoutParams(
                            ViewGroup.LayoutParams.MATCH_PARENT,
                            ViewGroup.LayoutParams.MATCH_PARENT,
                        ),
                    )
                }
                return frame
            }

            when (displayCells.size) {
                2 -> {
                    val cellSize = (gridWidth - gap) / 2
                    val rowLayout = LinearLayout(context).apply {
                        orientation = LinearLayout.HORIZONTAL
                    }
                    rowLayout.layoutParams = LinearLayout.LayoutParams(gridWidth, cellSize)
                    displayCells.forEachIndexed { index, cell ->
                        rowLayout.addView(
                            frameFor(index, cell),
                            LinearLayout.LayoutParams(cellSize, ViewGroup.LayoutParams.MATCH_PARENT).apply {
                                if (index > 0) marginStart = gap
                            },
                        )
                    }
                    container.addView(rowLayout)
                }
                3 -> {
                    val gridHeight = (gridWidth / 1.6f).toInt()
                    val columnWidth = (gridWidth - gap) / 2
                    val rightCellHeight = (gridHeight - gap) / 2
                    val rowLayout = LinearLayout(context).apply {
                        orientation = LinearLayout.HORIZONTAL
                    }
                    rowLayout.layoutParams = LinearLayout.LayoutParams(gridWidth, gridHeight)
                    rowLayout.addView(
                        frameFor(0, displayCells[0]),
                        LinearLayout.LayoutParams(columnWidth, ViewGroup.LayoutParams.MATCH_PARENT),
                    )
                    val rightColumn = LinearLayout(context).apply {
                        orientation = LinearLayout.VERTICAL
                    }
                    rowLayout.addView(
                        rightColumn,
                        LinearLayout.LayoutParams(columnWidth, ViewGroup.LayoutParams.MATCH_PARENT).apply {
                            marginStart = gap
                        },
                    )
                    rightColumn.addView(
                        frameFor(1, displayCells[1]),
                        LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, rightCellHeight),
                    )
                    rightColumn.addView(
                        frameFor(2, displayCells[2]),
                        LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, rightCellHeight).apply {
                            topMargin = gap
                        },
                    )
                    container.addView(rowLayout)
                }
                else -> {
                    val cellSize = (gridWidth - gap) / 2
                    val gridLayout = LinearLayout(context).apply {
                        orientation = LinearLayout.VERTICAL
                    }
                    gridLayout.layoutParams = LinearLayout.LayoutParams(gridWidth, gridWidth)
                    repeat(2) { rowIndex ->
                        val rowLayout = LinearLayout(context).apply {
                            orientation = LinearLayout.HORIZONTAL
                        }
                        gridLayout.addView(
                            rowLayout,
                            LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, cellSize).apply {
                                if (rowIndex > 0) topMargin = gap
                            },
                        )
                        repeat(2) { columnIndex ->
                            val index = rowIndex * 2 + columnIndex
                            rowLayout.addView(
                                frameFor(index, displayCells[index]),
                                LinearLayout.LayoutParams(cellSize, ViewGroup.LayoutParams.MATCH_PARENT).apply {
                                    if (columnIndex > 0) marginStart = gap
                                },
                            )
                        }
                    }
                    container.addView(gridLayout)
                }
            }
        }
    }

    private fun bindMediaCell(
        parent: FrameLayout,
        ownerKey: String,
        row: FeedRow,
        grid: FeedMediaGridModel,
        cell: FeedMediaCellModel,
        mediaIndex: Int,
        isSingle: Boolean = false,
        colors: NativeFeedColors,
        callbacks: NativeFeedCallbacks,
    ) {
        val image = ImageView(parent.context).apply {
            scaleType = nativeMediaScaleTypeFor(cell.descriptor, isSingle)
            setBackgroundColor(if (isSingle) Color.TRANSPARENT else colors.surface)
        }
        parent.addView(
            image,
            FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT,
            ),
        )
        loadMediaImage(
            image,
            cell.artworkUri(),
            parent.layoutParams?.width ?: 0,
            parent.layoutParams?.height ?: 0,
        )

        if (cell.descriptor.isVideo) {
            val playerView = PlayerView(parent.context).apply {
                useController = false
                resizeMode = AspectRatioFrameLayout.RESIZE_MODE_FIT
                setShutterBackgroundColor(Color.TRANSPARENT)
                visibility = View.GONE
                alpha = 0f
            }
            parent.addView(
                playerView,
                FrameLayout.LayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT,
                    ViewGroup.LayoutParams.MATCH_PARENT,
                ),
            )
            val streamUri = cell.streamUri()
            if (streamUri !is MediaUri.Missing) {
                val slot = NativeVideoSlot(
                    key = ownerKey,
                    streamUri = streamUri,
                    container = parent,
                    playerView = playerView,
                    poster = image,
                )
                videoSlots += slot
            }
        }

        parent.setOnClickListener {
            callbacks.onMediaOpen(row, mediaIndex, grid)
        }
    }

    private fun loadAvatar(imageView: ImageView, channelId: String, explicitAvatarUrl: String?) {
        val requestKey = "avatar:${channelId.trim()}:${explicitAvatarUrl.orEmpty().trim()}"
        imageView.tag = requestKey
        avatarJobs.remove(imageView)?.cancel()
        imageView.setImageDrawable(null)
        imageView.background = roundedFill(getColors().surfaceVariant, dp(999))

        val explicitUri = explicitAvatarUrl?.trim()
            ?.takeIf { it.isNotBlank() }
            ?.let(::avatarRemoteUri)
        if (channelId.isBlank()) {
            if (explicitUri != null) loadAvatarUri(imageView, explicitUri)
            return
        }

        avatarJobs[imageView] = scope.launch {
            val resolved = withContext(Dispatchers.IO) {
                mediaResolvers.avatarForChannel(channelId)
            }
            if (imageView.tag != requestKey) return@launch
            loadAvatarUri(
                imageView = imageView,
                uri = resolved.takeUnless { it is MediaUri.Missing } ?: explicitUri ?: MediaUri.Missing,
            )
        }
    }

    private fun loadAvatarUri(imageView: ImageView, uri: MediaUri) {
        val request = buildMediaImageRequest(
            context = imageView.context,
            uri = uri,
            bearerToken = authTokens.bearerTokenSync(),
            iglooHost = iglooHostProvider.hostSync(),
            widthPx = dp(48),
            heightPx = dp(48),
        )?.newBuilder(imageView.context)
            ?.target(ImageViewTarget(imageView))
            ?.build()
        if (request != null) {
            imageLoader.enqueue(request)
        }
    }

    private fun avatarRemoteUri(url: String): MediaUri.Remote {
        val resolved = when {
            url.startsWith("http://") || url.startsWith("https://") -> url
            url.startsWith("/") -> getBaseUrl().trim().trimEnd('/') + url
            else -> url
        }
        return MediaUri.Remote(resolved)
    }

    private fun cancelAvatarJobs() {
        avatarJobs.values.forEach { it.cancel() }
        avatarJobs.clear()
    }

    private fun loadMediaImage(imageView: ImageView, uri: MediaUri, widthPx: Int, heightPx: Int) {
        val request = buildMediaImageRequest(
            context = imageView.context,
            uri = uri,
            bearerToken = authTokens.bearerTokenSync(),
            iglooHost = iglooHostProvider.hostSync(),
            widthPx = widthPx.takeIf { it > 0 } ?: imageView.resources.displayMetrics.widthPixels,
            heightPx = heightPx.takeIf { it > 0 } ?: imageView.resources.displayMetrics.widthPixels,
        )?.newBuilder(imageView.context)
            ?.target(ImageViewTarget(imageView))
            ?.build()
        if (request != null) {
            imageLoader.enqueue(request)
        } else {
            imageView.setImageDrawable(null)
            imageView.setBackgroundColor(getColors().surfaceVariant)
        }
    }
}

private class NativeFeedChannelHeaderViews(context: Context) {
    val root: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.VERTICAL
        setPadding(0, 0, 0, 0)
        clipChildren = false
        clipToPadding = false
        layoutParams = RecyclerView.LayoutParams(
            ViewGroup.LayoutParams.MATCH_PARENT,
            ViewGroup.LayoutParams.WRAP_CONTENT,
        )
        tag = this@NativeFeedChannelHeaderViews
    }
    val bannerFrame: FrameLayout = FrameLayout(context).apply {
        clipChildren = false
        clipToPadding = false
        layoutParams = LinearLayout.LayoutParams(
            ViewGroup.LayoutParams.MATCH_PARENT,
            dp(NativeChannelHeaderBannerFrameHeightDp),
        )
    }
    val banner: ImageView = ImageView(context).apply {
        scaleType = ImageView.ScaleType.CENTER_CROP
    }
    val bannerAvatar: ImageView = ImageView(context).apply {
        scaleType = ImageView.ScaleType.CENTER_CROP
        background = roundedFill(Color.DKGRAY, dp(999))
        clipToOutline = true
    }
    val inlineAvatar: ImageView = ImageView(context).apply {
        scaleType = ImageView.ScaleType.CENTER_CROP
        background = roundedFill(Color.DKGRAY, dp(999))
        clipToOutline = true
    }
    private val content: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.VERTICAL
        setPadding(
            dp(ChannelProfileHeaderDefaults.CardHorizontalMarginDp),
            0,
            dp(ChannelProfileHeaderDefaults.CardHorizontalMarginDp),
            dp(ChannelProfileHeaderDefaults.CardHorizontalMarginDp),
        )
    }
    private val actionRow: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.HORIZONTAL
        gravity = Gravity.CENTER_VERTICAL
    }
    private val infoCard: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.VERTICAL
        setPadding(
            dp(ChannelProfileHeaderDefaults.CardHorizontalPaddingDp),
            dp(ChannelProfileHeaderDefaults.CardVerticalPaddingDp),
            dp(ChannelProfileHeaderDefaults.CardHorizontalPaddingDp),
            dp(ChannelProfileHeaderDefaults.CardVerticalPaddingDp),
        )
    }
    val follow: TextView = TextView(context).apply {
        gravity = Gravity.CENTER
        textSize = 16f
        setIncludeFontPadding(false)
        maxLines = 1
        setPadding(dp(20), 0, dp(20), 0)
    }
    val star: ImageButton = ImageButton(context).apply {
        background = null
        scaleType = ImageView.ScaleType.CENTER
        setPadding(dp(8), dp(8), dp(8), dp(8))
    }
    val menu: ImageButton = ImageButton(context).apply {
        background = null
        scaleType = ImageView.ScaleType.CENTER
        setPadding(dp(8), dp(8), dp(8), dp(8))
    }
    private val nameRow: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.HORIZONTAL
        gravity = Gravity.CENTER_VERTICAL
    }
    val name: TextView = TextView(context).apply {
        textSize = NativeChannelHeaderNameTextSp
        typeface = Typeface.DEFAULT_BOLD
        setIncludeFontPadding(false)
        setLineSpacing(0f, 1.0f)
        maxLines = 2
        ellipsize = TextUtils.TruncateAt.END
    }
    val verified: TextView = TextView(context).apply {
        text = "✓"
        gravity = Gravity.CENTER
        textSize = 16f
        setIncludeFontPadding(false)
        setTextColor(Color.WHITE)
        background = roundedFill(0xFF8DEBFF.toInt(), dp(999))
    }
    val handle: TextView = TextView(context).apply {
        textSize = NativeChannelHeaderMetaTextSp
        setIncludeFontPadding(false)
        maxLines = 1
        ellipsize = TextUtils.TruncateAt.END
    }
    val bio: TextView = TextView(context).apply {
        textSize = NativeChannelHeaderBioTextSp
        setIncludeFontPadding(false)
        setLineSpacing(0f, 1.0f)
        maxLines = 6
        ellipsize = TextUtils.TruncateAt.END
    }
    val website: TextView = TextView(context).apply {
        textSize = NativeChannelHeaderBioTextSp
        setIncludeFontPadding(false)
        maxLines = 1
        ellipsize = TextUtils.TruncateAt.END
    }
    val stats: TextView = TextView(context).apply {
        textSize = NativeChannelHeaderMetaTextSp
        setIncludeFontPadding(false)
        maxLines = 1
        ellipsize = TextUtils.TruncateAt.END
    }
    init {
        bannerFrame.addView(
            banner,
            FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                dp(NativeChannelHeaderBannerHeightDp),
            ).apply {
                gravity = Gravity.TOP
            },
        )
        bannerFrame.addView(
            bannerAvatar,
            FrameLayout.LayoutParams(
                dp(NativeChannelHeaderAvatarSizeDp),
                dp(NativeChannelHeaderAvatarSizeDp),
            ).apply {
                gravity = Gravity.START or Gravity.BOTTOM
                leftMargin = dp(16)
                bottomMargin = -dp(NativeChannelHeaderAvatarOverlapDp)
            },
        )
        root.addView(bannerFrame)

        actionRow.addView(
            inlineAvatar,
            LinearLayout.LayoutParams(
                dp(NativeChannelHeaderInlineAvatarSizeDp),
                dp(NativeChannelHeaderInlineAvatarSizeDp),
            ),
        )
        actionRow.addView(View(context), LinearLayout.LayoutParams(0, dp(NativeChannelHeaderActionRowHeightDp), 1f))
        actionRow.addView(follow, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, dp(NativeChannelHeaderFollowHeightDp)))
        actionRow.addView(star, LinearLayout.LayoutParams(dp(NativeChannelHeaderIconButtonSizeDp), dp(NativeChannelHeaderIconButtonSizeDp)))
        actionRow.addView(menu, LinearLayout.LayoutParams(dp(NativeChannelHeaderIconButtonSizeDp), dp(NativeChannelHeaderIconButtonSizeDp)))
        content.addView(actionRow)

        nameRow.addView(name, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT).apply {
            weight = 0f
        })
        nameRow.addView(verified, LinearLayout.LayoutParams(dp(30), dp(30)).apply {
            marginStart = dp(8)
        })
        infoCard.addView(nameRow)
        infoCard.addView(
            handle,
            LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT).apply {
                topMargin = dp(ChannelProfileHeaderDefaults.NameHandleSpacingDp)
            },
        )
        infoCard.addView(
            bio,
            LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT).apply {
                topMargin = dp(ChannelProfileHeaderDefaults.SectionSpacingDp)
            },
        )
        infoCard.addView(
            website,
            LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT).apply {
                topMargin = dp(ChannelProfileHeaderDefaults.SectionSpacingDp)
            },
        )
        infoCard.addView(
            stats,
            LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT).apply {
                topMargin = dp(ChannelProfileHeaderDefaults.SectionSpacingDp)
            },
        )
        content.addView(
            infoCard,
            LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT),
        )
        root.addView(content)
    }

    fun applyColors(colors: NativeFeedColors) {
        root.setBackgroundColor(colors.surface)
        bannerFrame.setBackgroundColor(colors.surfaceHighest)
        infoCard.background = roundedFill(colors.surfaceElevated, dp(ChannelProfileHeaderDefaults.CardRadiusDp))
        name.setTextColor(colors.onSurface)
        handle.setTextColor(colors.onSurfaceHandle)
        bio.setTextColor(colors.onSurface)
        stats.setTextColor(colors.onSurfaceMuted)
    }
}

private fun NativeFeedColors.channelProfileHeaderLinkColor(role: ChannelProfileHeaderLinkColorRole): Int =
    when (role) {
        ChannelProfileHeaderLinkColorRole.Primary -> primary
    }

private class NativeFeedCardViews(context: Context) {
    val root: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.VERTICAL
        setPadding(dp(10), dp(8), dp(10), dp(8))
        layoutParams = RecyclerView.LayoutParams(
            ViewGroup.LayoutParams.MATCH_PARENT,
            ViewGroup.LayoutParams.WRAP_CONTENT,
        ).apply {
            setMargins(dp(8), dp(2), dp(8), dp(2))
        }
        tag = this@NativeFeedCardViews
    }
    val retweeter: TextView = smallText(context)
    val header: NativeIdentityHeaderViews = NativeIdentityHeaderViews(context)
    val reply: TextView = smallText(context)
    val body: TextView = bodyText(context)
    val showMore: TextView = smallText(context)
    val media: LinearLayout = LinearLayout(context).apply { orientation = LinearLayout.VERTICAL }
    val quote: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.VERTICAL
        setPadding(dp(8), dp(8), dp(8), dp(8))
    }
    val quoteHeader: NativeIdentityHeaderViews = NativeIdentityHeaderViews(context)
    val quoteBody: TextView = quoteText(context)
    val quoteMedia: LinearLayout = LinearLayout(context).apply { orientation = LinearLayout.VERTICAL }
    val actionContainer: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.HORIZONTAL
        gravity = Gravity.CENTER_VERTICAL
    }
    val actions: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.HORIZONTAL
        gravity = Gravity.CENTER_VERTICAL
    }
    val menu: ImageButton = ImageButton(context).apply {
        background = null
        scaleType = ImageView.ScaleType.CENTER
        setPadding(dp(10), dp(6), dp(10), dp(6))
        setImageResource(R.drawable.ic_feed_more_vert_24)
        contentDescription = context.getString(R.string.action_more)
        layoutParams = LinearLayout.LayoutParams(dp(48), dp(36))
    }

    init {
        root.addView(retweeter)
        root.addView(header.root)
        root.addView(reply)
        root.addView(body)
        root.addView(showMore)
        root.addView(media, LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT)
        quote.addView(quoteHeader.root)
        quote.addView(quoteBody)
        quote.addView(quoteMedia, LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT)
        root.addView(quote, verticalSpacingLayoutParams())
        actionContainer.addView(menu, LinearLayout.LayoutParams(dp(48), dp(36)))
        actionContainer.addView(View(context), LinearLayout.LayoutParams(0, dp(40), 1f))
        actionContainer.addView(actions, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, dp(40)))
        root.addView(actionContainer)
    }

    fun applyColors(colors: NativeFeedColors) {
        root.background = roundedFill(colors.surfaceElevated, dp(8))
        listOf(retweeter, reply, showMore).forEach { it.setTextColor(colors.onSurfaceMuted) }
        body.setTextColor(colors.onSurface)
        quoteBody.setTextColor(colors.onSurface)
    }
}

private class NativeIdentityHeaderViews(context: Context) {
    val root: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.HORIZONTAL
        gravity = Gravity.CENTER_VERTICAL
        setPadding(0, dp(2), 0, dp(2))
    }
    val avatar: ImageView = ImageView(context).apply {
        scaleType = ImageView.ScaleType.CENTER_CROP
        background = roundedFill(Color.DKGRAY, dp(999))
        clipToOutline = true
    }
    private val textColumn: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.VERTICAL
        gravity = Gravity.CENTER_VERTICAL
    }
    private val nameRow: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.HORIZONTAL
        gravity = Gravity.CENTER_VERTICAL
    }
    private val metaRow: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.HORIZONTAL
        gravity = Gravity.CENTER_VERTICAL
    }
    val name: TextView = TextView(context).apply {
        textSize = 17f
        typeface = Typeface.DEFAULT_BOLD
        maxLines = 1
        ellipsize = TextUtils.TruncateAt.END
    }
    val follow: TextView = TextView(context).apply {
        gravity = Gravity.CENTER
        textSize = 13f
        maxLines = 1
        ellipsize = TextUtils.TruncateAt.END
        setPadding(dp(10), dp(4), dp(10), dp(4))
    }
    val meta: TextView = smallText(context).apply {
        maxLines = 1
        ellipsize = TextUtils.TruncateAt.END
    }
    val translate: LinearLayout = LinearLayout(context).apply {
        orientation = LinearLayout.HORIZONTAL
        gravity = Gravity.CENTER_VERTICAL
        visibility = View.GONE
        isClickable = true
        isFocusable = true
        setPadding(dp(4), 0, 0, 0)
    }
    val translateIcon: ImageView = ImageView(context).apply {
        setImageResource(R.drawable.ic_feed_translate_24)
    }
    val translateLabel: TextView = smallText(context).apply {
        textSize = 11f
        typeface = Typeface.DEFAULT_BOLD
        setPadding(dp(2), 0, 0, 0)
    }

    init {
        root.addView(avatar, LinearLayout.LayoutParams(dp(42), dp(42)))
        nameRow.addView(name, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        nameRow.addView(follow, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, dp(30)))
        textColumn.addView(nameRow)
        metaRow.addView(meta, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        translate.addView(translateIcon, LinearLayout.LayoutParams(dp(15), dp(15)))
        translate.addView(translateLabel, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT))
        metaRow.addView(translate, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT))
        textColumn.addView(metaRow)
        root.addView(textColumn, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f).apply {
            marginStart = dp(8)
        })
    }
}

private data class NativeVideoSlot(
    val key: String,
    val streamUri: MediaUri,
    val container: View,
    val playerView: PlayerView,
    val poster: ImageView,
)

internal data class NativeInlineVideoCandidate(
    val key: String,
    val streamUri: MediaUri,
    val visibleFraction: Float,
    val centerDistancePx: Float,
)

internal fun chooseNativeInlineVideoCandidate(
    candidates: Collection<NativeInlineVideoCandidate>,
    minVisibleFraction: Float = 0.25f,
): NativeInlineVideoCandidate? =
    candidates
        .asSequence()
        .filter { it.streamUri !is MediaUri.Missing }
        .filter { it.visibleFraction >= minVisibleFraction }
        .sortedWith(compareBy<NativeInlineVideoCandidate> { abs(it.centerDistancePx) }.thenBy { it.key })
        .firstOrNull()

internal enum class NativeInlineVideoSwitchDecision {
    ResumeActive,
    HandoffSameStream,
    PrepareNewStream,
}

internal fun nativeInlineVideoSwitchDecision(
    activeKey: String?,
    activeStreamUri: MediaUri?,
    selected: NativeInlineVideoCandidate,
): NativeInlineVideoSwitchDecision =
    when {
        selected.key == activeKey && selected.streamUri == activeStreamUri ->
            NativeInlineVideoSwitchDecision.ResumeActive
        selected.streamUri == activeStreamUri ->
            NativeInlineVideoSwitchDecision.HandoffSameStream
        else ->
            NativeInlineVideoSwitchDecision.PrepareNewStream
    }

private class NativeInlineVideoManager(
    private val player: ExoPlayer,
) {
    private var activeKey: String? = null
    private var activeStreamUri: MediaUri? = null
    private var activePlayerView: PlayerView? = null
    private var activePoster: ImageView? = null
    private var firstFrameRendered = false

    private val listener = object : Player.Listener {
        override fun onRenderedFirstFrame() {
            firstFrameRendered = true
            activePlayerView?.visibility = View.VISIBLE
            activePlayerView?.alpha = 1f
        }
    }

    init {
        player.volume = 0f
        player.repeatMode = Player.REPEAT_MODE_ALL
        player.addListener(listener)
    }

    fun selectFrom(recyclerView: RecyclerView) {
        val slots = visibleVideoSlots(recyclerView)
        val candidates = slots.map { (_, candidate) -> candidate }
        val selected = chooseNativeInlineVideoCandidate(candidates)
        if (selected == null) {
            val active = activeKey?.let { key -> slots.firstOrNull { it.second.key == key }?.second }
            if (active != null && active.visibleFraction > 0.08f) {
                player.playWhenReady = true
                player.play()
                return
            }
            clearActive(pause = true)
            return
        }
        val slot = slots.firstOrNull { it.second.key == selected.key }?.first ?: return
        when (nativeInlineVideoSwitchDecision(activeKey, activeStreamUri, selected)) {
            NativeInlineVideoSwitchDecision.ResumeActive -> {
                attachTo(slot, keepPrepared = true)
                player.playWhenReady = true
                player.play()
                return
            }
            NativeInlineVideoSwitchDecision.HandoffSameStream -> {
                attachTo(slot, keepPrepared = true)
                activeKey = selected.key
                player.playWhenReady = true
                player.play()
                return
            }
            NativeInlineVideoSwitchDecision.PrepareNewStream -> Unit
        }

        val mediaItem = selected.streamUri.toMedia3ItemOrNull() ?: return
        attachTo(slot, keepPrepared = false)
        activeKey = selected.key
        activeStreamUri = selected.streamUri
        firstFrameRendered = false
        player.setMediaItem(mediaItem)
        player.prepare()
        player.playWhenReady = true
        player.play()
    }

    fun detachSlot(key: String) {
        if (key != activeKey) return
        clearActive(pause = true)
    }

    fun pause() {
        player.playWhenReady = false
        player.pause()
    }

    fun release() {
        player.removeListener(listener)
        clearActive(pause = false)
        player.release()
    }

    private fun attachTo(slot: NativeVideoSlot, keepPrepared: Boolean) {
        if (activePlayerView !== slot.playerView) {
            activePlayerView?.player = null
            activePlayerView?.visibility = View.GONE
            activePlayerView?.alpha = 0f
            activePoster?.visibility = View.VISIBLE
            slot.playerView.player = player
            activePlayerView = slot.playerView
            activePoster = slot.poster
        }
        slot.playerView.visibility = View.VISIBLE
        slot.playerView.alpha = if (keepPrepared && firstFrameRendered) 1f else 0f
        slot.poster.visibility = View.VISIBLE
    }

    private fun clearActive(pause: Boolean) {
        if (pause) pause()
        activePlayerView?.player = null
        activePlayerView?.visibility = View.GONE
        activePlayerView?.alpha = 0f
        activePoster?.visibility = View.VISIBLE
        activePlayerView = null
        activePoster = null
        activeKey = null
        activeStreamUri = null
        firstFrameRendered = false
    }

    private fun visibleVideoSlots(recyclerView: RecyclerView): List<Pair<NativeVideoSlot, NativeInlineVideoCandidate>> {
        val viewport = Rect(0, recyclerView.paddingTop, recyclerView.width, recyclerView.height - recyclerView.paddingBottom)
        val viewportCenter = viewport.centerY().toFloat()
        val result = mutableListOf<Pair<NativeVideoSlot, NativeInlineVideoCandidate>>()
        for (index in 0 until recyclerView.childCount) {
            val holder = recyclerView.getChildViewHolder(recyclerView.getChildAt(index)) as? NativeFeedViewHolder ?: continue
            holder.videoSlotsForSelection().forEach { slot ->
                val rect = Rect()
                if (!slot.container.getGlobalVisibleRect(rect)) return@forEach
                val rvRect = Rect()
                recyclerView.getGlobalVisibleRect(rvRect)
                rect.offset(-rvRect.left, -rvRect.top)
                val visibleFraction = nativeVisibleHeightFraction(rect, viewport.height())
                result += slot to NativeInlineVideoCandidate(
                    key = slot.key,
                    streamUri = slot.streamUri,
                    visibleFraction = visibleFraction,
                    centerDistancePx = rect.centerY().toFloat() - viewportCenter,
                )
            }
        }
        return result
    }
}

internal fun nativeVisibleHeightFraction(bounds: Rect, viewportHeight: Int): Float {
    if (viewportHeight <= 0 || bounds.height() <= 0) return 0f
    val visibleTop = bounds.top.coerceAtLeast(0)
    val visibleBottom = bounds.bottom.coerceAtMost(viewportHeight)
    val visibleHeight = (visibleBottom - visibleTop).coerceAtLeast(0)
    return visibleHeight.toFloat() / bounds.height().toFloat()
}

internal fun nativeStableSingleMediaAspectRatio(cell: com.screwy.igloo.feed.FeedMediaCellDescriptor): Float =
    if (cell.aspectRatioKnown) cell.aspectRatio.coerceIn(0.55f, 2.4f) else 1f

internal fun nativeStableSingleMediaAspectRatio(cell: FeedMediaCellModel): Float =
    when {
        cell.descriptor.aspectRatioKnown -> cell.descriptor.aspectRatio.coerceIn(0.55f, 2.4f)
        else -> nativeLocalMediaAspectRatio(cell.previewItem)?.coerceIn(0.55f, 2.4f)
            ?: nativeStableSingleMediaAspectRatio(cell.descriptor)
    }

internal fun nativeLocalMediaAspectRatio(item: MediaItem?): Float? {
    val uri = when (item) {
        is MediaItem.Image -> item.uri
        is MediaItem.Video -> item.thumbnailUri
        is MediaItem.Gif, null -> return null
    }
    return nativeLocalImageAspectRatio(uri)
}

private fun nativeLocalImageAspectRatio(uri: MediaUri): Float? {
    if (uri !is MediaUri.Local) return null
    val options = BitmapFactory.Options().apply { inJustDecodeBounds = true }
    BitmapFactory.decodeFile(uri.file.absolutePath, options)
    val width = options.outWidth
    val height = options.outHeight
    if (width <= 0 || height <= 0) return null
    return width.toFloat() / height.toFloat()
}

internal data class NativeMediaDimensions(
    val widthPx: Int,
    val heightPx: Int,
)

internal fun nativeSingleMediaDimensions(
    context: Context,
    aspectRatio: Float,
): NativeMediaDimensions {
    val safeRatio = aspectRatio.coerceIn(0.55f, 2.4f)
    val maxWidth = nativeMediaGridWidthPx(context)
    val maxHeight = dp(560)
    val fullWidthHeight = (maxWidth / safeRatio).toInt()
    val height = fullWidthHeight.coerceAtMost(maxHeight).coerceAtLeast(dp(1))
    val width = (height * safeRatio).toInt().coerceAtMost(maxWidth).coerceAtLeast(dp(1))
    return NativeMediaDimensions(widthPx = width, heightPx = height)
}

internal fun nativeMediaGridWidthPx(context: Context): Int =
    (context.resources.displayMetrics.widthPixels - dp(36)).coerceAtLeast(dp(160))

internal fun nativeMediaScaleTypeFor(
    cell: com.screwy.igloo.feed.FeedMediaCellDescriptor,
    isSingle: Boolean = false,
): ImageView.ScaleType =
    if (isSingle) ImageView.ScaleType.FIT_START else ImageView.ScaleType.CENTER_CROP

private fun FeedMediaCellModel.artworkUri(): MediaUri {
    when (val item = previewItem) {
        is MediaItem.Image -> return item.uri
        is MediaItem.Video -> if (item.thumbnailUri !is MediaUri.Missing) return item.thumbnailUri
        is MediaItem.Gif -> return item.streamUri
        null -> Unit
    }
    val url = descriptor.posterUrl.ifBlank { descriptor.displayUrl }
    return url.takeIf { it.isNotBlank() }?.let(MediaUri::Remote) ?: MediaUri.Missing
}

private fun FeedMediaCellModel.streamUri(): MediaUri {
    when (val item = previewItem) {
        is MediaItem.Video -> return item.streamUri
        is MediaItem.Gif -> return item.streamUri
        is MediaItem.Image, null -> Unit
    }
    return descriptor.streamUrl.takeIf { it.isNotBlank() }?.let(MediaUri::Remote) ?: MediaUri.Missing
}

private fun MediaUri.toMedia3ItemOrNull(): Media3Item? = when (this) {
    is MediaUri.Local -> Media3Item.fromUri(file.toURI().toString())
    is MediaUri.Remote -> Media3Item.fromUri(url)
    MediaUri.Missing -> null
}

internal fun clickableText(
    raw: String,
    linkColor: Int,
    urlColor: Int = linkColor,
    onMentionClick: (String) -> Unit,
    onUrlClick: (String) -> Unit,
): SpannableString {
    val spannable = SpannableString(raw)
    MentionRegex.findAll(raw).forEach { match ->
        val handle = match.groupValues[1]
        spannable.setSpan(
            object : ClickableSpan() {
                override fun onClick(widget: View) = onMentionClick(handle)
                override fun updateDrawState(ds: TextPaint) {
                    ds.color = linkColor
                    ds.isUnderlineText = false
                }
            },
            match.range.first,
            match.range.last + 1,
            Spanned.SPAN_EXCLUSIVE_EXCLUSIVE,
        )
    }
    UrlRegex.findAll(raw).forEach { match ->
        val url = match.value
        spannable.setSpan(
            object : ClickableSpan() {
                override fun onClick(widget: View) = onUrlClick(url)
                override fun updateDrawState(ds: TextPaint) {
                    ds.color = urlColor
                    ds.isUnderlineText = true
                }
            },
            match.range.first,
            match.range.last + 1,
            Spanned.SPAN_EXCLUSIVE_EXCLUSIVE,
        )
    }
    return spannable
}

internal fun nativeTranslationPillFor(
    item: FeedItemEntity,
    active: Boolean,
    enabled: Boolean,
): NativeTranslationPill? {
    if (!enabled && !nativeHasForeignLanguage(item)) return null
    return NativeTranslationPill(
        sourceLangCode = nativeFirstForeignLanguage(item).uppercase(Locale.ROOT),
        active = active,
        enabled = enabled,
    )
}

private fun nativeHasForeignLanguage(item: FeedItemEntity): Boolean =
    nativeIsForeignOrUnknown(item.lang, item.bodyText) ||
        nativeIsForeignOrUnknown(item.quoteLang, item.quoteBodyText)

private fun nativeFirstForeignLanguage(item: FeedItemEntity): String =
    nativeForeignLanguageCode(item.lang, item.bodyText)
        ?: nativeForeignLanguageCode(item.quoteLang, item.quoteBodyText)
        ?: ""

private fun nativeIsForeignOrUnknown(lang: String?, text: String?): Boolean =
    !text.isNullOrBlank() && lang?.trim()?.lowercase(Locale.ROOT).let { it.isNullOrBlank() || it != "en" }

private fun nativeForeignLanguageCode(lang: String?, text: String?): String? {
    if (text.isNullOrBlank()) return null
    val normalized = lang?.trim()?.lowercase(Locale.ROOT).orEmpty()
    return normalized.takeIf { it.isNotBlank() && it != "en" }
}

internal fun nativeShouldClampBody(text: String): Boolean =
    text.length > 420 || text.count { it == '\n' } + 1 > NativeFeedBodyCollapsedLines

private fun remoteUriFor(url: String, baseUrl: String): MediaUri.Remote {
    val resolved = when {
        url.startsWith("http://") || url.startsWith("https://") -> url
        url.startsWith("/") -> baseUrl.trim().trimEnd('/') + url
        else -> url
    }
    return MediaUri.Remote(resolved)
}

private fun NativeFeedPrimaryAction.iconRes(selected: Boolean): Int = when (this) {
    NativeFeedPrimaryAction.Share -> R.drawable.ic_feed_share_24
    NativeFeedPrimaryAction.Like -> if (selected) R.drawable.ic_feed_favorite_24 else R.drawable.ic_feed_favorite_border_24
    NativeFeedPrimaryAction.Bookmark -> if (selected) R.drawable.ic_feed_bookmark_24 else R.drawable.ic_feed_bookmark_border_24
}

private fun NativeFeedPrimaryAction.contentDescription(context: Context, post: SocialPostModel): String = when (this) {
    NativeFeedPrimaryAction.Share -> context.getString(R.string.action_share)
    NativeFeedPrimaryAction.Like -> context.getString(if (post.actions.isLiked) R.string.action_unlike else R.string.action_like)
    NativeFeedPrimaryAction.Bookmark -> context.getString(
        if (post.actions.isBookmarked) R.string.action_remove_bookmark else R.string.action_bookmark,
    )
}

private fun showNativeFeedPopup(
    anchor: View,
    colors: NativeFeedColors,
    items: List<NativeFeedMenuItem>,
) {
    if (items.isEmpty()) return
    val context = anchor.context
    val menuWidth = dp(224)
    val content = LinearLayout(context).apply {
        orientation = LinearLayout.VERTICAL
        setPadding(dp(6), dp(6), dp(6), dp(6))
        background = roundedStroke(colors.surfaceElevated, colors.borderSubtle, dp(1), dp(10))
    }
    val popup = PopupWindow(content, menuWidth, ViewGroup.LayoutParams.WRAP_CONTENT, true).apply {
        isOutsideTouchable = true
        elevation = dp(10).toFloat()
        setBackgroundDrawable(ColorDrawable(Color.TRANSPARENT))
    }
    items.forEach { item ->
        content.addView(
            TextView(context).apply {
                text = item.label
                textSize = 15f
                maxLines = 1
                ellipsize = TextUtils.TruncateAt.END
                gravity = Gravity.CENTER_VERTICAL
                setIncludeFontPadding(false)
                setPadding(dp(10), 0, dp(10), 0)
                setTextColor(if (item.danger) colors.primary else colors.onSurface)
                background = roundedFill(Color.TRANSPARENT, dp(8))
                setOnClickListener {
                    popup.dismiss()
                    item.action()
                }
            },
            LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(40)),
        )
    }
    popup.showAsDropDown(anchor, anchor.width - menuWidth, dp(4))
}

private fun actionIconButton(context: Context, colors: NativeFeedColors): ImageButton =
    ImageButton(context).apply {
        background = null
        scaleType = ImageView.ScaleType.CENTER
        setPadding(dp(10), dp(6), dp(10), dp(6))
        setColorFilter(colors.onSurfaceMuted)
        layoutParams = LinearLayout.LayoutParams(dp(44), dp(38))
    }

private fun bodyText(context: Context): TextView =
    TextView(context).apply {
        textSize = 17f
        setIncludeFontPadding(false)
        setLineSpacing(0f, 1.22f)
        setPadding(0, dp(3), 0, dp(3))
    }

private fun quoteText(context: Context): TextView =
    TextView(context).apply {
        textSize = 15f
        setIncludeFontPadding(false)
        setLineSpacing(0f, 1.18f)
        setPadding(0, dp(2), 0, dp(2))
    }

private fun smallText(context: Context): TextView =
    TextView(context).apply {
        textSize = 14f
        maxLines = 1
        ellipsize = TextUtils.TruncateAt.END
        setPadding(0, dp(3), 0, dp(3))
    }

private fun verticalSpacingLayoutParams(): LinearLayout.LayoutParams =
    LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT).apply {
        topMargin = dp(6)
        bottomMargin = dp(6)
    }

private fun roundedFill(color: Int, radius: Int): GradientDrawable =
    GradientDrawable().apply {
        setColor(color)
        cornerRadius = radius.toFloat()
    }

private fun roundedStroke(fill: Int, stroke: Int, strokeWidth: Int, radius: Int): GradientDrawable =
    GradientDrawable().apply {
        setColor(fill)
        setStroke(strokeWidth, stroke)
        cornerRadius = radius.toFloat()
    }

private fun stableItemId(value: String): Long {
    var result = 1125899906842597L
    value.forEach { ch -> result = 31 * result + ch.code }
    return result
}

private fun dp(value: Int): Int =
    (value * android.content.res.Resources.getSystem().displayMetrics.density).toInt()

private val MentionRegex = Regex("""(?<![A-Za-z0-9_])@([A-Za-z0-9_]{1,30})""")
private val UrlRegex = Regex("""https?://\S+""")
