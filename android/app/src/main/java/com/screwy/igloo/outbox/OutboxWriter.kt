package com.screwy.igloo.outbox

import androidx.room.withTransaction
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.entity.BookmarkCategoryEntity
import com.screwy.igloo.data.entity.BookmarkEntity
import com.screwy.igloo.data.entity.ChannelFollowEntity
import com.screwy.igloo.data.entity.ChannelSettingEntity
import com.screwy.igloo.data.entity.ChannelStarEntity
import com.screwy.igloo.data.entity.FeedLikeEntity
import com.screwy.igloo.data.entity.FeedSeenEntity
import com.screwy.igloo.data.entity.MomentViewEntity
import com.screwy.igloo.data.entity.MutedAccountEntity
import com.screwy.igloo.data.entity.OutboxEntity
import com.screwy.igloo.data.entity.WatchHistoryEntity
import com.screwy.igloo.log.LogEntry
import com.screwy.igloo.log.LogLevel
import com.screwy.igloo.log.LogSink
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.FlowPreview
import kotlinx.coroutines.channels.BufferOverflow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.debounce
import kotlinx.coroutines.launch
import kotlinx.serialization.json.JsonObjectBuilder
import kotlinx.serialization.json.add
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

/**
 * Enqueue path for every user-initiated mutation + log event. One writer for
 * all kinds so the drain sees a single ordered queue.
 *
 * Each `enqueue(kind)` runs atomically inside one Room transaction:
 *  1. Compute server-corrected timestamp (`deviceNow + serverTimeOffsetMs`).
 *  2. Capture pre-image for rollback-capable kinds (bookmark, channel_setting).
 *  3. Build the JSON payload (typed per kind).
 *  4. Coalesce + insert into `outbox`.
 *  5. Apply the local side-table write (optimistic UI).
 *  6. Signal the drain (debounced write-time trigger per §5).
 *
 * The outbox row + local side-table mutation land in one transaction so Room's Flow
 * observers emit once with both visible — the UI never sees a half-state.
 *
 * `debounceSignal` (step 6) feeds a 3s collecting window that collapses rapid-fire
 * writes into one drain attempt. Reachability-upgrade and periodic-catchup triggers
 * bypass the debounce (those are driven by the scheduler directly).
 */
