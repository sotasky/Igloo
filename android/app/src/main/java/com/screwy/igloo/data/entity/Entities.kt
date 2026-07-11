package com.screwy.igloo.data.entity

import androidx.room.ColumnInfo
import androidx.room.Entity
import androidx.room.Index
import androidx.room.PrimaryKey
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * Room entities that mirror the server schema in snake_case. Every @ColumnInfo
 * carries an explicit name so cross-table greps stay honest and Kotlin property
 * renames cannot drift the schema.
 *
 * Room can't express partial indexes with WHERE clauses. The full-column indexes
 * here cover the query shapes they would service; revisit only if profiling says
 * the partial form matters.
 */

// ─── Server-mirrored core tables ──────────────────────────────────────────────────────

@Serializable
@Entity(
    tableName = "feed_items",
    indices = [
        Index(value = ["published_at"], orders = [Index.Order.DESC], name = "idx_feed_items_published"),
        Index(value = ["reply_to_status"], name = "idx_feed_items_reply_parent"),
        Index(
            value = ["channel_id", "published_at"],
            orders = [Index.Order.ASC, Index.Order.DESC],
            name = "idx_feed_items_channel",
        ),
        Index(value = ["quote_tweet_id"], name = "idx_feed_items_quote"),
        Index(value = ["content_hash"], name = "idx_feed_items_content_hash"),
    ],
)
data class FeedItemEntity(
    @PrimaryKey @ColumnInfo(name = "tweet_id") val tweetId: String,
    @ColumnInfo(name = "source_channel_id") val sourceChannelId: String? = null,
    @ColumnInfo(name = "body_text") val bodyText: String? = null,
    @ColumnInfo(name = "lang") val lang: String? = null,
    @ColumnInfo(name = "is_retweet") val isRetweet: Boolean = false,
    @ColumnInfo(name = "reposter_channel_id") val reposterChannelId: String? = null,

    @ColumnInfo(name = "quote_tweet_id") val quoteTweetId: String? = null,
    @ColumnInfo(name = "quote_channel_id") val quoteChannelId: String? = null,
    @ColumnInfo(name = "quote_body_text") val quoteBodyText: String? = null,
    @ColumnInfo(name = "quote_lang") val quoteLang: String? = null,
    @ColumnInfo(name = "quote_media_json") val quoteMediaJson: String? = null,
    @ColumnInfo(name = "quote_published_at") val quotePublishedAt: Long = 0,
    @ColumnInfo(name = "quote_canonical_url") val quoteCanonicalUrl: String? = null,

    @ColumnInfo(name = "media_json") val mediaJson: String? = null,

    @ColumnInfo(name = "views") val views: Long? = null,
    @ColumnInfo(name = "likes") val likes: Long? = null,
    @ColumnInfo(name = "retweets") val retweets: Long? = null,

    @ColumnInfo(name = "canonical_url") val canonicalUrl: String? = null,
    @ColumnInfo(name = "canonical_tweet_id") val canonicalTweetId: String? = null,
    @ColumnInfo(name = "reply_channel_id") val replyChannelId: String? = null,
    @ColumnInfo(name = "reply_to_status") val replyToStatus: String? = null,
    @ColumnInfo(name = "is_reply") val isReply: Boolean = false,
    @ColumnInfo(name = "is_ghost") val isGhost: Boolean = false,
    @ColumnInfo(name = "content_hash") val contentHash: String? = null,

    @ColumnInfo(name = "body_translation") val bodyTranslation: String? = null,
    @ColumnInfo(name = "body_source_lang") val bodySourceLang: String? = null,
    @ColumnInfo(name = "quote_translation") val quoteTranslation: String? = null,
    @ColumnInfo(name = "quote_source_lang") val quoteSourceLang: String? = null,

    @ColumnInfo(name = "published_at") val publishedAt: Long = 0,
    @ColumnInfo(name = "channel_id") val channelId: String? = null,
)

