package com.screwy.igloo.liked

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.entity.FeedRow
import com.screwy.igloo.data.entity.ThreadedFeedRow
import com.screwy.igloo.channel.ChannelRouteResolver
import com.screwy.igloo.feed.FeedMediaGridModel
import com.screwy.igloo.feed.FeedMediaModelStore
import com.screwy.igloo.outbox.OutboxKind
import com.screwy.igloo.outbox.OutboxWriter
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.sync.SyncCoordinator
import com.screwy.igloo.ui.UiEffect
import com.screwy.igloo.ui.UiEffects
import com.screwy.igloo.ui.UiState
import com.screwy.igloo.feed.attachThreadChains
import com.screwy.igloo.ui.component.BookmarkCategoryDisplay
import com.screwy.igloo.ui.component.BookmarkPayload
import com.screwy.igloo.ui.component.BookmarkState
import com.screwy.igloo.ui.component.BookmarkTarget
import com.screwy.igloo.ui.component.toBookmarkState
import com.screwy.igloo.feed.feedMediaCount
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

/**
 * Liked route state holder. Mirrors [com.screwy.igloo.feed.FeedViewModel] with the
 * liked-only query (ordered `liked_at DESC`).
 * "literally just Feed with a filter, keep it simple."
 *
 * Kept as a separate class (not a shared base) so each route owns its own Flow +
 * scope without subclassing ceremony.
 */
class LikedViewModel(
    private val db: IglooDatabase,
    private val outboxWriter: OutboxWriter,
    private val scheduler: SyncCoordinator,
    private val uiEffects: UiEffects,
    baseUrlProvider: ServerBaseUrlProvider,
    reachability: Reachability,
) : ViewModel() {

    private val rowsRaw: StateFlow<List<FeedRow>?> = db.feedReadDao()
        .likedFlow(limit = FEED_LIMIT, offset = 0)
        .map<List<FeedRow>, List<FeedRow>?> { it }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = null,
        )

    val rows: StateFlow<List<ThreadedFeedRow>> = rowsRaw
        .map { rows -> rows?.let { attachThreadChains(db.feedReadDao(), it) } ?: emptyList() }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = emptyList(),
        )

    val uiState: StateFlow<UiState<Unit>> = rowsRaw
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

    private val mediaModelStore = FeedMediaModelStore(
        db = db,
        baseUrlProvider = baseUrlProvider,
        reachability = reachability,
        scope = viewModelScope,
    )
    val mediaModels: StateFlow<Map<String, FeedMediaGridModel>> = mediaModelStore.mediaModels

    private val _isRefreshing = MutableStateFlow(false)
    val isRefreshing: StateFlow<Boolean> = _isRefreshing.asStateFlow()

    private val _pendingBookmark = MutableStateFlow<BookmarkTarget?>(null)
    val pendingBookmark: StateFlow<BookmarkTarget?> = _pendingBookmark.asStateFlow()

    /** Categories for the bookmark sheet — see [FeedViewModel.bookmarkCategories]. */
    val bookmarkCategories: StateFlow<List<BookmarkCategoryDisplay>> =
        db.bookmarkCategoryDao().allFlow()
            .map { entities -> entities.map { BookmarkCategoryDisplay(it.categoryId, it.name) } }
            .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000L), emptyList())

    val mutedChannelIds: StateFlow<Set<String>> = db.mutedChannelDao().allFlow()
        .map { rows -> rows.mapTo(linkedSetOf()) { it.channelId } }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000L), emptySet())

    fun refresh() {
        viewModelScope.launch {
            _isRefreshing.value = true
            scheduler.triggerAll()
            // Same pragmatic handshake as FeedViewModel.refresh — hold briefly so the
            // spinner paints; Room re-emits when the delta lands.
            delay(1_000L)
            _isRefreshing.value = false
        }
    }

    fun toggleLike(tweetId: String, newValue: Boolean) {
        val action = if (newValue) OutboxKind.Action.Set else OutboxKind.Action.Clear
        viewModelScope.launch {
            outboxWriter.enqueue(OutboxKind.Like(tweetId = tweetId, action = action))
        }
    }

    fun toggleFollow(channelId: String, newValue: Boolean) {
        val action = if (newValue) OutboxKind.Action.Set else OutboxKind.Action.Clear
        viewModelScope.launch {
            outboxWriter.enqueue(OutboxKind.Follow(channelId = channelId, action = action))
        }
    }

    fun toggleStar(channelId: String, newValue: Boolean) {
        val action = if (newValue) OutboxKind.Action.Set else OutboxKind.Action.Clear
        viewModelScope.launch {
            outboxWriter.enqueue(OutboxKind.Star(channelId = channelId, action = action))
        }
    }

    fun toggleMute(channelId: String, newValue: Boolean) {
        val action = if (newValue) OutboxKind.Action.Set else OutboxKind.Action.Clear
        viewModelScope.launch {
            outboxWriter.enqueue(OutboxKind.Mute(channelId = channelId, action = action))
        }
    }

    fun toggleBookmark(row: FeedRow) {
        if (row.isBookmarked == 1) {
            viewModelScope.launch {
                _pendingBookmark.value = bookmarkTargetForRow(
                    row = row,
                    currentBookmark = db.bookmarkDao().getById(row.item.tweetId)?.toBookmarkState(),
                )
            }
        } else {
            _pendingBookmark.value = bookmarkTargetForRow(row = row)
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
                )
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
                )
            )
        }
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

    /** See [com.screwy.igloo.feed.FeedViewModel.createCategory]. */
    fun createCategory(name: String) {
        viewModelScope.launch {
            val provisionalId = -System.currentTimeMillis()
            outboxWriter.enqueue(OutboxKind.CreateCategory(name = name, provisionalId = provisionalId))
        }
    }

    fun markSeen(tweetIds: List<String>) {
        if (tweetIds.isEmpty()) return
        viewModelScope.launch {
            for (id in tweetIds) outboxWriter.enqueue(OutboxKind.Seen(tweetId = id))
        }
    }

    fun setMediaModelRows(rows: List<FeedRow>) {
        mediaModelStore.setMediaModelRows(rows)
    }

    fun resolveMentionAndNavigate(handle: String) {
        viewModelScope.launch {
            val route = ChannelRouteResolver.routeForHandle(
                db = db,
                rawHandle = handle,
                fallbackPlatform = "twitter",
            )
            uiEffects.emit(UiEffect.NavigateTo(route))
        }
    }

    companion object {
        /**
         * Keep the liked timeline in the same safe CursorWindow range as the main feed.
         * The previous 10k query shape could crash on-device before the screen rendered.
         */
        const val FEED_LIMIT: Int = 500
    }
}
