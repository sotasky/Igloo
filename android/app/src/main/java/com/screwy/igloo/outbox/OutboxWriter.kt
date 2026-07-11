package com.screwy.igloo.outbox

import androidx.room.withTransaction
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.entity.MomentsCursorEntity
import com.screwy.igloo.data.entity.OutboxEntity
import com.screwy.igloo.log.LogEntry
import com.screwy.igloo.log.LogLevel
import com.screwy.igloo.log.LogSink
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.FlowPreview
import kotlinx.coroutines.channels.BufferOverflow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.debounce
import kotlinx.coroutines.launch
import kotlinx.serialization.json.JsonObjectBuilder
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

/** Persists user mutations and applies safe optimistic sets in one Room transaction. */
@OptIn(FlowPreview::class)
class OutboxWriter(
    private val db: IglooDatabase,
    private val prefs: PreferencesRepo,
    private val scope: CoroutineScope,
    private val onDrainRequested: () -> Unit = {},
    private val nowMsProvider: () -> Long = { System.currentTimeMillis() },
    private val writeDebounceMs: Long = WRITE_DEBOUNCE_MS,
) {

    private val _debounceSignal =
        MutableSharedFlow<Unit>(
            replay = 0,
            extraBufferCapacity = 8,
            onBufferOverflow = BufferOverflow.DROP_OLDEST,
        )

    init {
        scope.launch { _debounceSignal.debounce(writeDebounceMs).collect { onDrainRequested() } }
    }

    // ─── Enqueue ───────────────────────────────────────────────────────────────

    suspend fun enqueue(kind: OutboxKind) {
        val nowMs = serverCorrectedNowMs()
        db.withTransaction {
            val row = buildOutboxRow(kind, nowMs)
            val outbox = db.outboxDao()
            when (kind.coalesceKey) {
                OutboxKind.CoalesceKey.ByKindItemField -> outbox.coalesceAndInsert(row)
                OutboxKind.CoalesceKey.Fifo -> outbox.insert(row)
            }
            applyOptimisticMutation(db, row)
        }
        if (kind !is OutboxKind.Log && kind !is OutboxKind.LogDebug) {
            _debounceSignal.tryEmit(Unit)
        }
    }

    suspend fun recordMomentsCursor(
        videoId: String,
        positionMs: Long,
        scope: String,
        sortAtMs: Long? = null,
    ) {
        val normalized = PreferencesRepo.Defaults.normalizeMomentsTab(scope)
        if (normalized != "stories") {
            enqueue(OutboxKind.MomentsCursor(videoId, positionMs, normalized, sortAtMs))
            return
        }
        db.momentsCursorDao()
            .upsert(
                MomentsCursorEntity(
                    normalized,
                    videoId,
                    positionMs,
                    sortAtMs ?: 0L,
                    serverCorrectedNowMs(),
                )
            )
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
                    put("channel_id", kind.channelId)
                    put("action", kind.action.wire)
                }
                is OutboxKind.ChannelSetting -> {
                    put("channel_id", kind.channelId)
                    put("field", kind.settingField)
                    kind.value?.let { put("value", it) }
                }
                is OutboxKind.Seen -> put("tweet_id", kind.tweetId)
                is OutboxKind.MomentView -> put("video_id", kind.videoId)
                is OutboxKind.Progress -> {
                    put("video_id", kind.videoId)
                    put("position", kind.position)
                    put("duration", kind.duration)
                }
                is OutboxKind.MomentsCursor -> {
                    put("video_id", kind.videoId)
                    put("position_ms", kind.positionMs)
                    put("scope", kind.scope)
                    kind.sortAtMs?.takeIf { it > 0L }?.let { put("sort_at_ms", it) }
                }
                is OutboxKind.CreateCategory -> {
                    put("name", kind.name)
                    put("provisional_id", kind.provisionalId)
                    put("request_id", kind.requestId)
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

    // ─── Log sink plumbing ────────────────────────────────────────────────────

    /**
     * `LogSink` implementation — Logger calls into this and we enqueue log rows. Kept inline (not a
     * separate class) so one object owns both the queue-side contract and the outbox transaction.
     */
    val logSink: LogSink = LogSink { entry -> enqueue(entry.toOutboxKind()) }

    private fun LogEntry.toOutboxKind(): OutboxKind {
        val stringFields = fields.mapValues { (_, v) -> v?.toString() ?: "" }
        return when (level) {
            LogLevel.Debug -> OutboxKind.LogDebug(event, stringFields, timestampMs)
            LogLevel.Info ->
                OutboxKind.Log(
                    level = "info",
                    event = event,
                    fields = stringFields,
                    timestampMs = timestampMs,
                )
            LogLevel.Error ->
                OutboxKind.Log(
                    level = "error",
                    event = event,
                    fields = stringFields,
                    timestampMs = timestampMs,
                )
        }
    }

    // ─── Internal helpers ─────────────────────────────────────────────────────

    private fun serverCorrectedNowMs(): Long = nowMsProvider() + prefs.serverTimeOffsetMsSync()

    private fun JsonObjectBuilder.putJsonObject(key: String, block: JsonObjectBuilder.() -> Unit) {
        put(key, buildJsonObject(block))
    }

    private fun JsonObjectBuilder.putStringMap(key: String, map: Map<String, String>) {
        put(key, buildJsonObject { map.forEach { (k, v) -> put(k, v) } })
    }

    companion object {
        const val WRITE_DEBOUNCE_MS: Long = 3_000L
    }
}
