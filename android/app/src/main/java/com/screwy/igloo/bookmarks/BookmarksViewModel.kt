package com.screwy.igloo.bookmarks

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.screwy.igloo.channel.ChannelRouteResolver
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.stripPlatformPrefix
import com.screwy.igloo.data.entity.BookmarkCategoryEntity
import com.screwy.igloo.data.entity.BookmarkItem
import com.screwy.igloo.outbox.OutboxKind
import com.screwy.igloo.outbox.OutboxWriter
import com.screwy.igloo.ui.component.BookmarkCategoryDisplay
import com.screwy.igloo.ui.component.BookmarkPayload
import com.screwy.igloo.ui.component.BookmarkState
import com.screwy.igloo.ui.component.BookmarkTarget
import com.screwy.igloo.ui.component.parseStoredMediaIndices
import com.screwy.igloo.ui.component.parseStoredHandles
import com.screwy.igloo.ui.UiState
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

sealed class BookmarkFilter {
    object All : BookmarkFilter()
    data class Category(val categoryId: Long) : BookmarkFilter()
    data class Label(val label: String) : BookmarkFilter()
    object NoLabel : BookmarkFilter()
}

data class BookmarkLabelCount(
    val label: String?,
    val count: Int,
)

/** Bookmarks route state holder for the category/label-filtered grid. */
class BookmarksViewModel(
    private val db: IglooDatabase,
    private val outboxWriter: OutboxWriter,
    private val prefs: PreferencesRepo,
) : ViewModel() {

    /**
     * Player toggles mirrored from [PreferencesRepo] so the bookmarks-overlay
     * [com.screwy.igloo.ui.component.MomentsPlayer] reads + writes the same
     * auto-swipe / mute bit as the Moments tab — one app-wide setting, not
     * three per-screen copies.
     */
    val autoplayEnabled: StateFlow<Boolean> = prefs.autoplay().stateIn(
        scope = viewModelScope,
        started = SharingStarted.WhileSubscribed(5_000L),
        initialValue = PreferencesRepo.Defaults.AUTOPLAY,
    )

    val muted: StateFlow<Boolean> = prefs.muteDefault().stateIn(
        scope = viewModelScope,
        started = SharingStarted.WhileSubscribed(5_000L),
        initialValue = PreferencesRepo.Defaults.MUTE_DEFAULT,
    )

    fun setAutoplayEnabled(enabled: Boolean) {
        viewModelScope.launch { prefs.setAutoplay(enabled) }
    }

    fun setMuted(enabled: Boolean) {
        viewModelScope.launch { prefs.setMuteDefault(enabled) }
    }


    private val selectedBookmarkFilter = MutableStateFlow<BookmarkFilter>(BookmarkFilter.All)
    val selectedFilter: StateFlow<BookmarkFilter> = selectedBookmarkFilter.asStateFlow()

    fun selectAll() {
        selectedBookmarkFilter.value = BookmarkFilter.All
    }

    fun selectCategory(id: Long?) {
        selectedBookmarkFilter.value = id?.let(BookmarkFilter::Category) ?: BookmarkFilter.All
    }

    fun selectLabel(label: String) {
        val normalized = normalizeBookmarkLabel(label)
        selectedBookmarkFilter.value = normalized?.let(BookmarkFilter::Label) ?: BookmarkFilter.NoLabel
    }

    fun selectNoLabel() {
        selectedBookmarkFilter.value = BookmarkFilter.NoLabel
    }

    private val allItems: StateFlow<List<BookmarkItem>?> = db.bookmarkReadDao()
        .bookmarksFlow()
        .map<List<BookmarkItem>, List<BookmarkItem>?> { it }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = null,
        )

    val categories: StateFlow<List<BookmarkCategoryEntity>> = db.bookmarkCategoryDao()
        .allFlow()
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = emptyList(),
        )

    val bookmarkCategories: StateFlow<List<BookmarkCategoryDisplay>> = categories
        .map { rows -> rows.map { BookmarkCategoryDisplay(it.categoryId, it.name) } }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = emptyList(),
        )

    /** Per-category counts (including id=0 "uncategorized") so chips can show `name (N)`. */
    val counts: StateFlow<Map<Long, Int>> = allItems
        .map { items ->
            items.orEmpty()
                .groupingBy { it.bookmark.categoryId }
                .eachCount()
        }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = emptyMap(),
        )

    val labelCounts: StateFlow<List<BookmarkLabelCount>> = allItems
        .map { items -> bookmarkLabelCounts(items.orEmpty()) }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = emptyList(),
        )

    val items: StateFlow<List<BookmarkItem>> = combine(allItems, selectedBookmarkFilter) { list, filter ->
        filterBookmarkItems(list.orEmpty(), filter)
    }.stateIn(
        scope = viewModelScope,
        started = SharingStarted.WhileSubscribed(5_000L),
        initialValue = emptyList(),
    )

    val uiState: StateFlow<UiState<Unit>> = combine(allItems, selectedBookmarkFilter) { list, _ ->
        when {
            list == null -> UiState.Loading
            list.isEmpty() -> UiState.Empty
            else -> UiState.Data(Unit)
        }
    }.stateIn(
        scope = viewModelScope,
        started = SharingStarted.WhileSubscribed(5_000L),
        initialValue = UiState.Loading,
    )

    private val _pendingBookmark = MutableStateFlow<BookmarkTarget?>(null)
    val pendingBookmark: StateFlow<BookmarkTarget?> = _pendingBookmark.asStateFlow()

    fun removeBookmark(videoId: String) {
        viewModelScope.launch {
            val prev = outboxWriter.capturePreviousBookmark(videoId)
            outboxWriter.enqueue(
                OutboxKind.Bookmark(
                    videoId = videoId,
                    action = OutboxKind.Action.Clear,
                    prevRow = prev,
                )
            )
        }
    }

    fun requestBookmarkSheet(item: BookmarkItem) {
        val existingMediaIndices = parseStoredMediaIndices(item.bookmark.mediaIndices)
        _pendingBookmark.value = BookmarkTarget(
            itemId = item.bookmark.videoId,
            authorHandle = item.feedItem?.authorHandle
                ?: item.resolvedChannelSourceId
                ?: item.video?.channelId?.let(::stripPlatformPrefix)
                ?: item.bookmark.videoId,
            mediaCount = item.video?.slideCount?.coerceAtLeast(0) ?: 0,
            currentBookmark = BookmarkState(
                categoryId = item.bookmark.categoryId,
                customTitle = item.bookmark.customTitle,
                mediaIndices = existingMediaIndices,
                accountHandles = parseStoredHandles(item.bookmark.accountHandles),
            ),
            defaultTitle = defaultTitle(item),
            defaultMediaIndices = existingMediaIndices,
            sourceHandle = item.feedItem?.sourceHandle,
            quoteAuthorHandle = item.feedItem?.quoteAuthorHandle,
            bodyText = item.feedItem?.bodyText,
            isRetweet = item.feedItem?.isRetweet == true,
        )
    }

    fun dismissBookmarkSheet() {
        _pendingBookmark.value = null
    }

    fun confirmBookmark(payload: BookmarkPayload) {
        val target = _pendingBookmark.value ?: return
        _pendingBookmark.value = null
        viewModelScope.launch {
            val prev = outboxWriter.capturePreviousBookmark(target.itemId)
            outboxWriter.enqueue(
                OutboxKind.Bookmark(
                    videoId = target.itemId,
                    action = OutboxKind.Action.Set,
                    categoryId = payload.categoryId,
                    customTitle = payload.customTitle,
                    accountHandles = payload.accountHandles?.joinToString(","),
                    mediaIndices = payload.mediaIndices?.joinToString(","),
                    prevRow = prev,
                ),
            )
        }
    }

    fun removePendingBookmark() {
        val target = _pendingBookmark.value ?: return
        _pendingBookmark.value = null
        removeBookmark(target.itemId)
    }

    fun createCategory(name: String) {
        viewModelScope.launch {
            val provisionalId = -System.currentTimeMillis()
            outboxWriter.enqueue(OutboxKind.CreateCategory(name = name, provisionalId = provisionalId))
        }
    }

    suspend fun canonicalUrlFor(item: BookmarkItem): String {
        item.feedItem?.canonicalUrl?.takeIf { it.isNotBlank() }?.let { return it }
        return item.video?.canonicalUrl.orEmpty()
    }

    suspend fun routeForMention(handle: String, fallbackPlatform: String): String =
        ChannelRouteResolver.routeForHandle(
            db = db,
            rawHandle = handle,
            fallbackPlatform = fallbackPlatform,
        )

    private fun defaultTitle(item: BookmarkItem): String? =
        item.feedItem?.bodyText?.lineSequence()?.firstOrNull { it.isNotBlank() }
            ?: item.video?.title?.takeIf { !it.isNullOrBlank() }
            ?: item.video?.description?.lineSequence()?.firstOrNull { it.isNotBlank() }

}

