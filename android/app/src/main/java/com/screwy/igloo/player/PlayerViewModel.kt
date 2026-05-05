package com.screwy.igloo.player

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.screwy.igloo.channel.ChannelRouteResolver
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.ChannelProfileEntity
import com.screwy.igloo.data.entity.SponsorBlockSegmentEntity
import com.screwy.igloo.data.entity.VideoCommentEntity
import com.screwy.igloo.data.entity.VideoEntity
import com.screwy.igloo.data.entity.WatchHistoryEntity
import com.screwy.igloo.media.MediaResolvers
import com.screwy.igloo.media.MediaUri
import com.screwy.igloo.media.OwnerKind
import com.screwy.igloo.outbox.OutboxKind
import com.screwy.igloo.outbox.OutboxWriter
import com.screwy.igloo.sync.Scheduler
import com.screwy.igloo.sync.SyncStream
import com.screwy.igloo.ui.UiEffect
import com.screwy.igloo.ui.UiEffects
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import java.io.File

/**
 * Long-form YouTube player state holder.
 *
 * State flows (all Room-backed so the UI re-renders on any write without extra
 * glue):
 *  - [video] — the `videos` row for this id.
 *  - [channel] — the owning channel (for description header).
 *  - [comments] — `video_comments` rows, server presentation order.
 *  - [segments] — SponsorBlock segments for the scrubber overlay.
 *  - [subtitlePath] — local VTT path resolved from the retained subtitle row.
 *    Nice-to-have — nulls when no verified local subtitle is available yet.
 *  - [streamUri] — the playable URI, local if cached else remote (resolver rules).
 *  - [watchHistory] — resume position + duration for the last-known sync.
 *
 * Progress sampling is driven by the route (route owns `ExoPlayer`); this VM
 * exposes `onProgressSample` which coalesces through the outbox (`CODE_PROGRESS`
 * coalesces on `(kind, item_id)` per `OutboxKind`).
 */
@OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
class PlayerViewModel(
    private val videoId: String,
    private val db: IglooDatabase,
    private val outboxWriter: OutboxWriter,
    private val prefs: PreferencesRepo,
    private val scheduler: Scheduler,
    private val uiEffects: UiEffects,
    private val resolvers: MediaResolvers,
) : ViewModel() {

    private val _isRefreshingComments = MutableStateFlow(false)
    val isRefreshingComments: StateFlow<Boolean> = _isRefreshingComments.asStateFlow()

    /** Current DeArrow mode — drives title + thumbnail resolver at render sites. */
    val dearrowMode: StateFlow<String> = prefs.dearrowMode().stateIn(
        scope = viewModelScope,
        started = SharingStarted.WhileSubscribed(5_000L),
        initialValue = PreferencesRepo.Defaults.DEARROW_MODE,
    )

    private var seededHydration = false

    /** The `videos` row. Null until Room's first emission. */
    val video: StateFlow<VideoEntity?> = db.videoDao()
        .getByIdFlow(videoId)
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = null,
        )

    /**
     * Owning channel row — the route renders its `name` in the description block
     * and uses `channelId` for the "tap author → channel" nav. Flipped through
     * `flatMapLatest` so we stay reactive to video row changes (e.g., re-sync
     * flipping channel_id would rebind the channel flow).
     */
    val channel: StateFlow<ChannelEntity?> = video
        .flatMapLatest { v ->
            if (v == null) flow<ChannelEntity?> { emit(null) }
            else db.channelDao().getByIdFlow(v.channelId)
        }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = null,
        )

    val channelProfile: StateFlow<ChannelProfileEntity?> = video
        .flatMapLatest { v ->
            if (v == null) flow<ChannelProfileEntity?> { emit(null) }
            else db.channelProfileDao().getByIdFlow(v.channelId)
        }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = null,
        )

    /** Comments bundled inline with the video sync (02-sync.md §5). */
    val comments: StateFlow<List<VideoCommentEntity>> = db.videoCommentDao()
        .forVideoFlow(videoId)
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = emptyList(),
        )

    /** SponsorBlock segments for scrubber painting + skip-to-end taps. */
    val segments: StateFlow<List<SponsorBlockSegmentEntity>> = db.sponsorBlockSegmentDao()
        .forVideoFlow(videoId)
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = emptyList(),
        )

    /** Local VTT path for the subtitle overlay. Prefer current Sync assets. */
    val subtitlePath: StateFlow<String?> = localAssetPathFlow("subtitle")

    val previewSpritePath: StateFlow<String?> = localAssetPathFlow("preview_sprite")

    val previewTrackJsonPath: StateFlow<String?> = localAssetPathFlow("preview_track_json")

    val subtitleIsAuto: StateFlow<Boolean> = combine(
        db.androidSyncDao().latestVerifiedAssetsForOwnerFlow(videoId, listOf("subtitle")),
        db.mediaInventoryDao().forOwnerAndKindFlow(videoId, "subtitle"),
    ) { syncRows, fallbackRow ->
        syncRows.firstOrNull()?.subtitleIsAuto ?: fallbackRow?.subtitleIsAuto ?: true
    }
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = true,
        )

    /**
     * Playable URI. Re-resolved when Sync or the retained inventory fallback changes.
     */
    val streamUri: StateFlow<MediaUri> = resolvers.videoStreamFlow(videoId)
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = MediaUri.Missing,
        )

    val thumbnailUri: StateFlow<MediaUri> = resolvers.thumbnailForPostFlow(videoId, OwnerKind.YouTubeVideo)
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = MediaUri.Missing,
        )

    /** Watch-history for resume-position (in seconds per the server contract). */
    val watchHistory: StateFlow<WatchHistoryEntity?> = db.watchHistoryDao()
        .getByIdFlow(videoId)
        .stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = null,
        )

    /**
     * Enqueue a `progress` outbox row. `positionMs`/`durationMs` are millis from
     * ExoPlayer; the outbox payload stores seconds (Double) per the server
     * contract in `OutboxKind.Progress`.
     *
     * Coalesces on `(kind, item_id)` so back-to-back samples collapse — the drain
     * sees one row per video with the latest position. Caller throttling (route
     * samples every 5s + on pause/seek) keeps the queue warm but not noisy.
     */
    fun onProgressSample(positionMs: Long, durationMs: Long) {
        if (positionMs < 0L) return
        val safeDuration = durationMs.coerceAtLeast(0L)
        viewModelScope.launch {
            outboxWriter.enqueue(
                OutboxKind.Progress(
                    videoId = videoId,
                    position = positionMs / 1000.0,
                    duration = safeDuration / 1000.0,
                    source = "android",
                )
            )
        }
    }

    fun ensureHydrated() {
        if (seededHydration) return
        seededHydration = true
        if (video.value == null || comments.value.isEmpty()) {
            scheduler.triggerStream(SyncStream.Youtube)
        }
        if (channel.value == null ||
            (channel.value?.avatarUrl.isNullOrBlank() && channelProfile.value?.avatarUrl.isNullOrBlank())
        ) {
            scheduler.triggerStream(SyncStream.Channels)
        }
    }

    fun refreshComments() {
        viewModelScope.launch {
            _isRefreshingComments.value = true
            scheduler.triggerStream(SyncStream.Youtube)
            delay(1_000L)
            _isRefreshingComments.value = false
        }
    }

    fun resolveMentionAndNavigate(handle: String) {
        viewModelScope.launch {
            val route = ChannelRouteResolver.routeForHandle(
                db = db,
                rawHandle = handle,
                fallbackPlatform = "youtube",
            )
            uiEffects.emit(UiEffect.NavigateTo(route))
        }
    }

    private fun localAssetPathFlow(assetKind: String): StateFlow<String?> =
        combine(
            db.androidSyncDao().latestVerifiedLocalPathFlow(videoId, assetKind),
            db.mediaInventoryDao().forOwnerAndKindFlow(videoId, assetKind),
        ) { syncPath, fallbackRow ->
            syncPath?.takeIf { File(it).exists() }
                ?: fallbackRow?.localPath?.takeIf {
                    fallbackRow.state == "cached" && File(it).exists()
                }
        }.stateIn(
            scope = viewModelScope,
            started = SharingStarted.WhileSubscribed(5_000L),
            initialValue = null,
        )
}
