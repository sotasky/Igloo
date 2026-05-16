package com.screwy.igloo.sync

import androidx.room.withTransaction
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.entity.BookmarkCategoryEntity
import com.screwy.igloo.data.entity.BookmarkEntity
import com.screwy.igloo.data.entity.BookmarkLabelEntity
import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.ChannelFollowEntity
import com.screwy.igloo.data.entity.ChannelProfileEntity
import com.screwy.igloo.data.entity.ChannelSettingEntity
import com.screwy.igloo.data.entity.ChannelStarEntity
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.data.entity.FeedLikeEntity
import com.screwy.igloo.data.entity.FeedRankEntity
import com.screwy.igloo.data.entity.FeedSeenEntity
import com.screwy.igloo.data.entity.FeedThreadContextEntity
import com.screwy.igloo.data.entity.MomentViewEntity
import com.screwy.igloo.data.entity.MutedAccountEntity
import com.screwy.igloo.data.entity.RetweetSourceEntity
import com.screwy.igloo.data.entity.SponsorBlockCheckedEntity
import com.screwy.igloo.data.entity.SponsorBlockSegmentEntity
import com.screwy.igloo.data.entity.VideoCommentEntity
import com.screwy.igloo.data.entity.VideoEntity
import com.screwy.igloo.data.entity.VideoRepostSourceEntity
import com.screwy.igloo.data.entity.WatchHistoryEntity
import com.screwy.igloo.net.BundleEnvelope
import kotlinx.serialization.ExperimentalSerializationApi
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNamingStrategy
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.put

/**
 * Generic bundle ingester. Each bundle uses one transaction to upsert the
 * primary row, iterate attachments, apply user-state side tables with
 * preserve-local, and LWW-merge watch_history.
 *
 * Per-platform differences live in bundle contents (attachments map), not in code. A
 * TikTok `videos` bundle with zero attachments and a YouTube `videos` bundle with
 * `video_comments` + `sponsorblock_*` both flow through the same primary-upsert branch;
 * attachments iteration then handles YouTube's extra tables.
 *
 * Deserialization uses a dedicated `Json` with snake-case naming so Room entities
 * with camelCase properties deserialize from the server's snake_case JSON without a
 * DTO shadow layer. Room entities should mirror server schema columns directly where
 * the Android contract requires it.
 */
private const val FEED_RANK_MAX_ROWS = 5_000

