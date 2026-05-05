package com.screwy.igloo.player

import android.app.Activity
import android.content.Context
import android.content.pm.ActivityInfo
import java.io.File
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.produceState
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.media3.common.Player
import androidx.navigation.NavController
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import com.screwy.igloo.R
import com.screwy.igloo.data.Dearrow
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.dao.BookmarkDao
import com.screwy.igloo.data.dao.ChannelFollowDao
import com.screwy.igloo.data.dao.ChannelStarDao
import com.screwy.igloo.data.dao.MediaInventoryDao
import com.screwy.igloo.data.dao.VideoDao
import com.screwy.igloo.net.IglooHostProvider
import com.screwy.igloo.net.auth.AuthTokenProvider
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.ui.component.sharePlainText
import com.screwy.igloo.ui.nav.IglooNavigationSource
import com.screwy.igloo.ui.nav.consumeFullscreenMediaTransitionFromPrevious
import com.screwy.igloo.ui.nav.rememberIglooNavigator
import com.screwy.igloo.outbox.OutboxKind
import com.screwy.igloo.outbox.OutboxWriter
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import org.koin.androidx.compose.koinViewModel
import org.koin.compose.koinInject
import org.koin.core.parameter.parametersOf

/**
 * YouTube long-form player route.
 *
 * Layout (single continuous scroll — no tabs):
 *   1. Video surface (16:9, black, with overlay + gestures + subtitles).
 *   2. Title + channel row + stats + description card (with Show more).
 *   3. Inline "Comments" header.
 *   4. Comment list (replies indented under parents when `parent_id` is set).
 *
 * No `MainScaffold` wrapping — the spec calls this out explicitly as
 * full-screen chromeless.
 *
 * ExoPlayer lifecycle lives here (route-owned) and is released via
 * `DisposableEffect` on dispose. The VM exposes state as Flows and the
 * progress-sampler via `onProgressSample`.
 */
