package com.screwy.igloo.ui.component

import android.view.LayoutInflater
import androidx.activity.compose.BackHandler
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectHorizontalDragGestures
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.PressInteraction
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.VerticalPager
import androidx.compose.foundation.systemGestureExclusion
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBars
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.VolumeOff
import androidx.compose.material.icons.automirrored.filled.VolumeUp
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Bookmark
import androidx.compose.material.icons.filled.BrokenImage
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.PlayCircle
import androidx.compose.material.icons.filled.Share
import androidx.compose.material.icons.outlined.BookmarkBorder
import androidx.compose.material.icons.outlined.PlayCircleOutline
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
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
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Shadow
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.compose.ui.zIndex
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.media3.common.MediaItem
import androidx.media3.common.Player
import androidx.media3.common.VideoSize
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.ui.AspectRatioFrameLayout
import androidx.media3.ui.PlayerView
import coil3.compose.AsyncImage
import com.screwy.igloo.R
import com.screwy.igloo.data.stripPlatformPrefix
import com.screwy.igloo.data.dao.AndroidSyncDao
import com.screwy.igloo.data.dao.BookmarkDao
import com.screwy.igloo.data.dao.MediaInventoryDao
import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.data.entity.MediaInventoryEntity
import com.screwy.igloo.log.Logger
import com.screwy.igloo.media.MediaResolvers
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.media.OwnerKind
import com.screwy.igloo.media.ownerKindFromChannelId
import com.screwy.igloo.net.IglooHostProvider
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.net.auth.AuthTokenProvider
import com.screwy.igloo.player.buildIglooPlayer
import com.screwy.igloo.ui.nav.LocalDrawerController
import com.screwy.igloo.ui.theme.IglooColors
import com.screwy.igloo.ui.theme.iglooColors
import java.io.File
import kotlin.math.abs
import kotlin.math.max
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.launch
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

private const val MOMENTS_PREPARE_RADIUS = 1
private const val AUTO_SWIPE_SCROLL_DURATION_MS = 850
private const val MOMENT_STILL_ADVANCE_DELAY_MS = 3_000L
private const val MOMENTS_TRANSITION_POSTER_MIN_MS = 180L
private const val MOMENTS_STOP_OLD_PAGE_DELAY_MS = 200L
private const val MomentCaptionBaseBottomPaddingDp = 12
private const val MomentVideoCaptionBaseBottomPaddingDp = 16
private const val MomentCollapsedCaptionStartPaddingDp = 8
private const val MomentCollapsedCaptionMaxLines = 2

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

