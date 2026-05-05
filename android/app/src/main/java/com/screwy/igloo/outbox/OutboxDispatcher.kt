package com.screwy.igloo.outbox

import androidx.room.withTransaction
import com.screwy.igloo.R
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.entity.BookmarkCategoryEntity
import com.screwy.igloo.data.entity.BookmarkEntity
import com.screwy.igloo.data.entity.ChannelFollowEntity
import com.screwy.igloo.data.entity.ChannelSettingEntity
import com.screwy.igloo.data.entity.ChannelStarEntity
import com.screwy.igloo.data.entity.FeedLikeEntity
import com.screwy.igloo.data.entity.MutedAccountEntity
import com.screwy.igloo.data.entity.OutboxEntity
import com.screwy.igloo.log.Logger
import com.screwy.igloo.net.BookmarkAliasRequest
import com.screwy.igloo.net.BookmarkRequest
import com.screwy.igloo.net.IglooError
import com.screwy.igloo.net.ChannelSettingRequest
import com.screwy.igloo.net.CreateCategoryRequest
import com.screwy.igloo.net.CreateCategoryResponse
import com.screwy.igloo.net.LikeRequest
import com.screwy.igloo.net.LogBatchRequest
import com.screwy.igloo.net.LogEntryPayload
import com.screwy.igloo.net.MomentViewRequest
import com.screwy.igloo.net.MomentsCursorRequest
import com.screwy.igloo.net.MuteRequest
import com.screwy.igloo.net.OutboxApi
import com.screwy.igloo.net.ProgressRequest
import com.screwy.igloo.net.SeenRequest
import com.screwy.igloo.net.ToggleRequest
import com.screwy.igloo.net.iglooJson
import com.screwy.igloo.net.classify
import com.screwy.igloo.net.auth.AuthTokenProvider
import com.screwy.igloo.ui.UiEffect
import com.screwy.igloo.ui.UiEffects
import io.ktor.client.call.body
import io.ktor.client.statement.HttpResponse
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull

/**
 * Per-kind HTTP dispatch for the outbox drain.
 *
 * Recipes live one-per-kind. `dispatch(batch)` groups rows by kind, picks the
 * recipe, issues the HTTP call, classifies the response, and returns a per-row
 * result.
 *
 * Result flow:
 *  - 2xx           → `Ack`     — caller deletes the row. Cursor + server_time update
 *                                 happens automatically via `EnvelopeParser`
 *                                 (`ResponseObserver`). `CreateCategory` additionally
 *                                 runs a cascading provisional→real ID remap.
 *  - 401           → `AuthRefresh` — caller waits for auth refresh + retries.
 *  - 408/429/5xx /
 *    network       → `Retry(err)` — caller schedules backoff.
 *  - other 4xx     → `Dead(err)`  — caller marks dead + rolls back.
 *
 * `rollback(row)` runs the kind-specific revert. Dispatcher owns both because the
 * rollback shape lives in the recipe (same file as the forward path).
 */
