package com.screwy.igloo.videos

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavController
import com.screwy.igloo.ui.UiStateSwitch
import com.screwy.igloo.ui.component.VideoGrid
import com.screwy.igloo.ui.nav.IglooNavigationSource
import com.screwy.igloo.ui.nav.rememberIglooNavigator
import org.koin.androidx.compose.koinViewModel

/**
 * YouTube long-form videos tab.
 * `videos` — single-page Room load, pull-to-refresh triggers the YouTube sync stream.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun VideosRoute(
    navController: NavController,
    modifier: Modifier = Modifier,
) {
    val vm: VideosViewModel = koinViewModel()
    val items by vm.items.collectAsStateWithLifecycle()
    val uiState by vm.uiState.collectAsStateWithLifecycle()
    val isRefreshing by vm.isRefreshing.collectAsStateWithLifecycle()
    val navigator = rememberIglooNavigator(navController)

    UiStateSwitch(state = uiState, modifier = modifier) {
        PullToRefreshBox(
            isRefreshing = isRefreshing,
            onRefresh = vm::refresh,
            modifier = Modifier.fillMaxSize(),
        ) {
            VideoGrid(
                items = items,
                columns = 2,
                onVideoClick = { videoId ->
                    navigator.openVideo(videoId, IglooNavigationSource.Videos)
                },
                onVideoClickWithPoster = { videoId, posterUri ->
                    navigator.openVideo(videoId, IglooNavigationSource.Videos, posterUri)
                },
                onChannelClick = { channelId ->
                    navigator.openChannel(channelId, IglooNavigationSource.Videos)
                },
                showScrollFabs = true,
            )
        }
    }
}