private fun momentStreamUrl(baseUrl: String, videoId: String): String? {
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
        buildIglooPlayer(context, authTokens, iglooHostProvider)
    }
    DisposableEffect(slideshowAudioPlayer) {
        onDispose {
            slideshowAudioPlayer.release()
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
    // platform pager owns drag/fling physics; media lifecycle follows its
    // current page so we keep the page-owned players without custom scroll code.
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
    LaunchedEffect(muted) {
        slideshowAudioPlayer.volume = if (muted) 0f else 1f
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

internal fun storyProgressWindow(items: List<MomentItem>, currentIndex: Int): StoryProgressWindow {
    if (items.isEmpty() || currentIndex !in items.indices) return StoryProgressWindow(index = 0, count = 0)
    val channelId = items[currentIndex].channelId
    var start = currentIndex
    while (start > 0 && items[start - 1].channelId == channelId) {
        start -= 1
    }
    var end = currentIndex
    while (end < items.lastIndex && items[end + 1].channelId == channelId) {
        end += 1
    }
    return StoryProgressWindow(
        index = currentIndex - start,
        count = end - start + 1,
    )
}

@Composable
private fun StoryProgressControl(
    currentPage: Int,
    pageCount: Int,
    modifier: Modifier = Modifier,
) {
    if (pageCount <= 0) return
    Column(
        modifier = modifier
            .fillMaxWidth()
            .padding(horizontal = 12.dp, vertical = 4.dp),
        verticalArrangement = Arrangement.spacedBy(5.dp),
        horizontalAlignment = Alignment.End,
    ) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(4.dp),
        ) {
            repeat(pageCount) { index ->
                val alpha = when {
                    index < currentPage -> 0.82f
                    index == currentPage -> 1f
                    else -> 0.34f
                }
                val color = Color.White.copy(alpha = alpha)
                Box(
                    modifier = Modifier
                        .weight(1f)
                        .height(3.dp)
                        .clip(RoundedCornerShape(999.dp))
                        .background(color),
                )
            }
        }
    }
}

@Composable
private fun MomentsTabControl(
    activeTab: String,
    onTabSelected: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    val allLabel = stringResource(R.string.shorts_tab_all)
    val followingLabel = stringResource(R.string.shorts_tab_following)
    val storiesLabel = stringResource(R.string.shorts_tab_stories)
    CompositionLocalProvider(LocalLayoutDirection provides LayoutDirection.Ltr) {
        Row(
            modifier = modifier
                .padding(horizontal = 16.dp, vertical = 4.dp),
            horizontalArrangement = Arrangement.spacedBy(16.dp),
            verticalAlignment = Alignment.Top,
        ) {
            MomentsTabPill("all", allLabel, activeTab == "all", onTabSelected)
            MomentsTabPill("following", followingLabel, activeTab == "following", onTabSelected)
            MomentsTabPill("stories", storiesLabel, activeTab == "stories", onTabSelected)
        }
    }
}

@Composable
private fun MomentsTabPill(
    tab: String,
    label: String,
    active: Boolean,
    onTabSelected: (String) -> Unit,
) {
    Box(
        modifier = Modifier
            .width(116.dp)
            .height(34.dp)
            .clickable { onTabSelected(tab) }
            .padding(horizontal = 2.dp),
    ) {
        Text(
            text = label,
            color = if (active) Color.White else Color.White.copy(alpha = 0.68f),
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            style = MaterialTheme.typography.titleSmall.copy(
                fontWeight = if (active) FontWeight.Bold else FontWeight.SemiBold,
                shadow = DropShadow,
            ),
            modifier = Modifier.align(Alignment.TopCenter),
        )
        if (active) {
            Box(
                modifier = Modifier
                    .align(Alignment.BottomCenter)
                    .size(5.dp)
                    .clip(CircleShape)
                    .background(MaterialTheme.iglooColors.primary),
            )
        }
    }
}

private fun prepareMomentVideo(
    player: ExoPlayer,
    loadedKey: String?,
    item: MomentItem,
    pageIndex: Int,
    streamUri: MediaUri,
    seedPositionMs: Long = 0L,
    logger: Logger,
): String? {
    val targetLoadKey = momentStreamLoadKey(item.videoId, streamUri)
    if (targetLoadKey == null) {
        if (loadedKey != null || player.mediaItemCount > 0) {
            logger.debugMoment("moments_player_clear_missing_stream") {
                momentVideoDebugFields(
                    item = item,
                    pageIndex = pageIndex,
                    streamUri = streamUri,
                    player = player,
                    loadedKey = loadedKey,
                    targetLoadKey = null,
                    seedPositionMs = seedPositionMs,
                )
            }
            player.playWhenReady = false
            player.pause()
            player.clearMediaItems()
        }
        return null
    }

    if (
        loadedKey == targetLoadKey &&
        player.mediaItemCount > 0 &&
        player.currentMediaItem?.mediaId == item.videoId &&
        player.playbackState != Player.STATE_ENDED
    ) {
        return loadedKey
    }

    val mediaItem = momentPlayerMediaItem(item.videoId, streamUri) ?: return null
    logger.debugMoment("moments_player_prepare_page") {
        momentVideoDebugFields(
            item = item,
            pageIndex = pageIndex,
            streamUri = streamUri,
            player = player,
            loadedKey = loadedKey,
            targetLoadKey = targetLoadKey,
            seedPositionMs = seedPositionMs,
        )
    }
    replaceMomentPlayerMediaItem(player, mediaItem, seedPositionMs)
    return targetLoadKey
}

private fun replaceMomentPlayerMediaItem(
    player: ExoPlayer,
    mediaItem: MediaItem,
    startPositionMs: Long,
) {
    if (player.mediaItemCount > 0) {
        player.stop()
        player.clearMediaItems()
    }
    player.setMediaItem(mediaItem, startPositionMs)
    player.prepare()
}

@Composable
private fun MomentPage(
    pageIndex: Int,
    item: MomentItem,
    storyMode: Boolean,
    muted: Boolean,
    onMuteToggle: () -> Unit,
    autoSwipe: Boolean,
    onAutoSwipeToggle: () -> Unit,
    showAutoSwipeControl: Boolean,
    isActive: Boolean,
    shouldPrepare: Boolean,
    startPositionMs: Long,
    onInitialSeekConsumed: () -> Unit,
    cursorTracking: Boolean,
    onCursorAdvance: (videoId: String, positionMs: Long) -> Unit,
    onAutoAdvance: () -> Unit,
    onChannelClick: (channelId: String) -> Unit,
    onStoryClick: (channelId: String, firstVideoId: String) -> Unit,
    onMentionClick: (handle: String) -> Unit,
    onBookmarkToggle: (MomentItem) -> Unit,
    onRequestBookmarkSheet: (MomentItem) -> Unit,
    onShare: (MomentItem) -> Unit,
    onFollowChannel: (channelId: String) -> Unit,
    onRequestUnfollowChannel: (MomentItem) -> Unit,
    onSwipeLeftToChannel: (channelId: String) -> Unit,
    onSwipeRightFromEdge: () -> Unit,
    logger: Logger,
) {
    val colors = MaterialTheme.iglooColors
    val density = LocalDensity.current
    val swipeThresholdPx = with(density) { 80.dp.toPx() }
    val resolvers: MediaResolvers = koinInject()
    val bookmarkDao: BookmarkDao = koinInject()
    val baseUrlProvider: ServerBaseUrlProvider = koinInject()

    var dragAccumulator by remember(item.videoId) { mutableStateOf(0f) }
    // Description expand state resets on page change — simpler than persisting
    // per-video. `key1 = item.videoId` means swiping to a new moment collapses
    // the caption, matching the screenshot's default state.
    var expanded by remember(item.videoId) { mutableStateOf(false) }

    val mediaMode = remember(item.mediaKind, item.slideCount) {
        momentMediaMode(item.mediaKind, item.slideCount)
    }
    var manualSlideAdvanceTick by remember(item.videoId) { mutableIntStateOf(0) }
    val ownerKind = remember(item.ownerKind, item.channelId) { item.ownerKind ?: ownerKindFromChannelId(item.channelId) }
    val baseUrl = baseUrlProvider.baseUrl()
    val initialThumbnailUri = remember(
        item.videoId,
        item.mediaOwnerId,
        item.fallbackThumbnailPath,
        item.fallbackThumbnailUri,
        item.mediaKind,
        item.slideCount,
        ownerKind,
        baseUrl,
    ) {
        resolveInitialMomentThumbnailUri(
            videoId = item.mediaOwnerId,
            thumbnailPath = item.fallbackThumbnailPath,
            mediaKind = item.mediaKind,
            slideCount = item.slideCount,
            ownerKind = ownerKind,
            baseUrl = baseUrl,
            fallbackThumbnailUri = item.fallbackThumbnailUri,
        )
    }
    val resolvedThumbnailUri by resolvers.thumbnailForPostFlow(item.mediaOwnerId, ownerKind)
        .collectAsState(initial = initialThumbnailUri)
    val thumbnailUri = if (resolvedThumbnailUri is MediaUri.Missing) initialThumbnailUri else resolvedThumbnailUri
    val bookmarkRow by bookmarkDao.getByIdFlow(item.videoId).collectAsState(initial = null)
    val isBookmarked = bookmarkRow != null
    val bookmarkItem = if (isBookmarked == item.isBookmarked) item else item.copy(isBookmarked = isBookmarked)

    val pageModifier = if (storyMode) {
        Modifier
            .fillMaxSize()
            .background(Color.Black)
    } else {
        Modifier
            .fillMaxSize()
            .background(Color.Black)
            .pointerInput(item.videoId) {
                detectHorizontalDragGestures(
                    onDragEnd = {
                        if (isLeftSwipe(dragAccumulator, swipeThresholdPx)) {
                            onSwipeLeftToChannel(item.channelId)
                        }
                        dragAccumulator = 0f
                    },
                    onDragCancel = { dragAccumulator = 0f },
                    onHorizontalDrag = { _, delta -> dragAccumulator += delta },
                )
            }
    }

    Box(
        modifier = pageModifier,
    ) {
        when (mediaMode) {
            MomentMediaMode.Image -> MomentImageSurface(
                videoId = item.mediaOwnerId,
                thumbnailUri = thumbnailUri,
                isActive = isActive,
                autoSwipe = autoSwipe,
                onAutoAdvance = onAutoAdvance,
                modifier = Modifier.fillMaxSize(),
            )
            MomentMediaMode.Slideshow -> MomentSlideshowSurface(
                videoId = item.mediaOwnerId,
                slideCount = momentSlideCount(item.mediaKind, item.slideCount),
                thumbnailUri = thumbnailUri,
                isActive = isActive,
                autoSwipe = autoSwipe,
                onAutoAdvance = onAutoAdvance,
                manualAdvanceTick = manualSlideAdvanceTick,
                onManualAdvanceAtEnd = onAutoAdvance,
                modifier = Modifier.fillMaxSize(),
            )
            MomentMediaMode.Video -> {
                MomentVideoLayer(
                    pageIndex = pageIndex,
                    item = item,
                    thumbnailUri = thumbnailUri,
                    muted = muted,
                    isActive = isActive,
                    shouldPrepare = shouldPrepare,
                    autoSwipe = autoSwipe,
                    onAutoAdvance = onAutoAdvance,
                    startPositionMs = startPositionMs,
                    onInitialSeekConsumed = onInitialSeekConsumed,
                    cursorTracking = cursorTracking,
                    onCursorAdvance = onCursorAdvance,
                    logger = logger,
                    storyMode = storyMode,
                    modifier = Modifier.fillMaxSize(),
                )
            }
        }

        if (storyMode) {
            StoryTapAdvanceLayer(
                onTap = {
                    if (mediaMode == MomentMediaMode.Slideshow) {
                        manualSlideAdvanceTick++
                    } else {
                        onAutoAdvance()
                    }
                },
                modifier = Modifier.fillMaxSize(),
            )
        }

        // Top dim gradient keeps the TikTok-style tab row legible against any thumbnail.
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .height(96.dp)
                .background(
                    Brush.verticalGradient(
                        colors = listOf(Color.Black.copy(alpha = 0.45f), Color.Transparent),
                    ),
                ),
        )

        if (mediaMode != MomentMediaMode.Video) {
            MomentBottomScrim(modifier = Modifier.align(Alignment.BottomCenter))
        }

        // Right-edge action rail — avatar/follow state plus mute / autoplay / bookmark / share.
        // The rail sits lower than before so the avatar occupies the social-action position and
        // the first control starts where the older middle controls used to sit.
        //
        // Play/pause is not a side-action: tap the video surface to toggle. The
        // auto-swipe button controls whether the player advances to the next
        // short or loops the current one when it ends.
        Column(
            modifier = Modifier
                .align(Alignment.BottomEnd)
                .padding(end = 12.dp, bottom = 164.dp),
            verticalArrangement = Arrangement.spacedBy(18.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            val muteLabel = stringResource(R.string.action_mute)
            val unmuteLabel = stringResource(R.string.action_unmute)
            val autoSwipeStateLabel = stringResource(
                R.string.moments_auto_swipe_state,
                stringResource(if (autoSwipe) R.string.state_on else R.string.state_off),
            )
            val manageBookmarkLabel = stringResource(R.string.action_manage_bookmark)
            val addBookmarkLabel = stringResource(R.string.action_bookmark)
            val shareLabel = stringResource(R.string.action_share)
            MomentRailAvatar(
                item = item,
                onChannelClick = onChannelClick,
                onStoryClick = onStoryClick,
                onFollowChannel = onFollowChannel,
                onRequestUnfollowChannel = onRequestUnfollowChannel,
                colors = colors,
            )
            ShadowIcon(
                if (muted) Icons.AutoMirrored.Filled.VolumeOff else Icons.AutoMirrored.Filled.VolumeUp,
                if (muted) unmuteLabel else muteLabel,
                onMuteToggle,
                muted,
                colors.primary,
            )
            if (showAutoSwipeControl) {
                ShadowIcon(
                    if (autoSwipe) Icons.Filled.PlayCircle else Icons.Outlined.PlayCircleOutline,
                    autoSwipeStateLabel,
                    onAutoSwipeToggle,
                    autoSwipe,
                    colors.primary,
                )
            }
            ShadowIcon(
                if (isBookmarked) Icons.Filled.Bookmark else Icons.Outlined.BookmarkBorder,
                if (isBookmarked) manageBookmarkLabel else addBookmarkLabel,
                { onRequestBookmarkSheet(bookmarkItem) },
                isBookmarked,
                colors.primary,
            )
            ShadowIcon(
                Icons.Filled.Share,
                shareLabel,
                { if (bookmarkItem.canonicalUrl.isNotBlank()) onShare(bookmarkItem) },
                false,
                colors.primary,
                enabled = bookmarkItem.canonicalUrl.isNotBlank(),
            )
        }

        // Bottom overlay — timestamp + description. Tapping overflowing text
        // only changes the description line limit; the caption stays anchored.
        val captionBaseBottomPadding = momentCaptionBaseBottomPaddingDp(mediaMode).dp
        val captionBottomPadding = if (storyMode) {
            storyCaptionBottomPadding(captionBaseBottomPadding)
        } else {
            captionBaseBottomPadding
        }
        CollapsedDescription(
            item = item,
            expanded = expanded,
            onMentionClick = onMentionClick,
            onChannelClick = onChannelClick,
            onExpandedChange = { expanded = it },
            modifier = Modifier
                .align(Alignment.BottomStart)
                .padding(bottom = captionBottomPadding),
        )

        if (!storyMode) {
            MomentDrawerGestureHandle(onOpenDrawer = onSwipeRightFromEdge)
        }
    }
}

@Composable
private fun storyCaptionBottomPadding(base: Dp): Dp = with(LocalDensity.current) {
    max(base.value, WindowInsets.navigationBars.getBottom(this).toDp().value + 12f).dp
}

@Composable
private fun StoryTapAdvanceLayer(
    onTap: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Box(
        modifier = modifier.pointerInput(onTap) {
            detectTapGestures(onTap = { onTap() })
        },
    )
}

@Composable
private fun BoxScope.MomentVideoLayer(
    pageIndex: Int,
    item: MomentItem,
    thumbnailUri: MediaUri,
    muted: Boolean,
    isActive: Boolean,
    shouldPrepare: Boolean,
    autoSwipe: Boolean,
    onAutoAdvance: () -> Unit,
    startPositionMs: Long,
    onInitialSeekConsumed: () -> Unit,
    cursorTracking: Boolean,
    onCursorAdvance: (videoId: String, positionMs: Long) -> Unit,
    logger: Logger,
    storyMode: Boolean,
    modifier: Modifier = Modifier,
) {
    val context = LocalContext.current
    val authTokens: AuthTokenProvider = koinInject()
    val iglooHostProvider: IglooHostProvider = koinInject()
    val resolvers: MediaResolvers = koinInject()
    val baseUrlProvider: ServerBaseUrlProvider = koinInject()
    val baseUrl = baseUrlProvider.baseUrl()
    val initialStreamUri = remember(baseUrl, item.mediaOwnerId) {
        momentStreamUrl(baseUrl, item.mediaOwnerId)
            ?.let(MediaUri::Remote)
            ?: MediaUri.Missing
    }
    val resolvedStreamUri by resolvers.videoStreamFlow(item.mediaOwnerId)
        .collectAsState(initial = initialStreamUri)
    val candidateStreamUri = if (resolvedStreamUri is MediaUri.Missing) {
        initialStreamUri
    } else {
        resolvedStreamUri
    }
    var playbackStreamUri by remember(item.videoId) { mutableStateOf(candidateStreamUri) }
    LaunchedEffect(candidateStreamUri, isActive, item.videoId) {
        if (!isActive || playbackStreamUri is MediaUri.Missing) {
            playbackStreamUri = candidateStreamUri
        }
    }

    val player = remember(item.videoId, authTokens.bearerTokenSync()) {
        buildIglooPlayer(context, authTokens, iglooHostProvider).apply {
            repeatMode = Player.REPEAT_MODE_OFF
        }
    }
    DisposableEffect(player) {
        onDispose { player.release() }
    }
    var loadedKey by remember(item.videoId) { mutableStateOf<String?>(null) }
    var surfaceState by remember(item.videoId) { mutableStateOf(MomentVideoSurfaceState()) }
    MomentVideoDebugTelemetry(
        pageIndex = pageIndex,
        item = item,
        streamUri = playbackStreamUri,
        player = player,
        loadedKey = loadedKey,
        isActive = isActive,
        shouldPrepare = shouldPrepare,
        logger = logger,
    )

    LaunchedEffect(player, playbackStreamUri, item.videoId, shouldPrepare) {
        if (!shouldPrepare) {
            player.playWhenReady = false
            player.pause()
            player.clearMediaItems()
            loadedKey = null
            surfaceState = MomentVideoSurfaceState()
            return@LaunchedEffect
        }
        val seedPosition = if (startPositionMs > 0L && loadedKey == null) startPositionMs else 0L
        val nextLoadedKey = prepareMomentVideo(
            player = player,
            loadedKey = loadedKey,
            item = item,
            pageIndex = pageIndex,
            streamUri = playbackStreamUri,
            seedPositionMs = seedPosition,
            logger = logger,
        )
        loadedKey = nextLoadedKey
        if (seedPosition > 0L && nextLoadedKey != null) onInitialSeekConsumed()
    }

    LaunchedEffect(player, muted) {
        player.volume = if (muted) 0f else 1f
    }
    LaunchedEffect(player, isActive, shouldPrepare, loadedKey) {
        if (isActive && shouldPrepare && loadedKey != null) {
            player.playWhenReady = true
        } else {
            delay(MOMENTS_STOP_OLD_PAGE_DELAY_MS)
            player.playWhenReady = false
            player.pause()
            if (player.mediaItemCount > 0) player.seekTo(0L)
        }
    }

    DisposableEffect(player, item.videoId, autoSwipe, isActive) {
        val listener = object : Player.Listener {
            override fun onPlaybackStateChanged(state: Int) {
                if (state != Player.STATE_ENDED) return
                if (player.currentMediaItem?.mediaId != item.videoId) return
                if (autoSwipe) {
                    onAutoAdvance()
                    return
                }
                logger.debugMoment("moments_player_loop_restart") {
                    momentVideoDebugFields(
                        item = item,
                        pageIndex = pageIndex,
                        streamUri = playbackStreamUri,
                        player = player,
                        loadedKey = loadedKey,
                        targetLoadKey = loadedKey,
                    )
                }
                player.seekTo(0L)
                player.playWhenReady = isActive
            }
        }
        player.addListener(listener)
        onDispose { player.removeListener(listener) }
    }

    LaunchedEffect(cursorTracking, isActive, player, item.videoId) {
        if (!cursorTracking) return@LaunchedEffect
        while (true) {
            delay(2_000L)
            if (isActive && player.currentMediaItem?.mediaId == item.videoId) {
                onCursorAdvance(item.videoId, player.currentPosition)
            }
        }
    }

    val remoteOffline = isIglooRemoteOffline(playbackStreamUri)
    val showFallback = shouldShowMomentThumbnailFallback(
        remoteOffline = remoteOffline,
        surfaceState = surfaceState,
    )

    Box(
        modifier = modifier
            .fillMaxSize()
            .background(Color.Black),
    ) {
        if (showFallback) {
            ThumbnailFallback(
                thumbnailUri = thumbnailUri,
                alphaOverride = if (remoteOffline) 0.55f else 1f,
                brokenIconTint = MaterialTheme.iglooColors.onSurfaceFaint,
            )
        }
        if (shouldPrepare && playbackStreamUri !is MediaUri.Missing && !remoteOffline) {
            VideoSurface(
                player = player,
                mediaKey = item.videoId,
                onStateChange = { surfaceState = it },
                modifier = Modifier.fillMaxSize(),
            )
        }
        if (!storyMode && isActive && playbackStreamUri !is MediaUri.Missing && !remoteOffline) {
            MomentVideoGestureLayer(player = player, modifier = Modifier.fillMaxSize())
        }
        MomentBottomScrim(modifier = Modifier.align(Alignment.BottomCenter))
        if (
            playbackStreamUri !is MediaUri.Missing &&
            !remoteOffline &&
            shouldPrepare &&
            surfaceState.hasExpectedMedia &&
            surfaceState.renderedFirstFrame
        ) {
            MomentsVideoProgressBar(
                player = player,
                modifier = Modifier
                    .align(Alignment.BottomCenter)
                    .padding(bottom = 8.dp),
            )
        }
        if (remoteOffline) DownloadPendingBadge()
    }
}

@Composable
private fun MomentBottomScrim(modifier: Modifier = Modifier) {
    Box(
        modifier = modifier
            .fillMaxWidth()
            .height(220.dp)
            .background(
                Brush.verticalGradient(
                    colors = listOf(
                        Color.Transparent,
                        Color.Black.copy(alpha = 0.28f),
                        Color.Black.copy(alpha = 0.74f),
                    ),
                ),
            ),
    )
}

@Composable
private fun BoxScope.MomentVideoGestureLayer(
    player: ExoPlayer,
    modifier: Modifier = Modifier,
) {
    var seekIndicator by remember(player) { mutableStateOf<SeekIndicator?>(null) }
    Box(
        modifier = modifier.pointerInput(player) {
            detectTapGestures(
                onTap = {
                    seekIndicator = null
                    player.playWhenReady = !player.playWhenReady
                },
                onDoubleTap = { offset ->
                    val isLeft = offset.x < size.width / 2
                    val deltaMs = if (isLeft) -5_000L else 5_000L
                    val duration = player.duration.coerceAtLeast(0L)
                    val target = (player.currentPosition + deltaMs).coerceIn(0L, duration)
                    player.seekTo(target)
                    seekIndicator = if (isLeft) SeekIndicator.Back else SeekIndicator.Forward
                },
            )
        },
    )
    if (seekIndicator != null) {
        val indicator = seekIndicator
        LaunchedEffect(indicator) {
            delay(600L)
            if (seekIndicator == indicator) seekIndicator = null
        }
        val isBack = indicator == SeekIndicator.Back
        Box(
            modifier = Modifier
                .align(if (isBack) Alignment.CenterStart else Alignment.CenterEnd)
                .padding(horizontal = 32.dp)
                .background(Color.Black.copy(alpha = 0.55f), androidx.compose.foundation.shape.CircleShape)
                .padding(horizontal = 16.dp, vertical = 10.dp),
        ) {
            Text(
                text = if (isBack) "-5s" else "+5s",
                color = Color.White,
                style = MaterialTheme.typography.labelLarge,
            )
        }
    }
}

@Composable
private fun BoxScope.MomentDrawerGestureHandle(
    onOpenDrawer: () -> Unit,
) {
    val thresholdPx = with(LocalDensity.current) { 56.dp.toPx() }
    Box(
        modifier = Modifier
            .align(Alignment.CenterStart)
            .fillMaxHeight()
            .width(96.dp)
            .systemGestureExclusion()
            .pointerInput(onOpenDrawer, thresholdPx) {
                var totalDragX = 0f
                var opened = false
                detectHorizontalDragGestures(
                    onDragStart = {
                        totalDragX = 0f
                        opened = false
                    },
                    onDragCancel = {
                        totalDragX = 0f
                        opened = false
                    },
                    onDragEnd = {
                        totalDragX = 0f
                        opened = false
                    },
                    onHorizontalDrag = { change, delta ->
                        totalDragX = (totalDragX + delta).coerceAtLeast(0f)
                        if (totalDragX > 0f) change.consume()
                        if (!opened && totalDragX >= thresholdPx) {
                            opened = true
                            onOpenDrawer()
                        }
                    },
                )
            },
    )
}

/** Which side of the screen the user double-tapped — drives the seek indicator. */
private enum class SeekIndicator { Back, Forward }

private fun momentAuthorLabel(item: MomentItem): String {
    val normalizedHandle = normalizeHandle(item.authorHandle)
    return displayLabel(
        primary = item.authorDisplayName,
        handle = normalizedHandle,
        fallback = stripPlatformPrefix(item.channelId),
    )
}

@Composable
private fun momentRepostLabel(item: MomentItem): String? {
    val author = item.repostAuthorLabel?.takeIf { it.isNotBlank() } ?: return null
    return when {
        item.repostOtherCount <= 0 -> stringResource(R.string.feed_reposted_single, author)
        item.repostOtherCount == 1 -> stringResource(R.string.feed_reposted_one_other, author)
        else -> stringResource(R.string.feed_reposted_many_others, author, item.repostOtherCount)
    }
}

private fun momentSlideUrl(baseUrl: String, videoId: String, index: Int): String? {
    val root = baseUrl.trim().trimEnd('/')
    if (root.isBlank()) return null
    return "$root/api/media/slide/$videoId/$index"
}

private fun momentAudioUrl(baseUrl: String, videoId: String): String? {
    val root = baseUrl.trim().trimEnd('/')
    if (root.isBlank()) return null
    return "$root/api/media/audio/$videoId"
}

@Composable
private fun MomentImageSurface(
    videoId: String,
    thumbnailUri: MediaUri,
    isActive: Boolean,
    autoSwipe: Boolean,
    onAutoAdvance: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val baseUrlProvider: ServerBaseUrlProvider = koinInject()
    val mediaInventoryDao: MediaInventoryDao = koinInject()
    val syncDao: AndroidSyncDao = koinInject()
    val baseUrl = baseUrlProvider.baseUrl()
    val slideUris by momentSlideMediaFlow(
        mediaInventoryDao = mediaInventoryDao,
        syncDao = syncDao,
        baseUrl = baseUrl,
        videoId = videoId,
        fallbackSlideCount = 1,
    ).collectAsState(initial = emptyList())

    MomentStillImage(
        mediaUri = slideUris.firstOrNull() ?: thumbnailUri,
        contentDescription = stringResource(R.string.content_description_moment_image),
        modifier = modifier,
    )

    LaunchedEffect(videoId, isActive, autoSwipe) {
        if (!isActive || !autoSwipe) return@LaunchedEffect
        delay(MOMENT_STILL_ADVANCE_DELAY_MS)
        onAutoAdvance()
    }
}

@Composable
private fun MomentSlideshowSurface(
    videoId: String,
    slideCount: Int,
    thumbnailUri: MediaUri,
    isActive: Boolean,
    autoSwipe: Boolean,
    onAutoAdvance: () -> Unit,
    manualAdvanceTick: Int = 0,
    onManualAdvanceAtEnd: () -> Unit = {},
    modifier: Modifier = Modifier,
) {
    val baseUrlProvider: ServerBaseUrlProvider = koinInject()
    val mediaInventoryDao: MediaInventoryDao = koinInject()
    val syncDao: AndroidSyncDao = koinInject()
    val baseUrl = baseUrlProvider.baseUrl()
    val slideUris by momentSlideMediaFlow(
        mediaInventoryDao = mediaInventoryDao,
        syncDao = syncDao,
        baseUrl = baseUrl,
        videoId = videoId,
        fallbackSlideCount = slideCount,
    ).collectAsState(initial = emptyList())
    val effectiveSlideUris = remember(slideUris, thumbnailUri) {
        if (slideUris.isNotEmpty()) slideUris else listOf(thumbnailUri)
    }
    val effectiveSlideCount = effectiveSlideUris.size.coerceAtLeast(1)
    val pagerState = rememberPagerState(pageCount = { effectiveSlideCount })

    LaunchedEffect(manualAdvanceTick, effectiveSlideCount) {
        if (manualAdvanceTick == 0 || effectiveSlideCount <= 0) return@LaunchedEffect
        val page = pagerState.currentPage
        if (page < effectiveSlideCount - 1) {
            pagerState.animateScrollToPage(
                page = page + 1,
                animationSpec = tween(
                    durationMillis = AUTO_SWIPE_SCROLL_DURATION_MS,
                    easing = FastOutSlowInEasing,
                ),
            )
        } else {
            onManualAdvanceAtEnd()
        }
    }

    Box(
        modifier = modifier
            .fillMaxSize()
            .background(Color.Black),
    ) {
        if (effectiveSlideUris.isNotEmpty()) {
            HorizontalPager(
                state = pagerState,
                modifier = Modifier.fillMaxSize(),
            ) { page ->
                MomentStillImage(
                    mediaUri = effectiveSlideUris[page],
                    contentDescription = stringResource(R.string.content_description_slide_number, page + 1),
                    modifier = Modifier.fillMaxSize(),
                )
            }
        } else {
            MomentStillImage(
                mediaUri = thumbnailUri,
                contentDescription = stringResource(R.string.content_description_slide_number, 1),
                modifier = Modifier.fillMaxSize(),
            )
        }

        if (effectiveSlideCount > 1) {
            MomentSlideDots(
                currentPage = pagerState.currentPage,
                pageCount = effectiveSlideCount,
                modifier = Modifier
                    .align(Alignment.BottomCenter)
                    .padding(bottom = 96.dp),
            )
        }
    }

    LaunchedEffect(videoId, slideCount, isActive, autoSwipe, effectiveSlideCount) {
        if (!isActive || effectiveSlideCount <= 0) return@LaunchedEffect
        while (true) {
            val pageAtStart = pagerState.currentPage
            delay(MOMENT_STILL_ADVANCE_DELAY_MS)
            if (!isActive) return@LaunchedEffect
            if (pagerState.isScrollInProgress || pagerState.currentPage != pageAtStart) {
                continue
            }

            if (pageAtStart < effectiveSlideCount - 1) {
                pagerState.animateScrollToPage(
                    page = pageAtStart + 1,
                    animationSpec = tween(
                        durationMillis = AUTO_SWIPE_SCROLL_DURATION_MS,
                        easing = FastOutSlowInEasing,
                    ),
                )
            } else if (autoSwipe) {
                onAutoAdvance()
                return@LaunchedEffect
            } else {
                pagerState.animateScrollToPage(
                    page = 0,
                    animationSpec = tween(
                        durationMillis = AUTO_SWIPE_SCROLL_DURATION_MS,
                        easing = FastOutSlowInEasing,
                    ),
                )
            }
        }
    }
}

@Composable
private fun MomentStillImage(
    mediaUri: MediaUri,
    contentDescription: String,
    modifier: Modifier = Modifier,
) {
    Box(
        modifier = modifier
            .fillMaxSize()
            .background(Color.Black),
        contentAlignment = Alignment.Center,
    ) {
        when (mediaUri) {
            is MediaUri.Local -> AsyncImage(
                model = mediaUri.file,
                contentDescription = contentDescription,
                modifier = Modifier.fillMaxSize(),
                contentScale = momentFitWidthContentScale(),
            )
            is MediaUri.Remote -> AsyncImage(
                model = rememberRemoteImageModel(mediaUri.url),
                contentDescription = contentDescription,
                modifier = Modifier.fillMaxSize(),
                contentScale = momentFitWidthContentScale(),
            )
            MediaUri.Missing -> Icon(
                imageVector = Icons.Default.BrokenImage,
                contentDescription = stringResource(R.string.content_description_missing_media),
                tint = Color.White.copy(alpha = 0.70f),
                modifier = Modifier.size(40.dp),
            )
        }
    }
}

private fun momentSlideMediaFlow(
    mediaInventoryDao: MediaInventoryDao,
    syncDao: AndroidSyncDao,
    baseUrl: String,
    videoId: String,
    fallbackSlideCount: Int,
) = combine(
    mediaInventoryDao.forOwnerFlow(videoId),
    syncDao.latestVerifiedAssetsForOwnerFlow(videoId, listOf("post_media")),
) { rows, syncRows ->
    resolveMomentSlideUris(
        rows = rows,
        baseUrl = baseUrl,
        videoId = videoId,
        fallbackSlideCount = fallbackSlideCount,
        syncRows = syncRows,
    )
}
    .distinctUntilChanged()

private fun momentAudioUriFlow(
    mediaInventoryDao: MediaInventoryDao,
    syncDao: AndroidSyncDao,
    baseUrl: String,
    videoId: String,
) = combine(
    mediaInventoryDao.forOwnerFlow(videoId),
    syncDao.latestVerifiedAssetsForOwnerFlow(videoId, listOf("post_audio", "audio")),
) { rows, syncRows ->
    resolveMomentAudioUri(
        rows = rows,
        baseUrl = baseUrl,
        videoId = videoId,
        syncRows = syncRows,
    )
}
    .distinctUntilChanged()

internal fun resolveMomentSlideUris(
    rows: List<MediaInventoryEntity>,
    baseUrl: String,
    videoId: String,
    fallbackSlideCount: Int,
    syncRows: List<AndroidSyncAssetEntity> = emptyList(),
): List<MediaUri> {
    val syncSlideRows = syncRows
        .asSequence()
        .filter(::isMomentSyncSlideAsset)
        .sortedBy(::momentSyncSlideIndex)
        .toList()
    if (syncSlideRows.isNotEmpty()) {
        return syncSlideRows.map { row -> momentSyncAssetToMediaUri(row, baseUrl) }
    }

    val slideRows = rows
        .asSequence()
        .filter { row ->
            row.assetKind == "post_media" && row.serverUrl.contains("/api/media/slide/")
        }
        .sortedBy(::momentSlideIndex)
        .toList()
    if (slideRows.isNotEmpty()) {
        return slideRows.map { row -> momentInventoryRowToMediaUri(row, baseUrl) }
    }

    val fallbackCount = fallbackSlideCount.coerceAtLeast(0)
    if (fallbackCount == 0) return emptyList()
    return List(fallbackCount) { index ->
        momentSlideUrl(baseUrl, videoId, index)
            ?.let(MediaUri::Remote)
            ?: MediaUri.Missing
    }
}

internal fun resolveMomentAudioUri(
    rows: List<MediaInventoryEntity>,
    baseUrl: String,
    videoId: String,
    syncRows: List<AndroidSyncAssetEntity> = emptyList(),
): MediaUri {
    val syncAudioRow = syncRows.firstOrNull { row ->
        row.assetKind == "audio" ||
            row.assetKind == "post_audio" ||
            row.serverUrl.contains("/api/media/audio/")
    }
    if (syncAudioRow != null) {
        return momentSyncAssetToMediaUri(syncAudioRow, baseUrl)
    }

    val audioRow = rows.firstOrNull { row ->
        row.assetKind == "audio" ||
            row.assetKind == "post_audio" ||
            row.serverUrl.contains("/api/media/audio/")
    }
    if (audioRow != null) {
        return momentInventoryRowToMediaUri(audioRow, baseUrl)
    }
    return momentAudioUrl(baseUrl, videoId)
        ?.let(MediaUri::Remote)
        ?: MediaUri.Missing
}

private fun momentSlideIndex(row: MediaInventoryEntity): Int =
    row.serverUrl.substringAfterLast('/').toIntOrNull()
        ?: row.assetId.substringAfterLast('_').toIntOrNull()
        ?: Int.MAX_VALUE

private fun isMomentSyncSlideAsset(row: AndroidSyncAssetEntity): Boolean {
    if (row.assetKind != "post_media") return false
    val contentType = row.contentType?.trim()?.lowercase().orEmpty()
    return contentType.isBlank() || contentType.startsWith("image/")
}

private fun momentSyncSlideIndex(row: AndroidSyncAssetEntity): Int =
    row.assetId.slideIndexAfterPostMedia()
        ?: row.serverUrl.substringAfterLast('/').toIntOrNull()
        ?: Int.MAX_VALUE

private fun String.slideIndexAfterPostMedia(): Int? {
    val marker = "_post_media"
    val markerIndex = lastIndexOf(marker)
    if (markerIndex < 0) return null
    val suffix = substring(markerIndex + marker.length)
    if (suffix.isEmpty()) return 0
    if (!suffix.startsWith("_")) return null
    return suffix.drop(1).toIntOrNull()
}

private fun momentInventoryRowToMediaUri(
    row: MediaInventoryEntity,
    baseUrl: String,
): MediaUri {
    if (row.state == "cached" && !row.localPath.isNullOrBlank()) {
        val file = File(row.localPath)
        if (file.exists()) return MediaUri.Local(file)
    }

    val root = baseUrl.trim().trimEnd('/')
    if (root.isBlank()) return MediaUri.Missing
    return MediaUri.Remote(root + row.serverUrl)
}

private fun momentSyncAssetToMediaUri(
    row: AndroidSyncAssetEntity,
    baseUrl: String,
): MediaUri {
    if (row.state == "verified" && !row.localPath.isNullOrBlank()) {
        val file = File(row.localPath)
        if (file.exists()) return MediaUri.Local(file)
    }

    val root = baseUrl.trim().trimEnd('/')
    if (root.isBlank()) return MediaUri.Missing
    return MediaUri.Remote(root + row.serverUrl)
}

private fun prepareMomentAudio(
    player: ExoPlayer,
    loadedKey: String?,
    videoId: String,
    audioUri: MediaUri,
): String? {
    val targetKey = momentAudioLoadKey(videoId, audioUri) ?: return clearMomentAudio(player)
    if (loadedKey == targetKey) return loadedKey

    when (audioUri) {
        is MediaUri.Local -> player.setMediaItem(MediaItem.fromUri(audioUri.file.toURI().toString()))
        is MediaUri.Remote -> player.setMediaItem(MediaItem.fromUri(audioUri.url))
        MediaUri.Missing -> return clearMomentAudio(player)
    }
    player.repeatMode = Player.REPEAT_MODE_ONE
    player.prepare()
    return targetKey
}

private fun momentAudioLoadKey(videoId: String, audioUri: MediaUri): String? = when (audioUri) {
    is MediaUri.Local -> "local:$videoId:${audioUri.file.absolutePath}"
    is MediaUri.Remote -> "remote:$videoId:${audioUri.url}"
    MediaUri.Missing -> null
}

private fun clearMomentAudio(player: ExoPlayer): String? {
    player.playWhenReady = false
    player.pause()
    player.clearMediaItems()
    return null
}

@Composable
private fun MomentSlideDots(
    currentPage: Int,
    pageCount: Int,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier,
        horizontalArrangement = Arrangement.spacedBy(6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        repeat(pageCount) { page ->
            Box(
                modifier = Modifier
                    .size(if (page == currentPage) 8.dp else 6.dp)
                    .clip(CircleShape)
                    .background(
                        if (page == currentPage) Color.White
                        else Color.White.copy(alpha = 0.45f),
                    ),
            )
        }
    }
}

/**
 * Progress bar pinned to the bottom of a moments page. 4dp visible track,
 * 48dp touch target so users can easily hit it without occluding the
 * video. Single tap jumps to that position; horizontal drag scrubs. Poll cadence
 * is 150ms — fast enough to feel live, cheap enough to ignore on the main thread.
 */
@Composable
private fun MomentsVideoProgressBar(
    player: ExoPlayer,
    modifier: Modifier = Modifier,
) {
    val colors = MaterialTheme.iglooColors
    var progress by remember(player) { mutableStateOf(0f) }
    var isDragging by remember(player) { mutableStateOf(false) }
    var dragProgress by remember(player) { mutableStateOf(0f) }
    var barWidthPx by remember(player) { mutableIntStateOf(1) }

    LaunchedEffect(player) {
        while (true) {
            if (!isDragging) {
                val dur = player.duration
                if (dur > 0L) {
                    progress = (player.currentPosition.toFloat() / dur).coerceIn(0f, 1f)
                }
            }
            delay(150L)
        }
    }

    val shown = if (isDragging) dragProgress else progress
    Box(
        modifier = modifier
            .fillMaxWidth()
            .height(48.dp)
            .onSizeChanged { barWidthPx = it.width.coerceAtLeast(1) }
            .pointerInput(player) {
                detectTapGestures { offset ->
                    val frac = (offset.x / barWidthPx).coerceIn(0f, 1f)
                    val dur = player.duration
                    if (dur > 0L) {
                        player.seekTo((frac * dur).toLong())
                        progress = frac
                    }
                }
            }
            .pointerInput(player) {
                detectHorizontalDragGestures(
                    onDragStart = { offset ->
                        isDragging = true
                        dragProgress = (offset.x / barWidthPx).coerceIn(0f, 1f)
                    },
                    onHorizontalDrag = { _, delta ->
                        dragProgress = (dragProgress + delta / barWidthPx).coerceIn(0f, 1f)
                    },
                    onDragEnd = {
                        val dur = player.duration
                        if (dur > 0L) player.seekTo((dragProgress * dur).toLong())
                        progress = dragProgress
                        isDragging = false
                    },
                    onDragCancel = { isDragging = false },
                )
            },
        contentAlignment = Alignment.BottomStart,
    ) {
        // Background track
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .height(4.dp)
                .clip(RoundedCornerShape(percent = 50))
                .background(Color.White.copy(alpha = 0.24f)),
        )
        // Filled portion in the user's accent color
        Box(
            modifier = Modifier
                .fillMaxWidth(shown)
                .height(4.dp)
                .clip(RoundedCornerShape(percent = 50))
                .background(colors.primary),
        )
    }
}

