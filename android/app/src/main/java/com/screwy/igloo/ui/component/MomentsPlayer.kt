package com.screwy.igloo.ui.component

import androidx.activity.compose.BackHandler
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.VerticalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.compose.ui.zIndex
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.media3.common.MediaItem
import com.screwy.igloo.R
import com.screwy.igloo.data.dao.AndroidSyncDao
import com.screwy.igloo.log.Logger
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.media.OwnerKind
import com.screwy.igloo.media.assetOwnerKind
import com.screwy.igloo.net.IglooHostProvider
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.net.auth.AuthTokenProvider
import com.screwy.igloo.player.buildIglooPlayer
import com.screwy.igloo.ui.nav.LocalDrawerController
import kotlin.math.abs
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import org.koin.compose.koinInject

/**
 * UI-layer shape for one moments-player page. Callers map from the Room
 * [com.screwy.igloo.data.entity.MomentItem] projection into this item. Media state is resolved
 * lazily inside the player so a large moments dataset does not block first render.
 */
data class MomentItem(
    val videoId: String,
    val mediaOwnerId: String = videoId,
    val channelId: String,
    val canonicalUrl: String = "",
    val authorDisplayName: String? = null,
    val authorHandle: String,
    val description: String,
    val likeCount: Int?,
    val isLiked: Boolean,
    val isBookmarked: Boolean,
    val mediaKind: String? = null,
    val slideCount: Int = 0,
    val ownerKind: OwnerKind,
    val isAuthorFollowed: Boolean = true,
    val repostAuthorLabel: String? = null,
    val repostOtherCount: Int = 0,
    val storyRingState: StoryRingState = StoryRingState.None,
    val storyFirstVideoId: String = "",
    /**
     * Epoch millis for the "14h ago" muted timestamp above the description in the collapsed
     * overlay. `0L` → hide the timestamp (unknown publish time).
     */
    val publishedAt: Long = 0L,
)

/** Returns `true` iff a horizontal drag should be treated as a left-swipe to-channel. */
internal fun isLeftSwipe(deltaX: Float, thresholdPx: Float): Boolean = deltaX < -thresholdPx

internal fun momentCaptionBaseBottomPaddingDp(mediaMode: MomentMediaMode): Int =
    when (mediaMode) {
        MomentMediaMode.Video -> MomentVideoCaptionBaseBottomPaddingDp
        MomentMediaMode.Image,
        MomentMediaMode.Slideshow -> MomentCaptionBaseBottomPaddingDp
    }

internal fun momentCollapsedCaptionStartPaddingDp(): Int = MomentCollapsedCaptionStartPaddingDp

internal fun momentCaptionDescriptionMaxLines(expanded: Boolean): Int =
    if (expanded) Int.MAX_VALUE else MomentCollapsedCaptionMaxLines

internal fun momentCaptionExpandedAfterPlainTextClick(
    expanded: Boolean,
    descriptionCanExpand: Boolean,
): Boolean =
    when {
        expanded -> false
        descriptionCanExpand -> true
        else -> false
    }

internal fun momentCaptionBackgroundColor(expanded: Boolean): Color =
    if (expanded) Color.Black.copy(alpha = 0.28f) else Color.Transparent

internal const val MOMENTS_PREPARE_RADIUS = 1
internal const val AUTO_SWIPE_SCROLL_DURATION_MS = 850
internal const val MOMENT_STILL_ADVANCE_DELAY_MS = 3_000L
internal const val MOMENT_SLIDESHOW_ADVANCE_DELAY_MS = 2_000L
internal const val MOMENTS_STOP_OLD_PAGE_DELAY_MS = 200L
internal const val MomentCaptionBaseBottomPaddingDp = 12
internal const val MomentVideoCaptionBaseBottomPaddingDp = 16
internal const val MomentCollapsedCaptionStartPaddingDp = 8
internal const val MomentCollapsedCaptionMaxLines = 2

internal data class StoryProgressWindow(val index: Int, val count: Int)

internal data class StoryAdvanceTarget(
    val nextIndex: Int?,
    val shouldExit: Boolean,
    val animate: Boolean,
)

internal fun storyAdvanceTarget(
    items: List<MomentItem>,
    currentIndex: Int,
    crossProfile: Boolean,
): StoryAdvanceTarget {
    if (items.isEmpty() || currentIndex !in items.indices) {
        return StoryAdvanceTarget(nextIndex = null, shouldExit = true, animate = false)
    }
    val nextIndex = currentIndex + 1
    if (nextIndex !in items.indices) {
        return StoryAdvanceTarget(nextIndex = null, shouldExit = true, animate = false)
    }
    val crossesProfile = items[nextIndex].channelId != items[currentIndex].channelId
    if (!crossProfile && crossesProfile) {
        return StoryAdvanceTarget(nextIndex = null, shouldExit = true, animate = false)
    }
    return StoryAdvanceTarget(nextIndex = nextIndex, shouldExit = false, animate = crossesProfile)
}

