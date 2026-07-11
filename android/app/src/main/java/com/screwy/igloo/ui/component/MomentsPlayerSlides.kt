package com.screwy.igloo.ui.component

import android.view.ViewGroup
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.BrokenImage
import androidx.compose.material3.Icon
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.media3.common.MediaItem
import androidx.media3.common.Player
import androidx.media3.exoplayer.ExoPlayer
import coil3.compose.AsyncImage
import com.screwy.igloo.R
import com.screwy.igloo.data.dao.AndroidSyncDao
import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.media.OwnerKind
import com.screwy.igloo.media.assetOwnerKind
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.net.androidSyncAssetPath
import java.io.File
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.launch
import org.koin.compose.koinInject

@Composable
internal fun MomentImageSurface(
    videoId: String,
    ownerKind: OwnerKind,
    thumbnailUri: MediaUri,
    isActive: Boolean,
    autoSwipe: Boolean,
    onAutoAdvance: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val baseUrlProvider: ServerBaseUrlProvider = koinInject()
    val syncDao: AndroidSyncDao = koinInject()
    val reachability: Reachability = koinInject()
    val baseUrl = baseUrlProvider.baseUrl()
    val slideMediaFlow = remember(syncDao, reachability, baseUrl, videoId, ownerKind) {
        momentSlideMediaFlow(
            syncDao = syncDao,
            baseUrl = baseUrl,
            videoId = videoId,
            ownerKind = ownerKind.assetOwnerKind(),
            reachability = reachability,
        )
    }
    val slideMedia by slideMediaFlow.collectAsState(initial = emptyList())

    MomentStillImage(
        mediaUri = slideMedia.firstOrNull()?.uri ?: thumbnailUri,
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
internal fun MomentSlideshowSurface(
    videoId: String,
    ownerKind: OwnerKind,
    thumbnailUri: MediaUri,
    isActive: Boolean,
    autoSwipe: Boolean,
    onAutoAdvance: () -> Unit,
    manualAdvanceTick: Int = 0,
    onManualAdvanceAtEnd: () -> Unit = {},
    muted: Boolean = false,
    modifier: Modifier = Modifier,
) {
    val baseUrlProvider: ServerBaseUrlProvider = koinInject()
    val syncDao: AndroidSyncDao = koinInject()
    val reachability: Reachability = koinInject()
    val baseUrl = baseUrlProvider.baseUrl()
    val slideMediaFlow = remember(syncDao, reachability, baseUrl, videoId, ownerKind) {
        momentSlideMediaFlow(
            syncDao = syncDao,
            baseUrl = baseUrl,
            videoId = videoId,
            ownerKind = ownerKind.assetOwnerKind(),
            reachability = reachability,
        )
    }
    val slideMedia by slideMediaFlow.collectAsState(initial = emptyList())
    val effectiveSlideMedia = remember(slideMedia, thumbnailUri) {
        if (slideMedia.isNotEmpty()) slideMedia else listOf(MomentSlideMedia(thumbnailUri, MomentSlideKind.Image))
    }
    val effectiveSlideCount = effectiveSlideMedia.size.coerceAtLeast(1)
    val pagerState = rememberPagerState(pageCount = { effectiveSlideCount })
    val pagerScope = rememberCoroutineScope()

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
        if (effectiveSlideMedia.isNotEmpty()) {
            HorizontalPager(
                state = pagerState,
                modifier = Modifier.fillMaxSize(),
            ) { page ->
                val slide = effectiveSlideMedia[page]
                when (slide.kind) {
                    MomentSlideKind.Image -> MomentStillImage(
                        mediaUri = slide.uri,
                        contentDescription = stringResource(R.string.content_description_slide_number, page + 1),
                        modifier = Modifier.fillMaxSize(),
                    )
                    MomentSlideKind.Video -> MomentVideoSlide(
                        mediaUri = slide.uri,
                        isActive = isActive && pagerState.currentPage == page,
                        muted = muted,
                        onEnded = {
                            val currentPage = pagerState.currentPage
                            pagerScope.launch {
                                if (currentPage < effectiveSlideCount - 1) {
                                    pagerState.animateScrollToPage(
                                        page = currentPage + 1,
                                        animationSpec = tween(
                                            durationMillis = AUTO_SWIPE_SCROLL_DURATION_MS,
                                            easing = FastOutSlowInEasing,
                                        ),
                                    )
                                } else if (autoSwipe) {
                                    onAutoAdvance()
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
                        },
                        modifier = Modifier.fillMaxSize(),
                    )
                }
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

    LaunchedEffect(videoId, isActive, autoSwipe, effectiveSlideCount) {
        if (!isActive || effectiveSlideCount <= 0) return@LaunchedEffect
        while (true) {
            val pageAtStart = pagerState.currentPage
            delay(MOMENT_SLIDESHOW_ADVANCE_DELAY_MS)
            if (!isActive) return@LaunchedEffect
            if (pagerState.isScrollInProgress || pagerState.currentPage != pageAtStart) {
                continue
            }
            if (effectiveSlideMedia.getOrNull(pageAtStart)?.kind == MomentSlideKind.Video) {
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

internal enum class MomentSlideKind {
    Image,
    Video,
}

internal data class MomentSlideMedia(
    val uri: MediaUri,
    val kind: MomentSlideKind,
)

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

@Composable
private fun MomentVideoSlide(
    mediaUri: MediaUri,
    isActive: Boolean,
    muted: Boolean,
    onEnded: () -> Unit,
    modifier: Modifier = Modifier,
) {
    if (mediaUri == MediaUri.Missing) {
        MomentStillImage(
            mediaUri = MediaUri.Missing,
            contentDescription = stringResource(R.string.content_description_missing_media),
            modifier = modifier,
        )
        return
    }

    val context = LocalContext.current
    val currentOnEnded by rememberUpdatedState(onEnded)
    val player = remember {
        ExoPlayer.Builder(context).build().apply {
            repeatMode = Player.REPEAT_MODE_OFF
        }
    }
    val playerView = remember { createMomentPlayerView(context) }

    DisposableEffect(player) {
        val listener = object : Player.Listener {
            override fun onPlaybackStateChanged(playbackState: Int) {
                if (playbackState == Player.STATE_ENDED) currentOnEnded()
            }
        }
        player.addListener(listener)
        onDispose {
            player.removeListener(listener)
            player.release()
        }
    }

    LaunchedEffect(player, mediaUri) {
        val item = momentSlideVideoMediaItem(mediaUri)
        if (item == null) {
            player.stop()
            player.clearMediaItems()
            return@LaunchedEffect
        }
        player.setMediaItem(item)
        player.prepare()
    }

    LaunchedEffect(player, isActive, muted) {
        player.volume = if (muted) 0f else 1f
        if (isActive) {
            if (player.playbackState == Player.STATE_ENDED) player.seekTo(0)
            player.playWhenReady = true
            player.play()
        } else {
            player.playWhenReady = false
            player.pause()
        }
    }

    AndroidView(
        factory = {
            (playerView.parent as? ViewGroup)?.removeView(playerView)
            if (playerView.player !== player) playerView.player = player
            playerView
        },
        update = { view ->
            if (view.player !== player) view.player = player
        },
        modifier = modifier,
    )

    DisposableEffect(playerView) {
        onDispose {
            playerView.player = null
        }
    }
}

private fun momentSlideVideoMediaItem(mediaUri: MediaUri): MediaItem? = when (mediaUri) {
    is MediaUri.Local -> MediaItem.fromUri(mediaUri.file.toURI().toString())
    is MediaUri.Remote -> MediaItem.fromUri(mediaUri.url)
    MediaUri.Missing -> null
}

private fun momentSlideMediaFlow(
    syncDao: AndroidSyncDao,
    baseUrl: String,
    videoId: String,
    ownerKind: String,
    reachability: Reachability,
) = combine(syncDao.assetsForOwnerFlow(ownerKind, videoId), reachability.state) { syncRows, state ->
        resolveMomentSlideMedia(
            baseUrl = baseUrl,
            syncRows = syncRows,
            allowRemote = state is Reachability.State.Online,
        )
    }
    .distinctUntilChanged()

internal fun momentAudioUriFlow(
    syncDao: AndroidSyncDao,
    baseUrl: String,
    videoId: String,
    ownerKind: String,
    reachability: Reachability,
) = combine(syncDao.assetsForOwnerFlow(ownerKind, videoId), reachability.state) { syncRows, state ->
        resolveMomentAudioUri(
            baseUrl = baseUrl,
            syncRows = syncRows,
            allowRemote = state is Reachability.State.Online,
        )
    }
    .distinctUntilChanged()

internal fun resolveMomentSlideUris(
    baseUrl: String,
    syncRows: List<AndroidSyncAssetEntity> = emptyList(),
    allowRemote: Boolean = true,
): List<MediaUri> = resolveMomentSlideMedia(
    baseUrl = baseUrl,
    syncRows = syncRows,
    allowRemote = allowRemote,
).map { slide -> slide.uri }

internal fun resolveMomentSlideMedia(
    baseUrl: String,
    syncRows: List<AndroidSyncAssetEntity> = emptyList(),
    allowRemote: Boolean = true,
): List<MomentSlideMedia> {
    val syncSlideRows = syncRows
        .asSequence()
        .filter(::isMomentSyncPostMediaAsset)
        .sortedBy(::momentSyncSlideIndex)
        .toList()
    return syncSlideRows.mapNotNull { row ->
        momentSlideKind(row.contentType)?.let { kind ->
            MomentSlideMedia(
                uri = momentSyncAssetToMediaUri(row, baseUrl, allowRemote),
                kind = kind,
            )
        }
    }
}

internal fun resolveMomentAudioUri(
    baseUrl: String,
    syncRows: List<AndroidSyncAssetEntity> = emptyList(),
    allowRemote: Boolean = true,
): MediaUri {
    val syncAudioRow = syncRows.firstOrNull { row ->
        row.assetKind == "post_audio" &&
            row.contentType?.trim()?.startsWith("audio/", ignoreCase = true) == true
    }
    return syncAudioRow?.let { momentSyncAssetToMediaUri(it, baseUrl, allowRemote) } ?: MediaUri.Missing
}

private fun isMomentSyncPostMediaAsset(row: AndroidSyncAssetEntity): Boolean =
    row.assetKind == "post_media"

private fun momentSlideKind(
    contentType: String?,
): MomentSlideKind? {
    val type = contentType?.trim()?.lowercase().orEmpty()
    return when {
        type.startsWith("video/") -> MomentSlideKind.Video
        type.startsWith("image/") -> MomentSlideKind.Image
        else -> null
    }
}

private fun momentSyncSlideIndex(row: AndroidSyncAssetEntity): Int =
    row.mediaIndex

private fun momentSyncAssetToMediaUri(
    row: AndroidSyncAssetEntity,
    baseUrl: String,
    allowRemote: Boolean,
): MediaUri {
    if (!row.localPath.isNullOrBlank()) {
        return MediaUri.Local(File(row.localPath))
    }

    val root = baseUrl.trim().trimEnd('/')
    if (!allowRemote || root.isBlank() || row.state == "server_missing") return MediaUri.Missing
    return MediaUri.Remote(root + androidSyncAssetPath(row.assetId, row.revision))
}

internal fun prepareMomentAudio(
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

internal fun clearMomentAudio(player: ExoPlayer): String? {
    player.playWhenReady = false
    player.pause()
    player.clearMediaItems()
    return null
}
