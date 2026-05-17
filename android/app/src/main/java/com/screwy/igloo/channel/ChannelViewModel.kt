package com.screwy.igloo.channel

import androidx.annotation.StringRes
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.screwy.igloo.R
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.platformKeyFromChannelId
import com.screwy.igloo.data.stripPlatformPrefix
import com.screwy.igloo.data.entity.BookmarkCategoryEntity
import com.screwy.igloo.data.entity.ChannelDisplay
import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.ChannelProfileEntity
import com.screwy.igloo.data.entity.FeedRow
import com.screwy.igloo.data.entity.StoryChannelItem
import com.screwy.igloo.data.entity.VideoGridItem
import com.screwy.igloo.data.entity.durationMs
import com.screwy.igloo.media.ownerKindFromChannelId
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.net.ServerBaseUrlProvider
import com.screwy.igloo.outbox.OutboxKind
import com.screwy.igloo.outbox.OutboxWriter
import com.screwy.igloo.sync.SchedulerActions
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
import com.screwy.igloo.ui.component.storyRingState
import com.screwy.igloo.ui.component.toBookmarkState
import com.screwy.igloo.feed.FeedMediaGridModel
import com.screwy.igloo.feed.FeedMediaModelStore
import com.screwy.igloo.feed.feedMediaCount
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

