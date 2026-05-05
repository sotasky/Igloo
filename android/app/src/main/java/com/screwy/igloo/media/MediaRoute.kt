package com.screwy.igloo.media

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavController
import com.screwy.igloo.data.DatabaseHolder
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.outbox.OutboxKind
import com.screwy.igloo.outbox.OutboxWriter
import com.screwy.igloo.ui.UiStateSwitch
import com.screwy.igloo.ui.component.BookmarkSheet
import com.screwy.igloo.ui.component.BookmarkCategoryDisplay
import com.screwy.igloo.ui.component.BookmarkPayload
import com.screwy.igloo.ui.component.BookmarkTarget
import com.screwy.igloo.ui.component.MediaCellArtwork
import com.screwy.igloo.ui.component.MediaSet
import com.screwy.igloo.ui.component.MediaViewer
import com.screwy.igloo.ui.component.openExternalUrl
import com.screwy.igloo.ui.component.sharePlainText
import com.screwy.igloo.ui.component.toBookmarkState
import com.screwy.igloo.ui.UiState
import com.screwy.igloo.ui.nav.IglooNavigation
import com.screwy.igloo.ui.nav.IglooNavigationSource
import com.screwy.igloo.ui.nav.MediaOpenSnapshot
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.launch
import org.koin.androidx.compose.koinViewModel
import org.koin.compose.koinInject
import org.koin.core.parameter.parametersOf

@Composable
fun MediaRoute(
    ownerKind: String,
    ownerId: String,
    index: Int,
    navController: NavController,
    modifier: Modifier = Modifier,
    initialSnapshot: MediaOpenSnapshot? = null,
) {
    val snapshotMedia = initialSnapshot?.mediaSet
    if (snapshotMedia != null) {
        SnapshotMediaRoute(
            snapshot = initialSnapshot,
            media = snapshotMedia,
            navController = navController,
            modifier = modifier,
        )
        return
    }

    val vm: MediaRouteViewModel = koinViewModel(
        parameters = { parametersOf(ownerKind, ownerId, index) },
    )
    val uiState by vm.uiState.collectAsStateWithLifecycle()
    val mediaState by vm.mediaState.collectAsStateWithLifecycle()
    val actionState by vm.actionState.collectAsStateWithLifecycle()
    val pendingBookmark by vm.pendingBookmark.collectAsStateWithLifecycle()
    val categories by vm.bookmarkCategories.collectAsStateWithLifecycle()
    val prefs: PreferencesRepo = koinInject()
    val useEmbedFriendlyShareLinks by prefs.shareEmbedFriendlyLinks()
        .collectAsStateWithLifecycle(initialValue = PreferencesRepo.Defaults.SHARE_EMBED_FRIENDLY_LINKS)
    val context = LocalContext.current

    Box(
        modifier = modifier
            .fillMaxSize()
            .background(Color.Black),
    ) {
        val snapshot = initialSnapshot
        val state = mediaState
        val snapshotMedia = snapshot?.mediaSet
        val displayMedia = state?.mediaSet ?: snapshotMedia
        if (displayMedia != null) {
            val actions = actionState
            MediaViewer(
                media = displayMedia,
                initialIndex = state?.initialIndex ?: snapshot?.index ?: index,
                isLiked = actions?.isLiked ?: snapshot?.isLiked ?: (state?.row?.isLiked == 1),
                isBookmarked = actions?.isBookmarked ?: snapshot?.isBookmarked ?: (state?.row?.isBookmarked == 1),
                onDismiss = { navController.popBackStack() },
                onBookmarkToggle = vm::toggleBookmark,
                onLikeToggle = vm::toggleLike,
                onAuthorClick = vm::openAuthor,
                onShare = { url -> sharePlainText(context, url, useEmbedFriendlyShareLinks) },
                onOpenExternal = { url -> openExternalUrl(context, url) },
                modifier = Modifier.fillMaxSize(),
            )
        } else if (uiState is UiState.Loading && snapshot != null && snapshot.posterUri !is MediaUri.Missing) {
            MediaRouteLoadingPoster(
                posterUri = snapshot.posterUri,
                modifier = Modifier.fillMaxSize(),
            )
        } else {
            UiStateSwitch(state = uiState, modifier = Modifier.fillMaxSize()) {
                val readyState = mediaState ?: return@UiStateSwitch
                val actions = actionState
                MediaViewer(
                    media = readyState.mediaSet,
                    initialIndex = readyState.initialIndex,
                    isLiked = actions?.isLiked ?: initialSnapshot?.isLiked ?: (readyState.row.isLiked == 1),
                    isBookmarked = actions?.isBookmarked ?: initialSnapshot?.isBookmarked ?: (readyState.row.isBookmarked == 1),
                    onDismiss = { navController.popBackStack() },
                    onBookmarkToggle = vm::toggleBookmark,
                    onLikeToggle = vm::toggleLike,
                    onAuthorClick = vm::openAuthor,
                    onShare = { url -> sharePlainText(context, url, useEmbedFriendlyShareLinks) },
                    onOpenExternal = { url -> openExternalUrl(context, url) },
                    modifier = Modifier.fillMaxSize(),
                )
            }
        }
    }

    pendingBookmark?.let { target ->
        BookmarkSheet(
            target = target,
            categories = categories,
            onConfirm = vm::confirmBookmark,
            onRemove = vm::removePendingBookmark,
            onDismiss = vm::dismissBookmarkSheet,
            onCreateCategory = vm::createCategory,
        )
    }
}

