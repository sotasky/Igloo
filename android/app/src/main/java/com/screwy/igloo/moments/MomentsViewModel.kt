package com.screwy.igloo.moments

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.screwy.igloo.R
import com.screwy.igloo.channel.ChannelRouteResolver
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.stripPlatformPrefix
import com.screwy.igloo.data.entity.StoryChannelItem
import com.screwy.igloo.data.entity.durationMs
import com.screwy.igloo.media.MediaResolvers
import com.screwy.igloo.media.ownerKindFromChannelId
import com.screwy.igloo.outbox.OutboxKind
import com.screwy.igloo.outbox.OutboxWriter
import com.screwy.igloo.sync.Scheduler
import com.screwy.igloo.sync.SyncStream
import com.screwy.igloo.ui.UiEffect
import com.screwy.igloo.ui.UiEffects
import com.screwy.igloo.ui.UiState
import com.screwy.igloo.ui.component.BookmarkCategoryDisplay
import com.screwy.igloo.ui.component.BookmarkPayload
import com.screwy.igloo.ui.component.BookmarkState
import com.screwy.igloo.ui.component.BookmarkTarget
import com.screwy.igloo.ui.component.MomentThumbnailItem
import com.screwy.igloo.ui.component.StoryRingState
import com.screwy.igloo.ui.component.toBookmarkState
import com.screwy.igloo.ui.component.storyRingState
import com.screwy.igloo.ui.nav.FullscreenMediaTransition
import kotlinx.coroutines.ExperimentalCoroutinesApi
import com.screwy.igloo.data.entity.MomentItem as DbMomentItem
import com.screwy.igloo.ui.component.MomentItem as PlayerMomentItem
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

