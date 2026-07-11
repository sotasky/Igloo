package com.screwy.igloo.feed

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.screwy.igloo.channel.ChannelRouteResolver
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.entity.FeedHeadCandidate
import com.screwy.igloo.data.entity.FeedRow
import com.screwy.igloo.data.entity.FeedRowActionState
import com.screwy.igloo.data.entity.ThreadedFeedRow
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.outbox.OutboxKind
import com.screwy.igloo.outbox.OutboxWriter
import com.screwy.igloo.sync.SyncCoordinator
import com.screwy.igloo.ui.UiEffect
import com.screwy.igloo.ui.UiEffects
import com.screwy.igloo.ui.UiState
import com.screwy.igloo.ui.component.BookmarkCategoryDisplay
import com.screwy.igloo.ui.component.BookmarkPayload
import com.screwy.igloo.ui.component.BookmarkState
import com.screwy.igloo.ui.component.BookmarkTarget
import com.screwy.igloo.ui.component.NewPostPoster
import com.screwy.igloo.ui.component.parseStoredHandles
import com.screwy.igloo.ui.component.parseStoredMediaIndices
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

/**
 * Main Feed state holder. The native RecyclerView surface gets a bounded immutable session
 * snapshot; seen writes and action changes patch state locally instead of invalidating a pager or
 * mutating a Room deck while the user is scrolling.
 */