internal fun normalizeBookmarkLabel(label: String?): String? =
    label?.trim()?.takeIf { it.isNotEmpty() }

internal fun bookmarkLabelCounts(items: List<BookmarkItem>): List<BookmarkLabelCount> =
    items
        .groupingBy { normalizeBookmarkLabel(it.bookmark.customTitle) }
        .eachCount()
        .map { (label, count) -> BookmarkLabelCount(label = label, count = count) }
        .sortedWith(
            compareByDescending<BookmarkLabelCount> { it.count }
                .thenBy { it.label.orEmpty().lowercase() },
        )

internal fun filterBookmarkItems(
    items: List<BookmarkItem>,
    filter: BookmarkFilter,
): List<BookmarkItem> = when (filter) {
    BookmarkFilter.All -> items
    is BookmarkFilter.Category -> items.filter { it.bookmark.categoryId == filter.categoryId }
    is BookmarkFilter.Label -> {
        val label = normalizeBookmarkLabel(filter.label)
        if (label == null) {
            items.filter { normalizeBookmarkLabel(it.bookmark.customTitle) == null }
        } else {
            items.filter { normalizeBookmarkLabel(it.bookmark.customTitle) == label }
        }
    }
    BookmarkFilter.NoLabel -> items.filter { normalizeBookmarkLabel(it.bookmark.customTitle) == null }
}

internal fun filterBookmarkLabelCounts(
    labels: List<BookmarkLabelCount>,
    query: String,
    noLabelText: String = "No label",
): List<BookmarkLabelCount> {
    val normalized = query.trim().lowercase()
    if (normalized.isEmpty()) return labels
    return labels.filter { row ->
        (row.label ?: noLabelText).lowercase().contains(normalized)
    }
}
