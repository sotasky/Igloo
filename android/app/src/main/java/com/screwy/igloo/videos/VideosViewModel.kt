package com.screwy.igloo.videos

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.entity.VideoGridItem
import com.screwy.igloo.perf.PerfProbe
import com.screwy.igloo.sync.SchedulerActions
import com.screwy.igloo.sync.SyncStream
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
 * Videos route state holder. Backs `VideosRoute`.
 */
class VideosViewModel(
    db: IglooDatabase,
    private val scheduler: SchedulerActions,
) : ViewModel() {

    private val itemsRaw: StateFlow<List<VideoGridItem>?> = db.videoReadDao()
        .videosFlow()
        .map<List<VideoGridItem>, List<VideoGridItem>?> { rows ->
            PerfProbe.log(event = "full_list_room_emit") {
                mapOf("surface" to "videos", "rows" to rows.size)
            }
            rows
        }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = null,
        )

    val items: StateFlow<List<VideoGridItem>> = itemsRaw
        .map { rows ->
            PerfProbe.timed(
                event = "full_list_map",
                fields = { mapOf("surface" to "videos", "rows" to (rows?.size ?: 0)) },
            ) {
                rows ?: emptyList()
            }
        }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = emptyList(),
        )

    val uiState: StateFlow<UiState<Unit>> = itemsRaw
        .map { list ->
            when {
                list == null -> UiState.Loading
                list.isEmpty() -> UiState.Empty
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

    fun refresh() {
        viewModelScope.launch {
            _isRefreshing.value = true
            scheduler.triggerStream(SyncStream.Youtube)
            // Same pragmatic handshake as FeedViewModel.refresh — hold briefly so the
            // pull-to-refresh spinner paints at least one frame; Room re-emits when the
            // delta lands and drives the real UI update.
            delay(1_000L)
            _isRefreshing.value = false
        }
    }
}