class OutboxDispatcher(
    private val api: OutboxApi,
    private val db: IglooDatabase,
    private val authTokens: AuthTokenProvider,
    private val logger: Logger,
    private val uiEffects: UiEffects,
    private val nowMsProvider: () -> Long = { System.currentTimeMillis() },
) {

    sealed interface Result {
        data object Ack : Result
        data object AuthRefresh : Result
        data class Retry(val error: IglooError) : Result
        data class Dead(val error: IglooError) : Result
    }

    /** One HTTP call per batch — batch contains either one row (normal kinds) or many (batchable kinds). */
    suspend fun dispatch(batch: List<OutboxEntity>): Map<Long, Result> {
        if (batch.isEmpty()) return emptyMap()
        val kindCode = batch.first().kind
        return when (kindCode) {
            OutboxKind.CODE_LIKE              -> dispatchLike(batch)
            OutboxKind.CODE_BOOKMARK          -> dispatchBookmark(batch)
            OutboxKind.CODE_FOLLOW            -> dispatchFollow(batch)
            OutboxKind.CODE_STAR              -> dispatchStar(batch)
            OutboxKind.CODE_MUTE              -> dispatchMute(batch)
            OutboxKind.CODE_CHANNEL_SETTING   -> dispatchChannelSetting(batch)
            OutboxKind.CODE_SEEN              -> dispatchSeenBatch(batch)
            OutboxKind.CODE_MOMENT_VIEW       -> dispatchMomentView(batch)
            OutboxKind.CODE_PROGRESS          -> dispatchProgress(batch)
            OutboxKind.CODE_MOMENTS_CURSOR    -> dispatchMomentsCursor(batch)
            OutboxKind.CODE_CREATE_CATEGORY   -> dispatchCreateCategory(batch)
            OutboxKind.CODE_BOOKMARK_ALIAS    -> dispatchBookmarkAlias(batch)
            OutboxKind.CODE_LOG               -> dispatchLogBatch(batch, debug = false)
            OutboxKind.CODE_LOG_DEBUG         -> dispatchLogBatch(batch, debug = true)
            else -> batch.associate {
                it.id to Result.Dead(
                    IglooError.Dead(-1, "unknown_kind", "no recipe for $kindCode"),
                )
            }
        }
    }

    // ─── Per-kind recipes ─────────────────────────────────────────────────────

    private suspend fun dispatchLike(batch: List<OutboxEntity>): Map<Long, Result> =
        perRow(batch) { row ->
            val p = row.payload()
            api.like(LikeRequest(
                tweet_id = p.string("tweet_id") ?: row.itemId.orEmpty(),
                action = p.string("action") ?: "set",
                updated_at_ms = p.long("updated_at_ms") ?: row.createdAtMs,
            ))
        }

    private suspend fun dispatchBookmark(batch: List<OutboxEntity>): Map<Long, Result> =
        perRow(batch) { row ->
            val p = row.payload()
            api.bookmark(BookmarkRequest(
                video_id = p.string("video_id") ?: row.itemId.orEmpty(),
                action = p.string("action") ?: "set",
                category_id = p.long("category_id"),
                custom_title = p.string("custom_title"),
                account_handles = p.string("account_handles"),
                media_indices = p.string("media_indices"),
                updated_at_ms = p.long("updated_at_ms") ?: row.createdAtMs,
            ))
        }

    private suspend fun dispatchFollow(batch: List<OutboxEntity>): Map<Long, Result> =
        perRow(batch) { row ->
            val p = row.payload()
            api.follow(ToggleRequest(
                channelId = p.string("channel_id") ?: row.itemId.orEmpty(),
                action = p.string("action") ?: "set",
                updated_at_ms = p.long("updated_at_ms") ?: row.createdAtMs,
            ))
        }

    private suspend fun dispatchStar(batch: List<OutboxEntity>): Map<Long, Result> =
        perRow(batch) { row ->
            val p = row.payload()
            api.star(ToggleRequest(
                channelId = p.string("channel_id") ?: row.itemId.orEmpty(),
                action = p.string("action") ?: "set",
                updated_at_ms = p.long("updated_at_ms") ?: row.createdAtMs,
            ))
        }

    private suspend fun dispatchMute(batch: List<OutboxEntity>): Map<Long, Result> =
        perRow(batch) { row ->
            val p = row.payload()
            api.mute(MuteRequest(
                handle = p.string("handle") ?: row.itemId.orEmpty(),
                action = p.string("action") ?: "set",
                updated_at_ms = p.long("updated_at_ms") ?: row.createdAtMs,
            ))
        }

    private suspend fun dispatchChannelSetting(batch: List<OutboxEntity>): Map<Long, Result> =
        perRow(batch) { row ->
            val p = row.payload()
            api.channelSetting(ChannelSettingRequest(
                channel_id = p.string("channel_id") ?: row.itemId.orEmpty(),
                field = p.string("field") ?: row.field.orEmpty(),
                value = p.long("value"),
                updated_at_ms = p.long("updated_at_ms") ?: row.createdAtMs,
            ))
        }

    private suspend fun dispatchMomentView(batch: List<OutboxEntity>): Map<Long, Result> =
        perRow(batch) { row ->
            val p = row.payload()
            api.momentView(MomentViewRequest(
                video_id = p.string("video_id") ?: row.itemId.orEmpty(),
                updated_at_ms = p.long("updated_at_ms") ?: row.createdAtMs,
            ))
        }

    private suspend fun dispatchProgress(batch: List<OutboxEntity>): Map<Long, Result> =
        perRow(batch) { row ->
            val p = row.payload()
            api.progress(ProgressRequest(
                video_id = p.string("video_id") ?: row.itemId.orEmpty(),
                position = p.double("position") ?: 0.0,
                duration = p.double("duration") ?: 0.0,
                source = p.string("source") ?: "android",
                updated_at_ms = p.long("updated_at_ms") ?: row.createdAtMs,
            ))
        }

    private suspend fun dispatchMomentsCursor(batch: List<OutboxEntity>): Map<Long, Result> =
        perRow(batch) { row ->
            val p = row.payload()
            api.momentsCursor(MomentsCursorRequest(
                video_id = p.string("video_id").orEmpty(),
                position_ms = p.long("position_ms") ?: 0,
                updated_at_ms = p.long("updated_at_ms") ?: row.createdAtMs,
                scope = p.string("scope") ?: row.itemId ?: "all",
            ))
        }

    private suspend fun dispatchBookmarkAlias(batch: List<OutboxEntity>): Map<Long, Result> =
        perRow(batch) { row ->
            val p = row.payload()
            api.bookmarkAlias(BookmarkAliasRequest(
                original_handle = p.string("original_handle") ?: row.itemId.orEmpty(),
                display_alias = p.string("display_alias").orEmpty(),
                updated_at_ms = p.long("updated_at_ms") ?: row.createdAtMs,
            ))
        }

    // ─── Batchable kinds ──────────────────────────────────────────────────────

    private suspend fun dispatchSeenBatch(batch: List<OutboxEntity>): Map<Long, Result> {
        val tweetIds = batch.mapNotNull { it.payload().string("tweet_id") ?: it.itemId }
        val latestMs = batch.maxOf { it.createdAtMs }
        val response = runCatching {
            api.seen(SeenRequest(tweet_ids = tweetIds, updated_at_ms = latestMs))
        }
        return fanOutBatchResult(batch, response)
    }

    private suspend fun dispatchLogBatch(batch: List<OutboxEntity>, debug: Boolean): Map<Long, Result> {
        val entries = batch.map { row ->
            val p = row.payload()
            LogEntryPayload(
                level = if (debug) null else p.string("level"),
                event = p.string("event") ?: "unknown",
                fields = p.jsonObject("fields")?.stringMap(),
                timestamp_ms = p.long("timestamp_ms") ?: row.createdAtMs,
            )
        }
        val req = LogBatchRequest(entries = entries)
        val response = runCatching {
            if (debug) api.postLogDebug(req) else api.postLogServer(req)
        }
        return fanOutBatchResult(batch, response)
    }

    /** `create_category` needs special 2xx handling — cascading provisional→real remap. */
    private suspend fun dispatchCreateCategory(batch: List<OutboxEntity>): Map<Long, Result> {
        val results = HashMap<Long, Result>(batch.size)
        for (row in batch) {
            val p = row.payload()
            val provisionalId = p.long("provisional_id") ?: row.itemId?.toLongOrNull() ?: 0L
            val name = p.string("name").orEmpty()
            val callResult = runCatching {
                api.createCategory(CreateCategoryRequest(
                    name = name,
                    provisional_id = provisionalId.toString(),
                    updated_at_ms = p.long("updated_at_ms") ?: row.createdAtMs,
                ))
            }
            results[row.id] = handleCreateCategoryResponse(row, provisionalId, callResult)
        }
        return results
    }

    private suspend fun handleCreateCategoryResponse(
        row: OutboxEntity,
        provisionalId: Long,
        callResult: kotlin.Result<HttpResponse>,
    ): Result {
        val response = callResult.getOrElse { e ->
            return if (e is kotlinx.coroutines.CancellationException) throw e
            else Result.Retry(IglooError.Network(e))
        }
        val classification = response.classify()
        if (classification != null) return classificationToResult(classification)

        val parsed = runCatching { response.body<CreateCategoryResponse>() }
        val ok = parsed.getOrNull()
        if (ok == null) return Result.Dead(IglooError.Malformed("create_category response parse failed"))

        // Cascade provisional → real: update both tables in one tx.
        db.withTransaction {
            val catDao = db.bookmarkCategoryDao()
            val bookmarksDao = db.bookmarkDao()
            // `remapCategory` flips any bookmark currently pointing at the provisional id.
            bookmarksDao.remapCategory(oldId = provisionalId, newId = ok.category_id)
            catDao.delete(provisionalId)
            catDao.upsert(
                BookmarkCategoryEntity(
                    categoryId = ok.category_id,
                    name = row.payload().string("name").orEmpty(),
                    archivePath = null,
                    createdAt = row.createdAtMs,
                ),
            )
        }
        return Result.Ack
    }

    // ─── Rollback ─────────────────────────────────────────────────────────────

    /**
     * Run the kind-specific rollback. Caller holds the transaction
     * (so both the `state='dead'` update and the side-table revert land atomically).
     */
    suspend fun rollback(row: OutboxEntity) {
        val p = row.payload()
        when (row.kind) {
            OutboxKind.CODE_LIKE -> {
                val tweetId = p.string("tweet_id") ?: row.itemId ?: return
                when (p.string("action")) {
                    "set" -> db.feedLikeDao().delete(tweetId)
                    "clear" -> db.feedLikeDao().upsert(FeedLikeEntity(tweetId, row.createdAtMs))
                }
            }
            OutboxKind.CODE_FOLLOW -> {
                val channelId = p.string("channel_id") ?: row.itemId ?: return
                when (p.string("action")) {
                    "set" -> db.channelFollowDao().delete(channelId)
                    "clear" -> db.channelFollowDao().upsert(ChannelFollowEntity(channelId, row.createdAtMs))
                }
            }
            OutboxKind.CODE_STAR -> {
                val channelId = p.string("channel_id") ?: row.itemId ?: return
                when (p.string("action")) {
                    "set" -> db.channelStarDao().delete(channelId)
                    "clear" -> db.channelStarDao().upsert(ChannelStarEntity(channelId, row.createdAtMs))
                }
            }
            OutboxKind.CODE_MUTE -> {
                val handle = p.string("handle") ?: row.itemId ?: return
                when (p.string("action")) {
                    "set" -> db.mutedAccountDao().delete(handle)
                    "clear" -> db.mutedAccountDao().upsert(MutedAccountEntity(handle, row.createdAtMs))
                }
            }
            OutboxKind.CODE_BOOKMARK -> {
                val videoId = p.string("video_id") ?: row.itemId ?: return
                val prev = p.jsonObject("prev")
                val prevExisted = prev?.bool("existed") == true
                if (!prevExisted) {
                    db.bookmarkDao().delete(videoId)
                } else {
                    db.bookmarkDao().upsert(
                        BookmarkEntity(
                            videoId = videoId,
                            categoryId = prev?.long("category_id") ?: 0L,
                            customTitle = prev?.string("custom_title"),
                            accountHandles = prev?.string("account_handles"),
                            mediaIndices = prev?.string("media_indices"),
                            bookmarkedAt = prev?.long("bookmarked_at") ?: row.createdAtMs,
                        ),
                    )
                }
            }
            OutboxKind.CODE_CHANNEL_SETTING -> {
                val channelId = p.string("channel_id") ?: row.itemId ?: return
                val settingField = p.string("field") ?: row.field ?: return
                val prev = p.jsonObject("prev")
                val prevExisted = prev?.bool("existed") == true
                val dao = db.channelSettingDao()
                if (!prevExisted) {
                    dao.delete(channelId)
                } else {
                    val current = dao.getById(channelId) ?: ChannelSettingEntity(channelId = channelId)
                    dao.upsert(current.withField(settingField, prev?.long("value"), row.createdAtMs))
                }
            }
            OutboxKind.CODE_CREATE_CATEGORY -> {
                // Category rollback: drop the provisional row + null out any bookmarks
                // pointing at it. UI toast informs the user.
                val provisionalId = p.long("provisional_id") ?: row.itemId?.toLongOrNull() ?: return
                db.bookmarkDao().remapCategory(oldId = provisionalId, newId = 0L)
                db.bookmarkCategoryDao().delete(provisionalId)
            }
            // Fire-and-forget kinds (seen, moment_view, progress, moments_cursor,
            // bookmark_alias, log, log_debug) don't roll back.
            else -> Unit
        }

        if (row.kind in ROLLBACK_TOAST_KINDS) {
            uiEffects.emit(
                UiEffect.ToastRes(
                    R.string.outbox_could_not_save_kind,
                    formatArgs = listOf(row.kind.replace('_', ' ')),
                ),
            )
        }
    }

    // ─── Shared per-kind helpers ──────────────────────────────────────────────

    /** Run one HTTP call per row (non-batchable kinds). Each row's result is independent. */
    private suspend inline fun perRow(
        batch: List<OutboxEntity>,
        crossinline call: suspend (OutboxEntity) -> HttpResponse,
    ): Map<Long, Result> {
        val results = HashMap<Long, Result>(batch.size)
        for (row in batch) {
            val callResult = runCatching { call(row) }
            results[row.id] = toRowResult(callResult)
        }
        return results
    }

    /** Fan one batch response across every row's result. All rows share the classification. */
    private suspend fun fanOutBatchResult(
        batch: List<OutboxEntity>,
        callResult: kotlin.Result<HttpResponse>,
    ): Map<Long, Result> {
        val single = toRowResult(callResult)
        return batch.associate { it.id to single }
    }

    private suspend fun toRowResult(callResult: kotlin.Result<HttpResponse>): Result {
        val response = callResult.getOrElse { e ->
            if (e is kotlinx.coroutines.CancellationException) throw e
            return Result.Retry(IglooError.Network(e))
        }
        val classification = response.classify() ?: return Result.Ack
        return classificationToResult(classification)
    }

    private fun classificationToResult(err: IglooError): Result = when {
        err.requiresRefresh -> Result.AuthRefresh
        err.isTransient     -> Result.Retry(err)
        else                -> Result.Dead(err)
    }

    // ─── Payload / JSON accessors ─────────────────────────────────────────────

    private fun OutboxEntity.payload(): JsonObject =
        runCatching { iglooJson.parseToJsonElement(payloadJson).jsonObject }
            .getOrDefault(JsonObject(emptyMap()))

    private fun JsonObject.string(name: String): String? {
        val prim = this[name] as? JsonPrimitive ?: return null
        return if (prim.isString) prim.content else prim.contentOrNull
    }
    private fun JsonObject.long(name: String): Long? =
        (this[name] as? JsonPrimitive)?.longOrNull
    private fun JsonObject.double(name: String): Double? =
        (this[name] as? JsonPrimitive)?.let { runCatching { it.content.toDouble() }.getOrNull() }
    private fun JsonObject.bool(name: String): Boolean? =
        (this[name] as? JsonPrimitive)?.booleanOrNull
    private fun JsonObject.jsonObject(name: String): JsonObject? =
        this[name] as? JsonObject

    private fun JsonObject.stringMap(): Map<String, String> = buildMap {
        for ((k, v) in this@stringMap) {
            val s = (v as? JsonPrimitive)?.contentOrNull ?: continue
            put(k, s)
        }
    }

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

    companion object {
        private val ROLLBACK_TOAST_KINDS: Set<String> = setOf(
            OutboxKind.CODE_LIKE,
            OutboxKind.CODE_BOOKMARK,
            OutboxKind.CODE_FOLLOW,
            OutboxKind.CODE_STAR,
            OutboxKind.CODE_MUTE,
            OutboxKind.CODE_CHANNEL_SETTING,
            OutboxKind.CODE_CREATE_CATEGORY,
        )
    }
}