@Composable
fun PlayerRoute(
    videoId: String,
    navController: NavController,
    modifier: Modifier = Modifier,
) {
    val brightnessLabel = stringResource(R.string.player_brightness)
    val volumeLabel = stringResource(R.string.player_volume)

    val vm: PlayerViewModel = koinViewModel(
        parameters = { parametersOf(videoId) },
    )
    val video by vm.video.collectAsStateWithLifecycle()
    val channel by vm.channel.collectAsStateWithLifecycle()
    val comments by vm.comments.collectAsStateWithLifecycle()
    val segments by vm.segments.collectAsStateWithLifecycle()
    val subtitlePath by vm.subtitlePath.collectAsStateWithLifecycle()
    val subtitleIsAuto by vm.subtitleIsAuto.collectAsStateWithLifecycle()
    val previewSpritePath by vm.previewSpritePath.collectAsStateWithLifecycle()
    val previewTrackJsonPath by vm.previewTrackJsonPath.collectAsStateWithLifecycle()
    val streamUri by vm.streamUri.collectAsStateWithLifecycle()
    val thumbnailUri by vm.thumbnailUri.collectAsStateWithLifecycle()
    val watchHistory by vm.watchHistory.collectAsStateWithLifecycle()
    val isRefreshingComments by vm.isRefreshingComments.collectAsStateWithLifecycle()
    val dearrowMode by vm.dearrowMode.collectAsStateWithLifecycle()

    val ctx = LocalContext.current
    val configuration = LocalConfiguration.current
    val lifecycleOwner = LocalLifecycleOwner.current
    val uriHandler = LocalUriHandler.current
    val scope = rememberCoroutineScope()
    val listState = rememberLazyListState()
    val authTokens: AuthTokenProvider = koinInject()
    val iglooHostProvider: IglooHostProvider = koinInject()
    val prefs: PreferencesRepo = koinInject()
    val bookmarkDao: BookmarkDao = koinInject()
    val channelFollowDao: ChannelFollowDao = koinInject()
    val channelStarDao: ChannelStarDao = koinInject()
    val mediaInventoryDao: MediaInventoryDao = koinInject()
    val videoDao: VideoDao = koinInject()
    val outboxWriter: OutboxWriter = koinInject()
    val player = remember(authTokens.bearerTokenSync()) {
        buildIglooPlayer(ctx, authTokens, iglooHostProvider)
    }
    val playbackCoordinator = remember { PlaybackCoordinator() }
    val playbackPlayer = remember(player) { ExoPlayerPlaybackPlayer(player) }
    val activity = ctx.findActivity()
    var isFullscreen by remember { mutableStateOf(false) }
    var playerControlsVisible by remember { mutableStateOf(true) }
    var showUnfollowDialog by remember(videoId) { mutableStateOf(false) }
    var showDeleteLocalDialog by remember(videoId) { mutableStateOf(false) }
    var levelFeedback by remember(videoId) { mutableStateOf<PlayerLevelFeedback?>(null) }
    var levelFeedbackNonce by remember(videoId) { mutableStateOf(0L) }

    val sbSponsor by prefs.flowString(PreferencesRepo.Keys.SB_SPONSOR, PreferencesRepo.Defaults.SB_SPONSOR)
        .collectAsStateWithLifecycle(initialValue = PreferencesRepo.Defaults.SB_SPONSOR)
    val sbSelfPromo by prefs.flowString(PreferencesRepo.Keys.SB_SELF_PROMO, PreferencesRepo.Defaults.SB_SELF_PROMO)
        .collectAsStateWithLifecycle(initialValue = PreferencesRepo.Defaults.SB_SELF_PROMO)
    val sbInteraction by prefs.flowString(PreferencesRepo.Keys.SB_INTERACTION, PreferencesRepo.Defaults.SB_INTERACTION)
        .collectAsStateWithLifecycle(initialValue = PreferencesRepo.Defaults.SB_INTERACTION)
    val sbIntro by prefs.flowString(PreferencesRepo.Keys.SB_INTRO, PreferencesRepo.Defaults.SB_INTRO)
        .collectAsStateWithLifecycle(initialValue = PreferencesRepo.Defaults.SB_INTRO)
    val sbOutro by prefs.flowString(PreferencesRepo.Keys.SB_OUTRO, PreferencesRepo.Defaults.SB_OUTRO)
        .collectAsStateWithLifecycle(initialValue = PreferencesRepo.Defaults.SB_OUTRO)
    val sbPreview by prefs.flowString(PreferencesRepo.Keys.SB_PREVIEW, PreferencesRepo.Defaults.SB_PREVIEW)
        .collectAsStateWithLifecycle(initialValue = PreferencesRepo.Defaults.SB_PREVIEW)
    val sbFiller by prefs.flowString(PreferencesRepo.Keys.SB_FILLER, PreferencesRepo.Defaults.SB_FILLER)
        .collectAsStateWithLifecycle(initialValue = PreferencesRepo.Defaults.SB_FILLER)
    val sbMusic by prefs.flowString(PreferencesRepo.Keys.SB_MUSIC_OFFTOPIC, PreferencesRepo.Defaults.SB_MUSIC_OFFTOPIC)
        .collectAsStateWithLifecycle(initialValue = PreferencesRepo.Defaults.SB_MUSIC_OFFTOPIC)
    val useEmbedFriendlyShareLinks by prefs.shareEmbedFriendlyLinks()
        .collectAsStateWithLifecycle(initialValue = PreferencesRepo.Defaults.SHARE_EMBED_FRIENDLY_LINKS)

    val bookmarkRow by bookmarkDao.getByIdFlow(videoId).collectAsStateWithLifecycle(initialValue = null)
    val followedChannels by channelFollowDao.allFlow().collectAsStateWithLifecycle(initialValue = emptyList())
    val starredChannels by channelStarDao.allFlow().collectAsStateWithLifecycle(initialValue = emptyList())
    val mediaRows by mediaInventoryDao.forOwnerFlow(videoId).collectAsStateWithLifecycle(initialValue = emptyList())
    val transitionPosterUri = remember(videoId) {
        navController.consumeFullscreenMediaTransitionFromPrevious()
            ?.takeIf { it.mediaId == videoId }
            ?.posterUri
            ?.takeUnless { it is MediaUri.Missing }
    }

    val previousVideoId by produceState<String?>(initialValue = null, key1 = videoId) {
        value = videoDao.getPreviousVideoId(videoId)
    }
    val nextVideoId by produceState<String?>(initialValue = null, key1 = videoId) {
        value = videoDao.getNextVideoId(videoId)
    }
    DisposableEffect(Unit) {
        onDispose { player.release() }
    }

    DisposableEffect(lifecycleOwner, player) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_STOP) {
                player.playWhenReady = false
                player.pause()
            }
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose { lifecycleOwner.lifecycle.removeObserver(observer) }
    }

    DisposableEffect(activity, isFullscreen) {
        if (activity != null) {
            val controller = WindowCompat.getInsetsController(
                activity.window,
                activity.window.decorView,
            )
            if (isFullscreen) {
                activity.requestedOrientation = ActivityInfo.SCREEN_ORIENTATION_SENSOR_LANDSCAPE
                controller.hide(WindowInsetsCompat.Type.systemBars())
            } else {
                activity.requestedOrientation = ActivityInfo.SCREEN_ORIENTATION_FULL_SENSOR
                controller.show(WindowInsetsCompat.Type.systemBars())
            }
        }
        onDispose {
            activity?.requestedOrientation = ActivityInfo.SCREEN_ORIENTATION_UNSPECIFIED
            activity?.let {
                WindowCompat.getInsetsController(it.window, it.window.decorView)
                    .show(WindowInsetsCompat.Type.systemBars())
            }
        }
    }
    BackHandler(enabled = isFullscreen) { isFullscreen = false }
    LaunchedEffect(configuration.orientation) {
        if (configuration.orientation == android.content.res.Configuration.ORIENTATION_LANDSCAPE) {
            isFullscreen = true
        }
    }
    LaunchedEffect(videoId) {
        vm.ensureHydrated()
    }

    // Bind media item when the stream URI resolves. Re-run if Sync verifies a local
    // file after playback started. Stop first so a mid-session swap doesn't leak
    // a black frame or audio tail from the old item.
    LaunchedEffect(streamUri, videoId) {
        playbackCoordinator.bind(
            player = playbackPlayer,
            source = PlaybackSource(
                mediaUri = streamUri,
                resumeMs = ((watchHistory?.playbackPosition ?: 0.0) * 1000).toLong(),
            ),
        )
    }

    // Progress sampler: every 5s while playing + on pause/seek events. The
    // Listener wiring + periodic loop live together so either signal fires a
    // sample without duplicating call sites.
    LaunchedEffect(player) {
        val listener = object : Player.Listener {
            override fun onIsPlayingChanged(playing: Boolean) {
                // `isPlaying == false` covers both user-pause and buffer-pause —
                // either way, the current position is a valid sample to persist.
                if (!playing) {
                    vm.onProgressSample(player.currentPosition, player.duration)
                }
            }

            override fun onPositionDiscontinuity(
                oldPosition: Player.PositionInfo,
                newPosition: Player.PositionInfo,
                reason: Int,
            ) {
                if (reason == Player.DISCONTINUITY_REASON_SEEK) {
                    vm.onProgressSample(newPosition.positionMs, player.duration)
                }
            }
        }
        player.addListener(listener)
        try {
            while (isActive) {
                delay(5_000L)
                if (player.isPlaying) {
                    vm.onProgressSample(player.currentPosition, player.duration)
                }
            }
        } finally {
            player.removeListener(listener)
        }
    }

    var showSubtitles by remember(videoId) { mutableStateOf(false) }
    var subtitleDefaultApplied by remember(videoId) { mutableStateOf(false) }
    LaunchedEffect(videoId, subtitlePath, subtitleIsAuto) {
        if (!subtitleDefaultApplied && subtitlePath != null) {
            showSubtitles = !subtitleIsAuto
            subtitleDefaultApplied = true
        }
    }
    val metadataCounts = remember(video?.metadataJson) {
        parseVideoMetadataCounts(video?.metadataJson)
    }
    val sponsorBlockModes = remember(
        sbSponsor,
        sbSelfPromo,
        sbInteraction,
        sbIntro,
        sbOutro,
        sbPreview,
        sbFiller,
        sbMusic,
    ) {
        sponsorBlockModeMap(
            sponsor = sbSponsor,
            selfPromo = sbSelfPromo,
            interaction = sbInteraction,
            intro = sbIntro,
            outro = sbOutro,
            preview = sbPreview,
            filler = sbFiller,
            music = sbMusic,
        )
    }
    val sponsorBlockPlayback = rememberSponsorBlockPlaybackState(
        videoId = videoId,
        player = player,
        segments = segments,
        modes = sponsorBlockModes,
    )
    fun showLevelFeedback(label: String, level: Float) {
        levelFeedbackNonce += 1
        levelFeedback = PlayerLevelFeedback(label, level, levelFeedbackNonce)
    }

    LaunchedEffect(levelFeedback?.nonce) {
        if (levelFeedback != null) {
            delay(900L)
            levelFeedback = null
        }
    }
    val channelId = video?.channelId ?: channel?.channelId
    val navigator = rememberIglooNavigator(navController)
    val isBookmarked = bookmarkRow != null
    val isFollowed = channelId != null && followedChannels.any { it.channelId == channelId }
    val isStarred = channelId != null && starredChannels.any { it.channelId == channelId }
    val hasLocalMedia = mediaRows.any { !it.localPath.isNullOrBlank() }
    val displayPosterUri = transitionPosterUri ?: thumbnailUri
    val playerTitle = Dearrow.resolveTitle(
        dearrowMode,
        video?.title,
        video?.dearrowTitle,
        video?.dearrowTitleCasual,
        video?.displayTitle,
        video?.displayTitleCasual,
    )
    val canonicalShareUrl = video?.canonicalUrl?.takeIf { it.isNotBlank() }
    val onPreviousVideo = previousVideoId?.let { prevId ->
        { navigator.openVideo(prevId, IglooNavigationSource.Player) }
    }
    val onNextVideo = nextVideoId?.let { nextId ->
        { navigator.openVideo(nextId, IglooNavigationSource.Player) }
    }
    fun deleteLocalMedia() {
        scope.launch {
            mediaRows
                .mapNotNull { it.localPath }
                .distinct()
                .forEach { path ->
                    runCatching {
                        val file = File(path)
                        if (file.isDirectory) file.deleteRecursively() else file.delete()
                    }
                }
            mediaInventoryDao.deleteForOwner(videoId)
        }
    }
    if (isFullscreen) {
        PlayerSurface(
            mode = PlayerSurfaceMode.Fullscreen,
            player = player,
            posterUri = displayPosterUri,
            streamUri = streamUri,
            title = playerTitle,
            onBack = { isFullscreen = false },
            onPreviousVideo = onPreviousVideo,
            onNextVideo = onNextVideo,
            segments = sponsorBlockPlayback.visibleSegments,
            showSubtitles = showSubtitles,
            onToggleSubtitles = { showSubtitles = !showSubtitles },
            onToggleFullscreen = { isFullscreen = false },
            controlsVisible = playerControlsVisible,
            onControlsVisibleChange = { playerControlsVisible = it },
            previewSpritePath = previewSpritePath,
            previewTrackJsonPath = previewTrackJsonPath,
            subtitlePath = subtitlePath,
            currentPositionMs = { player.currentPosition },
            sponsorBlockSkipSegment = sponsorBlockPlayback.skipSegment,
            sponsorBlockAutoSkipMessage = sponsorBlockPlayback.autoSkipMessage,
            onSkipSponsorBlock = sponsorBlockPlayback.onSkip,
            levelFeedback = levelFeedback,
            onBrightnessChange = { level -> showLevelFeedback(brightnessLabel, level) },
            onVolumeChange = { level -> showLevelFeedback(volumeLabel, level) },
            modifier = modifier.fillMaxSize(),
        )
    } else {
        LazyColumn(
            state = listState,
            modifier = modifier.fillMaxSize(),
        ) {
            item(key = "player_status_spacer") {
                Spacer(
                    modifier = Modifier
                        .fillMaxWidth()
                        .statusBarsPadding()
                        .height(8.dp),
                )
            }

            item {
                PlayerSurface(
                    mode = PlayerSurfaceMode.Inline,
                    player = player,
                    posterUri = displayPosterUri,
                    streamUri = streamUri,
                    title = playerTitle,
                    onBack = { navController.popBackStack() },
                    onPreviousVideo = onPreviousVideo,
                    onNextVideo = onNextVideo,
                    segments = sponsorBlockPlayback.visibleSegments,
                    showSubtitles = showSubtitles,
                    onToggleSubtitles = { showSubtitles = !showSubtitles },
                    onToggleFullscreen = { isFullscreen = true },
                    controlsVisible = playerControlsVisible,
                    onControlsVisibleChange = { playerControlsVisible = it },
                    previewSpritePath = previewSpritePath,
                    previewTrackJsonPath = previewTrackJsonPath,
                    subtitlePath = subtitlePath,
                    currentPositionMs = { player.currentPosition },
                    sponsorBlockSkipSegment = sponsorBlockPlayback.skipSegment,
                    sponsorBlockAutoSkipMessage = sponsorBlockPlayback.autoSkipMessage,
                    onSkipSponsorBlock = sponsorBlockPlayback.onSkip,
                    levelFeedback = levelFeedback,
                    onBrightnessChange = { level -> showLevelFeedback(brightnessLabel, level) },
                    onVolumeChange = { level -> showLevelFeedback(volumeLabel, level) },
                    modifier = Modifier
                        .fillMaxWidth()
                        .aspectRatio(16f / 9f),
                )
            }

            item {
                VideoMetaBlock(
                    video = video,
                    dearrowMode = dearrowMode,
                    channel = channel,
                    metadataCounts = metadataCounts,
                    isBookmarked = isBookmarked,
                    isFollowed = isFollowed,
                    isStarred = isStarred,
                    hasLocalMedia = hasLocalMedia,
                    onChannelClick = { cid ->
                        navigator.openChannel(cid, IglooNavigationSource.Player)
                    },
                    shareEnabled = canonicalShareUrl != null,
                    onShare = { canonicalShareUrl?.let { sharePlainText(ctx, it, useEmbedFriendlyShareLinks) } },
                    onBookmark = {
                        scope.launch {
                            val prev = outboxWriter.capturePreviousBookmark(videoId)
                            outboxWriter.enqueue(
                                OutboxKind.Bookmark(
                                    videoId = videoId,
                                    action = if (isBookmarked) OutboxKind.Action.Clear else OutboxKind.Action.Set,
                                    categoryId = if (isBookmarked) null else 0L,
                                    prevRow = prev,
                                ),
                            )
                        }
                    },
                    onToggleStar = {
                        val cid = channelId
                        if (cid != null) {
                            scope.launch {
                                outboxWriter.enqueue(
                                    OutboxKind.Star(
                                        channelId = cid,
                                        action = if (isStarred) OutboxKind.Action.Clear else OutboxKind.Action.Set,
                                    ),
                                )
                            }
                        }
                    },
                    onUnfollow = { showUnfollowDialog = true },
                    onDeleteLocal = { showDeleteLocalDialog = true },
                    onMentionClick = vm::resolveMentionAndNavigate,
                    onUrlClick = uriHandler::openUri,
                    onTimestampClick = { targetMs ->
                        player.seekTo(targetMs)
                        scope.launch { listState.animateScrollToItem(0) }
                    },
                )
            }

            item {
                CommentsHeader(
                    isRefreshing = isRefreshingComments,
                    onRefresh = vm::refreshComments,
                )
            }

            if (comments.isEmpty()) {
                item {
                    CommentsEmptyState(isRefreshing = isRefreshingComments)
                }
            } else {
                itemsIndexed(items = comments, key = { _, comment -> comment.commentId }) { _, comment ->
                    CommentRow(
                        comment = comment,
                        threadDepth = commentRenderDepth(comment),
                        replyToAuthor = commentReplyAuthor(comment),
                        isCreator = commentIsCreator(comment),
                        onMentionClick = vm::resolveMentionAndNavigate,
                        onUrlClick = uriHandler::openUri,
                        onTimestampClick = { targetMs ->
                            player.seekTo(targetMs)
                            scope.launch { listState.animateScrollToItem(0) }
                        },
                    )
                }
            }
        }
    }

    if (showDeleteLocalDialog) {
        AlertDialog(
            onDismissRequest = { showDeleteLocalDialog = false },
            title = { Text(stringResource(R.string.action_delete_local_media)) },
            text = { Text(stringResource(R.string.confirm_delete_local_media_body)) },
            confirmButton = {
                TextButton(
                    onClick = {
                        showDeleteLocalDialog = false
                        deleteLocalMedia()
                    },
                ) {
                    Text(stringResource(R.string.action_delete), color = MaterialTheme.colorScheme.error)
                }
            },
            dismissButton = {
                TextButton(onClick = { showDeleteLocalDialog = false }) {
                    Text(stringResource(R.string.action_cancel))
                }
            },
        )
    }

    if (showUnfollowDialog && channelId != null) {
        val channelName = channel?.name ?: stringResource(R.string.confirm_unfollow_channel_default_name)
        AlertDialog(
            onDismissRequest = { showUnfollowDialog = false },
            title = { Text(stringResource(R.string.confirm_unfollow_channel_title)) },
            text = { Text(stringResource(R.string.confirm_unfollow_channel_body, channelName)) },
            confirmButton = {
                TextButton(
                    onClick = {
                        showUnfollowDialog = false
                        scope.launch {
                            outboxWriter.enqueue(
                                OutboxKind.Follow(
                                    channelId = channelId,
                                    action = OutboxKind.Action.Clear,
                                ),
                            )
                        }
                    },
                ) {
                    Text(stringResource(R.string.action_unfollow))
                }
            },
            dismissButton = {
                TextButton(onClick = { showUnfollowDialog = false }) {
                    Text(stringResource(R.string.action_cancel))
                }
            },
        )
    }
}

private tailrec fun Context.findActivity(): Activity? = when (this) {
    is Activity -> this
    is android.content.ContextWrapper -> baseContext.findActivity()
    else -> null
}