/** Black drop shadow reused by overlay text + icons so they stay legible on any frame. */
private val DropShadow = Shadow(
    color = Color.Black.copy(alpha = 0.65f),
    offset = Offset(0f, 1f),
    blurRadius = 4f,
)

private val CaptionShadow = Shadow(
    color = Color.Black.copy(alpha = 0.45f),
    offset = Offset(0f, 1f),
    blurRadius = 2f,
)

/**
 * One flat action icon for the right-edge column. No background pill / halo —
 * just icon plus shadow. Tint rules:
 *  - pressed       → [accent] (tactile flash while the finger is down)
 *  - [isActive]    → [accent] (persistent "this mode is on" indicator)
 *  - otherwise     → white
 */
@Composable
private fun ShadowIcon(
    icon: androidx.compose.ui.graphics.vector.ImageVector,
    contentDescription: String,
    onClick: () -> Unit,
    isActive: Boolean = false,
    accent: Color = Color.White,
    enabled: Boolean = true,
) {
    val interactionSource = remember { MutableInteractionSource() }
    val isPressed by interactionSource.collectIsPressedAsState()
    val tint = when {
        !enabled -> Color.White.copy(alpha = 0.38f)
        isActive || isPressed -> accent
        else -> Color.White
    }
    val clickModifier = if (enabled) {
        Modifier.pointerInput(Unit) {
            detectTapGestures(
                onPress = { offset ->
                    val press = PressInteraction.Press(offset)
                    interactionSource.emit(press)
                    val released = tryAwaitRelease()
                    interactionSource.emit(
                        if (released) PressInteraction.Release(press)
                        else PressInteraction.Cancel(press),
                    )
                },
                onTap = { onClick() },
            )
        }
    } else {
        Modifier
    }

    Box(
        modifier = Modifier
            .size(44.dp)
            .then(clickModifier),
        contentAlignment = Alignment.Center,
    ) {
        Icon(
            imageVector = icon,
            contentDescription = contentDescription,
            tint = tint,
            modifier = Modifier.size(28.dp),
        )
    }
}

