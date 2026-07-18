package com.screwy.igloo.thread

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.screwy.igloo.channel.ChannelRouteResolver
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.data.entity.FeedRow
import com.screwy.igloo.data.entity.FeedRowActionState
import com.screwy.igloo.feed.FeedMediaGridModel
import com.screwy.igloo.feed.FeedMediaModelStore
import com.screwy.igloo.feed.feedMediaCount
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.outbox.OutboxKind
import com.screwy.igloo.outbox.OutboxWriter
import com.screwy.igloo.ui.UiEffect
import com.screwy.igloo.ui.UiEffects
import com.screwy.igloo.ui.component.BookmarkCategoryDisplay
import com.screwy.igloo.ui.component.BookmarkPayload
import com.screwy.igloo.ui.component.BookmarkState
import com.screwy.igloo.ui.component.BookmarkTarget
import com.screwy.igloo.ui.component.normalizeHandle
import com.screwy.igloo.ui.component.toBookmarkState
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

class ThreadViewModel(
    private val db: IglooDatabase,
    private val outboxWriter: OutboxWriter,
    private val uiEffects: UiEffects,
    baseUrlProvider: ServerBaseUrlProvider,
    reachability: Reachability,
) : ViewModel() {
    private val _chain = MutableStateFlow<List<FeedRow>>(emptyList())
    val chain: StateFlow<List<FeedRow>> = _chain.asStateFlow()

    private val mediaModelStore = FeedMediaModelStore(
        db = db,
        baseUrlProvider = baseUrlProvider,
        reachability = reachability,
        scope = viewModelScope,
    )
    val mediaModels: StateFlow<Map<String, FeedMediaGridModel>> = mediaModelStore.mediaModels

    private val _pendingBookmark = MutableStateFlow<BookmarkTarget?>(null)
    val pendingBookmark: StateFlow<BookmarkTarget?> = _pendingBookmark.asStateFlow()

    val bookmarkCategories: StateFlow<List<BookmarkCategoryDisplay>> =
        db.bookmarkCategoryDao().allFlow()
            .map { entities -> entities.map { BookmarkCategoryDisplay(it.categoryId, it.name) } }
            .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000L), emptyList())

    val mutedChannelIds: StateFlow<Set<String>> = db.mutedChannelDao().allFlow()
        .map { rows -> rows.mapTo(linkedSetOf()) { it.channelId } }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000L), emptySet())

    init {
        viewModelScope.launch {
            chain
                .map { rows -> rows.map { it.item.tweetId } }
                .distinctUntilChanged()
                .collectLatest { tweetIds ->
                    if (tweetIds.isEmpty()) return@collectLatest
                    db.feedReadDao().actionStateFlow(tweetIds).collect(::applyActionStates)
                }
        }
    }

    fun load(tweetId: String) {
        viewModelScope.launch {
            loadBlocking(tweetId)
        }
    }

    suspend fun loadBlocking(tweetId: String) {
        val rows = loadThreadRows(tweetId)
        _chain.value = rows
        mediaModelStore.setMediaModelRows(rows)
    }

    fun toggleLike(tweetId: String, newValue: Boolean) {
        val action = if (newValue) OutboxKind.Action.Set else OutboxKind.Action.Clear
        viewModelScope.launch {
            outboxWriter.enqueue(OutboxKind.Like(tweetId = tweetId, action = action))
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

    fun createCategory(name: String) {
        viewModelScope.launch {
            val provisionalId = -System.currentTimeMillis()
            outboxWriter.enqueue(OutboxKind.CreateCategory(name = name, provisionalId = provisionalId))
        }
    }

    fun toggleFollow(channelId: String, newValue: Boolean) {
        val action = if (newValue) OutboxKind.Action.Set else OutboxKind.Action.Clear
        patchRows({ row -> row.item.channelId == channelId || row.quoteChannelId == channelId }) { row ->
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
            _chain.value =
                _chain.value.filterNot { row ->
                    row.item.channelId == channelId || row.item.reposterChannelId == channelId
                }
        }
        viewModelScope.launch {
            outboxWriter.enqueue(OutboxKind.Mute(channelId = channelId, action = action))
        }
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

    fun setMediaModelRows(rows: List<FeedRow>) {
        mediaModelStore.setMediaModelRows(rows)
    }

    private fun applyActionStates(states: List<FeedRowActionState>) {
        if (states.isEmpty()) return

        val byId = states.associateBy { it.tweetId }
        _chain.value = _chain.value.map { row ->
            val state = byId[row.item.tweetId] ?: return@map row
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

    private fun patchRows(
        predicate: (FeedRow) -> Boolean,
        transform: (FeedRow) -> FeedRow,
    ) {
        _chain.value = _chain.value.map { row -> if (predicate(row)) transform(row) else row }
    }

    private suspend fun loadThreadRows(tweetId: String): List<FeedRow> {
        val normalizedTweetId = tweetId.trim()
        val rows = db.feedReadDao().getThreadTree(normalizedTweetId)
        if (rows.isNotEmpty()) return rows

        return db.feedReadDao()
            .getQuoteFallbackSource(normalizedTweetId)
            ?.let { quoteFallbackRow(normalizedTweetId, it) }
            ?.let(::listOf)
            .orEmpty()
    }

    private suspend fun quoteFallbackRow(tweetId: String, sourceRow: FeedRow): FeedRow? {
        val source = sourceRow.item
        val quoteId = source.quoteTweetId?.trim()?.takeIf { it == tweetId } ?: return null
        val quoteHandle = normalizeHandle(sourceRow.quoteAuthorHandle)
        val quoteDisplayName = sourceRow.quoteAuthorDisplayName.trimOrNull()
        val quoteBody = source.quoteBodyText.trimOrNull()
        val quoteMediaJson = source.quoteMediaJson.trimOrNull()
        val hasUsableQuotePayload = quoteHandle.isNotBlank() ||
            quoteDisplayName != null ||
            quoteBody != null ||
            quoteMediaJson != null ||
            source.quoteCanonicalUrl.trimOrNull() != null
        if (!hasUsableQuotePayload) return null

        val quoteChannelId = sourceRow.quoteChannelId.trimOrNull()
        val quoteChannel = quoteChannelId?.let { db.channelDao().getById(it) }
        val quoteProfile = quoteChannelId?.let { db.channelProfileDao().getById(it) }
        val bookmark = db.bookmarkDao().getById(quoteId)
        val quoteIsLiked = if (db.feedLikeDao().exists(quoteId)) 1 else 0
        val quoteIsFollowed = quoteChannelId
            ?.let { if (db.channelFollowDao().exists(it)) 1 else 0 }
            ?: sourceRow.quoteChannelIsFollowed
        val quoteIsStarred = quoteChannelId
            ?.let { if (db.channelStarDao().exists(it)) 1 else 0 }
            ?: 0
        return FeedRow(
            item = FeedItemEntity(
                tweetId = quoteId,
                sourceChannelId = quoteChannelId,
                bodyText = quoteBody,
                lang = source.quoteLang.trimOrNull(),
                mediaJson = quoteMediaJson,
                canonicalUrl = source.quoteCanonicalUrl.trimOrNull(),
                canonicalTweetId = quoteId,
                publishedAt = source.quotePublishedAt,
                channelId = quoteChannelId,
            ),
            channelName = quoteProfile?.displayName.trimOrNull()
                ?: quoteChannel?.name.trimOrNull()
                ?: quoteDisplayName
                ?: quoteHandle.takeIf { it.isNotBlank() },
            channelPlatform = quoteProfile?.platform.trimOrNull()
                ?: quoteChannel?.platform.trimOrNull()
                ?: "twitter",
            authorHandle = quoteHandle,
            authorDisplayName = quoteDisplayName,
            sourceHandle = quoteHandle,
            isLiked = quoteIsLiked,
            likedAt = null,
            isBookmarked = if (bookmark != null) 1 else 0,
            bookmarkCategoryId = bookmark?.categoryId,
            bookmarkCustomTitle = bookmark?.customTitle,
            bookmarkedAt = bookmark?.bookmarkedAt,
            bookmarkAccountHandles = bookmark?.accountHandles,
            bookmarkMediaIndices = bookmark?.mediaIndices,
            channelIsFollowed = quoteIsFollowed,
            channelIsStarred = quoteIsStarred,
        )
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

}

private fun String?.trimOrNull(): String? =
    this?.trim()?.takeIf { it.isNotBlank() }
