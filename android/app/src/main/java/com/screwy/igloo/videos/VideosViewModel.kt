package com.screwy.igloo.videos

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.entity.VideoGridItem
import com.screwy.igloo.sync.SyncCoordinator
import com.screwy.igloo.ui.UiState
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.collect
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

private const val VideoPageSize = 40

/** State for a vertically paged, local Room-backed video list. */
data class VideoListPageState(
    val items: List<VideoGridItem> = emptyList(),
    val isInitialLoading: Boolean = true,
    val isLoadingMore: Boolean = false,
    val canLoadMore: Boolean = false,
)

/**
 * Expands a bounded Room query as the grid reaches its end.
 *
 * Each [loadMore] call only changes the local query limit. It never triggers a sync or any
 * network request. Keeping the current visible range as one Room flow also means inserts,
 * removals, and changed download state stay reflected while the user is scrolling.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class VideoPageLoader(
    scope: CoroutineScope,
    private val pageFlow: (limit: Int) -> Flow<List<VideoGridItem>>,
    private val pageSize: Int = VideoPageSize,
) {
    private val requestedItemCount = MutableStateFlow(pageSize)
    private val _state = MutableStateFlow(VideoListPageState())
    val state: StateFlow<VideoListPageState> = _state.asStateFlow()

    init {
        require(pageSize > 0) { "pageSize must be positive" }
        scope.launch {
            requestedItemCount
                .flatMapLatest { requested ->
                    // One extra row tells the grid whether another local page exists.
                    pageFlow(requested + 1).map { rows -> requested to rows }
                }
                .collect { (requested, rows) ->
                    _state.value = VideoListPageState(
                        items = rows.take(requested),
                        isInitialLoading = false,
                        isLoadingMore = false,
                        canLoadMore = rows.size > requested,
                    )
                }
        }
    }

    fun loadMore() {
        val current = _state.value
        if (current.isInitialLoading || current.isLoadingMore || !current.canLoadMore) return

        _state.update { it.copy(isLoadingMore = true) }
        requestedItemCount.value += pageSize
    }
}

/** Videos route state holder. Backs `VideosRoute`. */
class VideosViewModel(db: IglooDatabase, private val scheduler: SyncCoordinator) : ViewModel() {

    private val pageLoader = VideoPageLoader(
        scope = viewModelScope,
        pageFlow = db.videoReadDao()::youtubeVideosPageFlow,
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
            // Same pragmatic handshake as FeedViewModel.refresh — hold briefly so the
            // pull-to-refresh spinner paints at least one frame; the bounded Room query re-emits
            // when the delta lands and drives the real UI update.
            delay(1_000L)
            _isRefreshing.value = false
        }
    }
}