@Serializable
@Entity(
    tableName = "videos",
    indices = [
        Index(
            value = ["channel_id", "published_at"],
            orders = [Index.Order.ASC, Index.Order.DESC],
            name = "idx_videos_channel_published",
        ),
        Index(
            value = ["source_kind", "published_at"],
            orders = [Index.Order.ASC, Index.Order.DESC],
            name = "idx_videos_source_kind",
        ),
    ],
)
data class VideoEntity(
    @PrimaryKey @ColumnInfo(name = "video_id") val videoId: String,
    @ColumnInfo(name = "channel_id") val channelId: String,
    @SerialName("owner_kind")
    @ColumnInfo(name = "owner_kind") val ownerKind: String,
    @ColumnInfo(name = "title") val title: String? = null,
    @ColumnInfo(name = "description") val description: String? = null,
    @ColumnInfo(name = "duration") val duration: Long? = null,
    @ColumnInfo(name = "published_at") val publishedAt: Long = 0,
    @ColumnInfo(name = "media_kind") val mediaKind: String? = null,
    @ColumnInfo(name = "slide_count") val slideCount: Int = 0,
    @ColumnInfo(name = "source_kind") val sourceKind: String? = null,
    @ColumnInfo(name = "metadata_json") val metadataJson: String? = null,
    @ColumnInfo(name = "canonical_url") val canonicalUrl: String? = null,
    @ColumnInfo(name = "dearrow_title") val dearrowTitle: String? = null,
    @ColumnInfo(name = "dearrow_title_casual") val dearrowTitleCasual: String? = null,
)

@Serializable
@Entity(
    tableName = "channels",
    indices = [
        Index(value = ["platform"], name = "idx_channels_platform"),
    ],
)
data class ChannelEntity(
    @PrimaryKey @ColumnInfo(name = "channel_id") val channelId: String,
    @ColumnInfo(name = "source_id") val sourceId: String? = null,
    @ColumnInfo(name = "name") val name: String,
    @ColumnInfo(name = "url") val url: String? = null,
    @ColumnInfo(name = "platform") val platform: String,
)

@Serializable
@Entity(tableName = "channel_profiles")
data class ChannelProfileEntity(
    @PrimaryKey @ColumnInfo(name = "channel_id") val channelId: String,
    @ColumnInfo(name = "platform") val platform: String,
    @ColumnInfo(name = "handle") val handle: String? = null,
    @ColumnInfo(name = "display_name") val displayName: String? = null,
    @ColumnInfo(name = "bio") val bio: String? = null,
    @ColumnInfo(name = "website") val website: String? = null,
    @ColumnInfo(name = "followers") val followers: Int = 0,
    @ColumnInfo(name = "following") val following: Int = 0,
    @ColumnInfo(name = "verified") val verified: Boolean = false,
    @ColumnInfo(name = "verified_type") val verifiedType: String? = null,
    @SerialName("protected")
    @ColumnInfo(name = "protected") val isProtected: Boolean = false,
)

@Serializable
@Entity(
    tableName = "video_comments",
    primaryKeys = ["video_id", "comment_id"],
    indices = [
        Index(
            value = ["video_id", "published_at"],
            orders = [Index.Order.ASC, Index.Order.DESC],
            name = "idx_video_comments_video_published",
        ),
        Index(value = ["author_id"], name = "idx_video_comments_author"),
    ],
)
data class VideoCommentEntity(
    @ColumnInfo(name = "video_id") val videoId: String,
    @SerialName("id")
    @ColumnInfo(name = "comment_id") val commentId: String,
    @SerialName("parent")
    @ColumnInfo(name = "parent_id") val parentId: String? = null,
    @SerialName("author")
    @ColumnInfo(name = "author_name") val authorName: String? = null,
    @ColumnInfo(name = "author_id") val authorId: String? = null,
    @ColumnInfo(name = "text") val text: String? = null,
    @ColumnInfo(name = "like_count") val likeCount: Long? = null,
    @ColumnInfo(name = "published_at") val publishedAt: Long = 0,
)

