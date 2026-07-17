package com.screwy.igloo.media

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavController
import com.screwy.igloo.ui.UiStateSwitch
import com.screwy.igloo.ui.component.MediaViewer
import org.koin.androidx.compose.koinViewModel
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

    Box(
        modifier = modifier.fillMaxSize().background(Color.Black),
    ) {
        UiStateSwitch(state = uiState, modifier = Modifier.fillMaxSize()) {
            val readyState = mediaState ?: return@UiStateSwitch
            MediaViewer(
                media = readyState.mediaSet,
                initialIndex = readyState.initialIndex,
                onDismiss = { navController.popBackStack() },
                modifier = Modifier.fillMaxSize(),
            )
        }
    }
}