internal fun momentStreamLoadKey(videoId: String, streamUri: MediaUri): String? =
    when (streamUri) {
        is MediaUri.Local -> "local:$videoId:${streamUri.file.absolutePath}"
        is MediaUri.Remote -> "remote:$videoId:${streamUri.url}"
        MediaUri.Missing -> null
    }

internal fun momentPlayerMediaItem(videoId: String, streamUri: MediaUri): MediaItem? =
    when (streamUri) {
        is MediaUri.Local ->
            MediaItem.Builder()
                .setMediaId(videoId)
                .setUri(streamUri.file.toURI().toString())
                .build()
        is MediaUri.Remote -> MediaItem.Builder().setMediaId(videoId).setUri(streamUri.url).build()
        MediaUri.Missing -> null
    }

internal fun momentStreamLoadKeyVideoId(loadKey: String?): String? {
    if (loadKey.isNullOrBlank()) return null
    val first = loadKey.indexOf(':')
    if (first < 0) return null
    val second = loadKey.indexOf(':', startIndex = first + 1)
    if (second <= first + 1) return null
    return loadKey.substring(first + 1, second)
}

internal fun momentSlideshowAdvanceDelayMs(): Long = MOMENT_SLIDESHOW_ADVANCE_DELAY_MS

/**
 * TikTok-style vertical-swipe video player. Used by the moments tab, the bookmarks
 * Twitter-as-TikTok viewer, and TikTok channel pages.
 */