@Serializable
@Entity(
    tableName = "retweet_sources",
    primaryKeys = ["content_hash", "retweeter_channel_id"],
    indices = [
        Index(value = ["tweet_id"], name = "idx_retweet_sources_tweet"),
    ],
)
data class RetweetSourceEntity(
    @ColumnInfo(name = "content_hash") val contentHash: String,
    @ColumnInfo(name = "retweeter_channel_id") val retweeterChannelId: String,
    @ColumnInfo(name = "tweet_id") val tweetId: String,
    @ColumnInfo(name = "published_at") val publishedAt: Long = 0,
)

@Serializable
@Entity(
    tableName = "video_repost_sources",
    primaryKeys = ["video_id", "reposter_channel_id"],
    indices = [
        Index(value = ["video_id"], name = "idx_video_repost_sources_video"),
        Index(value = ["reposter_channel_id"], name = "idx_video_repost_sources_reposter"),
        Index(
            value = ["reposted_at_ms", "first_seen_at_ms"],
            orders = [Index.Order.DESC, Index.Order.DESC],
            name = "idx_video_repost_sources_time",
        ),
    ],
)
data class VideoRepostSourceEntity(
    @ColumnInfo(name = "video_id") val videoId: String,
    @ColumnInfo(name = "reposter_channel_id") val reposterChannelId: String,
    @ColumnInfo(name = "reposted_at_ms") val repostedAtMs: Long = 0,
    @ColumnInfo(name = "first_seen_at_ms") val firstSeenAtMs: Long = 0,
    @ColumnInfo(name = "updated_at_ms") val updatedAtMs: Long = 0,
)

@Serializable
@Entity(
    tableName = "sponsorblock_segments",
    primaryKeys = ["video_id", "start_time"],
)
data class SponsorBlockSegmentEntity(
    @ColumnInfo(name = "video_id") val videoId: String,
    @SerialName("start")
    @ColumnInfo(name = "start_time") val startTime: Double,
    @SerialName("end")
    @ColumnInfo(name = "end_time") val endTime: Double,
    @ColumnInfo(name = "category") val category: String,
)

@Serializable
@Entity(tableName = "sponsorblock_checked")
data class SponsorBlockCheckedEntity(
    @PrimaryKey @ColumnInfo(name = "video_id") val videoId: String,
    @SerialName("checked_at_ms")
    @ColumnInfo(name = "checked_at") val checkedAt: Long = 0,
    @ColumnInfo(name = "video_age_at_check") val videoAgeAtCheck: String? = null,
)

// ─── User-state side tables (server-mirrored) ─────────────────────────────────────────

@Serializable
@Entity(tableName = "feed_likes")
data class FeedLikeEntity(
    @PrimaryKey @ColumnInfo(name = "tweet_id") val tweetId: String,
    @ColumnInfo(name = "liked_at") val likedAt: Long = 0,
)

@Serializable
@Entity(
    tableName = "bookmarks",
    indices = [
        Index(value = ["bookmarked_at"], orders = [Index.Order.DESC], name = "idx_bookmarks_date"),
        Index(value = ["category_id"], name = "idx_bookmarks_cat"),
    ],
)
data class BookmarkEntity(
    @PrimaryKey @ColumnInfo(name = "video_id") val videoId: String,
    @ColumnInfo(name = "category_id") val categoryId: Long = 0,
    @ColumnInfo(name = "custom_title") val customTitle: String? = null,
    @ColumnInfo(name = "account_handles") val accountHandles: String? = null,
    @ColumnInfo(name = "media_indices") val mediaIndices: String? = null,
    @ColumnInfo(name = "bookmarked_at") val bookmarkedAt: Long = 0,
)

@Serializable
@Entity(tableName = "bookmark_categories")
data class BookmarkCategoryEntity(
    @PrimaryKey @ColumnInfo(name = "category_id") val categoryId: Long,
    @ColumnInfo(name = "name") val name: String,
    @ColumnInfo(name = "archive_path") val archivePath: String? = null,
    @ColumnInfo(name = "created_at") val createdAt: Long = 0,
)