@Composable
private fun MomentRailAvatar(
    item: MomentItem,
    onChannelClick: (channelId: String) -> Unit,
    onStoryClick: (channelId: String, firstVideoId: String) -> Unit,
    onFollowChannel: (channelId: String) -> Unit,
    onRequestUnfollowChannel: (MomentItem) -> Unit,
    colors: IglooColors,
    modifier: Modifier = Modifier,
) {
    val accent = colors.primary
    val storyTarget = item.storyFirstVideoId.takeIf {
        it.isNotBlank() && item.storyRingState != StoryRingState.None
    }
    Box(
        modifier = modifier.size(50.dp),
        contentAlignment = Alignment.Center,
    ) {
        Avatar(
            channelId = item.channelId,
            size = 44.dp,
            modifier = Modifier.storyRingBorder(item.storyRingState, colors),
            onClick = {
                if (storyTarget != null) {
                    onStoryClick(item.channelId, storyTarget)
                } else {
                    onChannelClick(item.channelId)
                }
            },
            showPendingBadge = false,
        )
        val badgeModifier = Modifier
            .align(Alignment.BottomCenter)
            .size(18.dp)
            .clip(CircleShape)
            .background(if (item.isAuthorFollowed) Color.White else accent)
            .border(2.dp, Color.Black.copy(alpha = 0.70f), CircleShape)
            .clickable {
                if (item.isAuthorFollowed) {
                    onRequestUnfollowChannel(item)
                } else {
                    onFollowChannel(item.channelId)
                }
            }
        Box(
            modifier = badgeModifier,
            contentAlignment = Alignment.Center,
        ) {
            val followingLabel = stringResource(R.string.action_following)
            val followLabel = stringResource(R.string.action_follow)
            Icon(
                imageVector = if (item.isAuthorFollowed) Icons.Filled.Check else Icons.Filled.Add,
                contentDescription = if (item.isAuthorFollowed) followingLabel else followLabel,
                tint = if (item.isAuthorFollowed) accent else Color.White,
                modifier = Modifier.size(12.dp),
            )
        }
    }
}