@Composable
fun MomentsPlayer(
    items: List<MomentItem>,
    startIndex: Int = 0,
    // Match PreferencesRepo.Defaults: auto-swipe OFF, mute ON. Every real caller
    // now passes a PreferencesRepo-backed value through — these defaults only
    // matter for previews and tests, but keeping them aligned avoids surprises
    // when someone forgets to plumb the setting.
    autoSwipeDefault: Boolean = false,
    muteDefault: Boolean = true,
    onAutoSwipeChanged: (Boolean) -> Unit = {},
    onMuteChanged: (Boolean) -> Unit = {},
    onIndexChange: (Int) -> Unit,
    onViewEvent: (videoId: String) -> Unit,
    onChannelClick: (channelId: String) -> Unit,
    onBookmarkToggle: (MomentItem) -> Unit,
    onRequestBookmarkSheet: (MomentItem) -> Unit = {},
    onShare: (MomentItem) -> Unit = {},
    onFollowChannel: (channelId: String) -> Unit = {},
    onUnfollowChannel: (channelId: String) -> Unit = {},
    onMentionClick: (handle: String) -> Unit,
    onSwipeLeftToChannel: (channelId: String) -> Unit,
    onOpenAllMomentsGrid: (() -> Unit)? = null,
    onEndReached: () -> Unit = {},
    forceAutoSwipe: Boolean = false,
    exitOnEnd: Boolean = false,
    storyCrossProfileAdvance: Boolean = false,
    onStoryClick: (channelId: String, firstVideoId: String) -> Unit = { _, _ -> },
    activeTab: String? = null,
    onTabSelected: ((String) -> Unit)? = null,
    modifier: Modifier = Modifier,
) {
    if (items.isEmpty()) return

    val context = LocalContext.current
    val lifecycleOwner = LocalLifecycleOwner.current
    val authTokens: AuthTokenProvider = koinInject()
    val iglooHostProvider: IglooHostProvider = koinInject()
    val syncDao: AndroidSyncDao = koinInject()
    val baseUrlProvider: ServerBaseUrlProvider = koinInject()
    val reachability: Reachability = koinInject()
    val logger: Logger = koinInject()
    val drawerController = LocalDrawerController.current
    val slideshowAudioPlayer =
        remember(authTokens.bearerTokenSync()) {
            buildIglooPlayer(context, authTokens, iglooHostProvider).also {}
        }
    DisposableEffect(slideshowAudioPlayer) { onDispose { slideshowAudioPlayer.release() } }

    var lifecycleStarted by remember {
        mutableStateOf(lifecycleOwner.lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED))
    }
    var loadedSlideshowAudioKey by remember { mutableStateOf<String?>(null) }
    var autoSwipeState by remember { mutableStateOf(autoSwipeDefault) }
    LaunchedEffect(autoSwipeDefault) { autoSwipeState = autoSwipeDefault }
    val effectiveAutoSwipe = forceAutoSwipe || autoSwipeState

    val safeStart = startIndex.coerceIn(0, items.lastIndex)
    val pagerState = rememberPagerState(initialPage = safeStart, pageCount = { items.size })
    val currentIndex = pagerState.currentPage.coerceIn(0, items.lastIndex)
    val storyMode = exitOnEnd
    val storyProgressWindow =
        remember(storyMode, currentIndex, items) {
            if (storyMode) storyProgressWindow(items, currentIndex)
            else StoryProgressWindow(index = 0, count = 0)
        }
    LaunchedEffect(safeStart, items.size) {
        if (safeStart in items.indices && pagerState.currentPage != safeStart) {
            pagerState.scrollToPage(safeStart)
        }
    }

    if (onOpenAllMomentsGrid != null) {
        BackHandler { onOpenAllMomentsGrid() }
    }

    DisposableEffect(lifecycleOwner, slideshowAudioPlayer) {
        val observer = LifecycleEventObserver { _, event ->
            when (event) {
                Lifecycle.Event.ON_STOP -> {
                    lifecycleStarted = false
                    slideshowAudioPlayer.playWhenReady = false
                    slideshowAudioPlayer.pause()
                }
                Lifecycle.Event.ON_START -> lifecycleStarted = true
                else -> Unit
            }
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose { lifecycleOwner.lifecycle.removeObserver(observer) }
    }

    // Drive view-event + onIndexChange from the pager's selected page. The
    // platform pager owns drag/fling physics; the shared video player follows
    // the current page so swipes do not build and release a player per page.
    var lastFiredPage by remember { mutableStateOf<Int?>(null) }
    LaunchedEffect(pagerState, items) {
        snapshotFlow { pagerState.currentPage.coerceIn(0, items.lastIndex) }
            .distinctUntilChanged()
            .collect { page ->
                if (page !in items.indices) return@collect

                if (lastFiredPage != page) {
                    onIndexChange(page)
                    onViewEvent(items[page].videoId)
                    lastFiredPage = page
                }
            }
    }

    var muted by remember { mutableStateOf(muteDefault) }
    LaunchedEffect(muteDefault) { muted = muteDefault }
    LaunchedEffect(muted) { slideshowAudioPlayer.volume = if (muted) 0f else 1f }
    var pendingUnfollowItem by remember { mutableStateOf<MomentItem?>(null) }

    LaunchedEffect(pagerState, items, syncDao, baseUrlProvider.baseUrl()) {
        val baseUrl = baseUrlProvider.baseUrl()
        combine(
                snapshotFlow { pagerState.currentPage.coerceIn(0, items.lastIndex) },
                snapshotFlow { pagerState.isScrollInProgress },
                snapshotFlow { lifecycleStarted },
            ) { page, scrolling, started ->
                Triple(page, scrolling, started)
            }
            .distinctUntilChanged()
            .collectLatest { (page, scrolling, started) ->
                val currentItem = items.getOrNull(page)
                if (
                    currentItem == null ||
                        momentMediaMode(currentItem.mediaKind, currentItem.slideCount) !=
                            MomentMediaMode.Slideshow
                ) {
                    loadedSlideshowAudioKey = clearMomentAudio(slideshowAudioPlayer)
                    return@collectLatest
                }

                momentAudioUriFlow(
                        syncDao = syncDao,
                        baseUrl = baseUrl,
                        videoId = currentItem.videoId,
                        ownerKind = currentItem.ownerKind.assetOwnerKind(),
                        reachability = reachability,
                    )
                    .collect { audioUri ->
                        loadedSlideshowAudioKey =
                            prepareMomentAudio(
                                player = slideshowAudioPlayer,
                                loadedKey = loadedSlideshowAudioKey,
                                videoId = currentItem.videoId,
                                audioUri = audioUri,
                            )
                        if (
                            started &&
                                shouldPlayMomentPage(
                                    isCurrentPage = true,
                                    isScrollInProgress = scrolling,
                                ) &&
                                audioUri !is MediaUri.Missing
                        ) {
                            slideshowAudioPlayer.playWhenReady = true
                        } else {
                            slideshowAudioPlayer.playWhenReady = false
                            slideshowAudioPlayer.pause()
                        }
                    }
            }
    }

    var advanceTick by remember { mutableIntStateOf(0) }
    LaunchedEffect(advanceTick) {
        if (advanceTick == 0) return@LaunchedEffect
        val page = currentIndex
        var animateAdvance = false
        val next =
            if (storyMode) {
                val target =
                    storyAdvanceTarget(
                        items = items,
                        currentIndex = page,
                        crossProfile = storyCrossProfileAdvance,
                    )
                if (target.shouldExit) {
                    onEndReached()
                    return@LaunchedEffect
                }
                animateAdvance = target.animate
                target.nextIndex
            } else {
                animateAdvance = true
                nextMomentPageForAutoSwipe(
                    currentPage = page,
                    lastIndex = items.lastIndex,
                    autoSwipeEnabled = effectiveAutoSwipe,
                )
            }
        next ?: return@LaunchedEffect
        if (animateAdvance) {
            pagerState.animateScrollToPage(
                page = next,
                animationSpec =
                    tween(
                        durationMillis = AUTO_SWIPE_SCROLL_DURATION_MS,
                        easing = FastOutSlowInEasing,
                    ),
            )
        } else {
            pagerState.scrollToPage(next)
        }
    }

    Box(modifier = modifier.fillMaxSize().background(Color.Black)) {
        val pageContent: @Composable (Int) -> Unit = { page ->
            val item = items[page]
            MomentPage(
                pageIndex = page,
                item = item,
                storyMode = storyMode,
                muted = muted,
                onMuteToggle = {
                    val next = !muted
                    muted = next
                    onMuteChanged(next)
                },
                autoSwipe = effectiveAutoSwipe,
                onAutoSwipeToggle = {
                    if (!forceAutoSwipe) {
                        val next = !autoSwipeState
                        autoSwipeState = next
                        onAutoSwipeChanged(next)
                    }
                },
                showAutoSwipeControl = !forceAutoSwipe,
                isActive =
                    lifecycleStarted &&
                        shouldPlayMomentPage(page == currentIndex, pagerState.isScrollInProgress),
                pagerScrolling = pagerState.isScrollInProgress,
                shouldPrepare = abs(page - currentIndex) <= MOMENTS_PREPARE_RADIUS,
                onAutoAdvance = { advanceTick++ },
                onChannelClick = onChannelClick,
                onStoryClick = onStoryClick,
                onMentionClick = onMentionClick,
                onBookmarkToggle = onBookmarkToggle,
                onRequestBookmarkSheet = onRequestBookmarkSheet,
                onShare = onShare,
                onFollowChannel = onFollowChannel,
                onRequestUnfollowChannel = { pendingUnfollowItem = it },
                onSwipeLeftToChannel = onSwipeLeftToChannel,
                onSwipeRightFromEdge = drawerController::open,
                logger = logger,
            )
        }
        if (storyMode) {
            HorizontalPager(
                state = pagerState,
                key = { page -> items[page].videoId },
                beyondViewportPageCount = 0,
                contentPadding = PaddingValues(0.dp),
                pageSpacing = 0.dp,
                modifier = Modifier.fillMaxSize().clipToBounds(),
            ) { page ->
                Box(modifier = Modifier.fillMaxSize().clipToBounds()) { pageContent(page) }
            }
        } else {
            VerticalPager(
                state = pagerState,
                key = { page -> items[page].videoId },
                beyondViewportPageCount = MOMENTS_PREPARE_RADIUS,
                modifier = Modifier.fillMaxSize(),
            ) { page ->
                pageContent(page)
            }
        }

        if (storyMode) {
            StoryProgressControl(
                currentPage = storyProgressWindow.index,
                pageCount = storyProgressWindow.count,
                modifier =
                    Modifier.align(Alignment.TopCenter)
                        .zIndex(3f)
                        .padding(top = overlayIdentityTopPadding()),
            )
        } else if (activeTab != null && onTabSelected != null) {
            MomentsTabControl(
                activeTab = activeTab,
                onTabSelected = onTabSelected,
                modifier =
                    Modifier.align(Alignment.TopCenter)
                        .zIndex(2f)
                        .padding(top = overlayIdentityTopPadding()),
            )
        }
        pendingUnfollowItem?.let { target ->
            val label =
                target.authorDisplayName?.takeIf { it.isNotBlank() }
                    ?: target.authorHandle.takeIf { it.isNotBlank() }
                    ?: target.channelId
            AlertDialog(
                onDismissRequest = { pendingUnfollowItem = null },
                title = { Text(stringResource(R.string.confirm_unfollow_account_title)) },
                text = {
                    Text(stringResource(R.string.confirm_unfollow_channel_delete_media_body, label))
                },
                confirmButton = {
                    TextButton(
                        onClick = {
                            pendingUnfollowItem = null
                            onUnfollowChannel(target.channelId)
                        }
                    ) {
                        Text(stringResource(R.string.action_unfollow))
                    }
                },
                dismissButton = {
                    TextButton(onClick = { pendingUnfollowItem = null }) {
                        Text(stringResource(R.string.action_cancel))
                    }
                },
            )
        }
    }
}
