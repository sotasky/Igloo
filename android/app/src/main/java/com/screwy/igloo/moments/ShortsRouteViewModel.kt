package com.screwy.igloo.moments

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.screwy.igloo.bookmarks.opensBookmarkInMomentsOverlay
import com.screwy.igloo.bookmarks.toBookmarkMomentItem
import com.screwy.igloo.channel.ChannelRouteResolver
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.stripPlatformPrefix
import com.screwy.igloo.data.entity.StoryChannelItem
import com.screwy.igloo.media.ownerKindFromChannelId
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.outbox.OutboxKind
import com.screwy.igloo.outbox.OutboxWriter
import com.screwy.igloo.ui.UiEffect
import com.screwy.igloo.ui.UiEffects
import com.screwy.igloo.ui.UiState
import com.screwy.igloo.ui.component.BookmarkCategoryDisplay
import com.screwy.igloo.ui.component.BookmarkPayload
import com.screwy.igloo.ui.component.BookmarkState
import com.screwy.igloo.ui.component.BookmarkTarget
import com.screwy.igloo.ui.component.storyRingState
import com.screwy.igloo.ui.component.toBookmarkState
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import com.screwy.igloo.data.entity.MomentItem as DbMomentItem
import com.screwy.igloo.ui.component.MomentItem as PlayerMomentItem

