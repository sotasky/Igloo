package com.screwy.igloo.sync

import androidx.room.withTransaction
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.PreferencesRepo
import com.screwy.igloo.data.dao.CursorDao
import com.screwy.igloo.data.dao.OutboxDao
import com.screwy.igloo.data.entity.BookmarkCategoryEntity
import com.screwy.igloo.data.entity.BookmarkEntity
import com.screwy.igloo.data.entity.ChannelFollowEntity
import com.screwy.igloo.data.entity.ChannelSettingEntity
import com.screwy.igloo.data.entity.ChannelStarEntity
import com.screwy.igloo.data.entity.FeedLikeEntity
import com.screwy.igloo.data.entity.FeedSeenEntity
import com.screwy.igloo.data.entity.MomentViewEntity
import com.screwy.igloo.data.entity.MutedAccountEntity
import com.screwy.igloo.data.entity.WatchHistoryEntity
import com.screwy.igloo.log.Logger
import com.screwy.igloo.net.MutationChange
import com.screwy.igloo.net.MutationDeltaApi
import com.screwy.igloo.net.Reachability
import com.screwy.igloo.outbox.OutboxKind
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.doubleOrNull
import kotlinx.serialization.json.longOrNull

/**
 * Inbound interaction stream for web/server-originated mutations.
 *
 * Content/rank sync still owns rows and ordering snapshots; this class mirrors the thin
 * user-state side tables that can change from another client between content pages.
 */
