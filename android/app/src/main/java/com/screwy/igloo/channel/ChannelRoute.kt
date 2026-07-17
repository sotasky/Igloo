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
import com.screwy.igloo.data.platformKeyFromChannelId
import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.ChannelProfileEntity
import com.screwy.igloo.data.entity.FeedRow
import com.screwy.igloo.data.entity.ThreadedFeedRow
import com.screwy.igloo.feed.FeedMediaGridModel
import com.screwy.igloo.feed.SocialPostModel
import com.screwy.igloo.feed.buildProfileOpenSnapshot
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
import com.screwy.igloo.ui.component.channelProfileOverflowControls
import com.screwy.igloo.ui.component.normalizeHandle
import com.screwy.igloo.ui.component.parsePlatform
import com.screwy.igloo.ui.nav.ApplyOverlayChrome
import com.screwy.igloo.ui.nav.IglooNavigationSource
import com.screwy.igloo.ui.nav.OverlayChromeState
import com.screwy.igloo.ui.nav.ProfileOpenSnapshot
import com.screwy.igloo.ui.nav.rememberIglooNavigator
import org.koin.androidx.compose.koinViewModel
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
    val vm: ChannelViewModel = koinViewModel(
        parameters = { parametersOf(channelId) },
    )
    val channel by vm.channel.collectAsStateWithLifecycle()
    val channelProfile by vm.channelProfile.collectAsStateWithLifecycle()
    val storyStatus by vm.storyStatus.collectAsStateWithLifecycle()
    val uiState by vm.uiState.collectAsStateWithLifecycle()
    val pendingBookmark by vm.pendingBookmark.collectAsStateWithLifecycle()
    val bookmarkCategories by vm.bookmarkCategories.collectAsStateWithLifecycle()
    val mutedChannelIds by vm.mutedChannelIds.collectAsStateWithLifecycle()
    val repostsEnabled by vm.repostsEnabled.collectAsStateWithLifecycle()
    val isChannelMuted by vm.isChannelMuted.collectAsStateWithLifecycle()
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
        val routePlatform = resolveChannelRoutePlatform(profileForHeader, display.channel)
        val headerLabels = ChannelProfileHeaderLabels(
            following = stringResource(R.string.profile_following),
            followers = stringResource(R.string.profile_followers),
            subscribers = stringResource(R.string.profile_subscribers),
            protectedAccount = stringResource(R.string.profile_protected_account),
            browser = stringResource(R.string.system_browser),
        )

        fun buildProfileHeader(authorDisplayNames: List<String> = emptyList()): ChannelProfileHeaderUiModel {
            val displayNameOverride = channelRouteDisplayNameOverride(
                profileDisplayName = channelProfile?.displayName,
                snapshotDisplayName = matchingSnapshot?.displayName,
                routePlatform = routePlatform,
                primaryName = display.channel.name,
                sourceHandle = profileForHeader?.handle ?: display.channel.sourceId,
                twitterAuthorDisplayNames = authorDisplayNames,
            )
			return channelProfileHeaderUiModel(
				channel = display,
				profile = profileForHeader,
				displayNameOverride = displayNameOverride,
				labels = headerLabels,
            ).copy(
                storyRingState = storyStatus.ringState,
                storyFirstVideoId = storyStatus.firstVideoId,
            )
        }

        fun headerContent(profileHeader: ChannelProfileHeaderUiModel): @Composable () -> Unit = {
            val overflowControls = channelProfileOverflowControls(
                platform = profileHeader.platform,
                isFollowed = profileHeader.isFollowed,
                repostsEnabled = repostsEnabled,
                isMuted = isChannelMuted,
            )
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
                overflowControls = overflowControls,
                onRepostsEnabledChange = vm::setRepostsEnabled,
                onMutedChange = vm::setChannelMuted,
            )
        }

        when (routePlatform) {
            Platform.Twitter -> {
                val twitterRows by vm.twitterRows.collectAsStateWithLifecycle()
                val mediaModels by vm.mediaModels.collectAsStateWithLifecycle()
                val profileHeader = buildProfileHeader(
                    authorDisplayNames = twitterRows.mapNotNull { it.row.authorDisplayName },
                )
                ChannelTwitterBody(
                    vm = vm,
                    rows = twitterRows,
                    mutedChannelIds = mutedChannelIds,
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
							snapshot = buildProfileOpenSnapshot(post),
                        )
                    },
                    onMediaOpen = { row, mediaIndex, _ ->
                        navigator.openMedia(
                            ownerKind = "tweet",
                            ownerId = row.item.tweetId,
                            index = mediaIndex,
                            source = IglooNavigationSource.Channel,
                        )
                    },
                    onQuoteOpen = { tweetId ->
                        navigator.openThread(tweetId, IglooNavigationSource.Channel)
                    },
                )
            }
            Platform.TikTok, Platform.Instagram -> {
                val profileHeader = buildProfileHeader()
                ChannelMomentsBody(
                    vm = vm,
                    currentChannelId = channelId,
                    headerContent = headerContent(profileHeader),
                    onOpenMoment = { item ->
                        navigator.openShorts(
                            playlistType = "channel",
                            playlistId = channelId,
                            videoId = item.videoId,
                            source = IglooNavigationSource.Channel,
                        )
                    },
                    onChannelClick = { cid ->
                        navigator.openChannel(cid, IglooNavigationSource.Channel)
                    },
                )
            }
            Platform.YouTube -> {
                val profileHeader = buildProfileHeader()
                ChannelVideosBody(
                    vm = vm,
                    headerContent = headerContent(profileHeader),
                    onVideoClick = { vid ->
                        navigator.openVideo(vid, IglooNavigationSource.Channel)
                    },
                    onChannelClick = { cid ->
                        navigator.openChannel(cid, IglooNavigationSource.Channel)
                    },
                )
            }
            null -> androidx.compose.foundation.layout.Column(modifier = Modifier.fillMaxSize()) {
                val profileHeader = buildProfileHeader()
                val unknownHeaderContent = headerContent(profileHeader)
                unknownHeaderContent()
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
    rows: List<ThreadedFeedRow>,
    mutedChannelIds: Set<String>,
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
    NativeFeedSurface(
        rows = rows,
        uiState = UiState.Data(Unit),
        isRefreshing = false,
        pendingBookmark = pendingBookmark,
        bookmarkCategories = bookmarkCategories,
        mutedChannelIds = mutedChannelIds,
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
        onMediaRowsChanged = vm::setMediaModelRows,
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

private fun ProfileOpenSnapshot.toChannelProfileEntity(): ChannelProfileEntity =
	ChannelProfileEntity(
		channelId = channelId,
		platform = platform,
		handle = handle.takeIf { it.isNotBlank() },
		displayName = displayName.takeIf { it.isNotBlank() },
	)

internal fun resolveChannelRoutePlatform(
    profile: ChannelProfileEntity?,
    channel: ChannelEntity,
): Platform? =
    parsePlatform(profile?.platform)
        ?: parsePlatform(channel.platform)
        ?: parsePlatform(platformKeyFromChannelId(channel.channelId))

internal fun channelRouteDisplayNameOverride(
    profileDisplayName: String?,
    snapshotDisplayName: String?,
    routePlatform: Platform?,
    primaryName: String?,
    sourceHandle: String?,
    twitterAuthorDisplayNames: List<String>,
): String? =
    profileDisplayName?.takeIf { it.isNotBlank() }
        ?: snapshotDisplayName?.takeIf { it.isNotBlank() }
        ?: if (routePlatform == Platform.Twitter) {
            resolveHeaderDisplayName(
                primaryName = primaryName,
                sourceHandle = sourceHandle,
                authorDisplayNames = twitterAuthorDisplayNames,
            )
        } else {
            null
        }

@Composable
private fun ChannelVideosBody(
    vm: ChannelViewModel,
    headerContent: @Composable () -> Unit,
    onVideoClick: (String) -> Unit,
    onChannelClick: (String) -> Unit,
) {
    val videos by vm.videos.collectAsStateWithLifecycle()
    VideoGrid(
        items = videos,
        columns = 2,
        onVideoClick = onVideoClick,
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
