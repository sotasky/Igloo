package com.screwy.igloo.channel

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavController
import com.screwy.igloo.R
import com.screwy.igloo.data.entity.ChannelProfileEntity
import com.screwy.igloo.data.entity.FeedRow
import com.screwy.igloo.data.entity.ThreadedFeedRow
import com.screwy.igloo.feed.FeedMediaGridModel
import com.screwy.igloo.feed.SocialPostModel
import com.screwy.igloo.feed.buildFeedMediaOpenSnapshot
import com.screwy.igloo.feed.buildProfileOpenSnapshot
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.ui.UiState
import com.screwy.igloo.ui.UiStateSwitch
import com.screwy.igloo.ui.component.BookmarkCategoryDisplay
import com.screwy.igloo.ui.component.BookmarkTarget
import com.screwy.igloo.ui.component.ChannelProfileHeaderLabels
import com.screwy.igloo.ui.component.ChannelProfileHeaderUiModel
import com.screwy.igloo.ui.component.ComposeChannelHeader
import com.screwy.igloo.ui.component.MomentThumbnailItem
import com.screwy.igloo.ui.component.MomentsGrid
import com.screwy.igloo.ui.component.NativeFeedSurface
import com.screwy.igloo.ui.component.Platform
import com.screwy.igloo.ui.component.VideoGrid
import com.screwy.igloo.ui.component.channelProfileHeaderUiModel
import com.screwy.igloo.ui.component.normalizeHandle
import com.screwy.igloo.ui.component.resolveInitialMomentThumbnailUri
import com.screwy.igloo.ui.nav.ApplyOverlayChrome
import com.screwy.igloo.ui.nav.IglooNavigationSource
import com.screwy.igloo.ui.nav.OverlayChromeState
import com.screwy.igloo.ui.nav.ProfileOpenSnapshot
import com.screwy.igloo.ui.nav.rememberIglooNavigator
import org.koin.androidx.compose.koinViewModel
import org.koin.compose.koinInject
import org.koin.core.parameter.parametersOf