@Serializable
@Entity(tableName = "feed_seen")
data class FeedSeenEntity(
    @PrimaryKey @ColumnInfo(name = "tweet_id") val tweetId: String,
    @ColumnInfo(name = "seen_at") val seenAt: Long = 0,
)

@Serializable
@Entity(tableName = "moment_views")
data class MomentViewEntity(
    @PrimaryKey @ColumnInfo(name = "video_id") val videoId: String,
    @ColumnInfo(name = "viewed_at") val viewedAt: Long = 0,
)

@Serializable
@Entity(tableName = "watch_history")
data class WatchHistoryEntity(
    @PrimaryKey @ColumnInfo(name = "video_id") val videoId: String,
    @ColumnInfo(name = "playback_position") val playbackPosition: Double = 0.0,
    @ColumnInfo(name = "duration") val duration: Double? = null,
    @ColumnInfo(name = "updated_at_ms") val updatedAtMs: Long = 0,
)

@Serializable
@Entity(tableName = "moments_cursors")
data class MomentsCursorEntity(
    @PrimaryKey @ColumnInfo(name = "scope") val scope: String,
    @ColumnInfo(name = "video_id") val videoId: String,
    @ColumnInfo(name = "position_ms") val positionMs: Long = 0,
    @ColumnInfo(name = "sort_at_ms") val sortAtMs: Long = 0,
    @ColumnInfo(name = "updated_at_ms") val updatedAtMs: Long = 0,
)

@Serializable
@Entity(tableName = "muted_channels")
data class MutedChannelEntity(
    @PrimaryKey @ColumnInfo(name = "channel_id") val channelId: String,
    @ColumnInfo(name = "muted_at") val mutedAt: Long = 0,
)

@Serializable
@Entity(tableName = "channel_follows")
data class ChannelFollowEntity(
    @PrimaryKey @ColumnInfo(name = "channel_id") val channelId: String,
    @ColumnInfo(name = "followed_at") val followedAt: Long = 0,
)

@Serializable
@Entity(tableName = "channel_stars")
data class ChannelStarEntity(
    @PrimaryKey @ColumnInfo(name = "channel_id") val channelId: String,
    @ColumnInfo(name = "starred_at") val starredAt: Long = 0,
)

@Serializable
@Entity(
    tableName = "feed_rank",
    indices = [
        Index(value = ["rank_position", "tweet_id"], name = "idx_feed_rank_position"),
    ],
)
data class FeedRankEntity(
    @PrimaryKey @ColumnInfo(name = "tweet_id") val tweetId: String,
    @ColumnInfo(name = "rank_position") val rankPosition: Int,
    @ColumnInfo(name = "snapshot_at") val snapshotAt: Long = 0,
)

@Entity(
    tableName = "outbox",
    indices = [
        Index(value = ["state", "next_attempt_at_ms"], name = "idx_outbox_claim"),
        Index(value = ["kind", "field", "item_id"], name = "idx_outbox_coalesce"),
    ],
)
data class OutboxEntity(
    @PrimaryKey(autoGenerate = true) @ColumnInfo(name = "id") val id: Long = 0,
    @ColumnInfo(name = "kind") val kind: String,
    @ColumnInfo(name = "item_id") val itemId: String? = null,
    @ColumnInfo(name = "field") val field: String? = null,
    @ColumnInfo(name = "payload_json") val payloadJson: String,
    @ColumnInfo(name = "state") val state: String = "pending",
    @ColumnInfo(name = "attempt_count") val attemptCount: Int = 0,
    @ColumnInfo(name = "next_attempt_at_ms") val nextAttemptAtMs: Long = 0,
    @ColumnInfo(name = "last_error_code") val lastErrorCode: Int? = null,
    @ColumnInfo(name = "last_error_body") val lastErrorBody: String? = null,
    @ColumnInfo(name = "created_at_ms") val createdAtMs: Long = 0,
)

