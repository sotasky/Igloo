package com.screwy.igloo.media

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.feed.buildFeedMediaSet
import com.screwy.igloo.feed.loadFeedMediaAssetRows
import com.screwy.igloo.feed.mediaViewerInitialIndex
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.ui.UiState
import com.screwy.igloo.ui.component.MediaSet
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

data class MediaRouteState(
    val mediaSet: MediaSet,
    val initialIndex: Int,
)

class MediaRouteViewModel(
    private val ownerKind: String,
    private val ownerId: String,
    private val requestedIndex: Int,
    private val db: IglooDatabase,
    private val baseUrlProvider: ServerBaseUrlProvider,
    private val reachability: Reachability,
) : ViewModel() {
    private val _mediaState = MutableStateFlow<MediaRouteState?>(null)
    val mediaState: StateFlow<MediaRouteState?> = _mediaState.asStateFlow()

    private val _uiState = MutableStateFlow<UiState<Unit>>(UiState.Loading)
    val uiState: StateFlow<UiState<Unit>> = _uiState.asStateFlow()

    init {
        viewModelScope.launch {
            load()
        }
    }

    private suspend fun load() {
        if (ownerKind != OwnerKindRouteTweet) {
            _uiState.value = UiState.Empty
            return
        }
        val row = db.feedReadDao()
            .getThreadChain(ownerId)
            .lastOrNull { it.item.tweetId == ownerId }
        if (row == null) {
            _uiState.value = UiState.Empty
            return
        }
        val mediaSet = buildFeedMediaSet(
            row = row,
            assetRows = loadFeedMediaAssetRows(db, row),
            baseUrl = baseUrlProvider.baseUrl(),
            allowRemote = reachability.state.value is Reachability.State.Online,
        )
        if (mediaSet == null) {
            _uiState.value = UiState.Empty
            return
        }
        _mediaState.value = MediaRouteState(
            mediaSet = mediaSet,
            initialIndex = mediaViewerInitialIndex(row, requestedIndex),
        )
        _uiState.value = UiState.Data(Unit)
    }

    companion object {
        const val OwnerKindRouteTweet = "tweet"
    }
}