@OptIn(ExperimentalCoroutinesApi::class)
class ShortsRouteViewModel(
    private val playlistSpec: ShortsPlaylistSpec,
    startVideoId: String,
    private val db: IglooDatabase,
    private val outboxWriter: OutboxWriter,
    private val prefs: PreferencesRepo,
    private val uiEffects: UiEffects,
    baseUrlProvider: ServerBaseUrlProvider,
) : ViewModel() {
    private data class RepostMeta(
        val authorLabel: String,
        val otherCount: Int,
    )

    private val baseUrl = baseUrlProvider.baseUrl()
    private val initialVideoId = startVideoId.trim()
    private val activeVideoId = MutableStateFlow(initialVideoId)
    val currentVideoId: StateFlow<String> = activeVideoId.asStateFlow()
    private val momentsCursorScope: String =
        if (playlistSpec.type == ShortsPlaylistType.Moments) "following" else "all"
    private val recordsMomentViews: Boolean =
        playlistSpec.type != ShortsPlaylistType.Bookmarks

    private val storyStatusByChannel: StateFlow<Map<String, StoryChannelItem>> = prefs.storiesWindowHours()
        .flatMapLatest { hours -> db.momentReadDao().storyStatusesFlow(storyCutoffMillis(hours)) }
        .map { rows -> rows.associateBy { it.channelId } }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = emptyMap(),
        )
    val storyChannels: StateFlow<List<StoryChannelItem>> = prefs.storiesWindowHours()
        .flatMapLatest { hours -> db.momentReadDao().storyChannelsFlow(storyCutoffMillis(hours)) }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = emptyList(),
        )

    private val rawItems: StateFlow<List<PlayerMomentItem>?> = combine(playlistFlow(), storyStatusByChannel) { rows, storyStatuses ->
            rows.map { item ->
                val storyStatus = storyStatuses[item.channelId]
                item.copy(
                    storyRingState = storyRingState(storyStatus?.storyCount ?: 0, storyStatus?.unseenCount ?: 0),
                    storyFirstVideoId = storyStatus?.startVideoId().orEmpty(),
                )
            }
        }
        .map<List<PlayerMomentItem>, List<PlayerMomentItem>?> { it }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = null,
        )

    val items: StateFlow<List<PlayerMomentItem>> = rawItems
        .map { it.orEmpty() }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = emptyList(),
        )

    val startIndex: StateFlow<Int> = combine(items, activeVideoId) { currentItems, targetId ->
        shortsStartIndex(currentItems.map { it.videoId }, targetId)
    }.stateIn(
        scope = viewModelScope,
        started = SharingStarted.WhileSubscribed(5_000L),
        initialValue = 0,
    )

    val uiState: StateFlow<UiState<Unit>> = rawItems
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

    private val _pendingBookmark = MutableStateFlow<BookmarkTarget?>(null)
    val pendingBookmark: StateFlow<BookmarkTarget?> = _pendingBookmark.asStateFlow()

    val bookmarkCategories: StateFlow<List<BookmarkCategoryDisplay>> =
        db.bookmarkCategoryDao().allFlow()
            .map { entities -> entities.map { BookmarkCategoryDisplay(it.categoryId, it.name) } }
            .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000L), emptyList())

    fun setAutoplayEnabled(enabled: Boolean) {
        viewModelScope.launch { prefs.setAutoplay(enabled) }
    }

    fun setMuted(enabled: Boolean) {
        viewModelScope.launch { prefs.setMuteDefault(enabled) }
    }

    fun onIndexChange(index: Int) {
        val item = items.value.getOrNull(index) ?: return
        activeVideoId.value = item.videoId
        if (!playlistSpec.recordsMomentsCursor) return
        viewModelScope.launch {
            outboxWriter.enqueue(
                OutboxKind.MomentsCursor(videoId = item.videoId, positionMs = 0L, scope = momentsCursorScope),
            )
        }
    }

    fun onViewEvent(videoId: String) {
        if (!recordsMomentViews) return
        viewModelScope.launch {
            outboxWriter.enqueue(OutboxKind.MomentView(videoId = videoId))
        }
    }

    fun onCursorAdvance(videoId: String, positionMs: Long) {
        if (!playlistSpec.recordsMomentsCursor) return
        activeVideoId.value = videoId
        viewModelScope.launch {
            outboxWriter.enqueue(
                OutboxKind.MomentsCursor(videoId = videoId, positionMs = positionMs, scope = momentsCursorScope),
            )
        }
    }

    fun toggleBookmark(item: PlayerMomentItem) {
        viewModelScope.launch {
            val current = db.bookmarkDao().getById(item.videoId)
            val action = if (current != null || item.isBookmarked) {
                OutboxKind.Action.Clear
            } else {
                OutboxKind.Action.Set
            }
            val prev = outboxWriter.capturePreviousBookmark(item.videoId)
            outboxWriter.enqueue(
                OutboxKind.Bookmark(
                    videoId = item.videoId,
                    action = action,
                    prevRow = prev,
                ),
            )
        }
    }

    fun requestBookmarkSheet(item: PlayerMomentItem) {
        viewModelScope.launch {
            _pendingBookmark.value = bookmarkTargetForMoment(
                item = item,
                currentBookmark = db.bookmarkDao().getById(item.videoId)?.toBookmarkState(),
            )
        }
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
        viewModelScope.launch {
            val prev = outboxWriter.capturePreviousBookmark(target.itemId)
            outboxWriter.enqueue(
                OutboxKind.Bookmark(
                    videoId = target.itemId,
                    action = OutboxKind.Action.Clear,
                    prevRow = prev,
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

    fun resolveMentionAndNavigate(handle: String) {
        viewModelScope.launch {
            uiEffects.emit(
                UiEffect.NavigateTo(
                    ChannelRouteResolver.routeForHandle(
                        db = db,
                        rawHandle = handle,
                        fallbackPlatform = "tiktok",
                    ),
                ),
            )
        }
    }

    fun followChannel(channelId: String) {
        viewModelScope.launch {
            outboxWriter.enqueue(
                OutboxKind.Follow(channelId = channelId, action = OutboxKind.Action.Set),
            )
        }
    }

    fun unfollowChannel(channelId: String) {
        viewModelScope.launch {
            outboxWriter.enqueue(
                OutboxKind.Follow(channelId = channelId, action = OutboxKind.Action.Clear),
            )
        }
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    private fun playlistFlow() = when (playlistSpec.type) {
        ShortsPlaylistType.Moments -> db.momentReadDao()
            .playerMomentsFollowingFlow()
            .map { rows -> rows.map(::toPlayerMomentItem) }
        ShortsPlaylistType.AllMoments -> prefs.momentsIncludeRepostsDefault()
            .flatMapLatest { includeReposts ->
                db.momentReadDao().playerMomentsAllFlow(includeReposts = includeReposts)
            }
            .map { rows -> rows.map(::toPlayerMomentItem) }
        ShortsPlaylistType.Channel -> db.momentReadDao()
            .channelMomentsFlow(playlistSpec.playlistId)
            .map { rows -> rows.map(::toPlayerMomentItem) }
        ShortsPlaylistType.Story -> prefs.storiesWindowHours()
            .flatMapLatest { hours ->
                db.momentReadDao().storyPlaylistFlow(
                    channelId = playlistSpec.playlistId,
                    cutoffMs = storyCutoffMillis(hours),
                )
            }
            .map { rows -> rows.map(::toPlayerMomentItem) }
        ShortsPlaylistType.StoryTray -> prefs.storiesWindowHours()
            .flatMapLatest { hours ->
                flow {
                    val rows = db.momentReadDao()
                        .storyTrayPlaylistFlow(cutoffMs = storyCutoffMillis(hours))
                        .first()
                    emit(rotateStoryTrayPlaylist(rows, initialVideoId))
                }
            }
            .map { rows -> rows.map(::toPlayerMomentItem) }
        ShortsPlaylistType.Bookmarks -> db.bookmarkReadDao()
            .bookmarksFlow()
            .map { rows ->
                rows
                    .filter(::opensBookmarkInMomentsOverlay)
                    .map { item -> toBookmarkMomentItem(item, baseUrl) }
            }
    }

    private fun bookmarkTargetForMoment(
        item: PlayerMomentItem,
        currentBookmark: BookmarkState? = null,
    ): BookmarkTarget =
        BookmarkTarget(
            itemId = item.videoId,
            authorHandle = item.authorHandle,
            mediaCount = item.slideCount.coerceAtLeast(0),
            currentBookmark = currentBookmark,
            defaultTitle = item.description.lineSequence().firstOrNull(),
            bodyText = item.description,
        )

    private fun toPlayerMomentItem(row: DbMomentItem): PlayerMomentItem {
        val video = row.video
        val handle = row.channelSourceId?.takeIf { it.isNotBlank() }
            ?: stripPlatformPrefix(video.channelId)
        val repost = repostMeta(row)
        return PlayerMomentItem(
            videoId = video.videoId,
            channelId = video.channelId,
            canonicalUrl = video.canonicalUrl.orEmpty(),
            authorDisplayName = row.channelName?.takeIf { it.isNotBlank() },
            authorHandle = if (handle.isNotBlank()) "@$handle" else "",
            description = momentDisplayText(video.description, video.title),
            likeCount = null,
            isLiked = false,
            isBookmarked = false,
            mediaKind = video.mediaMode?.takeIf { it.isNotBlank() } ?: video.mediaKind,
            slideCount = video.slideCount,
            ownerKind = ownerKindFromChannelId(video.channelId),
            fallbackThumbnailPath = video.thumbnailPath,
            publishedAt = video.publishedAt,
            isAuthorFollowed = row.channelIsFollowed == 1,
            repostAuthorLabel = repost?.authorLabel,
            repostOtherCount = repost?.otherCount ?: 0,
        )
    }

    private fun storyCutoffMillis(hours: Int): Long =
        System.currentTimeMillis() - PreferencesRepo.Defaults.normalizeStoriesWindowHours(hours) * 3_600_000L

    private fun rotateStoryTrayPlaylist(
        rows: List<DbMomentItem>,
        startVideoId: String,
    ): List<DbMomentItem> {
        val anchorChannelId = rows.firstOrNull { it.video.videoId == startVideoId }?.video?.channelId
            ?: return rows
        val anchorIndex = rows.indexOfFirst { it.video.channelId == anchorChannelId }
        if (anchorIndex <= 0) return rows
        return rows.drop(anchorIndex) + rows.take(anchorIndex)
    }

    private fun StoryChannelItem.startVideoId(): String =
        firstUnseenVideoId.takeIf { it.isNotBlank() } ?: firstVideoId

    private fun repostMeta(row: DbMomentItem): RepostMeta? {
        if (row.repostIntroduced != 1) return null
        return RepostMeta(
            authorLabel = row.repostAuthorLabel?.takeIf { it.isNotBlank() } ?: return null,
            otherCount = (row.repostCount - 1).coerceAtLeast(0),
        )
    }
}