@Composable
private fun CollapsedDescription(
    item: MomentItem,
    expanded: Boolean,
    onMentionClick: (String) -> Unit,
    onChannelClick: (String) -> Unit,
    onExpandedChange: (Boolean) -> Unit,
    modifier: Modifier = Modifier,
) {
    val linkColor = MaterialTheme.iglooColors.primary
    val uriHandler = LocalUriHandler.current
    val collapsedDescription = remember(item.description) {
        collapseMomentCaptionWhitespace(item.description)
    }
    var descriptionCanExpand by remember(item.videoId, collapsedDescription) { mutableStateOf(false) }
    val expandedHorizontalPadding = if (expanded) 8.dp else 0.dp
    Column(
        modifier = modifier
            .fillMaxWidth()
            .padding(
                start = momentCollapsedCaptionStartPaddingDp().dp,
                end = 16.dp,
            )
            .background(
                color = momentCaptionBackgroundColor(expanded),
                shape = RoundedCornerShape(8.dp),
            )
            .clickable(enabled = expanded) { onExpandedChange(false) },
    ) {
        Column(
            modifier = Modifier.padding(
                start = expandedHorizontalPadding,
                top = 8.dp,
                end = expandedHorizontalPadding,
                bottom = MomentCollapsedCaptionBottomPadding,
            ),
            verticalArrangement = Arrangement.spacedBy(3.dp),
        ) {
            momentRepostLabel(item)?.let { label ->
                Text(
                    text = label,
                    style = MaterialTheme.typography.labelSmall.copy(
                        fontWeight = FontWeight.SemiBold,
                        shadow = CaptionShadow,
                    ),
                    color = Color.White,
                )
            }
            val timestamp = localizedRelativeTime(item.publishedAt)
            if (item.publishedAt > 0L && timestamp.isNotEmpty()) {
                Text(
                    text = timestamp,
                    style = MaterialTheme.typography.labelSmall.copy(shadow = CaptionShadow),
                    color = Color.White.copy(alpha = 0.70f),
                )
            }
            Text(
                text = momentAuthorLabel(item),
                style = MaterialTheme.typography.titleMedium.copy(
                    fontWeight = FontWeight.Bold,
                    shadow = CaptionShadow,
                ),
                color = Color.White,
                modifier = Modifier.clickable { onChannelClick(item.channelId) },
            )
            if (collapsedDescription.isNotBlank()) {
                AtMentionText(
                    text = collapsedDescription,
                    onMentionClick = onMentionClick,
                    onUrlClick = uriHandler::openUri,
                    maxLines = momentCaptionDescriptionMaxLines(expanded),
                    overflow = TextOverflow.Ellipsis,
                    style = MaterialTheme.typography.bodyMedium.copy(
                        color = Color.White,
                        shadow = CaptionShadow,
                    ),
                    mentionColorOverride = linkColor,
                    urlColorOverride = linkColor,
                    onPlainTextClick = {
                        onExpandedChange(
                            momentCaptionExpandedAfterPlainTextClick(
                                expanded = expanded,
                                descriptionCanExpand = descriptionCanExpand,
                            ),
                        )
                    },
                    onTextLayout = { layout ->
                        if (!expanded) descriptionCanExpand = layout.hasVisualOverflow
                    },
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        }
    }
}

private val MomentCaptionWhitespace = Regex("\\s+")
private val MomentCollapsedCaptionBottomPadding = 4.dp

private fun collapseMomentCaptionWhitespace(text: String): String =
    text.replace(MomentCaptionWhitespace, " ").trim()

@Composable
private fun VideoSurface(
    player: ExoPlayer,
    mediaKey: String,
    onStateChange: (MomentVideoSurfaceState) -> Unit,
    modifier: Modifier = Modifier,
) {
    val context = LocalContext.current
    var surfaceState by remember(player, mediaKey) { mutableStateOf(MomentVideoSurfaceState()) }
    DisposableEffect(player, mediaKey) {
        var renderedFrameMediaId: String? = null
        var renderedFrameCount = 0
        fun currentSurfaceState(): MomentVideoSurfaceState {
            val size = player.videoSize
            val currentMediaId = player.currentMediaItem?.mediaId
            val matchingFrameCount = if (renderedFrameMediaId == currentMediaId) renderedFrameCount else 0
            return momentVideoSurfaceStateFor(
                expectedMediaId = mediaKey,
                currentMediaId = currentMediaId,
                playbackState = player.playbackState,
                videoWidth = size.width,
                videoHeight = size.height,
                renderedFrameCount = matchingFrameCount,
                playerIsPlaying = player.isPlaying,
                playerPositionMs = player.currentPosition,
            )
        }
        fun publish() {
            val next = currentSurfaceState()
            surfaceState = next
            onStateChange(next)
        }
        publish()
        val listener = object : Player.Listener {
            override fun onMediaItemTransition(mediaItem: MediaItem?, reason: Int) {
                renderedFrameMediaId = mediaItem?.mediaId
                renderedFrameCount = 0
                publish()
            }

            override fun onVideoSizeChanged(videoSize: VideoSize) {
                publish()
            }

            override fun onPlaybackStateChanged(playbackState: Int) {
                if (playbackState == Player.STATE_IDLE || playbackState == Player.STATE_ENDED) {
                    renderedFrameCount = 0
                    renderedFrameMediaId = player.currentMediaItem?.mediaId
                }
                publish()
            }

            override fun onIsPlayingChanged(isPlaying: Boolean) {
                publish()
            }

            override fun onRenderedFirstFrame() {
                val currentMediaId = player.currentMediaItem?.mediaId.orEmpty()
                if (currentMediaId != mediaKey) return
                if (renderedFrameMediaId != currentMediaId) {
                    renderedFrameMediaId = currentMediaId
                    renderedFrameCount = 0
                }
                renderedFrameCount = (renderedFrameCount + 1).coerceAtLeast(1)
                publish()
            }
        }
        player.addListener(listener)
        onDispose {
            player.removeListener(listener)
            onStateChange(MomentVideoSurfaceState())
        }
    }

    val playerView = remember {
        (LayoutInflater.from(context).inflate(R.layout.moment_player_view, null) as PlayerView).apply {
            setBackgroundColor(android.graphics.Color.BLACK)
            setShutterBackgroundColor(android.graphics.Color.BLACK)
        }
    }

    AndroidView(
        factory = { playerView },
        update = { view ->
            if (view.player !== player) view.player = player
            view.setBackgroundColor(android.graphics.Color.BLACK)
            view.resizeMode = momentsVideoResizeMode(
                width = surfaceState.videoWidth,
                height = surfaceState.videoHeight,
            )
        },
        modifier = modifier.alpha(
            if (surfaceState.hasExpectedMedia && surfaceState.renderedFirstFrame) 1f else 0f,
        ),
    )

    DisposableEffect(player) {
        onDispose {
            playerView.player = null
        }
    }
}

internal fun momentsVideoResizeMode(width: Int, height: Int): Int =
    if (isVerticalMomentVideo(width, height)) {
        AspectRatioFrameLayout.RESIZE_MODE_ZOOM
    } else {
        AspectRatioFrameLayout.RESIZE_MODE_FIT
    }

internal fun momentFitWidthContentScale(): ContentScale = ContentScale.Fit

/**
 * Fit-width thumbnail fallback rendered in place of the PlayerView when there's
 * no mountable stream. Two callers per §7 lines 1301-1306:
 *  - `streamUri is MediaUri.Missing` → `alphaOverride = null`, thumbnail's own
 *    `mediaAlpha()` decides fade (Remote+offline thumbnails fade; Local+Missing
 *    thumbnails stay full).
 *  - `isIglooRemoteOffline(streamUri)` → `alphaOverride = 0.55f`, because
 *    the fade decision is on the *stream* not the thumbnail.
 */
@Composable
private fun BoxScope.ThumbnailFallback(
    thumbnailUri: MediaUri,
    alphaOverride: Float?,
    brokenIconTint: Color,
) {
    when (thumbnailUri) {
        is MediaUri.Local -> AsyncImage(
            model = thumbnailUri.file,
            contentDescription = null,
            modifier = Modifier
                .fillMaxSize()
                .alpha(alphaOverride ?: mediaAlpha(thumbnailUri)),
            contentScale = momentFitWidthContentScale(),
        )
        is MediaUri.Remote -> AsyncImage(
            model = rememberRemoteImageModel(thumbnailUri.url),
            contentDescription = null,
            modifier = Modifier
                .fillMaxSize()
                .alpha(alphaOverride ?: mediaAlpha(thumbnailUri)),
            contentScale = momentFitWidthContentScale(),
        )
        is MediaUri.Missing -> Icon(
            imageVector = Icons.Default.BrokenImage,
            contentDescription = stringResource(R.string.content_description_missing_media),
            tint = brokenIconTint,
            modifier = Modifier.align(Alignment.Center),
        )
    }
}