/**
 * Per-channel page with a platform-dispatched body. Twitter channels use the
 * native feed/header path; non-Twitter channel bodies keep the shared Compose header.
 *
 * Body dispatch:
 *  - Twitter → native feed rows filtered to this channel.
 *  - TikTok / Instagram → [MomentsGrid] (shorts-only).
 *  - YouTube → [VideoGrid] 1-column.
 *  - Unknown platform → minimal "Unknown" fallback.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ChannelRoute(
    channelId: String,
    navController: NavController,
    modifier: Modifier = Modifier,
    initialSnapshot: ProfileOpenSnapshot? = null,
) {
    val uriHandler = LocalUriHandler.current
    val baseUrlProvider: ServerBaseUrlProvider = koinInject()
    val baseUrl = baseUrlProvider.baseUrl()
    val vm: ChannelViewModel = koinViewModel(
        parameters = { parametersOf(channelId) },
    )
    val channel by vm.channel.collectAsStateWithLifecycle()
    val channelProfile by vm.channelProfile.collectAsStateWithLifecycle()
    val storyStatus by vm.storyStatus.collectAsStateWithLifecycle()
    val uiState by vm.uiState.collectAsStateWithLifecycle()
    val pendingBookmark by vm.pendingBookmark.collectAsStateWithLifecycle()
    val bookmarkCategories by vm.bookmarkCategories.collectAsStateWithLifecycle()
    val mutedHandles by vm.mutedHandles.collectAsStateWithLifecycle()
    val twitterRows by vm.twitterRows.collectAsStateWithLifecycle()
    val mediaModels by vm.mediaModels.collectAsStateWithLifecycle()
    var confirmUnfollow by remember { mutableStateOf(false) }
    val navigator = rememberIglooNavigator(navController)

    ApplyOverlayChrome(
        if (pendingBookmark != null) {
            OverlayChromeState.HideTopBar
        } else {
            OverlayChromeState.None
        },
    )

    UiStateSwitch(state = uiState, modifier = modifier) {
        val matchingSnapshot = initialSnapshot?.takeIf { it.channelId == channelId }
        val display = channel
        val profileForHeader = channelProfile ?: matchingSnapshot?.toChannelProfileEntity()
        val displayNameOverride = channelProfile?.displayName?.takeIf { it.isNotBlank() }
            ?: matchingSnapshot?.displayName?.takeIf { it.isNotBlank() }
            ?: resolveHeaderDisplayName(
                primaryName = display.channel.name,
                sourceHandle = profileForHeader?.handle ?: display.channel.sourceId,
                authorDisplayNames = twitterRows.mapNotNull { it.item.authorDisplayName },
            )
        val headerLabels = ChannelProfileHeaderLabels(
            following = stringResource(R.string.profile_following),
            followers = stringResource(R.string.profile_followers),
            subscribers = stringResource(R.string.profile_subscribers),
            protectedAccount = stringResource(R.string.profile_protected_account),
            browser = stringResource(R.string.system_browser),
        )
        val profileHeader = channelProfileHeaderUiModel(
            baseUrl = baseUrl,
            channel = display,
            profile = profileForHeader,
            displayNameOverride = displayNameOverride,
            initialAvatarUri = matchingSnapshot?.avatarUri ?: MediaUri.Missing,
            initialBannerUri = matchingSnapshot?.bannerUri ?: MediaUri.Missing,
            labels = headerLabels,
        ).copy(
            storyRingState = storyStatus.ringState,
            storyFirstVideoId = storyStatus.firstVideoId,
        )
        val headerContent: @Composable () -> Unit = {
            ComposeChannelHeader(
                header = profileHeader,
                onFollowToggle = { newValue ->
                    if (newValue) {
                        vm.toggleFollow(true)
                    } else {
                        confirmUnfollow = true
                    }
                },
                onStarToggle = vm::toggleStar,
                onRefresh = vm::refresh,
                onOpenInPlatform = {
                    profileHeader.platformUrl?.takeIf { it.isNotBlank() }?.let(uriHandler::openUri)
                },
                onStoryClick = { cid, firstVideoId ->
                    navigator.openShorts(
                        playlistType = "story",
                        playlistId = cid,
                        videoId = firstVideoId,
                        source = IglooNavigationSource.Channel,
                    )
                },
                onMentionClick = vm::resolveMentionAndNavigate,
                onOpenUrl = uriHandler::openUri,
            )
        }

        when (profileHeader.platform) {
            Platform.Twitter -> ChannelTwitterBody(
                vm = vm,
                mutedHandles = mutedHandles,
                mediaModels = mediaModels,
                pendingBookmark = pendingBookmark,
                bookmarkCategories = bookmarkCategories,
                header = profileHeader,
                onHeaderFollowToggle = { newValue ->
                    if (newValue) {
                        vm.toggleFollow(true)
                    } else {
                        confirmUnfollow = true
                    }
                },
                onHeaderStarToggle = vm::toggleStar,
                onHeaderRefresh = vm::refresh,
                onHeaderOpenInPlatform = {
                    profileHeader.platformUrl?.takeIf { it.isNotBlank() }?.let(uriHandler::openUri)
                },
                onChannelClick = { cid ->
                    navigator.openChannel(cid, IglooNavigationSource.Channel)
                },
                onProfileOpen = { post ->
                    navigator.openChannel(
                        channelId = post.author.channelId,
                        source = IglooNavigationSource.Channel,
                        originItemId = post.row.item.tweetId,
                        snapshot = buildProfileOpenSnapshot(post, baseUrl),
                    )
                },
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
                        source = IglooNavigationSource.Channel,
                        posterUri = snapshot.posterUri,
                        snapshot = snapshot,
                    )
                },
                onQuoteOpen = { tweetId ->
                    navigator.openThread(tweetId, IglooNavigationSource.Channel)
                },
            )
            Platform.TikTok, Platform.Instagram -> ChannelMomentsBody(
                vm = vm,
                currentChannelId = channelId,
                headerContent = headerContent,
                onOpenMoment = { item ->
                    navigator.openShorts(
                        playlistType = "channel",
                        playlistId = channelId,
                        videoId = item.videoId,
                        source = IglooNavigationSource.Channel,
                        posterUri = item.routePosterUri(baseUrl),
                    )
                },
                onChannelClick = { cid ->
                    navigator.openChannel(cid, IglooNavigationSource.Channel)
                },
            )
            Platform.YouTube -> ChannelVideosBody(
                vm = vm,
                headerContent = headerContent,
                onVideoClick = { vid ->
                    navigator.openVideo(vid, IglooNavigationSource.Channel)
                },
                onVideoClickWithPoster = { vid, posterUri ->
                    navigator.openVideo(vid, IglooNavigationSource.Channel, posterUri)
                },
                onChannelClick = { cid ->
                    navigator.openChannel(cid, IglooNavigationSource.Channel)
                },
            )
            null -> androidx.compose.foundation.layout.Column(modifier = Modifier.fillMaxSize()) {
                headerContent()
                Text(
                    text = stringResource(
                        R.string.error_unknown_platform,
                        display.channel.platform,
                    ),
                    style = MaterialTheme.typography.bodyMedium,
                    modifier = Modifier.padding(16.dp),
                )
            }
        }
    }

    if (confirmUnfollow) {
        AlertDialog(
            onDismissRequest = { confirmUnfollow = false },
            title = { Text(stringResource(R.string.confirm_unfollow_account_title)) },
            text = {
                Text(
                    stringResource(R.string.confirm_unfollow_account_body)
                )
            },
            confirmButton = {
                TextButton(
                    onClick = {
                        confirmUnfollow = false
                        vm.toggleFollow(false)
                    }
                ) {
                    Text(stringResource(R.string.action_unfollow))
                }
            },
            dismissButton = {
                TextButton(onClick = { confirmUnfollow = false }) {
                    Text(stringResource(R.string.action_cancel))
                }
            },
        )
    }
}

/** Twitter body — channel-scoped native feed rows with a native header item. */
@Composable
private fun ChannelTwitterBody(
    vm: ChannelViewModel,
    mutedHandles: Set<String>,
    mediaModels: Map<String, FeedMediaGridModel>,
    pendingBookmark: BookmarkTarget?,
    bookmarkCategories: List<BookmarkCategoryDisplay>,
    header: ChannelProfileHeaderUiModel,
    onHeaderFollowToggle: (Boolean) -> Unit,
    onHeaderStarToggle: (Boolean) -> Unit,
    onHeaderRefresh: () -> Unit,
    onHeaderOpenInPlatform: () -> Unit,
    onChannelClick: (String) -> Unit,
    onProfileOpen: (SocialPostModel) -> Unit,
    onMediaOpen: (FeedRow, Int, FeedMediaGridModel) -> Unit,
    onQuoteOpen: (String) -> Unit,
) {
    val rows by vm.twitterRows.collectAsStateWithLifecycle()
    val threadedRows = remember(rows) { rows.map { ThreadedFeedRow(row = it, chain = emptyList()) } }

    NativeFeedSurface(
        rows = threadedRows,
        uiState = UiState.Data(Unit),
        isRefreshing = false,
        pendingBookmark = pendingBookmark,
        bookmarkCategories = bookmarkCategories,
        mutedHandles = mutedHandles,
        mediaModels = mediaModels,
        onRefresh = vm::refresh,
        onChannelClick = onChannelClick,
        onProfileOpen = onProfileOpen,
        onMentionClick = vm::resolveMentionAndNavigate,
        onLikeToggle = vm::toggleLike,
        onBookmarkToggle = vm::toggleBookmark,
        onFollowToggle = vm::toggleRowFollow,
        onStarToggle = vm::toggleRowStar,
        onMuteToggle = vm::toggleRowMute,
        onMediaOpen = { row, mediaIndex, visibleMediaModel -> onMediaOpen(row, mediaIndex, visibleMediaModel) },
        onQuoteOpen = onQuoteOpen,
        onSeenReached = { /* server sees feed_seen via main feed route */ },
        onConfirmBookmark = vm::confirmBookmark,
        onRemoveBookmark = vm::removePendingBookmark,
        onDismissBookmarkSheet = vm::dismissBookmarkSheet,
        onCreateCategory = vm::createCategory,
        onWarmMediaRows = vm::warmMediaModels,
        channelHeader = header,
        onHeaderFollowToggle = onHeaderFollowToggle,
        onHeaderStarToggle = onHeaderStarToggle,
        onHeaderRefresh = onHeaderRefresh,
        onHeaderOpenInPlatform = onHeaderOpenInPlatform,
    )
}