/**
 * Nav-graph-scoped ViewModel shared by `MomentsRoute` (the TikTok-style player) and
 * `AllMomentsRoute` (the 3-column grid). Both routes live in the `moments-graph`
 * nested nav graph; the route composables resolve this VM against that graph's
 * `NavBackStackEntry` ViewModelStore so tapping a cell in the grid seeds the
 * player's startIndex through [selectResumeVideoId].
 * Resolver note: the grid thumbnails are still resolved eagerly here because the
 * all-moments grid is a thumbnail surface. The player list is intentionally cheap:
 * it emits metadata only, and the player resolves stream/thumbnail/bookmark state
 * lazily for the current and neighboring pages.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class MomentsViewModel(
    private val db: IglooDatabase,
    private val outboxWriter: OutboxWriter,
    private val prefs: PreferencesRepo,
    private val scheduler: Scheduler,
    private val uiEffects: UiEffects,
    private val resolvers: MediaResolvers,
) : ViewModel() {
    private data class RepostMeta(
        val authorLabel: String,
        val otherCount: Int,
    )


    private data class ActiveCursor(
        val videoId: String,
        val positionMs: Long,
        val sortAtMs: Long?,
        val scope: String,
    )

    data class StoryChannelUiItem(
        val channelId: String,
        val displayName: String,
        val handle: String,
        val count: Int,
        val unseenCount: Int,
        val latestAtMs: Long,
        val firstVideoId: String,
        val firstUnseenVideoId: String,
        val ringState: StoryRingState,
    ) {
        val startVideoId: String
            get() = firstUnseenVideoId.takeIf { it.isNotBlank() } ?: firstVideoId
    }

    private val sessionTabOverride = MutableStateFlow<String?>(null)

    val activeTab: StateFlow<String> = combine(
        prefs.momentsDefaultTab(),
        sessionTabOverride,
    ) { defaultTab, override ->
        PreferencesRepo.Defaults.normalizeMomentsTab(override ?: defaultTab)
    }.distinctUntilChanged().stateIn(
        scope = viewModelScope,
        started = SharingStarted.WhileSubscribed(5_000L),
        initialValue = PreferencesRepo.Defaults.MOMENTS_DEFAULT_TAB,
    )

    private val storyCutoffMillis: StateFlow<Long> = prefs.storiesWindowHours()
        .map { hours -> storyCutoffMillis(hours) }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = storyCutoffMillis(PreferencesRepo.Defaults.STORIES_WINDOW_HOURS),
        )

    private val storyStatusRows: StateFlow<List<StoryChannelItem>> = storyCutoffMillis
        .flatMapLatest { cutoff -> db.momentReadDao().storyStatusesFlow(cutoff) }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = emptyList(),
        )

    private val storyStatusByChannel: StateFlow<Map<String, StoryChannelItem>> = storyStatusRows
        .map { rows -> rows.associateBy { it.channelId } }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = emptyMap(),
        )

    val storyChannels: StateFlow<List<StoryChannelUiItem>> = storyCutoffMillis
        .flatMapLatest { cutoff -> db.momentReadDao().storyChannelsFlow(cutoff) }
        .map { rows -> rows.map(::toStoryChannelUiItem) }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = emptyList(),
        )

    /**
     * Raw Room projection — the single source-of-truth for both grid and player. `null`
     * until the first Room emission so `uiState` can paint Loading.
     */
    private val rowsRaw: StateFlow<List<DbMomentItem>?> = activeTab.flatMapLatest { tab ->
        if (tab == "stories") {
            flowOf(emptyList())
        } else if (tab == "following") {
            db.momentReadDao().momentsFollowingFlow()
        } else {
            db.momentReadDao().momentsAllFlow()
        }
    }.map<List<DbMomentItem>, List<DbMomentItem>?> { it }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = null,
        )

    /**
     * Player rows deliberately ignore `moment_views`, because the player writes a
     * view row on every swipe. It still observes `videos` and `channels`, so new
     * shorts, prunes, and channel/unfollow effects continue to update the player.
     */
    private val playerRowsRaw: StateFlow<List<DbMomentItem>?> = activeTab.flatMapLatest { tab ->
        if (tab == "following") {
            db.momentReadDao().playerMomentsFollowingFlow()
        } else {
            db.momentReadDao().playerMomentsAllFlow()
        }
    }.map<List<DbMomentItem>, List<DbMomentItem>?> { it }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = null,
        )

    /**
     * Grid-shaped items for `AllMomentsRoute`. `MomentThumbnailItem` carries a resolved
     * [com.screwy.igloo.media.MediaUri] — we resolve per row per emission via
     * `transformLatest`-style map inside a coroutine block.
     */
    val items: StateFlow<List<MomentThumbnailItem>> = rowsRaw
        .map { rows ->
            if (rows == null) emptyList()
            else rows.map { row ->
                val handle = momentHandle(row.channelSourceId, row.video.channelId)
                MomentThumbnailItem(
                    videoId = row.video.videoId,
                    channelId = row.video.channelId,
                    ownerKind = ownerKindFromChannelId(row.video.channelId),
                    thumbnailPath = row.video.thumbnailPath,
                    mediaKind = row.video.mediaMode?.takeIf { it.isNotBlank() } ?: row.video.mediaKind,
                    slideCount = row.video.slideCount,
                    durationMs = row.video.durationMs(),
                    publishedAt = row.video.publishedAt,
                    isViewed = row.isViewed == 1,
                    authorDisplayName = row.channelName?.takeIf { it.isNotBlank() },
                    authorHandle = if (handle.isNotBlank()) "@$handle" else "",
                )
            }
        }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = emptyList(),
        )

    /**
     * Player-shaped items for `MomentsRoute`. Keep this projection cheap so the
     * route can render immediately even when the moments dataset is large.
     * Stream URIs, thumbnails, and bookmark state are resolved lazily in the player.
     */
    val playerItems: StateFlow<List<PlayerMomentItem>> = combine(playerRowsRaw, storyStatusByChannel) { rows, storyStatuses ->
            if (rows == null) emptyList()
            else rows.map { row ->
                val video = row.video
                val handle = momentHandle(row.channelSourceId, video.channelId)
                val storyStatus = storyStatuses[video.channelId]
                val repost = repostMeta(row)
                PlayerMomentItem(
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
                    storyRingState = storyStatus.storyRingState(),
                    storyFirstVideoId = storyStatus?.startVideoId().orEmpty(),
                )
            }
        }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = emptyList(),
        )

    /**
     * Three-state primary UI hint. Loading until Room emits; Empty on first empty
     * emission; Data once rows arrive. Mirrors FeedViewModel's nullable-sentinel
     * pattern so `MomentsRoute` and `AllMomentsRoute` share one Loading branch.
     */
    val uiState: StateFlow<UiState<Unit>> = combine(activeTab, rowsRaw, storyChannels) { tab, list, _ ->
            when {
                list == null -> UiState.Loading
                tab == "stories" -> UiState.Data(Unit)
                list.isEmpty() -> UiState.Empty
                else -> UiState.Data(Unit)
            }
        }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = UiState.Loading,
        )

    private val activeCursor = MutableStateFlow<ActiveCursor?>(null)
    private val scopedResumeVideoId: StateFlow<String?> = activeTab
        .flatMapLatest { prefs.momentsResumeVideoId(scope = it) }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000L), null)
    private val scopedResumeStoredSortAtMs: StateFlow<Long?> = activeTab
        .flatMapLatest { prefs.momentsResumeSortAtMs(scope = it) }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000L), null)
    private val scopedResumeVideoSortAtMs: StateFlow<Long?> = scopedResumeVideoId
        .flatMapLatest { videoId ->
            val target = videoId?.trim()?.takeIf { it.isNotEmpty() }
            if (target == null) flowOf(null) else db.momentReadDao().momentSortAtFlow(target)
        }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000L), null)
    private val scopedResumeSortAtMs: StateFlow<Long?> = combine(
        scopedResumeStoredSortAtMs,
        scopedResumeVideoSortAtMs,
    ) { stored, current ->
        stored?.takeIf { it > 0L } ?: current
    }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000L), null)
    private val scopedResumePositionMs: StateFlow<Long> = activeTab
        .flatMapLatest { prefs.momentsResumePositionMs(scope = it) }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000L), 0L)

    /**
     * Index of the active moments cursor inside the rows list. The in-memory cursor
     * updates immediately on page changes and grid taps; prefs remain the durable
     * fallback for cold start / process death.
     */
    val startIndex: StateFlow<Int> = combine(
        playerRowsRaw,
        activeCursor,
        scopedResumeVideoId,
        scopedResumeSortAtMs,
        activeTab,
    ) { rows, active, resumeId, resumeSortAtMs, tab ->
        val activeForTab = active?.takeIf { it.scope == tab }
        val targetVideoId = activeForTab?.videoId ?: resumeId
        if (rows == null || targetVideoId.isNullOrEmpty()) 0
        else shortsStartIndex(
            rows.map { row ->
                ShortsStartItem(
                    videoId = row.video.videoId,
                    sortAtMs = momentSortAtMs(row),
                )
            },
            targetVideoId,
            fallbackSortAtMs = activeForTab?.sortAtMs ?: resumeSortAtMs,
        )
    }.stateIn(
        scope = viewModelScope,
        started = SharingStarted.WhileSubscribed(5_000L),
        initialValue = 0,
    )

    /**
     * Resume playhead (ms) for the video at [startIndex]. Mirrors [startIndex]:
     * use the in-memory cursor for immediate UI jumps, fall back to prefs for a
     * fresh process.
     */
    val startPositionMs: StateFlow<Long> = combine(
        activeCursor,
        scopedResumePositionMs,
        activeTab,
    ) { active, resumePositionMs, tab ->
        active?.takeIf { it.scope == tab }?.positionMs ?: resumePositionMs
    }.stateIn(
        scope = viewModelScope,
        started = SharingStarted.WhileSubscribed(5_000L),
        initialValue = 0L,
    )

    /** Global moments/bookmarks playback toggles from Preferences. */
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

    private val _isRefreshing = MutableStateFlow(false)
    val isRefreshing: StateFlow<Boolean> = _isRefreshing.asStateFlow()

    /** Non-null when the bookmark sheet is open for the carried target. */
    private val _pendingBookmark = MutableStateFlow<BookmarkTarget?>(null)
    val pendingBookmark: StateFlow<BookmarkTarget?> = _pendingBookmark.asStateFlow()

    /** Poster handoff from the all-moments grid back to the already-composed player. */
    private val _pendingFullscreenTransition = MutableStateFlow<FullscreenMediaTransition?>(null)
    val pendingFullscreenTransition: StateFlow<FullscreenMediaTransition?> =
        _pendingFullscreenTransition.asStateFlow()

    /** Category chip rows — same stream FeedViewModel uses. */
    val bookmarkCategories: StateFlow<List<BookmarkCategoryDisplay>> =
        db.bookmarkCategoryDao().allFlow()
            .map { entities -> entities.map { BookmarkCategoryDisplay(it.categoryId, it.name) } }
            .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000L), emptyList())

    /**
     * Settled page changed — record the cursor so moments resume where the user left
     * off. Position is 0ms at index-change time; the 2s periodic tick in MomentsPlayer
     * supplies the real playhead via [onCursorAdvance].
     */
    fun onIndexChange(idx: Int) {
        viewModelScope.launch {
            val rows = playerRowsRaw.value ?: return@launch
            if (idx !in rows.indices) return@launch
            val row = rows[idx]
            val videoId = row.video.videoId
            val scope = activeTab.value
            val sortAtMs = momentSortAtMs(row)
            activeCursor.value = ActiveCursor(videoId = videoId, positionMs = 0L, sortAtMs = sortAtMs, scope = scope)
            outboxWriter.enqueue(
                OutboxKind.MomentsCursor(
                    videoId = videoId,
                    positionMs = 0L,
                    scope = scope,
                    sortAtMs = sortAtMs,
                ),
            )
        }
    }

    /** One-per-video FIFO log of "this was shown on screen". */
    fun onViewEvent(videoId: String) {
        viewModelScope.launch {
            outboxWriter.enqueue(OutboxKind.MomentView(videoId = videoId))
        }
    }

    /**
     * Moments cursor tracks which short the user was on, not an in-video resume point.
     * TikTok/IG clips always reopen from t=0 even if the previous session sampled a later
     * playhead.
     */
    fun onCursorAdvance(videoId: String, positionMs: Long) {
        viewModelScope.launch {
            val scope = activeTab.value
            val sortAtMs = playerRowsRaw.value
                ?.firstOrNull { it.video.videoId == videoId }
                ?.let(::momentSortAtMs)
            activeCursor.value = ActiveCursor(videoId = videoId, positionMs = 0L, sortAtMs = sortAtMs, scope = scope)
            outboxWriter.enqueue(
                OutboxKind.MomentsCursor(
                    videoId = videoId,
                    positionMs = 0L,
                    scope = scope,
                    sortAtMs = sortAtMs,
                ),
            )
        }
    }

    fun setAutoplayEnabled(enabled: Boolean) {
        viewModelScope.launch { prefs.setAutoplay(enabled) }
    }

    fun setMuted(enabled: Boolean) {
        viewModelScope.launch { prefs.setMuteDefault(enabled) }
    }

    fun setActiveTab(tab: String) {
        sessionTabOverride.value = PreferencesRepo.Defaults.normalizeMomentsTab(tab)
    }

    /**
     * Direct bookmark toggle — used when the row is already bookmarked (tap clears
     * it) or from the pager-level `onBookmarkToggle` hook. New-bookmark flow goes
     * through [requestBookmarkSheet] so the user can pick a category.
     */
    fun toggleBookmark(item: PlayerMomentItem) {
        val action = if (item.isBookmarked) OutboxKind.Action.Clear else OutboxKind.Action.Set
        viewModelScope.launch {
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

    /**
     * User tapped bookmark on a not-yet-bookmarked moment — open the bookmark
     * sheet so they can pick a category + label before saving.
     */
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

    private fun bookmarkTargetForMoment(
        item: PlayerMomentItem,
        currentBookmark: BookmarkState? = null,
    ): BookmarkTarget =
        BookmarkTarget(
            itemId = item.videoId,
            authorHandle = item.authorHandle,
            // Moments are single-media video posts; the multi-image picker row
            // is hidden when mediaCount <= 1 (see BookmarkSheet §3 media picker).
            mediaCount = 0,
            currentBookmark = currentBookmark,
            defaultTitle = item.description.lineSequence().firstOrNull(),
            bodyText = item.description,
        )

    fun createCategory(name: String) {
        viewModelScope.launch {
            val provisionalId = -System.currentTimeMillis()
            outboxWriter.enqueue(OutboxKind.CreateCategory(name = name, provisionalId = provisionalId))
        }
    }

    /**
     * Tapping a grid cell writes the resume cursor so the player's `startIndex`
     * recomputes to land on the tapped video. Mirrors the MomentsCursor outbox
     * kind so the server learns about the jump too.
     */
    fun selectResumeVideoId(videoId: String) {
        viewModelScope.launch {
            val scope = activeTab.value
            val sortAtMs = playerRowsRaw.value
                ?.firstOrNull { it.video.videoId == videoId }
                ?.let(::momentSortAtMs)
            activeCursor.value = ActiveCursor(videoId = videoId, positionMs = 0L, sortAtMs = sortAtMs, scope = scope)
            outboxWriter.enqueue(
                OutboxKind.MomentsCursor(
                    videoId = videoId,
                    positionMs = 0L,
                    scope = scope,
                    sortAtMs = sortAtMs,
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

    fun setPendingFullscreenTransition(transition: FullscreenMediaTransition) {
        _pendingFullscreenTransition.value = transition
    }

    fun clearPendingFullscreenTransition(mediaId: String) {
        if (_pendingFullscreenTransition.value?.mediaId == mediaId) {
            _pendingFullscreenTransition.value = null
        }
    }

    /** Pull-to-refresh — kicks the shorts sync stream and holds the spinner briefly. */
    fun refresh() {
        viewModelScope.launch {
            _isRefreshing.value = true
            scheduler.triggerStream(SyncStream.Shorts)
            delay(1_000L)
            _isRefreshing.value = false
        }
    }

    fun notifyUpToDate() {
        viewModelScope.launch {
            uiEffects.emit(UiEffect.ToastRes(R.string.status_up_to_date))
        }
    }

    fun resolveMentionAndNavigate(handle: String) {
        viewModelScope.launch {
            val route = ChannelRouteResolver.routeForHandle(
                db = db,
                rawHandle = handle,
                fallbackPlatform = "tiktok",
            )
            uiEffects.emit(UiEffect.NavigateTo(route))
        }
    }

    private fun toStoryChannelUiItem(row: StoryChannelItem): StoryChannelUiItem {
        val handle = row.channelSourceId?.takeIf { it.isNotBlank() } ?: stripPlatformPrefix(row.channelId)
        return StoryChannelUiItem(
            channelId = row.channelId,
            displayName = row.channelName?.takeIf { it.isNotBlank() } ?: handle.ifBlank { row.channelId },
            handle = if (handle.isNotBlank()) "@$handle" else "",
            count = row.storyCount,
            unseenCount = row.unseenCount,
            latestAtMs = row.latestAtMs,
            firstVideoId = row.firstVideoId,
            firstUnseenVideoId = row.firstUnseenVideoId,
            ringState = row.storyRingState(),
        )
    }

    private fun storyCutoffMillis(hours: Int): Long =
        System.currentTimeMillis() - PreferencesRepo.Defaults.normalizeStoriesWindowHours(hours) * 3_600_000L

    private fun StoryChannelItem?.storyRingState(): StoryRingState =
        storyRingState(this?.storyCount ?: 0, this?.unseenCount ?: 0)

    private fun StoryChannelItem.startVideoId(): String =
        firstUnseenVideoId.takeIf { it.isNotBlank() } ?: firstVideoId

    private fun momentHandle(channelSourceId: String?, channelId: String): String =
        channelSourceId?.takeIf { it.isNotBlank() } ?: stripPlatformPrefix(channelId)

    private fun momentSortAtMs(row: DbMomentItem): Long =
        row.effectiveMomentAtMs.takeIf { it > 0L } ?: row.video.publishedAt

    private fun repostMeta(row: DbMomentItem): RepostMeta? {
        if (row.repostIntroduced != 1) return null
        return RepostMeta(
            authorLabel = row.repostAuthorLabel?.takeIf { it.isNotBlank() } ?: return null,
            otherCount = (row.repostCount - 1).coerceAtLeast(0),
        )
    }
}