@Entity(tableName = "preferences")
data class PreferenceEntity(
    @PrimaryKey @ColumnInfo(name = "key") val key: String,
    @ColumnInfo(name = "value") val value: String? = null,
    @ColumnInfo(name = "updated_at") val updatedAt: Long = 0,
)

@Serializable
@Entity(tableName = "channel_settings")
data class ChannelSettingEntity(
    @PrimaryKey @ColumnInfo(name = "channel_id") val channelId: String,
    @ColumnInfo(name = "media_only") val mediaOnly: Int? = null,
    @ColumnInfo(name = "include_reposts") val includeReposts: Int? = null,
    @ColumnInfo(name = "media_download_limit") val mediaDownloadLimit: Int? = null,
    @ColumnInfo(name = "max_videos") val maxVideos: Int? = null,
    @ColumnInfo(name = "download_subtitles") val downloadSubtitles: Int? = null,
    @ColumnInfo(name = "updated_at") val updatedAt: Long = 0,
)

@Entity(tableName = "android_sync_state")
data class AndroidSyncStateEntity(
    @PrimaryKey @ColumnInfo(name = "id") val id: Int = 1,
    @ColumnInfo(name = "mode") val mode: String,
    @ColumnInfo(name = "cursor") val cursor: String,
    @ColumnInfo(name = "feed_days") val feedDays: Int,
    @ColumnInfo(name = "youtube_days") val youtubeDays: Int,
    @ColumnInfo(name = "moments_days") val momentsDays: Int,
    @ColumnInfo(name = "story_hours") val storyHours: Int,
    @ColumnInfo(name = "bootstrap_required") val bootstrapRequired: Boolean = false,
)

@Entity(
    tableName = "android_sync_heads",
    primaryKeys = ["owner_kind", "owner_id"],
    indices = [
        Index(
            value = ["retention_bucket", "retain_at_ms"],
            name = "idx_android_sync_heads_retention",
        ),
    ],
)
data class AndroidSyncHeadEntity(
    @ColumnInfo(name = "owner_kind") val ownerKind: String,
    @ColumnInfo(name = "owner_id") val ownerId: String,
    @ColumnInfo(name = "retention_bucket") val retentionBucket: String,
    @ColumnInfo(name = "retain_at_ms") val retainAtMs: Long,
    @ColumnInfo(name = "bootstrap_seen") val bootstrapSeen: Boolean = true,
)

@Entity(
    tableName = "android_sync_assets",
    indices = [
        Index(value = ["state", "next_attempt_at_ms"], name = "idx_android_sync_assets_claim"),
        Index(value = ["owner_kind", "owner_id", "asset_kind", "media_index"], name = "idx_android_sync_assets_owner"),
    ],
)
data class AndroidSyncAssetEntity(
    @PrimaryKey @ColumnInfo(name = "asset_id") val assetId: String,
    @ColumnInfo(name = "asset_kind") val assetKind: String,
    @ColumnInfo(name = "media_index") val mediaIndex: Int = 0,
    @ColumnInfo(name = "owner_id") val ownerId: String,
    @ColumnInfo(name = "owner_kind") val ownerKind: String,
    @ColumnInfo(name = "bucket") val bucket: String,
    @ColumnInfo(name = "content_type") val contentType: String? = null,
    @ColumnInfo(name = "size_bytes") val sizeBytes: Long = 0,
    @ColumnInfo(name = "sha256") val sha256: String? = null,
    @ColumnInfo(name = "revision") val revision: Long,
    @ColumnInfo(name = "subtitle_is_auto") val subtitleIsAuto: Boolean = true,
    @ColumnInfo(name = "state") val state: String = "ready",
    @ColumnInfo(name = "local_path") val localPath: String? = null,
    @ColumnInfo(name = "verified_at_ms") val verifiedAtMs: Long? = null,
    @ColumnInfo(name = "next_attempt_at_ms") val nextAttemptAtMs: Long = 0,
)