class FeedViewModel(
    private val db: IglooDatabase,
    private val outboxWriter: OutboxWriter,
    private val scheduler: SyncCoordinator,
    private val uiEffects: UiEffects,
    private val baseUrlProvider: ServerBaseUrlProvider,
    private val reachability: Reachability,
) : ViewModel() {

    private var snapshotHeadId: String? = null
    private val activeRows = MutableStateFlow<List<ThreadedFeedRow>>(emptyList())
    val rows: StateFlow<List<ThreadedFeedRow>> = activeRows.asStateFlow()

    private val _uiState = MutableStateFlow<UiState<Unit>>(UiState.Loading)
    val uiState: StateFlow<UiState<Unit>> = _uiState.asStateFlow()

    private val _newPostsAvailable = MutableStateFlow(false)
    val newPostsAvailable: StateFlow<Boolean> = _newPostsAvailable.asStateFlow()

    private val _newPostPosters = MutableStateFlow<List<NewPostPoster>>(emptyList())
    val newPostPosters: StateFlow<List<NewPostPoster>> = _newPostPosters.asStateFlow()

    private val mediaModelStore =
        FeedMediaModelStore(
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

    val mutedChannelIds: StateFlow<Set<String>> =
        db.mutedChannelDao()
            .allFlow()
            .map { rows -> rows.mapTo(linkedSetOf()) { it.channelId } }
            .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000L), emptySet())

    val bookmarkCategories: StateFlow<List<BookmarkCategoryDisplay>> =
        db.bookmarkCategoryDao()
            .allFlow()
            .map { entities -> entities.map { BookmarkCategoryDisplay(it.categoryId, it.name) } }
            .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000L), emptyList())

    init {
        viewModelScope.launch { loadSnapshot(resetCue = true) }
        viewModelScope.launch {
            db.feedReadDao()
                .mainFeedHeadCandidatesFlow(NEW_POST_SCAN_LIMIT)
                .collect(::applyNewPostCandidates)
        }
        viewModelScope.launch {
            activeRows
                .map { rows ->
                    rows
                        .flatMap { threaded -> threaded.chain + threaded.row }
                        .map { it.item.tweetId }
                }
                .distinctUntilChanged()
                .collectLatest { tweetIds ->
                    if (tweetIds.isEmpty()) return@collectLatest
                    db.feedReadDao().actionStateFlow(tweetIds).collect(::applyActionStates)
                }
        }
    }

    fun refresh() {
        viewModelScope.launch {
            _isRefreshing.value = true
            scheduler.triggerAll()
            delay(1_000L)
            loadSnapshot(resetCue = true)
            _isRefreshing.value = false
        }
    }

    fun showNewPosts() {
        viewModelScope.launch { loadSnapshot(resetCue = true) }
    }

    fun toggleLike(tweetId: String, newValue: Boolean) {

        val action = if (newValue) OutboxKind.Action.Set else OutboxKind.Action.Clear
        val likedAt = if (newValue) System.currentTimeMillis() else null
        patchRows({ it.item.tweetId == tweetId }) { row ->
            row.copy(isLiked = if (newValue) 1 else 0, likedAt = likedAt)
        }
        viewModelScope.launch {
            outboxWriter.enqueue(OutboxKind.Like(tweetId = tweetId, action = action))
        }
    }

    fun toggleBookmark(row: FeedRow) {

        _pendingBookmark.value =
            if (row.isBookmarked == 1) {
                bookmarkTargetForRow(row = row, currentBookmark = bookmarkStateFromRow(row))
            } else {
                bookmarkTargetForRow(row = row)
            }
    }

    fun dismissBookmarkSheet() {
        _pendingBookmark.value = null
    }

    fun confirmBookmark(payload: BookmarkPayload) {
        val target = _pendingBookmark.value ?: return

        _pendingBookmark.value = null
        patchBookmarkRows(target.itemId) { row ->
            row.copy(
                isBookmarked = 1,
                bookmarkCategoryId = payload.categoryId,
                bookmarkCustomTitle = payload.customTitle,
                bookmarkedAt = System.currentTimeMillis(),
                bookmarkAccountHandles = payload.accountHandles?.joinToString(","),
                bookmarkMediaIndices = payload.mediaIndices?.joinToString(","),
            )
        }
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
        patchBookmarkRows(target.itemId) { row ->
            row.copy(
                isBookmarked = 0,
                bookmarkCategoryId = null,
                bookmarkCustomTitle = null,
                bookmarkedAt = null,
                bookmarkAccountHandles = null,
                bookmarkMediaIndices = null,
            )
        }
        viewModelScope.launch {
            outboxWriter.enqueue(
                OutboxKind.Bookmark(videoId = target.itemId, action = OutboxKind.Action.Clear)
            )
        }
    }

    fun createCategory(name: String) {
        viewModelScope.launch {
            val provisionalId = -System.currentTimeMillis()
            outboxWriter.enqueue(
                OutboxKind.CreateCategory(name = name, provisionalId = provisionalId)
            )
        }
    }

    fun toggleFollow(channelId: String, newValue: Boolean) {

        val action = if (newValue) OutboxKind.Action.Set else OutboxKind.Action.Clear
        patchRows({ row -> row.item.channelId == channelId || row.quoteChannelId == channelId }) {
            row ->
            row.copy(
                channelIsFollowed =
                    if (row.item.channelId == channelId) {
                        if (newValue) 1 else 0
                    } else {
                        row.channelIsFollowed
                    },
                quoteChannelIsFollowed =
                    if (row.quoteChannelId == channelId) {
                        if (newValue) 1 else 0
                    } else {
                        row.quoteChannelIsFollowed
                    },
            )
        }
        viewModelScope.launch {
            outboxWriter.enqueue(OutboxKind.Follow(channelId = channelId, action = action))
        }
    }

    fun toggleStar(channelId: String, newValue: Boolean) {

        val action = if (newValue) OutboxKind.Action.Set else OutboxKind.Action.Clear
        patchRows({ it.item.channelId == channelId }) { row ->
            row.copy(channelIsStarred = if (newValue) 1 else 0)
        }
        viewModelScope.launch {
            outboxWriter.enqueue(OutboxKind.Star(channelId = channelId, action = action))
        }
    }

    fun toggleMute(channelId: String, newValue: Boolean) {
        val action = if (newValue) OutboxKind.Action.Set else OutboxKind.Action.Clear
        if (newValue) {
            val filteredRows =
                activeRows.value.filterNot { threaded ->
                    val row = threaded.row.item
                    row.channelId == channelId || row.reposterChannelId == channelId
                }
            activeRows.value = filteredRows
            snapshotHeadId = filteredRows.firstOrNull()?.row?.item?.tweetId
            _newPostsAvailable.value = false
            _newPostPosters.value = emptyList()
        }
        viewModelScope.launch {
            outboxWriter.enqueue(OutboxKind.Mute(channelId = channelId, action = action))
        }
    }

    fun markSeen(tweetIds: List<String>) {
        if (tweetIds.isEmpty()) return

        viewModelScope.launch {
            for (id in tweetIds) {
                outboxWriter.enqueue(OutboxKind.Seen(tweetId = id))
            }
        }
    }

    fun setMediaModelRows(rows: List<FeedRow>) {

        mediaModelStore.setMediaModelRows(rows)
    }

    fun resolveMentionAndNavigate(handle: String) {
        viewModelScope.launch {
            val route =
                ChannelRouteResolver.routeForHandle(
                    db = db,
                    rawHandle = handle,
                    fallbackPlatform = "twitter",
                )
            uiEffects.emit(UiEffect.NavigateTo(route))
        }
    }

    private suspend fun loadSnapshot(resetCue: Boolean) {
        val rows = db.feedReadDao().feedFlow(limit = FEED_LIMIT, offset = 0).first()

        val threadedRows = attachThreadChains(db.feedReadDao(), rows)

        if (resetCue) {
            _newPostsAvailable.value = false
            _newPostPosters.value = emptyList()
        }
        activeRows.value = threadedRows
        snapshotHeadId = threadedRows.firstOrNull()?.row?.item?.tweetId
        if (threadedRows.isNotEmpty()) {
            val mediaRows =
                threadedRows.take(INITIAL_MEDIA_MODEL_ROWS).flatMap { threaded ->
                    threaded.chain + threaded.row
                }
            mediaModelStore.setMediaModelRows(mediaRows)
        }
        _uiState.value = if (threadedRows.isEmpty()) UiState.Empty else UiState.Data(Unit)
    }

    private fun applyNewPostCandidates(incoming: List<FeedHeadCandidate>) {
        val currentRows = activeRows.value
        val currentHeadId = snapshotHeadId ?: currentRows.firstOrNull()?.row?.item?.tweetId
        if (currentHeadId.isNullOrBlank() || incoming.isEmpty()) {
            _newPostsAvailable.value = false
            _newPostPosters.value = emptyList()
            return
        }

        if (incoming.first().tweetId == currentHeadId) {
            _newPostsAvailable.value = false
            _newPostPosters.value = emptyList()
            return
        }

        val currentHeadInIncoming = incoming.indexOfFirst { it.tweetId == currentHeadId }
        val currentIds =
            currentRows
                .flatMap { threaded -> threaded.chain + threaded.row }
                .mapTo(hashSetOf()) { it.item.tweetId }
        val candidates =
            if (currentHeadInIncoming > 0) {
                incoming.take(currentHeadInIncoming)
            } else {
                incoming.filter { it.tweetId !in currentIds }
            }

        if (candidates.isEmpty()) {
            _newPostsAvailable.value = false
            _newPostPosters.value = emptyList()
            return
        }
        _newPostsAvailable.value = true
        _newPostPosters.value = buildNewPostPosters(candidates)
    }

    private fun buildNewPostPosters(candidates: List<FeedHeadCandidate>): List<NewPostPoster> {
        val seenHandles = linkedSetOf<String>()
        return candidates
            .mapNotNull { row ->
                val handle = row.authorHandle.trim().trimStart('@')
                if (handle.isEmpty()) return@mapNotNull null
                val key = handle.lowercase()
                if (!seenHandles.add(key)) return@mapNotNull null
                NewPostPoster(
                    channelId = row.channelId?.trim()?.takeIf { it.isNotEmpty() } ?: "twitter_$key",
                    contentDescription = row.authorDisplayName?.takeIf { it.isNotBlank() } ?: handle,
                )
            }
            .take(3)
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

    private fun bookmarkStateFromRow(row: FeedRow): BookmarkState? {
        if (row.isBookmarked != 1) return null
        return BookmarkState(
            categoryId = row.bookmarkCategoryId ?: 0L,
            customTitle = row.bookmarkCustomTitle,
            mediaIndices = parseStoredMediaIndices(row.bookmarkMediaIndices),
            accountHandles = parseStoredHandles(row.bookmarkAccountHandles),
        )
    }

    private fun applyActionStates(states: List<FeedRowActionState>) {
        if (states.isEmpty()) return

        val byId = states.associateBy { it.tweetId }
        patchRows({ row -> row.item.tweetId in byId }) { row ->
            val state = byId.getValue(row.item.tweetId)
            row.copy(
                isLiked = state.isLiked,
                likedAt = state.likedAt,
                isBookmarked = state.isBookmarked,
                bookmarkCategoryId = state.bookmarkCategoryId,
                bookmarkCustomTitle = state.bookmarkCustomTitle,
                bookmarkedAt = state.bookmarkedAt,
                bookmarkAccountHandles = state.bookmarkAccountHandles,
                bookmarkMediaIndices = state.bookmarkMediaIndices,
            )
        }
    }

    private fun patchBookmarkRows(tweetId: String, transform: (FeedRow) -> FeedRow) {
        val targetHash =
            activeRows.value
                .asSequence()
                .flatMap { threaded -> (threaded.chain + threaded.row).asSequence() }
                .firstOrNull { it.item.tweetId == tweetId }
                ?.item
                ?.contentHash
                ?.trim()
                ?.takeIf { it.isNotBlank() }
        patchRows(
            { row ->
                row.item.tweetId == tweetId ||
                    (targetHash != null && row.item.contentHash?.trim() == targetHash)
            },
            transform,
        )
    }

    private fun patchRows(predicate: (FeedRow) -> Boolean, transform: (FeedRow) -> FeedRow) {
        activeRows.value =
            activeRows.value.map { threaded ->
                threaded.copy(
                    row = threaded.row.let { row -> if (predicate(row)) transform(row) else row },
                    chain =
                        threaded.chain.map { row -> if (predicate(row)) transform(row) else row },
                )
            }
    }

    companion object {
        const val FEED_LIMIT: Int = 200
        private const val INITIAL_MEDIA_MODEL_ROWS: Int = 16
        private const val NEW_POST_SCAN_LIMIT: Int = 80
    }
}