class MutationDeltaSync(
    private val db: IglooDatabase,
    private val prefs: PreferencesRepo,
    private val cursorDao: CursorDao,
    private val outboxDao: OutboxDao,
    private val api: MutationDeltaApi,
    private val reachability: Reachability,
    private val logger: Logger,
    private val nowMsProvider: () -> Long = { System.currentTimeMillis() },
) {
    private val mutex = Mutex()

    suspend fun sync(): MutationDeltaResult = mutex.withLock {
        if (reachability.state.value !is Reachability.State.Online) {
            logger.debug("mutation_delta_skipped_offline")
            return@withLock MutationDeltaResult()
        }

        var since = cursorDao.get(CURSOR_KEY)?.cursor
        var totalApplied = 0
        var rankAffecting = false
        while (true) {
            val requestMarker = since
            val response = api.delta(since = requestMarker, limit = PAGE_SIZE)
            var pageApplied = 0
            var pageRankAffecting = false
            var lastVersion = requestMarker?.toLongOrNull() ?: 0L

            db.withTransaction {
                val guard = PreserveLocalGuard(outboxDao)
                for (change in response.changes) {
                    lastVersion = maxOf(lastVersion, change.version)
                    val applied = applyChange(change, guard)
                    if (applied) pageApplied += 1
                    if (change.type in RANK_AFFECTING_TYPES) pageRankAffecting = true
                }
            }

            totalApplied += pageApplied
            rankAffecting = rankAffecting || pageRankAffecting

            if (response.truncated) {
                val previousVersion = requestMarker?.toLongOrNull() ?: 0L
                if (response.changes.isEmpty() || lastVersion <= previousVersion) {
                    throw IllegalStateException("mutation delta marker stalled at ${requestMarker.orEmpty()}")
                }
            }

            val nextCursor = when {
                response.truncated && response.changes.isNotEmpty() -> lastVersion.toString()
                response.version > 0 -> response.version.toString()
                response.changes.isNotEmpty() -> lastVersion.toString()
                else -> requestMarker
            }

            if (nextCursor != null && nextCursor != requestMarker) {
                cursorDao.upsert(CURSOR_KEY, nextCursor, nowMsProvider())
                since = nextCursor
            }

            logger.info(
                event = "mutation_delta_page_applied",
                fields = mapOf(
                    "count" to pageApplied.toString(),
                    "request_marker" to requestMarker.orEmpty(),
                    "next_marker" to since.orEmpty(),
                    "truncated" to response.truncated.toString(),
                ),
            )

            if (!response.truncated) break
        }

        MutationDeltaResult(applied = totalApplied, rankAffecting = rankAffecting)
    }

    private suspend fun applyChange(change: MutationChange, guard: PreserveLocalGuard): Boolean {
        val itemId = change.item_id.trim()
        val value = change.value
        val nowMs = value.firstLong("updated_at_ms", "UpdatedAtMs", "ts") ?: change.created_at.takeIf { it > 0 } ?: nowMsProvider()
        return when (change.type) {
            OutboxKind.CODE_LIKE -> applyToggle(
                itemId = itemId,
                value = value,
                setKeys = listOf("liked"),
                pending = { guard.likePending(itemId) },
                set = { db.feedLikeDao().upsert(FeedLikeEntity(itemId, nowMs)) },
                clear = { db.feedLikeDao().delete(itemId) },
            )
            OutboxKind.CODE_BOOKMARK -> applyBookmark(itemId, value, guard, nowMs)
            OutboxKind.CODE_SEEN -> applySeen(itemId, value, guard, nowMs)
            OutboxKind.CODE_MUTE -> applyToggle(
                itemId = itemId,
                value = value,
                setKeys = listOf("muted"),
                pending = { guard.mutePending(itemId) },
                set = { db.mutedAccountDao().upsert(MutedAccountEntity(itemId, nowMs)) },
                clear = { db.mutedAccountDao().delete(itemId) },
            )
            OutboxKind.CODE_FOLLOW, "subscribe", "unsubscribe" -> applyFollow(change.type, itemId, value, guard, nowMs)
            OutboxKind.CODE_STAR -> applyToggle(
                itemId = itemId,
                value = value,
                setKeys = listOf("starred"),
                pending = { guard.starPending(itemId) },
                set = { db.channelStarDao().upsert(ChannelStarEntity(itemId, nowMs)) },
                clear = { db.channelStarDao().delete(itemId) },
            )
            OutboxKind.CODE_CHANNEL_SETTING -> applyChannelSetting(itemId, value, guard, nowMs)
            OutboxKind.CODE_MOMENT_VIEW -> {
                if (guard.momentViewPending(itemId)) return false
                db.momentViewDao().upsert(MomentViewEntity(itemId, nowMs))
                true
            }
            OutboxKind.CODE_MOMENTS_CURSOR -> applyMomentsCursor(itemId, value, guard)
            OutboxKind.CODE_PROGRESS, "watch_progress" -> applyProgress(itemId, value, guard, nowMs)
            "video_watched" -> applyVideoWatched(itemId, value, guard, nowMs)
            OutboxKind.CODE_CREATE_CATEGORY -> applyCreateCategory(value, nowMs)
            OutboxKind.CODE_BOOKMARK_CATEGORY -> applyBookmarkCategory(itemId, value, nowMs)
            OutboxKind.CODE_BOOKMARK_ALIAS -> false
            else -> {
                logger.debug(
                    event = "mutation_delta_unknown_type",
                    fields = mapOf("type" to change.type, "item_id" to itemId),
                )
                false
            }
        }
    }

    private suspend fun applyBookmark(itemId: String, value: JsonObject, guard: PreserveLocalGuard, nowMs: Long): Boolean {
        if (guard.bookmarkPending(itemId)) return false
        val action = value.action()
        val bookmarked = value.firstBool("bookmarked")
        val clear = action == "clear" || bookmarked == false
        if (clear) {
            db.bookmarkDao().delete(itemId)
            return true
        }
        val set = action == "set" || bookmarked == true || action == null
        if (!set) return false

        val existing = db.bookmarkDao().getById(itemId)
        db.bookmarkDao().upsert(
            BookmarkEntity(
                videoId = itemId,
                categoryId = value.firstLong("category_id", "CategoryID") ?: existing?.categoryId ?: 0L,
                customTitle = value.presentString("custom_title", "CustomTitle") ?: existing?.customTitle,
                accountHandles = value.presentString("account_handles", "AccountHandles") ?: existing?.accountHandles,
                mediaIndices = value.presentString("media_indices", "MediaIndices") ?: existing?.mediaIndices,
                bookmarkedAt = value.firstLong("bookmarked_at", "BookmarkedAt") ?: existing?.bookmarkedAt ?: nowMs,
            ),
        )
        return true
    }

    private suspend fun applySeen(itemId: String, value: JsonObject, guard: PreserveLocalGuard, nowMs: Long): Boolean {
        val ids = value.stringList("tweet_ids", "TweetIDs").ifEmpty { listOf(itemId) }
            .map(String::trim)
            .filter(String::isNotBlank)
        var applied = false
        for (id in ids) {
            if (guard.seenPending(id)) continue
            db.feedSeenDao().upsert(FeedSeenEntity(id, nowMs))
            applied = true
        }
        return applied
    }

    private suspend fun applyFollow(
        type: String,
        itemId: String,
        value: JsonObject,
        guard: PreserveLocalGuard,
        nowMs: Long,
    ): Boolean {
        if (guard.followPending(itemId)) return false
        val action = value.action()
        val subscribed = value.firstBool("subscribed", "followed")
        val set = type == "subscribe" || action == "set" || subscribed == true
        val clear = type == "unsubscribe" || action == "clear" || subscribed == false
        return when {
            clear -> {
                db.channelFollowDao().delete(itemId)
                true
            }
            set -> {
                db.channelFollowDao().upsert(ChannelFollowEntity(itemId, nowMs))
                true
            }
            else -> false
        }
    }

    private suspend fun applyChannelSetting(itemId: String, value: JsonObject, guard: PreserveLocalGuard, nowMs: Long): Boolean {
        val field = value.firstString("field", "Field") ?: return false
        if (guard.channelSettingPending(itemId, field)) return false
        val updated = (db.channelSettingDao().getById(itemId) ?: ChannelSettingEntity(channelId = itemId))
            .withField(field, value.firstLong("value", "Value"), nowMs)
        db.channelSettingDao().upsert(updated)
        return true
    }

    private suspend fun applyMomentsCursor(itemId: String, value: JsonObject, guard: PreserveLocalGuard): Boolean {
        val scope = PreferencesRepo.Defaults.normalizeMomentsTab(value.firstString("scope", "Scope") ?: "all")
        if (guard.isPending(OutboxKind.CODE_MOMENTS_CURSOR, scope)) return false
        val videoId = value.firstString("video_id", "VideoID") ?: itemId
        if (videoId.isBlank()) return false
        val positionMs = value.firstLong("position_ms", "PositionMs")
        val previousVideoId = prefs.getMomentsResumeVideoId(scope = scope)
        prefs.setMomentsResumeVideoId(videoId, scope = scope)
        if (previousVideoId != videoId || (positionMs != null && positionMs > 0L)) {
            prefs.setMomentsResumePositionMs(positionMs ?: 0L, scope = scope)
        }
        return true
    }

    private suspend fun applyProgress(itemId: String, value: JsonObject, guard: PreserveLocalGuard, nowMs: Long): Boolean {
        if (guard.progressPending(itemId)) return false
        val serverTs = value.firstLong("updated_at_ms", "UpdatedAtMs", "ts") ?: nowMs
        val local = db.watchHistoryDao().getById(itemId)
        if (local != null && local.progressUpdatedAtMs > serverTs) return false
        db.watchHistoryDao().upsert(
            WatchHistoryEntity(
                videoId = itemId,
                playbackPosition = value.firstDouble("position", "playback_position") ?: 0.0,
                duration = value.firstDouble("duration"),
                progressUpdatedAtMs = serverTs,
                progressSource = value.firstString("source"),
                lastWatched = value.firstLong("last_watched") ?: serverTs,
            ),
        )
        return true
    }

    private suspend fun applyBookmarkCategory(itemId: String, value: JsonObject, nowMs: Long): Boolean {
        val categoryId = value.firstLong("category_id", "CategoryID") ?: itemId.toLongOrNull() ?: return false
        if (categoryId <= 0L) return false
        val action = value.action()
        if (action == "clear") {
            db.bookmarkCategoryDao().delete(categoryId)
            db.bookmarkDao().remapCategory(oldId = categoryId, newId = 0L)
            return true
        }

        val existing = db.bookmarkCategoryDao().getById(categoryId)
        val name = value.presentString("name", "Name")?.trim()
            ?: existing?.name
            ?: return false
        if (name.isBlank()) return false
        db.bookmarkCategoryDao().upsert(
            BookmarkCategoryEntity(
                categoryId = categoryId,
                name = name,
                archivePath = value.presentString("archive_path", "ArchivePath") ?: existing?.archivePath,
                createdAt = value.firstLong("created_at", "created_at_ms", "updated_at_ms") ?: existing?.createdAt ?: nowMs,
            ),
        )
        return true
    }

    private suspend fun applyVideoWatched(itemId: String, value: JsonObject, guard: PreserveLocalGuard, nowMs: Long): Boolean {
        if (guard.progressPending(itemId)) return false
        val watched = value.firstBool("watched") ?: true
        if (!watched) {
            db.watchHistoryDao().delete(itemId)
            return true
        }
        val video = db.videoDao().getById(itemId)
        val duration = video?.duration?.toDouble()
        db.watchHistoryDao().upsert(
            WatchHistoryEntity(
                videoId = itemId,
                playbackPosition = duration ?: 0.0,
                duration = duration,
                progressUpdatedAtMs = nowMs,
                progressSource = "web",
                lastWatched = nowMs,
            ),
        )
        return true
    }

    private suspend fun applyCreateCategory(value: JsonObject, nowMs: Long): Boolean {
        val categoryId = value.firstLong("category_id", "CategoryID") ?: return false
        val name = value.firstString("name", "Name") ?: "Category $categoryId"
        db.bookmarkCategoryDao().upsert(
            BookmarkCategoryEntity(
                categoryId = categoryId,
                name = name,
                archivePath = null,
                createdAt = value.firstLong("created_at", "created_at_ms", "updated_at_ms") ?: nowMs,
            ),
        )
        return true
    }

    private suspend fun applyToggle(
        itemId: String,
        value: JsonObject,
        setKeys: List<String>,
        pending: suspend () -> Boolean,
        set: suspend () -> Unit,
        clear: suspend () -> Unit,
    ): Boolean {
        if (pending()) return false
        val action = value.action()
        val flag = setKeys.firstNotNullOfOrNull { value.firstBool(it) }
        return when {
            action == "clear" || flag == false -> {
                clear()
                true
            }
            action == "set" || flag == true || action == null -> {
                set()
                true
            }
            else -> false
        }
    }

    private fun ChannelSettingEntity.withField(name: String, value: Long?, nowMs: Long): ChannelSettingEntity {
        val intVal = value?.toInt()
        return when (name) {
            "media_only" -> copy(mediaOnly = intVal, updatedAt = nowMs)
            "include_reposts" -> copy(includeReposts = intVal, updatedAt = nowMs)
            "media_download_limit" -> copy(mediaDownloadLimit = intVal, updatedAt = nowMs)
            "max_videos" -> copy(maxVideos = intVal, updatedAt = nowMs)
            "download_subtitles" -> copy(downloadSubtitles = intVal, updatedAt = nowMs)
            else -> this
        }
    }

    private fun JsonObject.action(): String? = firstString("action", "Action")?.lowercase()

    private fun JsonObject.firstString(vararg names: String): String? =
        names.firstNotNullOfOrNull { name ->
            (this[name] as? JsonPrimitive)?.let { prim ->
                if (prim.isString) prim.content else prim.contentOrNull
            }?.takeIf(String::isNotEmpty)
        }

    private fun JsonObject.presentString(vararg names: String): String? {
        for (name in names) {
            if (!containsKey(name)) continue
            val prim = this[name] as? JsonPrimitive ?: return null
            return if (prim.isString) prim.content else prim.contentOrNull
        }
        return null
    }

    private fun JsonObject.firstLong(vararg names: String): Long? =
        names.firstNotNullOfOrNull { name -> (this[name] as? JsonPrimitive)?.longOrNull }

    private fun JsonObject.firstDouble(vararg names: String): Double? =
        names.firstNotNullOfOrNull { name -> (this[name] as? JsonPrimitive)?.doubleOrNull }

    private fun JsonObject.firstBool(vararg names: String): Boolean? =
        names.firstNotNullOfOrNull { name ->
            (this[name] as? JsonPrimitive)?.let { prim ->
                prim.booleanOrNull ?: prim.content.lowercase().let {
                    when (it) {
                        "true", "1" -> true
                        "false", "0" -> false
                        else -> null
                    }
                }
            }
        }

    private fun JsonObject.stringList(vararg names: String): List<String> =
        names.firstNotNullOfOrNull { name ->
            val element = this[name] ?: return@firstNotNullOfOrNull null
            when (element) {
                is kotlinx.serialization.json.JsonArray -> element.mapNotNull {
                    (it as? JsonPrimitive)?.contentOrNull
                }
                is JsonPrimitive -> element.contentOrNull?.split(',')?.map(String::trim)
                else -> null
            }
        }.orEmpty()

    companion object {
        const val CURSOR_KEY = "mutations"
        private const val PAGE_SIZE = 500

        private val RANK_AFFECTING_TYPES = setOf(
            OutboxKind.CODE_LIKE,
            OutboxKind.CODE_BOOKMARK,
            OutboxKind.CODE_SEEN,
            OutboxKind.CODE_MUTE,
            OutboxKind.CODE_FOLLOW,
            "subscribe",
            "unsubscribe",
            OutboxKind.CODE_STAR,
            OutboxKind.CODE_CHANNEL_SETTING,
        )
    }
}

data class MutationDeltaResult(
    val applied: Int = 0,
    val rankAffecting: Boolean = false,
)