/**
 * Channel page state holder.
 * `channel/{channel_id}`.
 *
 * One VM for every platform variant — the route dispatches on `channel.platform`
 * and shows the right body composable. Each body pulls from the DAO flow that
 * corresponds to its platform; the other flows are still emitted but ignored.
 * (Simpler than stopping / starting flows per platform, and Room will idle any
 * flow with no collectors under `WhileSubscribed`.)
 *
 * Loading-state note: we consider "no channel row yet" Loading, and if Room still
 * reports null after the first emission, fire `scheduler.triggerStream(Channels)`
 * once to encourage an incremental sync.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class ChannelViewModel(
    private val channelId: String,
    private val db: IglooDatabase,
    private val outboxWriter: OutboxWriter,
    private val prefs: PreferencesRepo,
    private val scheduler: SchedulerActions,
    private val uiEffects: UiEffects,
    private val reachability: Reachability,
    baseUrlProvider: ServerBaseUrlProvider,
) : ViewModel() {

    data class ChannelStoryStatus(
        val ringState: StoryRingState,
        val firstVideoId: String,
    )

    /**
     * Composed channel-header shape. `ChannelDisplay` joins `channels` with
     * `channel_follows` + `channel_stars`; the ChannelReadDao `allFlow()` is list-
     * shaped so we combine per-table flows here for this single channel.
     */
    val channel: StateFlow<ChannelDisplay> = combine(
        db.channelDao().getByIdFlow(channelId),
        db.channelFollowDao().allFlow().map { list ->
            list.any { it.channelId == channelId }
        },
        db.channelStarDao().allFlow().map { list ->
            list.any { it.channelId == channelId }
        },
    ) { entity, isFollowed, isStarred ->
        ChannelDisplay(
            channel = entity ?: syntheticChannelEntity(channelId),
            isStarred = if (isStarred) 1 else 0,
            isFollowed = if (isFollowed) 1 else 0,
        )
    }.stateIn(
        scope = viewModelScope,
        started = SharingStarted.WhileSubscribed(5_000L),
        initialValue = ChannelDisplay(
            channel = syntheticChannelEntity(channelId),
            isStarred = 0,
            isFollowed = 0,
        ),
    )

    val channelProfile: StateFlow<ChannelProfileEntity?> = db.channelProfileDao()
        .getByIdFlow(channelId)
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = null,
        )

    val storyStatus: StateFlow<ChannelStoryStatus> = prefs.storiesWindowHours()
        .flatMapLatest { hours ->
            db.momentReadDao().storyStatusesFlow(storyCutoffMillis(hours))
        }
        .map { rows ->
            rows.firstOrNull { it.channelId == channelId }.toChannelStoryStatus()
        }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = ChannelStoryStatus(StoryRingState.None, ""),
        )

    /**
     * Twitter channel body — posts filtered to this channel, in sync_seq order.
     * Older locally cached rows can predate the server-enriched `channel_id`, so
     * pass the account handle as a secondary key.
     */
    val twitterRows: StateFlow<List<FeedRow>> = channel
        .flatMapLatest { display ->
            db.feedReadDao().channelFeedFlow(
                channelId = channelId,
                channelHandle = display.channel.sourceId?.takeIf { it.isNotBlank() }
                    ?: stripPlatformPrefix(channelId),
                limit = TWITTER_FEED_LIMIT,
                offset = 0,
            )
        }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = emptyList(),
        )

    private val mediaModelStore = FeedMediaModelStore(
        db = db,
        baseUrlProvider = baseUrlProvider,
        scope = viewModelScope,
    )
    val mediaModels: StateFlow<Map<String, FeedMediaGridModel>> = mediaModelStore.mediaModels

    /**
     * TikTok/Instagram channel body — shorts grid metadata. Thumbnail resolution is
     * owned by `MomentsGrid`, keeping this VM as a Room-backed model provider.
     */
    val momentThumbs: StateFlow<List<MomentThumbnailItem>> = db.momentReadDao()
        .channelMomentsFlow(channelId)
        .map { rows ->
            rows.map { row ->
                val handle = row.channelSourceId?.takeIf { it.isNotBlank() }
                    ?: stripPlatformPrefix(row.video.channelId)
                MomentThumbnailItem(
                    videoId = row.video.videoId,
                    channelId = row.video.channelId,
                    ownerKind = ownerKindFromChannelId(row.video.channelId),
                    thumbnailPath = row.video.thumbnailPath,
                    mediaKind = row.video.mediaKind,
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

    /** YouTube channel body — long-form videos grid. */
    val videos: StateFlow<List<VideoGridItem>> = db.videoReadDao()
        .channelVideosFlow(channelId)
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = emptyList(),
        )

    val bookmarkCategories: StateFlow<List<BookmarkCategoryDisplay>> =
        db.bookmarkCategoryDao().allFlow()
            .map { entities: List<BookmarkCategoryEntity> ->
                entities.map { BookmarkCategoryDisplay(it.categoryId, it.name) }
            }
            .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000L), emptyList())

    val mutedHandles: StateFlow<Set<String>> = db.mutedAccountDao().allFlow()
        .map { rows -> rows.mapTo(linkedSetOf()) { it.handle.lowercase() } }
        .stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000L), emptySet())

    private val _pendingBookmark = MutableStateFlow<BookmarkTarget?>(null)
    val pendingBookmark: StateFlow<BookmarkTarget?> = _pendingBookmark.asStateFlow()

    /**
     * Loading until the first Room-backed route state lands; Data once the route can
     * render, even if that state is still the synthetic fallback derived from the
     * channel id.
     */
    val uiState: StateFlow<UiState<Unit>> = channel
        .map { UiState.Data(Unit) }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = UiState.Loading,
        )

    init {
        // Spec lines 913-914: unknown channel id → fire a Channels sync once so the
        // server can backfill. Only trigger when the row really is missing; firing
        // unconditionally on every VM construction would burn a sync request for
        // every channel nav, even when we already have the entity cached.
        viewModelScope.launch {
            if (db.channelDao().getById(channelId) == null) {
                scheduler.triggerStream(SyncStream.Channels)
            }
        }
    }

    fun toggleFollow(newValue: Boolean) {
        enqueueFollow(channelId = channelId, newValue = newValue)
    }

    fun toggleStar(newValue: Boolean) {
        val action = if (newValue) OutboxKind.Action.Set else OutboxKind.Action.Clear
        viewModelScope.launch {
            outboxWriter.enqueue(OutboxKind.Star(channelId = channelId, action = action))
        }
    }

    fun refresh() {
        val stream = when (channel.value.channel.platform.lowercase()) {
            "youtube" -> SyncStream.Youtube
            "tiktok", "instagram" -> SyncStream.Shorts
            else -> SyncStream.Feed
        }
        scheduler.triggerStream(stream)
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
                )
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
            authorHandle = row.item.authorHandle,
            mediaCount = feedMediaCount(row.item),
            currentBookmark = currentBookmark,
            defaultTitle = row.item.bodyText?.lineSequence()?.firstOrNull(),
            sourceHandle = row.item.sourceHandle,
            quoteAuthorHandle = row.item.quoteAuthorHandle,
            bodyText = row.item.bodyText,
            isRetweet = row.item.isRetweet,
        )

    fun createCategory(name: String) {
        viewModelScope.launch {
            val provisionalId = -System.currentTimeMillis()
            outboxWriter.enqueue(OutboxKind.CreateCategory(name = name, provisionalId = provisionalId))
        }
    }

    fun toggleRowFollow(channelId: String, newValue: Boolean) {
        enqueueFollow(channelId = channelId, newValue = newValue)
    }

    private fun enqueueFollow(channelId: String, newValue: Boolean) {
        val action = if (newValue) OutboxKind.Action.Set else OutboxKind.Action.Clear
        viewModelScope.launch {
            outboxWriter.enqueue(OutboxKind.Follow(channelId = channelId, action = action))
            uiEffects.emit(
                UiEffect.ToastRes(
                    resId = followQueuedMessageRes(newValue, reachability.state.value),
                    longDuration = reachability.state.value !is Reachability.State.Online,
                )
            )
        }
    }

    fun toggleRowStar(channelId: String, newValue: Boolean) {
        val action = if (newValue) OutboxKind.Action.Set else OutboxKind.Action.Clear
        viewModelScope.launch {
            outboxWriter.enqueue(OutboxKind.Star(channelId = channelId, action = action))
        }
    }

    fun toggleRowMute(handle: String, newValue: Boolean) {
        val action = if (newValue) OutboxKind.Action.Set else OutboxKind.Action.Clear
        viewModelScope.launch {
            outboxWriter.enqueue(OutboxKind.Mute(handle = handle, action = action))
        }
    }

    fun resolveMentionAndNavigate(handle: String) {
        viewModelScope.launch {
            val route = ChannelRouteResolver.routeForHandle(
                db = db,
                rawHandle = handle,
                fallbackPlatform = mentionFallbackPlatform(),
            )
            uiEffects.emit(UiEffect.NavigateTo(route))
        }
    }

    fun warmMediaModels(rows: List<FeedRow>) {
        mediaModelStore.warmMediaModels(rows)
    }

    private fun storyCutoffMillis(hours: Int): Long =
        System.currentTimeMillis() - PreferencesRepo.Defaults.normalizeStoriesWindowHours(hours) * 3_600_000L

    private fun StoryChannelItem?.toChannelStoryStatus(): ChannelStoryStatus =
        ChannelStoryStatus(
            ringState = storyRingState(this?.storyCount ?: 0, this?.unseenCount ?: 0),
            firstVideoId = this?.firstUnseenVideoId?.takeIf { it.isNotBlank() } ?: this?.firstVideoId.orEmpty(),
        )

    private fun syntheticChannelEntity(channelId: String): ChannelEntity {
        val platform = platformKeyFromChannelId(channelId).ifBlank { "twitter" }
        val handle = stripPlatformPrefix(channelId).trim().takeIf { it.isNotEmpty() }
        return ChannelEntity(
            channelId = channelId,
            sourceId = handle,
            name = handle ?: channelId,
            platform = platform,
        )
    }

    private fun mentionFallbackPlatform(): String {
        val current = channel.value.channel.platform
            .trim()
            .lowercase()
            .takeIf { it.isNotBlank() }
        return when (current) {
            "x" -> "twitter"
            null -> platformKeyFromChannelId(channelId).ifBlank { "twitter" }
            else -> current
        }
    }

    private companion object {
        /**
         * Bound the channel tweet slice so channel navigation from feed doesn't reopen
         * the same CursorWindow crash class as the old oversized feed queries.
         */
        const val TWITTER_FEED_LIMIT: Int = 500
    }
}

@StringRes
internal fun followQueuedMessageRes(newValue: Boolean, state: Reachability.State): Int =
    if (newValue) {
        if (state is Reachability.State.Online) {
            R.string.follow_queued_syncing
        } else {
            R.string.follow_queued_waiting
        }
    } else {
        if (state is Reachability.State.Online) {
            R.string.unfollow_queued_syncing
        } else {
            R.string.unfollow_queued_waiting
        }
    }