@Composable
private fun SnapshotMediaRoute(
    snapshot: MediaOpenSnapshot,
    media: MediaSet,
    navController: NavController,
    modifier: Modifier = Modifier,
) {
    val context = LocalContext.current
    val databaseHolder: DatabaseHolder = koinInject()
    val prefs: PreferencesRepo = koinInject()
    val useEmbedFriendlyShareLinks by prefs.shareEmbedFriendlyLinks()
        .collectAsStateWithLifecycle(initialValue = PreferencesRepo.Defaults.SHARE_EMBED_FRIENDLY_LINKS)
    val db = remember(databaseHolder) { databaseHolder.requireCurrent() }
    val outboxWriter: OutboxWriter = koinInject()
    val coroutineScope = rememberCoroutineScope()
    val categories by db.bookmarkCategoryDao()
        .allFlow()
        .map { entities -> entities.map { BookmarkCategoryDisplay(it.categoryId, it.name) } }
        .collectAsStateWithLifecycle(initialValue = emptyList())
    val likeRow by db.feedLikeDao()
        .getByIdFlow(snapshot.ownerId)
        .collectAsStateWithLifecycle(initialValue = null)
    val bookmarkRow by db.bookmarkDao()
        .getByIdFlow(snapshot.ownerId)
        .collectAsStateWithLifecycle(initialValue = null)
    var isLiked by remember(snapshot.ownerId) { mutableStateOf(snapshot.isLiked) }
    var isBookmarked by remember(snapshot.ownerId) { mutableStateOf(snapshot.isBookmarked) }
    var pendingBookmark by remember(snapshot.ownerId) { mutableStateOf<BookmarkTarget?>(null) }

    LaunchedEffect(likeRow) {
        likeRow?.let { isLiked = true }
    }
    LaunchedEffect(bookmarkRow) {
        bookmarkRow?.let { isBookmarked = true }
    }

    MediaViewer(
        media = media,
        initialIndex = snapshot.index,
        isLiked = isLiked,
        isBookmarked = isBookmarked,
        onDismiss = { navController.popBackStack() },
        onBookmarkToggle = {
            pendingBookmark = BookmarkTarget(
                itemId = snapshot.ownerId,
                authorHandle = media.authorHandle,
                mediaCount = media.items.size,
                currentBookmark = if (isBookmarked) bookmarkRow?.toBookmarkState() else null,
                defaultTitle = media.bodyText.lineSequence().firstOrNull(),
                bodyText = media.bodyText,
            )
        },
        onLikeToggle = {
            val next = !isLiked
            isLiked = next
            coroutineScope.launch {
                outboxWriter.enqueue(
                    OutboxKind.Like(
                        tweetId = snapshot.ownerId,
                        action = if (next) OutboxKind.Action.Set else OutboxKind.Action.Clear,
                    ),
                )
            }
        },
        onAuthorClick = {
            IglooNavigation
                .routeForChannel(media.authorChannelId, IglooNavigationSource.MediaViewer)
                ?.let(navController::navigate)
        },
        onShare = { url -> sharePlainText(context, url, useEmbedFriendlyShareLinks) },
        onOpenExternal = { url -> openExternalUrl(context, url) },
        modifier = modifier.fillMaxSize(),
    )

    pendingBookmark?.let { target ->
        BookmarkSheet(
            target = target,
            categories = categories,
            onConfirm = { payload: BookmarkPayload ->
                pendingBookmark = null
                isBookmarked = true
                coroutineScope.launch {
                    val prev = outboxWriter.capturePreviousBookmark(target.itemId)
                    outboxWriter.enqueue(
                        OutboxKind.Bookmark(
                            videoId = target.itemId,
                            action = OutboxKind.Action.Set,
                            categoryId = payload.categoryId,
                            customTitle = payload.customTitle,
                            accountHandles = payload.accountHandles?.joinToString(","),
                            mediaIndices = payload.mediaIndices?.joinToString(","),
                            prevRow = prev,
                        ),
                    )
                }
            },
            onRemove = {
                pendingBookmark = null
                isBookmarked = false
                coroutineScope.launch {
                    val prev = outboxWriter.capturePreviousBookmark(target.itemId)
                    outboxWriter.enqueue(
                        OutboxKind.Bookmark(
                            videoId = target.itemId,
                            action = OutboxKind.Action.Clear,
                            prevRow = prev,
                        ),
                    )
                }
            },
            onDismiss = { pendingBookmark = null },
            onCreateCategory = { name ->
                coroutineScope.launch {
                    outboxWriter.enqueue(
                        OutboxKind.CreateCategory(
                            name = name,
                            provisionalId = -System.currentTimeMillis(),
                        ),
                    )
                }
            },
        )
    }
}

@Composable
private fun MediaRouteLoadingPoster(
    posterUri: MediaUri,
    modifier: Modifier = Modifier,
) {
    Box(
        modifier = modifier.background(Color.Black),
    ) {
        MediaCellArtwork(
            thumbnailUri = posterUri,
            contentDescription = null,
            contentScale = ContentScale.Fit,
        )
    }
}