class BundleIngest(
    private val db: IglooDatabase,
    private val nowMsProvider: () -> Long = { System.currentTimeMillis() },
) {

    @OptIn(ExperimentalSerializationApi::class)
    private val bundleJson: Json = Json {
        ignoreUnknownKeys = true
        coerceInputValues = true
        isLenient = true
        explicitNulls = false
        namingStrategy = JsonNamingStrategy.SnakeCase
    }

    /**
     * Ingest one bundle. Runs inside a Room transaction so observers see primary +
     * attachments + side-table updates atomically. On deserialization failure the
     * bundle is skipped.
     */
    suspend fun ingest(bundle: BundleEnvelope, guard: PreserveLocalGuard): IngestResult {
        return try {
            db.withTransaction {
                val primaryVideoId = bundle.primary["video_id"]?.jsonPrimitive?.contentOrNull
                val primaryTweetId = bundle.primary["tweet_id"]?.jsonPrimitive?.contentOrNull
                when (bundle.primary_kind) {
                    "feed_items" -> ingestFeedItem(bundle, guard)
                    "videos"     -> ingestVideo(bundle, guard)
                    "channels"   -> ingestChannel(bundle, guard)
                    "channel_profiles" -> ingestChannelProfile(bundle)
                    "feed_rank"  -> ingestFeedRank(bundle)
                    "bookmark_metadata" -> ingestBookmarkMetadata(bundle)
                    else         -> return@withTransaction IngestResult.UnknownKind(bundle.primary_kind)
                }
                ingestAttachments(bundle.attachments, primaryVideoId, primaryTweetId, guard)
                IngestResult.Ok
            }
        } catch (e: Exception) {
            IngestResult.ParseFailure(bundle.primary_kind, e)
        }
    }

    // ─── Per-primary-kind branches ─────────────────────────────────────────────

    private suspend fun ingestFeedItem(bundle: BundleEnvelope, guard: PreserveLocalGuard) {
        val row = bundleJson.decodeFromJsonElement(FeedItemEntity.serializer(), bundle.primary)
        db.feedItemDao().upsert(row)
    }

    private suspend fun ingestVideo(bundle: BundleEnvelope, guard: PreserveLocalGuard) {
        val row = bundleJson.decodeFromJsonElement(VideoEntity.serializer(), bundle.primary)
        db.videoDao().upsert(row)
    }

    private suspend fun ingestChannel(bundle: BundleEnvelope, guard: PreserveLocalGuard) {
        val row = bundleJson.decodeFromJsonElement(ChannelEntity.serializer(), bundle.primary)
        db.channelDao().upsert(row)
    }

    private suspend fun ingestChannelProfile(bundle: BundleEnvelope) {
        val row = bundleJson.decodeFromJsonElement(ChannelProfileEntity.serializer(), bundle.primary)
        db.channelProfileDao().upsert(row)
    }

    private suspend fun ingestFeedRank(bundle: BundleEnvelope) {
        val payload = bundleJson.decodeFromJsonElement(FeedRankPayload.serializer(), bundle.primary)
        if (payload.snapshotAt <= 0) return
        val currentSnapshotAt = db.feedRankDao().currentSnapshotAt()
        if (currentSnapshotAt > payload.snapshotAt) return

        val boundedRows = payload.rows.take(FEED_RANK_MAX_ROWS)
        if (payload.rowCount != payload.rows.size || payload.rowCount > FEED_RANK_MAX_ROWS || payload.rows.size > FEED_RANK_MAX_ROWS) return

        db.feedRankDao().deleteAll()
        val rows = boundedRows.mapNotNull { row ->
            val tweetId = row.tweetId.trim()
            val position = row.rankPosition
            if (tweetId.isBlank() || position <= 0) {
                null
            } else {
                FeedRankEntity(
                    tweetId = tweetId,
                    rankPosition = position,
                    snapshotAt = payload.snapshotAt,
                )
            }
        }
        if (rows.isNotEmpty()) {
            db.feedRankDao().upsert(rows)
        }
    }

    private suspend fun ingestBookmarkMetadata(bundle: BundleEnvelope) {
        val payload = bundleJson.decodeFromJsonElement(BookmarkMetadataPayload.serializer(), bundle.primary)
        val remoteCategories = payload.categories.mapNotNull { row ->
            val name = row.name.trim()
            if (row.categoryId <= 0L || name.isBlank()) {
                null
            } else {
                BookmarkCategoryEntity(
                    categoryId = row.categoryId,
                    name = name,
                    archivePath = row.archivePath?.takeIf { it.isNotBlank() },
                    createdAt = row.createdAt,
                )
            }
        }
        val remoteNames = remoteCategories.map { it.name.trim().lowercase() }.toSet()
        val staleProvisionalIds = db.bookmarkCategoryDao()
            .all()
            .filter { it.categoryId < 0L }
            .filter { it.name.trim().lowercase() in remoteNames }
            .map { it.categoryId }

        db.bookmarkCategoryDao().deletePositive()
        for (categoryId in staleProvisionalIds) {
            db.bookmarkCategoryDao().delete(categoryId)
        }
        if (remoteCategories.isNotEmpty()) {
            db.bookmarkCategoryDao().upsert(remoteCategories)
        }

        val syncedAt = payload.snapshotAt.takeIf { it > 0L } ?: nowMsProvider()
        val labels = payload.labels
            .asSequence()
            .map { it.label.trim() }
            .filter { it.isNotEmpty() }
            .distinct()
            .sortedBy { it.lowercase() }
            .map { BookmarkLabelEntity(label = it, syncedAt = syncedAt) }
            .toList()
        db.bookmarkLabelDao().deleteAll()
        if (labels.isNotEmpty()) {
            db.bookmarkLabelDao().upsert(labels)
        }
    }

    // ─── Shared helpers ────────────────────────────────────────────────────────

    /**
     * Apply bookmark delta row. Content deltas often omit bookmark metadata fields,
     * so preserve any existing local values unless the payload explicitly carries a
     * replacement.
     */
    private suspend fun applyBookmarkState(row: BookmarkStatePayload, fallbackMs: Long, guard: PreserveLocalGuard) {
        val videoId = row.videoId.trim()
        if (videoId.isBlank()) return
        if (guard.bookmarkPending(videoId)) return
        if (!row.bookmarked) {
            db.bookmarkDao().delete(videoId)
            return
        }
        val existing = db.bookmarkDao().getById(videoId)
        db.bookmarkDao().upsert(BookmarkEntity(
            videoId = videoId,
            categoryId = row.categoryId ?: existing?.categoryId ?: 0L,
            customTitle = row.customTitle ?: existing?.customTitle,
            accountHandles = row.accountHandles ?: existing?.accountHandles,
            mediaIndices = row.mediaIndices ?: existing?.mediaIndices,
            bookmarkedAt = row.bookmarkedAt ?: existing?.bookmarkedAt ?: fallbackMs,
        ))
    }

    private suspend fun mergeWatchHistory(row: WatchHistoryStatePayload, guard: PreserveLocalGuard) {
        val videoId = row.videoId.trim()
        if (videoId.isBlank()) return
        val serverTs = row.progressUpdatedAtMs
        if (serverTs <= 0) return
        if (guard.progressPending(videoId)) return

        val local = db.watchHistoryDao().getById(videoId)
        if (local != null && local.progressUpdatedAtMs >= serverTs) return

        db.watchHistoryDao().upsert(WatchHistoryEntity(
            videoId = videoId,
            playbackPosition = row.playbackPosition,
            duration = row.duration,
            progressUpdatedAtMs = serverTs,
            progressSource = row.progressSource,
            lastWatched = row.lastWatched ?: serverTs,
        ))
    }

    private suspend fun ingestAttachments(
        attachments: JsonObject?,
        primaryVideoId: String?,
        primaryTweetId: String?,
        guard: PreserveLocalGuard,
    ) {
        if (attachments == null) return
        for ((table, element) in attachments) {
            when (table) {
                "user_state"           -> applyUserState(element, guard)
                "video_comments"        -> decodeRows<VideoCommentEntity>(element, VideoCommentEntity.serializer())
                    .takeIf { it.isNotEmpty() }?.let { db.videoCommentDao().upsert(it) }
                "retweet_sources"       -> decodeRows<RetweetSourceEntity>(element, RetweetSourceEntity.serializer())
                    .takeIf { it.isNotEmpty() }?.let { db.retweetSourceDao().upsert(it) }
                "feed_thread_context"    -> primaryTweetId?.takeIf { it.isNotBlank() }?.let { tweetId ->
                    db.feedThreadContextDao().replaceForLeaf(
                        tweetId,
                        decodeRows<FeedThreadContextEntity>(element, FeedThreadContextEntity.serializer()),
                    )
                }
                "video_repost_sources"  -> primaryVideoId?.takeIf { it.isNotBlank() }?.let { videoId ->
                    db.videoRepostSourceDao().replaceForVideo(
                        videoId,
                        decodeRows<VideoRepostSourceEntity>(element, VideoRepostSourceEntity.serializer()),
                    )
                }
                "sponsorblock_segments" -> decodeSponsorBlockSegments(element, primaryVideoId)
                    .takeIf { it.isNotEmpty() }?.let { db.sponsorBlockSegmentDao().upsert(it) }
                "sponsorblock_checked"  -> decodeMarker<SponsorBlockCheckedEntity>(element, SponsorBlockCheckedEntity.serializer())
                    ?.let { db.sponsorBlockCheckedDao().upsert(it) }
                "channel_profile"       -> decodeMarker<ChannelProfileEntity>(element, ChannelProfileEntity.serializer())
                    ?.let { db.channelProfileDao().upsert(it) }
                "channel_settings"      -> decodeMarker<ChannelSettingEntity>(element, ChannelSettingEntity.serializer())
                    ?.let { db.channelSettingDao().upsert(it) }
                // Unknown table names are logged and skipped.
                else -> { /* no-op; server added a new attachment kind the client doesn't know yet */ }
            }
        }
    }

    private suspend fun applyUserState(element: JsonElement, guard: PreserveLocalGuard) {
        val state = runCatching {
            bundleJson.decodeFromJsonElement(UserStatePayload.serializer(), element)
        }.getOrNull() ?: return
        val nowMs = nowMsProvider()
        for (row in state.feedLikes) {
            val tweetId = row.tweetId.trim()
            if (tweetId.isBlank() || guard.likePending(tweetId)) continue
            if (row.liked) db.feedLikeDao().upsert(FeedLikeEntity(tweetId = tweetId, likedAt = row.likedAt ?: nowMs))
            else db.feedLikeDao().delete(tweetId)
        }
        for (row in state.feedSeen) {
            val tweetId = row.tweetId.trim()
            if (tweetId.isBlank() || guard.seenPending(tweetId) || !row.seen) continue
            db.feedSeenDao().upsert(FeedSeenEntity(tweetId = tweetId, seenAt = row.seenAt ?: nowMs))
        }
        for (row in state.bookmarks) {
            applyBookmarkState(row, nowMs, guard)
        }
        for (row in state.channelFollows) {
            val channelId = row.channelId.trim()
            if (channelId.isBlank() || guard.followPending(channelId)) continue
            if (row.followed) db.channelFollowDao().upsert(ChannelFollowEntity(channelId = channelId, followedAt = row.followedAt ?: nowMs))
            else db.channelFollowDao().delete(channelId)
        }
        for (row in state.channelStars) {
            val channelId = row.channelId.trim()
            if (channelId.isBlank() || guard.starPending(channelId)) continue
            if (row.starred) db.channelStarDao().upsert(ChannelStarEntity(channelId = channelId, starredAt = row.starredAt ?: nowMs))
            else db.channelStarDao().delete(channelId)
        }
        for (row in state.mutedAccounts) {
            val handle = row.handle.trim()
            if (handle.isBlank() || guard.mutePending(handle)) continue
            if (row.muted) db.mutedAccountDao().upsert(MutedAccountEntity(handle = handle, mutedAt = row.mutedAt ?: nowMs))
            else db.mutedAccountDao().delete(handle)
        }
        for (row in state.momentViews) {
            val videoId = row.videoId.trim()
            if (videoId.isBlank() || guard.momentViewPending(videoId) || !row.viewed) continue
            db.momentViewDao().upsert(MomentViewEntity(videoId = videoId, viewedAt = row.viewedAt ?: nowMs))
        }
        for (row in state.watchHistory) {
            mergeWatchHistory(row, guard)
        }
    }

    private fun <T> decodeRows(element: kotlinx.serialization.json.JsonElement, serializer: kotlinx.serialization.KSerializer<T>): List<T> {
        val array = element as? JsonArray ?: return emptyList()
        return array.mapNotNull {
            runCatching { bundleJson.decodeFromJsonElement(serializer, it) }.getOrNull()
        }
    }

    private fun decodeSponsorBlockSegments(
        element: JsonElement,
        primaryVideoId: String?,
    ): List<SponsorBlockSegmentEntity> {
        val videoId = primaryVideoId?.takeIf { it.isNotBlank() } ?: return emptyList()
        val array = element as? JsonArray ?: return emptyList()
        return array.mapNotNull { row ->
            val obj = row as? JsonObject ?: return@mapNotNull null
            val normalized = buildJsonObject {
                put("video_id", videoId)
                for ((key, value) in obj) put(key, value)
            }
            runCatching {
                bundleJson.decodeFromJsonElement(SponsorBlockSegmentEntity.serializer(), normalized)
            }.getOrNull()
        }
    }

    private fun <T> decodeMarker(element: kotlinx.serialization.json.JsonElement, serializer: kotlinx.serialization.KSerializer<T>): T? {
        // Marker-shaped attachments (`sponsorblock_checked`, `channel_settings`) arrive either
        // as a single object or a single-element array. Tolerate both.
        return when (element) {
            is JsonObject -> runCatching { bundleJson.decodeFromJsonElement(serializer, element) }.getOrNull()
            is JsonArray  -> element.firstOrNull()?.let {
                runCatching { bundleJson.decodeFromJsonElement(serializer, it) }.getOrNull()
            }
            else -> null
        }
    }

}

