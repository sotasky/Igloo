package com.screwy.igloo.videos

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.entity.VideoGridItem
import com.screwy.igloo.sync.SyncCoordinator
import com.screwy.igloo.ui.UiState
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

/**
 * Grid state for completed device downloads plus server-temporary videos.
 *
 * Paging remains entirely local; refresh is only an explicit request to reconcile the mirror.
 */
class DownloadedVideosViewModel(
    db: IglooDatabase,
    private val scheduler: SyncCoordinator,
) : ViewModel() {
    private val pageLoader = VideoPageLoader(
        scope = viewModelScope,
        pageFlow = db.videoReadDao()::downloadedVideosPageFlow,
    )

    val page: StateFlow<VideoListPageState> = pageLoader.state

    val items: StateFlow<List<VideoGridItem>> = page
        .map { it.items }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = emptyList(),
        )

    val canLoadMore: StateFlow<Boolean> = page
        .map { it.canLoadMore }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = false,
        )

    val isLoadingMore: StateFlow<Boolean> = page
        .map { it.isLoadingMore }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = false,
        )

    val uiState: StateFlow<UiState<Unit>> = page
        .map { state ->
            when {
                state.isInitialLoading -> UiState.Loading
                state.items.isEmpty() -> UiState.Empty
                else -> UiState.Data(Unit)
            }
        }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = UiState.Loading,
        )

    private val _isRefreshing = MutableStateFlow(false)
    val isRefreshing: StateFlow<Boolean> = _isRefreshing.asStateFlow()

    fun loadMore() = pageLoader.loadMore()

    fun refresh() {
        viewModelScope.launch {
            _isRefreshing.value = true
            scheduler.triggerAll()
            delay(1_000L)
            _isRefreshing.value = false
        }
    }
}
