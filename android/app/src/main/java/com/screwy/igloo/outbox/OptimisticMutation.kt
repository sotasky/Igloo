package com.screwy.igloo.outbox

import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.entity.BookmarkCategoryEntity
import com.screwy.igloo.data.entity.BookmarkEntity
import com.screwy.igloo.data.entity.ChannelFollowEntity
import com.screwy.igloo.data.entity.ChannelSettingEntity
import com.screwy.igloo.data.entity.ChannelStarEntity
import com.screwy.igloo.data.entity.FeedLikeEntity
import com.screwy.igloo.data.entity.FeedSeenEntity
import com.screwy.igloo.data.entity.MomentViewEntity
import com.screwy.igloo.data.entity.MomentsCursorEntity
import com.screwy.igloo.data.entity.MutedChannelEntity
import com.screwy.igloo.data.entity.OutboxEntity
import com.screwy.igloo.data.entity.WatchHistoryEntity
import com.screwy.igloo.net.iglooJson
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.doubleOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.longOrNull

internal suspend fun applyOptimisticMutation(db: IglooDatabase, row: OutboxEntity) {
    val payload = row.mutationPayload()
    val itemId = row.itemId.orEmpty()
    val updatedAt = payload.long("updated_at_ms") ?: row.createdAtMs
    when (row.kind) {
        OutboxKind.CODE_LIKE ->
            if (payload.text("action") == "set") {
                db.feedLikeDao().upsert(FeedLikeEntity(itemId, updatedAt))
                db.feedSeenDao().upsert(FeedSeenEntity(itemId, updatedAt))
            }
        OutboxKind.CODE_BOOKMARK ->
            if (payload.text("action") == "set") {
                val existing = db.bookmarkDao().getById(itemId)
                db.bookmarkDao()
                    .upsert(
                        BookmarkEntity(
                            videoId = itemId,
                            categoryId = payload.long("category_id") ?: 0,
                            customTitle = payload.text("custom_title"),
                            accountHandles = payload.text("account_handles") ?: existing?.accountHandles,
                            mediaIndices = payload.text("media_indices") ?: existing?.mediaIndices,
                            bookmarkedAt = updatedAt,
                        )
                    )
            }
        OutboxKind.CODE_FOLLOW ->
            if (payload.text("action") == "set") {
                db.channelFollowDao().upsert(ChannelFollowEntity(itemId, updatedAt))
            }
        OutboxKind.CODE_STAR ->
            if (payload.text("action") == "set") {
                db.channelStarDao().upsert(ChannelStarEntity(itemId, updatedAt))
            }
        OutboxKind.CODE_MUTE ->
            if (payload.text("action") == "set") {
                db.mutedChannelDao().upsert(MutedChannelEntity(itemId, updatedAt))
            } else if (payload.text("action") == "clear") {
                db.mutedChannelDao().delete(itemId)
            }
        OutboxKind.CODE_CHANNEL_SETTING -> {
            val field = row.field ?: payload.text("field").orEmpty()
            val existing = db.channelSettingDao().getById(itemId) ?: ChannelSettingEntity(itemId)
            db.channelSettingDao().upsert(existing.withField(field, payload.long("value"), updatedAt))
        }
        OutboxKind.CODE_SEEN -> db.feedSeenDao().upsert(FeedSeenEntity(itemId, updatedAt))
        OutboxKind.CODE_MOMENT_VIEW ->
            db.momentViewDao().upsert(MomentViewEntity(itemId, updatedAt))
        OutboxKind.CODE_PROGRESS ->
            db.watchHistoryDao()
                .upsert(
                    WatchHistoryEntity(
                        videoId = itemId,
                        playbackPosition = payload.double("position") ?: 0.0,
                        duration = payload.double("duration"),
                        updatedAtMs = updatedAt,
                    )
                )
        OutboxKind.CODE_MOMENTS_CURSOR ->
            db.momentsCursorDao()
                .upsert(
                    MomentsCursorEntity(
                        scope = itemId,
                        videoId = payload.text("video_id").orEmpty(),
                        positionMs = payload.long("position_ms") ?: 0,
                        sortAtMs = payload.long("sort_at_ms") ?: 0,
                        updatedAtMs = updatedAt,
                    )
                )
        OutboxKind.CODE_CREATE_CATEGORY -> {
            val provisionalId = payload.long("provisional_id") ?: itemId.toLongOrNull() ?: return
            db.bookmarkCategoryDao()
                .upsert(
                    BookmarkCategoryEntity(
                        categoryId = provisionalId,
                        name = payload.text("name").orEmpty(),
                        createdAt = updatedAt,
                    )
                )
        }
        OutboxKind.CODE_LOG,
        OutboxKind.CODE_LOG_DEBUG -> Unit
    }
}

private fun ChannelSettingEntity.withField(
    name: String,
    value: Long?,
    updatedAt: Long,
): ChannelSettingEntity {
    val intValue = value?.toInt()
    return when (name) {
        "media_only" -> copy(mediaOnly = intValue, updatedAt = updatedAt)
        "include_reposts" -> copy(includeReposts = intValue, updatedAt = updatedAt)
        "media_download_limit" -> copy(mediaDownloadLimit = intValue, updatedAt = updatedAt)
        "max_videos" -> copy(maxVideos = intValue, updatedAt = updatedAt)
        "download_subtitles" -> copy(downloadSubtitles = intValue, updatedAt = updatedAt)
        else -> this
    }
}

private fun OutboxEntity.mutationPayload(): JsonObject =
    runCatching { iglooJson.parseToJsonElement(payloadJson).jsonObject }
        .getOrDefault(JsonObject(emptyMap()))

private fun JsonObject.text(key: String): String? =
    (this[key] as? JsonPrimitive)?.contentOrNull

private fun JsonObject.long(key: String): Long? =
    (this[key] as? JsonPrimitive)?.longOrNull

private fun JsonObject.double(key: String): Double? =
    (this[key] as? JsonPrimitive)?.doubleOrNull
