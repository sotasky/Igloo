package com.screwy.igloo.media

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.screwy.igloo.channel.ChannelRouteResolver
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.entity.FeedRow
import com.screwy.igloo.feed.buildFeedMediaSet
import com.screwy.igloo.feed.feedMediaCount
import com.screwy.igloo.feed.loadFeedMediaAssetRows
import com.screwy.igloo.feed.mediaViewerInitialIndex
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.outbox.OutboxKind
import com.screwy.igloo.outbox.OutboxWriter
import com.screwy.igloo.ui.UiEffect
import com.screwy.igloo.ui.UiEffects
import com.screwy.igloo.ui.UiState
import com.screwy.igloo.ui.component.BookmarkCategoryDisplay
import com.screwy.igloo.ui.component.BookmarkPayload
import com.screwy.igloo.ui.component.BookmarkState
import com.screwy.igloo.ui.component.BookmarkTarget
import com.screwy.igloo.ui.component.MediaSet
import com.screwy.igloo.ui.component.toBookmarkState
import com.screwy.igloo.ui.nav.IglooNavigation
import com.screwy.igloo.ui.nav.IglooNavigationSource
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

data class MediaRouteState(
    val mediaSet: MediaSet,
    val row: FeedRow,
    val initialIndex: Int,
)

data class MediaActionState(
    val isLiked: Boolean,
    val isBookmarked: Boolean,
)