/** TikTok / Instagram body — channel-scoped shorts grid. */
@Composable
private fun ChannelMomentsBody(
    vm: ChannelViewModel,
    currentChannelId: String,
    headerContent: @Composable () -> Unit,
    onOpenMoment: (MomentThumbnailItem) -> Unit,
    onChannelClick: (String) -> Unit,
) {
    val thumbs by vm.momentThumbs.collectAsStateWithLifecycle()
    MomentsGrid(
        items = thumbs,
        onItemClick = { _, index ->
            thumbs.getOrNull(index)?.let(onOpenMoment)
        },
        onSwipeLeftOnItem = { cid ->
            if (cid != currentChannelId) onChannelClick(cid)
        },
        showScrollFabs = true,
        headerContent = headerContent,
    )
}

private fun MomentThumbnailItem.routePosterUri(baseUrl: String): MediaUri =
    resolveInitialMomentThumbnailUri(
        videoId = videoId,
        thumbnailPath = thumbnailPath,
        mediaKind = mediaKind,
        slideCount = slideCount,
        ownerKind = ownerKind,
        baseUrl = baseUrl,
    )

private fun ProfileOpenSnapshot.toChannelProfileEntity(): ChannelProfileEntity =
    ChannelProfileEntity(
        channelId = channelId,
        platform = platform,
        handle = handle.takeIf { it.isNotBlank() },
        displayName = displayName.takeIf { it.isNotBlank() },
        avatarUrl = avatarUri.remoteUrlOrNull(),
        bannerUrl = bannerUri.remoteUrlOrNull(),
    )

