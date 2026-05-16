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
import androidx.compose.material3.MaterialTheme
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
import androidx.media3.common.Player
import com.screwy.igloo.R
import com.screwy.igloo.data.dao.AndroidSyncDao
import com.screwy.igloo.data.dao.MediaInventoryDao
import com.screwy.igloo.data.entity.MediaInventoryEntity
import com.screwy.igloo.log.Logger
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.media.OwnerKind
import com.screwy.igloo.net.IglooHostProvider
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.net.auth.AuthTokenProvider
import com.screwy.igloo.perf.PerfProbe
import com.screwy.igloo.player.buildIglooPlayer
import com.screwy.igloo.ui.nav.LocalDrawerController
import com.screwy.igloo.ui.theme.iglooColors
import kotlin.math.abs
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import org.koin.compose.koinInject

/**
 * UI-layer shape for one moments-player page. Callers map from the Room
 * [com.screwy.igloo.data.entity.MomentItem]
 * projection into this item. Media state is resolved lazily inside the player so
 * a large moments dataset does not block first render.
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
    val ownerKind: OwnerKind? = null,
    // Raw DB/server thumbnail path carried into the overlay so it can reuse the
    // same immediate poster the grid already had on screen.
    val fallbackThumbnailPath: String? = null,
    val fallbackThumbnailUri: MediaUri = MediaUri.Missing,
    val isAuthorFollowed: Boolean = true,
    val repostAuthorLabel: String? = null,
    val repostOtherCount: Int = 0,
    val storyRingState: StoryRingState = StoryRingState.None,
    val storyFirstVideoId: String = "",
    /**
     * Epoch millis for the "14h ago" muted timestamp above the description in the
     * collapsed overlay. `0L` → hide the timestamp (unknown publish time).
     */
    val publishedAt: Long = 0L,
)

/** Returns `true` iff a horizontal drag should be treated as a left-swipe to-channel. */
internal fun isLeftSwipe(deltaX: Float, thresholdPx: Float): Boolean =
    deltaX < -thresholdPx

internal fun momentCaptionBaseBottomPaddingDp(mediaMode: MomentMediaMode): Int = when (mediaMode) {
    MomentMediaMode.Video -> MomentVideoCaptionBaseBottomPaddingDp
    MomentMediaMode.Image,
    MomentMediaMode.Slideshow -> MomentCaptionBaseBottomPaddingDp
}

internal fun momentShareEnabled(item: MomentItem): Boolean =
    item.canonicalUrl.isNotBlank()

internal fun momentCollapsedCaptionStartPaddingDp(): Int = MomentCollapsedCaptionStartPaddingDp

internal fun momentCaptionDescriptionMaxLines(expanded: Boolean): Int =
    if (expanded) Int.MAX_VALUE else MomentCollapsedCaptionMaxLines