class MediaRouteViewModel(
    private val ownerKind: String,
    private val ownerId: String,
    private val requestedIndex: Int,
    private val db: IglooDatabase,
    private val outboxWriter: OutboxWriter,
    private val baseUrlProvider: ServerBaseUrlProvider,
    private val reachability: Reachability,
    private val uiEffects: UiEffects,
) : ViewModel() {
    private val _baseMediaState = MutableStateFlow<MediaRouteState?>(null)
    val mediaState: StateFlow<MediaRouteState?> = combine(
        _baseMediaState,
        db.feedLikeDao().getByIdFlow(ownerId),
        db.bookmarkDao().getByIdFlow(ownerId),
    ) { state, like, bookmark ->
        state?.copy(
            row = state.row.copy(
                isLiked = if (like != null) 1 else 0,
                likedAt = like?.likedAt,
                isBookmarked = if (bookmark != null) 1 else 0,
                bookmarkCategoryId = bookmark?.categoryId,
                bookmarkCustomTitle = bookmark?.customTitle,
                bookmarkedAt = bookmark?.bookmarkedAt,
            ),
        )
    }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000L), null)

    private val _actionState = MutableStateFlow<MediaActionState?>(null)
    val actionState: StateFlow<MediaActionState?> = _actionState.asStateFlow()

    private val _uiState = MutableStateFlow<UiState<Unit>>(UiState.Loading)
    val uiState: StateFlow<UiState<Unit>> = _uiState.asStateFlow()

    private val _pendingBookmark = MutableStateFlow<BookmarkTarget?>(null)
    val pendingBookmark: StateFlow<BookmarkTarget?> = _pendingBookmark.asStateFlow()

    val bookmarkCategories: StateFlow<List<BookmarkCategoryDisplay>> =
        db.bookmarkCategoryDao().allFlow()
            .map { entities -> entities.map { BookmarkCategoryDisplay(it.categoryId, it.name) } }
            .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000L), emptyList())

    init {
        viewModelScope.launch {
            load()
        }
        if (ownerKind == OwnerKindRouteTweet) {
            viewModelScope.launch {
                combine(
                    db.feedLikeDao().getByIdFlow(ownerId),
                    db.bookmarkDao().getByIdFlow(ownerId),
                ) { like, bookmark ->
                    MediaActionState(
                        isLiked = like != null,
                        isBookmarked = bookmark != null,
                    )
                }.collect { state ->
                    _actionState.value = state
                }
            }
        }
    }

    fun toggleLike() {
        val row = mediaState.value?.row ?: _baseMediaState.value?.row
        val tweetId = row?.item?.tweetId ?: ownerId.takeIf { ownerKind == OwnerKindRouteTweet } ?: return
        val isLiked = _actionState.value?.isLiked ?: (row?.isLiked == 1)
        val action = if (isLiked) OutboxKind.Action.Clear else OutboxKind.Action.Set
        viewModelScope.launch {
            outboxWriter.enqueue(OutboxKind.Like(tweetId = tweetId, action = action))
        }
    }

    fun toggleBookmark() {
        val row = mediaState.value?.row ?: _baseMediaState.value?.row
        val itemId = row?.item?.tweetId ?: ownerId.takeIf { ownerKind == OwnerKindRouteTweet } ?: return
        val isBookmarked = _actionState.value?.isBookmarked ?: (row?.isBookmarked == 1)
        if (isBookmarked) {
            viewModelScope.launch {
                _pendingBookmark.value = if (row != null) {
                    bookmarkTargetForRow(
                        row = row,
                        currentBookmark = db.bookmarkDao().getById(row.item.tweetId)?.toBookmarkState(),
                    )
                } else {
                    basicBookmarkTarget(itemId)
                }
            }
        } else {
            _pendingBookmark.value = row?.let(::bookmarkTargetForRow) ?: basicBookmarkTarget(itemId)
        }
    }

    fun dismissBookmarkSheet() {
        _pendingBookmark.value = null
    }

    fun confirmBookmark(payload: BookmarkPayload) {
        val target = _pendingBookmark.value ?: return
        _pendingBookmark.value = null
        viewModelScope.launch {
            outboxWriter.enqueue(
                OutboxKind.Bookmark(
                    videoId = target.itemId,
                    action = OutboxKind.Action.Set,
                    categoryId = payload.categoryId,
                    customTitle = payload.customTitle,
                    accountHandles = payload.accountHandles?.joinToString(","),
                    mediaIndices = payload.mediaIndices?.joinToString(","),
                ),
            )
        }
    }

    fun removePendingBookmark() {
        val target = _pendingBookmark.value ?: return
        _pendingBookmark.value = null
        viewModelScope.launch {
            outboxWriter.enqueue(
                OutboxKind.Bookmark(
                    videoId = target.itemId,
                    action = OutboxKind.Action.Clear,
                ),
            )
        }
    }

    fun createCategory(name: String) {
        viewModelScope.launch {
            val provisionalId = -System.currentTimeMillis()
            outboxWriter.enqueue(OutboxKind.CreateCategory(name = name, provisionalId = provisionalId))
        }
    }

    fun openAuthor() {
        viewModelScope.launch {
            val row = mediaState.value?.row ?: return@launch
            uiEffects.emit(
                UiEffect.NavigateTo(
                    authorRoute(
                        channelId = row.item.channelId,
                        handle = row.authorHandle,
                    ),
                ),
            )
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
        _baseMediaState.value = MediaRouteState(
            mediaSet = mediaSet,
            row = row,
            initialIndex = mediaViewerInitialIndex(row, requestedIndex),
        )
        _uiState.value = UiState.Data(Unit)
    }

    private fun bookmarkTargetForRow(
        row: FeedRow,
        currentBookmark: BookmarkState? = null,
    ): BookmarkTarget =
        BookmarkTarget(
            itemId = row.item.tweetId,
            authorHandle = row.authorHandle.orEmpty(),
            mediaCount = feedMediaCount(row.item),
            currentBookmark = currentBookmark,
            defaultTitle = row.item.bodyText?.lineSequence()?.firstOrNull(),
            sourceHandle = row.sourceHandle,
            quoteAuthorHandle = row.quoteAuthorHandle,
            bodyText = row.item.bodyText,
            isRetweet = row.item.isRetweet,
        )

    private fun basicBookmarkTarget(itemId: String): BookmarkTarget =
        BookmarkTarget(
            itemId = itemId,
            authorHandle = "",
            mediaCount = 0,
        )

    private suspend fun authorRoute(channelId: String?, handle: String?): String {
        val normalizedChannelId = channelId?.trim().orEmpty()
        IglooNavigation.routeForChannel(normalizedChannelId, IglooNavigationSource.MediaViewer)?.let {
            return it
        }
        return ChannelRouteResolver.routeForHandle(
            db = db,
            rawHandle = handle.orEmpty(),
            fallbackPlatform = "twitter",
        )
    }

    companion object {
        const val OwnerKindRouteTweet = "tweet"
    }
}