/** Result of one bundle ingest. `ParseFailure` is a caller signal, not an abort. */
sealed interface IngestResult {
    data object Ok : IngestResult
    data class UnknownKind(val kind: String) : IngestResult
    data class ParseFailure(val kind: String, val cause: Throwable) : IngestResult
}

@kotlinx.serialization.Serializable
private data class FeedRankPayload(
    val snapshotAt: Long = 0,
    val rowCount: Int = 0,
    val rows: List<FeedRankRowPayload> = emptyList(),
)

@kotlinx.serialization.Serializable
private data class FeedRankRowPayload(
    val tweetId: String,
    val rankPosition: Int,
)

@kotlinx.serialization.Serializable
private data class BookmarkMetadataPayload(
    val version: Int = 1,
    val snapshotAt: Long = 0,
    val categories: List<BookmarkMetadataCategoryPayload> = emptyList(),
    val labels: List<BookmarkMetadataLabelPayload> = emptyList(),
)

@kotlinx.serialization.Serializable
private data class BookmarkMetadataCategoryPayload(
    val categoryId: Long,
    val name: String,
    val archivePath: String? = null,
    val createdAt: Long = 0,
)

@kotlinx.serialization.Serializable
private data class BookmarkMetadataLabelPayload(
    val label: String,
)

