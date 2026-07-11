package com.screwy.igloo.media

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavController
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.ui.UiStateSwitch
import com.screwy.igloo.ui.component.BookmarkSheet
import com.screwy.igloo.ui.component.MediaViewer
import com.screwy.igloo.ui.component.openExternalUrl
import com.screwy.igloo.ui.component.sharePlainText
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
) {
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
        UiStateSwitch(state = uiState, modifier = Modifier.fillMaxSize()) {
            val readyState = mediaState ?: return@UiStateSwitch
            val actions = actionState
            MediaViewer(
                media = readyState.mediaSet,
                initialIndex = readyState.initialIndex,
                isLiked = actions?.isLiked ?: (readyState.row.isLiked == 1),
                isBookmarked = actions?.isBookmarked ?: (readyState.row.isBookmarked == 1),
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