@OptIn(FlowPreview::class)
class OutboxWriter(
    private val db: IglooDatabase,
    private val prefs: PreferencesRepo,
    private val scope: CoroutineScope,
    private val nowMsProvider: () -> Long = { System.currentTimeMillis() },
    private val writeDebounceMs: Long = WRITE_DEBOUNCE_MS,
) {

    private val _debounceSignal = MutableSharedFlow<Unit>(
        replay = 0,
        extraBufferCapacity = 8,
        onBufferOverflow = BufferOverflow.DROP_OLDEST,
    )

    private val _drainSignal = MutableSharedFlow<Unit>(
        replay = 0,
        extraBufferCapacity = 8,
        onBufferOverflow = BufferOverflow.DROP_OLDEST,
    )

    /**
     * Drain-side subscription surface. `OutboxDrain.run()` collects this to unblock
     * its claim-dispatch loop. Emitted after the 3s debounce window collapses any
     * rapid-fire writes, and also as a direct signal from `signalDrainNow()` for
     * reachability/periodic triggers.
     */
    val drainSignal: SharedFlow<Unit> = _drainSignal.asSharedFlow()

    init {
        scope.launch {
            _debounceSignal
                .debounce(writeDebounceMs)
                .collect { _drainSignal.emit(Unit) }
        }
    }

    /** Bypass the debounce (reachability upgrade, periodic tick). */
    fun signalDrainNow() {
        _drainSignal.tryEmit(Unit)
    }

    // ─── Enqueue ───────────────────────────────────────────────────────────────

    suspend fun enqueue(kind: OutboxKind) {
        val nowMs = serverCorrectedNowMs()
        db.withTransaction {
            val row = buildOutboxRow(kind, nowMs)
            val outbox = db.outboxDao()
            when (kind.coalesceKey) {
                OutboxKind.CoalesceKey.ByKindItemField ->
                    outbox.coalesceAndInsert(row)
                OutboxKind.CoalesceKey.Singleton ->
                    outbox.coalesceAndInsertSingleton(row)
                OutboxKind.CoalesceKey.Fifo ->
                    outbox.insert(row)
            }
            applyLocalMutation(kind, nowMs)
        }
        _debounceSignal.tryEmit(Unit)
    }

    // ─── Row construction ──────────────────────────────────────────────────────

    private fun buildOutboxRow(kind: OutboxKind, nowMs: Long): OutboxEntity {
        val payload = buildPayload(kind, nowMs)
        return OutboxEntity(
            kind = kind.code,
            itemId = kind.itemId,
            field = kind.field,
            payloadJson = payload,
            state = "pending",
            attemptCount = 0,
            nextAttemptAtMs = 0,
            lastErrorCode = null,
            lastErrorBody = null,
            createdAtMs = nowMs,
        )
    }

    private fun buildPayload(kind: OutboxKind, nowMs: Long): String {
        val obj = buildJsonObject {
            put("updated_at_ms", nowMs)
            when (kind) {
                is OutboxKind.Like -> {
                    put("tweet_id", kind.tweetId)
                    put("action", kind.action.wire)
                }
                is OutboxKind.Bookmark -> {
                    put("video_id", kind.videoId)
                    put("action", kind.action.wire)
                    kind.categoryId?.let { put("category_id", it) }
                    kind.customTitle?.let { put("custom_title", it) }
                    kind.accountHandles?.let { put("account_handles", it) }
                    kind.mediaIndices?.let { put("media_indices", it) }
                    kind.prevRow?.let { prev ->
                        putJsonObject("prev") {
                            put("existed", prev.existed)
                            put("category_id", prev.categoryId)
                            prev.customTitle?.let { put("custom_title", it) }
                            prev.accountHandles?.let { put("account_handles", it) }
                            prev.mediaIndices?.let { put("media_indices", it) }
                            put("bookmarked_at", prev.bookmarkedAt)
                        }
                    }
                }
                is OutboxKind.Follow -> {
                    put("channel_id", kind.channelId)
                    put("action", kind.action.wire)
                }
                is OutboxKind.Star -> {
                    put("channel_id", kind.channelId)
                    put("action", kind.action.wire)
                }
                is OutboxKind.Mute -> {
                    put("handle", kind.handle)
                    put("action", kind.action.wire)
                }
                is OutboxKind.ChannelSetting -> {
                    put("channel_id", kind.channelId)
                    put("field", kind.settingField)
                    kind.value?.let { put("value", it) }
                    putJsonObject("prev") {
                        put("existed", kind.prevExisted)
                        kind.prevValue?.let { put("value", it) }
                    }
                }
                is OutboxKind.Seen -> put("tweet_id", kind.tweetId)
                is OutboxKind.MomentView -> put("video_id", kind.videoId)
                is OutboxKind.Progress -> {
                    put("video_id", kind.videoId)
                    put("position", kind.position)
                    put("duration", kind.duration)
                    put("source", kind.source)
                }
                is OutboxKind.MomentsCursor -> {
                    put("video_id", kind.videoId)
                    put("position_ms", kind.positionMs)
                    put("scope", kind.scope)
                }
                is OutboxKind.CreateCategory -> {
                    put("name", kind.name)
                    put("provisional_id", kind.provisionalId)
                }
                is OutboxKind.Log -> {
                    put("level", kind.level)
                    put("event", kind.event)
                    put("timestamp_ms", kind.timestampMs)
                    putStringMap("fields", kind.fields)
                }
                is OutboxKind.LogDebug -> {
                    put("event", kind.event)
                    put("timestamp_ms", kind.timestampMs)
                    putStringMap("fields", kind.fields)
                }
            }
        }
        return obj.toString()
    }

    // ─── Local side-table apply (optimistic UI) ───────────────────────────────

    private suspend fun applyLocalMutation(kind: OutboxKind, nowMs: Long) {
        when (kind) {
            is OutboxKind.Like -> when (kind.action) {
                OutboxKind.Action.Set -> {
                    db.feedLikeDao().upsert(FeedLikeEntity(kind.tweetId, nowMs))
                    db.feedSeenDao().upsert(FeedSeenEntity(kind.tweetId, nowMs))
                }
                OutboxKind.Action.Clear -> db.feedLikeDao().delete(kind.tweetId)
            }
            is OutboxKind.Bookmark -> when (kind.action) {
                OutboxKind.Action.Set -> db.bookmarkDao().upsert(
                    BookmarkEntity(
                        videoId = kind.videoId,
                        categoryId = kind.categoryId ?: 0L,
                        customTitle = kind.customTitle,
                        accountHandles = kind.accountHandles ?: kind.prevRow?.accountHandles,
                        mediaIndices = kind.mediaIndices ?: kind.prevRow?.mediaIndices,
                        bookmarkedAt = nowMs,
                    )
                )
                OutboxKind.Action.Clear -> db.bookmarkDao().delete(kind.videoId)
            }
            is OutboxKind.Follow -> when (kind.action) {
                OutboxKind.Action.Set -> db.channelFollowDao().upsert(ChannelFollowEntity(kind.channelId, nowMs))
                OutboxKind.Action.Clear -> db.channelFollowDao().delete(kind.channelId)
            }
            is OutboxKind.Star -> when (kind.action) {
                OutboxKind.Action.Set -> db.channelStarDao().upsert(ChannelStarEntity(kind.channelId, nowMs))
                OutboxKind.Action.Clear -> db.channelStarDao().delete(kind.channelId)
            }
            is OutboxKind.Mute -> when (kind.action) {
                OutboxKind.Action.Set -> db.mutedAccountDao().upsert(MutedAccountEntity(kind.handle, nowMs))
                OutboxKind.Action.Clear -> db.mutedAccountDao().delete(kind.handle)
            }
            is OutboxKind.ChannelSetting -> applyChannelSetting(kind, nowMs)
            is OutboxKind.Seen -> db.feedSeenDao().upsert(FeedSeenEntity(kind.tweetId, nowMs))
            is OutboxKind.MomentView -> db.momentViewDao().upsert(MomentViewEntity(kind.videoId, nowMs))
            is OutboxKind.Progress -> db.watchHistoryDao().upsert(
                WatchHistoryEntity(
                    videoId = kind.videoId,
                    playbackPosition = kind.position,
                    duration = kind.duration,
                    progressUpdatedAtMs = nowMs,
                    progressSource = kind.source,
                    lastWatched = nowMs,
                )
            )
            is OutboxKind.MomentsCursor -> {
                // Position is per-videoId: on a real video change the payload's
                // position (usually 0) is authoritative; on a same-video write we
                // only update position when it's a real playback sample, so
                // settle-time writes with positionMs=0 don't clobber the 2s
                // periodic tick's real progress. Without this guard the resume
                // position for a TikTok always ended up at 0.
                val prevVideoId = prefs.getMomentsResumeVideoId(scope = kind.scope)
                prefs.setMomentsResumeVideoId(kind.videoId, scope = kind.scope)
                if (prevVideoId != kind.videoId || kind.positionMs > 0L) {
                    prefs.setMomentsResumePositionMs(kind.positionMs, scope = kind.scope)
                }
            }
            is OutboxKind.CreateCategory -> db.bookmarkCategoryDao().upsert(
                BookmarkCategoryEntity(
                    categoryId = kind.provisionalId,
                    name = kind.name,
                    archivePath = null,
                    createdAt = nowMs,
                )
            )
            // No local side-table write for these.
            is OutboxKind.Log,
            is OutboxKind.LogDebug -> Unit
        }
    }

    private suspend fun applyChannelSetting(kind: OutboxKind.ChannelSetting, nowMs: Long) {
        val dao = db.channelSettingDao()
        val existing = dao.getById(kind.channelId)
        val updated = (existing ?: ChannelSettingEntity(channelId = kind.channelId))
            .withField(kind.settingField, kind.value, nowMs)
        dao.upsert(updated)
    }

    // ─── Pre-image capture (rollback support) ─────────────────────────────────

    /**
     * Read the current bookmark row so the dispatcher can rollback without re-reading
     * after a dead classification. Writer passes this into `OutboxKind.Bookmark`.
     */
    suspend fun capturePreviousBookmark(videoId: String): OutboxKind.BookmarkPreImage {
        val row = db.bookmarkDao().getById(videoId)
        return if (row == null) OutboxKind.BookmarkPreImage(existed = false)
        else OutboxKind.BookmarkPreImage(
            existed = true,
            categoryId = row.categoryId,
            customTitle = row.customTitle,
            accountHandles = row.accountHandles,
            mediaIndices = row.mediaIndices,
            bookmarkedAt = row.bookmarkedAt,
        )
    }

    /**
     * Read the current channel-setting value for the named field so dispatcher can
     * rollback after a dead classification without re-querying.
     */
    suspend fun capturePreviousChannelSetting(channelId: String, settingField: String): Pair<Boolean, Long?> {
        val row = db.channelSettingDao().getById(channelId) ?: return Pair(false, null)
        val value = when (settingField) {
            "media_only"           -> row.mediaOnly?.toLong()
            "include_reposts"      -> row.includeReposts?.toLong()
            "media_download_limit" -> row.mediaDownloadLimit?.toLong()
            "max_videos"           -> row.maxVideos?.toLong()
            "download_subtitles"   -> row.downloadSubtitles?.toLong()
            else                   -> null
        }
        return Pair(true, value)
    }

    // ─── Log sink plumbing ────────────────────────────────────────────────────

    /**
     * `LogSink` implementation — Logger calls into this and we enqueue log rows. Kept
     * inline (not a separate class) so one object owns both the queue-side contract
     * and the outbox transaction.
     */
    val logSink: LogSink = LogSink { entry -> enqueue(entry.toOutboxKind()) }

    private fun LogEntry.toOutboxKind(): OutboxKind {
        val stringFields = fields.mapValues { (_, v) -> v?.toString() ?: "" }
        return when (level) {
            LogLevel.Debug -> OutboxKind.LogDebug(event, stringFields, timestampMs)
            LogLevel.Info  -> OutboxKind.Log(level = "info", event = event, fields = stringFields, timestampMs = timestampMs)
            LogLevel.Error -> OutboxKind.Log(level = "error", event = event, fields = stringFields, timestampMs = timestampMs)
        }
    }

    // ─── Internal helpers ─────────────────────────────────────────────────────

    private fun serverCorrectedNowMs(): Long = nowMsProvider() + prefs.serverTimeOffsetMsSync()

    private fun ChannelSettingEntity.withField(name: String, value: Long?, nowMs: Long): ChannelSettingEntity {
        val intVal = value?.toInt()
        return when (name) {
            "media_only"           -> copy(mediaOnly           = intVal, updatedAt = nowMs)
            "include_reposts"      -> copy(includeReposts      = intVal, updatedAt = nowMs)
            "media_download_limit" -> copy(mediaDownloadLimit  = intVal, updatedAt = nowMs)
            "max_videos"           -> copy(maxVideos           = intVal, updatedAt = nowMs)
            "download_subtitles"   -> copy(downloadSubtitles   = intVal, updatedAt = nowMs)
            else                   -> this
        }
    }

    private fun JsonObjectBuilder.putJsonObject(key: String, block: JsonObjectBuilder.() -> Unit) {
        put(key, buildJsonObject(block))
    }

    private fun JsonObjectBuilder.putStringMap(key: String, map: Map<String, String>) {
        put(key, buildJsonObject {
            map.forEach { (k, v) -> put(k, v) }
        })
    }

    companion object {
        /** Write-time debounce window — 03-outbox.md §5 calls for 3 seconds. */
        const val WRITE_DEBOUNCE_MS: Long = 3_000L
    }
}