@kotlinx.serialization.Serializable
private data class UserStatePayload(
    val version: Int = 1,
    val feedLikes: List<FeedLikeStatePayload> = emptyList(),
    val feedSeen: List<FeedSeenStatePayload> = emptyList(),
    val bookmarks: List<BookmarkStatePayload> = emptyList(),
    val channelFollows: List<ChannelFollowStatePayload> = emptyList(),
    val channelStars: List<ChannelStarStatePayload> = emptyList(),
    val mutedAccounts: List<MutedAccountStatePayload> = emptyList(),
    val momentViews: List<MomentViewStatePayload> = emptyList(),
    val watchHistory: List<WatchHistoryStatePayload> = emptyList(),
)

@kotlinx.serialization.Serializable
private data class FeedLikeStatePayload(
    val tweetId: String,
    val liked: Boolean,
    val likedAt: Long? = null,
)

@kotlinx.serialization.Serializable
private data class FeedSeenStatePayload(
    val tweetId: String,
    val seen: Boolean = true,
    val seenAt: Long? = null,
)

@kotlinx.serialization.Serializable
private data class BookmarkStatePayload(
    val videoId: String,
    val bookmarked: Boolean,
    val categoryId: Long? = null,
    val customTitle: String? = null,
    val accountHandles: String? = null,
    val mediaIndices: String? = null,
    val bookmarkedAt: Long? = null,
)

@kotlinx.serialization.Serializable
private data class ChannelFollowStatePayload(
    val channelId: String,
    val followed: Boolean,
    val followedAt: Long? = null,
)

@kotlinx.serialization.Serializable
private data class ChannelStarStatePayload(
    val channelId: String,
    val starred: Boolean,
    val starredAt: Long? = null,
)

@kotlinx.serialization.Serializable
private data class MutedAccountStatePayload(
    val handle: String,
    val muted: Boolean,
    val mutedAt: Long? = null,
)

@kotlinx.serialization.Serializable
private data class MomentViewStatePayload(
    val videoId: String,
    val viewed: Boolean = true,
    val viewedAt: Long? = null,
)

@kotlinx.serialization.Serializable
private data class WatchHistoryStatePayload(
    val videoId: String,
    val playbackPosition: Double = 0.0,
    val duration: Double? = null,
    val progressUpdatedAtMs: Long = 0,
    val progressSource: String? = null,
    val lastWatched: Long? = null,
)