internal fun momentCaptionExpandedAfterPlainTextClick(
    expanded: Boolean,
    descriptionCanExpand: Boolean,
): Boolean = when {
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
private const val MOMENTS_TRANSITION_POSTER_MIN_MS = 180L
internal const val MOMENTS_STOP_OLD_PAGE_DELAY_MS = 200L
internal const val MomentCaptionBaseBottomPaddingDp = 12
internal const val MomentVideoCaptionBaseBottomPaddingDp = 16
internal const val MomentCollapsedCaptionStartPaddingDp = 8
internal const val MomentCollapsedCaptionMaxLines = 2

private data class MomentTransitionPoster(
    val videoId: String,
    val uri: MediaUri,
)

internal data class StoryProgressWindow(
    val index: Int,
    val count: Int,
)

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

internal fun momentStreamUrl(baseUrl: String, videoId: String): String? {
    val root = baseUrl.trim().trimEnd('/')
    if (root.isBlank()) return null
    return "$root/api/media/stream/$videoId"
}

internal fun resolveInitialMomentStreamUri(
    rows: List<MediaInventoryEntity>,
    baseUrl: String,
    videoId: String,
): MediaUri {
    val preferredRow = rows.firstOrNull { it.assetKind == "video_stream" && it.state == "cached" }
        ?: rows.firstOrNull { it.assetKind == "video_stream" }
        ?: rows.firstOrNull { it.assetKind == "post_media" && it.state == "cached" }
        ?: rows.firstOrNull { it.assetKind == "post_media" }
    if (preferredRow != null) return momentInventoryRowToMediaUri(preferredRow, baseUrl)
    return momentStreamUrl(baseUrl, videoId)
        ?.let(MediaUri::Remote)
        ?: MediaUri.Missing
}

internal fun momentStreamLoadKey(videoId: String, streamUri: MediaUri): String? = when (streamUri) {
    is MediaUri.Local -> "local:$videoId:${streamUri.file.absolutePath}"
    is MediaUri.Remote -> "remote:$videoId:${streamUri.url}"
    MediaUri.Missing -> null
}

internal fun momentPlayerMediaItem(videoId: String, streamUri: MediaUri): MediaItem? = when (streamUri) {
    is MediaUri.Local -> MediaItem.Builder()
        .setMediaId(videoId)
        .setUri(streamUri.file.toURI().toString())
        .build()
    is MediaUri.Remote -> MediaItem.Builder()
        .setMediaId(videoId)
        .setUri(streamUri.url)
        .build()
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
 * TikTok-style vertical-swipe video player. Used by the moments tab, the
 * bookmarks Twitter-as-TikTok viewer, and TikTok channel pages.
 */
@Composable
fun MomentsPlayer(
    items: List<MomentItem>,
    startIndex: Int = 0,
    startPositionMs: Long = 0L,
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
    cursorTracking: Boolean,
    onCursorAdvance: (videoId: String, positionMs: Long) -> Unit = { _, _ -> },
    forceAutoSwipe: Boolean = false,
    exitOnEnd: Boolean = false,
    storyCrossProfileAdvance: Boolean = false,
    onStoryClick: (channelId: String, firstVideoId: String) -> Unit = { _, _ -> },
    initialTransitionPosterVideoId: String? = null,
    initialTransitionPosterUri: MediaUri = MediaUri.Missing,
    onTransitionPosterDismissed: (videoId: String) -> Unit = {},
    activeTab: String? = null,
    onTabSelected: ((String) -> Unit)? = null,
    modifier: Modifier = Modifier,
) {
    if (items.isEmpty()) return

    val context = LocalContext.current
    val lifecycleOwner = LocalLifecycleOwner.current
    val authTokens: AuthTokenProvider = koinInject()
    val iglooHostProvider: IglooHostProvider = koinInject()
    val mediaInventoryDao: MediaInventoryDao = koinInject()
    val syncDao: AndroidSyncDao = koinInject()
    val baseUrlProvider: ServerBaseUrlProvider = koinInject()
    val logger: Logger = koinInject()
    val drawerController = LocalDrawerController.current
    val slideshowAudioPlayer = remember(authTokens.bearerTokenSync()) {
        buildIglooPlayer(context, authTokens, iglooHostProvider).also {
            PerfProbe.incrementCounter("igloo_moments_slideshow_audio_build_count")
            PerfProbe.log(event = "moments_slideshow_audio_build") { mapOf("items" to items.size) }
        }
    }
    DisposableEffect(slideshowAudioPlayer) {
        onDispose {
            PerfProbe.incrementCounter("igloo_moments_slideshow_audio_release_count")
            PerfProbe.log(event = "moments_slideshow_audio_release") { mapOf("items" to items.size) }
            slideshowAudioPlayer.release()
        }
    }
    val hasVideoItems = remember(items) {
        items.any { item ->
            momentMediaMode(item.mediaKind, item.slideCount) == MomentMediaMode.Video
        }
    }
    val momentsVideoPlayer = remember(authTokens.bearerTokenSync(), hasVideoItems) {
        if (!hasVideoItems) {
            null
        } else {
            buildIglooPlayer(context, authTokens, iglooHostProvider).apply {
                repeatMode = Player.REPEAT_MODE_OFF
                PerfProbe.incrementCounter("igloo_moments_player_build_count")
                PerfProbe.log(
                    event = "moments_player_build",
                ) { mapOf("page" to -1, "items" to items.size, "shared" to true) }
            }
        }
    }
    val momentsVideoPlayerView = remember(context, momentsVideoPlayer) {
        momentsVideoPlayer?.let { createMomentPlayerView(context) }
    }
    DisposableEffect(momentsVideoPlayer, momentsVideoPlayerView) {
        if (momentsVideoPlayer == null) {
            return@DisposableEffect onDispose { }
        }
        onDispose {
            momentsVideoPlayerView?.player = null
            PerfProbe.incrementCounter("igloo_moments_player_release_count")
            PerfProbe.log(
                event = "moments_player_release",
            ) { mapOf("page" to -1, "items" to items.size, "shared" to true) }
            momentsVideoPlayer.release()
        }
    }
    var lifecycleStarted by remember {
        mutableStateOf(lifecycleOwner.lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED))
    }
    var loadedSlideshowAudioKey by remember { mutableStateOf<String?>(null) }
    var autoSwipeState by remember { mutableStateOf(autoSwipeDefault) }
    LaunchedEffect(autoSwipeDefault) { autoSwipeState = autoSwipeDefault }
    val effectiveAutoSwipe = forceAutoSwipe || autoSwipeState

    val safeStart = startIndex.coerceIn(0, items.lastIndex)
    val pagerState = rememberPagerState(
        initialPage = safeStart,
        pageCount = { items.size },
    )
    val currentIndex = pagerState.currentPage.coerceIn(0, items.lastIndex)
    val storyMode = exitOnEnd
    val storyProgressWindow = remember(storyMode, currentIndex, items) {
        if (storyMode) storyProgressWindow(items, currentIndex) else StoryProgressWindow(index = 0, count = 0)
    }
    LaunchedEffect(momentsVideoPlayer, currentIndex, items, lifecycleStarted) {
        val player = momentsVideoPlayer ?: return@LaunchedEffect
        val currentItem = items.getOrNull(currentIndex)
        val currentIsVideo = currentItem != null &&
            momentMediaMode(currentItem.mediaKind, currentItem.slideCount) == MomentMediaMode.Video
        if (!lifecycleStarted) {
            player.playWhenReady = false
            player.pause()
            return@LaunchedEffect
        }
        if (!currentIsVideo && player.mediaItemCount > 0) {
            PerfProbe.log(
                event = "moments_player_clear",
            ) { mapOf("reason" to "current_page_not_video", "page" to currentIndex, "shared" to true) }
            player.playWhenReady = false
            player.pause()
            player.clearMediaItems()
        }
    }

    LaunchedEffect(safeStart, items.size) {
        if (safeStart in items.indices && pagerState.currentPage != safeStart) {
            pagerState.scrollToPage(safeStart)
        }
    }

    if (onOpenAllMomentsGrid != null) {
        BackHandler { onOpenAllMomentsGrid() }
    }

    DisposableEffect(lifecycleOwner, slideshowAudioPlayer, momentsVideoPlayer) {
        val observer = LifecycleEventObserver { _, event ->
            when (event) {
                Lifecycle.Event.ON_STOP -> {
                    lifecycleStarted = false
                    slideshowAudioPlayer.playWhenReady = false
                    slideshowAudioPlayer.pause()
                    momentsVideoPlayer?.playWhenReady = false
                    momentsVideoPlayer?.pause()
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
                PerfProbe.log(
                    event = "moments_pager_page",
                ) {
                    mapOf(
                        "page" to page,
                        "items" to items.size,
                        "story_mode" to storyMode,
                    )
                }
                if (lastFiredPage != page) {
                    onIndexChange(page)
                    onViewEvent(items[page].videoId)
                    lastFiredPage = page
                }
            }
    }

    var muted by remember { mutableStateOf(muteDefault) }
    LaunchedEffect(muteDefault) { muted = muteDefault }
    LaunchedEffect(muted) {
        slideshowAudioPlayer.volume = if (muted) 0f else 1f
        momentsVideoPlayer?.volume = if (muted) 0f else 1f
    }
    var pendingUnfollowItem by remember { mutableStateOf<MomentItem?>(null) }

    LaunchedEffect(pagerState, items, mediaInventoryDao, syncDao, baseUrlProvider.baseUrl()) {
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
                PerfProbe.log(
                    event = "moments_pager_scroll_state",
                ) {
                    mapOf(
                        "page" to page,
                        "scrolling" to scrolling,
                        "lifecycle_started" to started,
                        "items" to items.size,
                    )
                }
                val currentItem = items.getOrNull(page)
                if (
                    currentItem == null ||
                    momentMediaMode(currentItem.mediaKind, currentItem.slideCount) != MomentMediaMode.Slideshow
                ) {
                    loadedSlideshowAudioKey = clearMomentAudio(slideshowAudioPlayer)
                    return@collectLatest
                }

                momentAudioUriFlow(
                    mediaInventoryDao = mediaInventoryDao,
                    syncDao = syncDao,
                    baseUrl = baseUrl,
                    videoId = currentItem.videoId,
                ).collect { audioUri ->
                    loadedSlideshowAudioKey = prepareMomentAudio(
                        player = slideshowAudioPlayer,
                        loadedKey = loadedSlideshowAudioKey,
                        videoId = currentItem.videoId,
                        audioUri = audioUri,
                    )
                    if (started && shouldPlayMomentPage(isCurrentPage = true, isScrollInProgress = scrolling) && audioUri !is MediaUri.Missing) {
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
        val next = if (storyMode) {
            val target = storyAdvanceTarget(
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
                animationSpec = tween(
                    durationMillis = AUTO_SWIPE_SCROLL_DURATION_MS,
                    easing = FastOutSlowInEasing,
                ),
            )
        } else {
            pagerState.scrollToPage(next)
        }
    }

    var initialSeekConsumed by remember(safeStart, startPositionMs) { mutableStateOf(startPositionMs <= 0L) }
    var transitionPoster by remember(initialTransitionPosterVideoId, initialTransitionPosterUri) {
        mutableStateOf(
            initialTransitionPosterVideoId
                ?.takeIf { initialTransitionPosterUri !is MediaUri.Missing }
                ?.let { MomentTransitionPoster(videoId = it, uri = initialTransitionPosterUri) },
        )
    }
    LaunchedEffect(transitionPoster?.videoId, currentIndex, items) {
        val poster = transitionPoster ?: return@LaunchedEffect
        if (items.getOrNull(currentIndex)?.videoId != poster.videoId) return@LaunchedEffect
        delay(MOMENTS_TRANSITION_POSTER_MIN_MS)
        transitionPoster = null
        onTransitionPosterDismissed(poster.videoId)
    }

    Box(
        modifier = modifier
            .fillMaxSize()
            .background(Color.Black),
    ) {
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
                isActive = lifecycleStarted && shouldPlayMomentPage(page == currentIndex, pagerState.isScrollInProgress),
                shouldPrepare = abs(page - currentIndex) <= MOMENTS_PREPARE_RADIUS,
                startPositionMs = if (page == safeStart && !initialSeekConsumed) startPositionMs else 0L,
                onInitialSeekConsumed = { if (page == safeStart) initialSeekConsumed = true },
                cursorTracking = cursorTracking,
                onCursorAdvance = onCursorAdvance,
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
                sharedVideoPlayer = momentsVideoPlayer,
                sharedPlayerView = momentsVideoPlayerView,
            )
        }
        if (storyMode) {
            HorizontalPager(
                state = pagerState,
                key = { page -> items[page].videoId },
                beyondViewportPageCount = 0,
                contentPadding = PaddingValues(0.dp),
                pageSpacing = 0.dp,
                modifier = Modifier
                    .fillMaxSize()
                    .clipToBounds(),
            ) { page ->
                Box(modifier = Modifier.fillMaxSize().clipToBounds()) {
                    pageContent(page)
                }
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

        val poster = transitionPoster
        if (poster != null && items.getOrNull(currentIndex)?.videoId == poster.videoId) {
            Box(
                modifier = Modifier
                    .fillMaxSize()
                    .background(Color.Black),
            ) {
                ThumbnailFallback(
                    thumbnailUri = poster.uri,
                    alphaOverride = 1f,
                    brokenIconTint = MaterialTheme.iglooColors.onSurfaceFaint,
                )
            }
        }
        if (storyMode) {
            StoryProgressControl(
                currentPage = storyProgressWindow.index,
                pageCount = storyProgressWindow.count,
                modifier = Modifier
                    .align(Alignment.TopCenter)
                    .zIndex(3f)
                    .padding(top = overlayIdentityTopPadding()),
            )
        } else if (activeTab != null && onTabSelected != null) {
            MomentsTabControl(
                activeTab = activeTab,
                onTabSelected = onTabSelected,
                modifier = Modifier
                    .align(Alignment.TopCenter)
                    .zIndex(2f)
                    .padding(top = overlayIdentityTopPadding()),
            )
        }
        pendingUnfollowItem?.let { target ->
            val label = target.authorDisplayName
                ?.takeIf { it.isNotBlank() }
                ?: target.authorHandle.takeIf { it.isNotBlank() }
                ?: target.channelId
            AlertDialog(
                onDismissRequest = { pendingUnfollowItem = null },
                title = { Text(stringResource(R.string.confirm_unfollow_account_title)) },
                text = {
                    Text(
                        stringResource(
                            R.string.confirm_unfollow_channel_delete_media_body,
                            label,
                        ),
                    )
                },
                confirmButton = {
                    TextButton(
                        onClick = {
                            pendingUnfollowItem = null
                            onUnfollowChannel(target.channelId)
                        },
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