private fun MediaUri.remoteUrlOrNull(): String? =
    (this as? MediaUri.Remote)?.url?.takeIf { it.isNotBlank() }

/**
 * YouTube body — 2-column long-form grid per the channel-feed (YouTube)
 * screenshot. Faded-but-tappable for not-yet-downloaded entries is already
 * handled inside [VideoGrid] via `video.filePath.isNullOrEmpty()`.
 */
@Composable
private fun ChannelVideosBody(
    vm: ChannelViewModel,
    headerContent: @Composable () -> Unit,
    onVideoClick: (String) -> Unit,
    onVideoClickWithPoster: (String, MediaUri) -> Unit,
    onChannelClick: (String) -> Unit,
) {
    val videos by vm.videos.collectAsStateWithLifecycle()
    VideoGrid(
        items = videos,
        columns = 2,
        onVideoClick = onVideoClick,
        onVideoClickWithPoster = onVideoClickWithPoster,
        onChannelClick = onChannelClick,
        headerContent = headerContent,
    )
}

internal fun resolveHeaderDisplayName(
    primaryName: String?,
    sourceHandle: String?,
    authorDisplayNames: List<String>,
): String? {
    val normalizedPrimary = normalizeHandle(primaryName)
    val normalizedHandle = normalizeHandle(sourceHandle)
    if (normalizedPrimary.isNotBlank() && !normalizedPrimary.equals(normalizedHandle, ignoreCase = true)) {
        return null
    }

    return authorDisplayNames
        .asSequence()
        .map { it.trim().removePrefix("@").trim() }
        .firstOrNull { candidate ->
            candidate.isNotBlank() && !candidate.equals(normalizedHandle, ignoreCase = true)
        }
}
