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
        Index(value = ["sync_seq"], orders = [Index.Order.DESC], name = "idx_feed_items_sync_seq"),
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
    @ColumnInfo(name = "source_handle") val sourceHandle: String? = null,
    @ColumnInfo(name = "author_handle") val authorHandle: String,
    @ColumnInfo(name = "author_display_name") val authorDisplayName: String? = null,
    @ColumnInfo(name = "author_avatar_url") val authorAvatarUrl: String? = null,
    @ColumnInfo(name = "body_text") val bodyText: String? = null,
    @ColumnInfo(name = "lang") val lang: String? = null,
    @ColumnInfo(name = "is_retweet") val isRetweet: Boolean = false,
    @ColumnInfo(name = "retweeted_by_handle") val retweetedByHandle: String? = null,
    @ColumnInfo(name = "retweeted_by_display_name") val retweetedByDisplayName: String? = null,

    @ColumnInfo(name = "quote_tweet_id") val quoteTweetId: String? = null,
    @ColumnInfo(name = "quote_author_handle") val quoteAuthorHandle: String? = null,
    @ColumnInfo(name = "quote_author_display_name") val quoteAuthorDisplayName: String? = null,
    @ColumnInfo(name = "quote_author_avatar_url") val quoteAuthorAvatarUrl: String? = null,
    @ColumnInfo(name = "quote_body_text") val quoteBodyText: String? = null,
    @ColumnInfo(name = "quote_lang") val quoteLang: String? = null,
    @ColumnInfo(name = "quote_media_json") val quoteMediaJson: String? = null,
    @ColumnInfo(name = "quote_published_at") val quotePublishedAt: Long = 0,
    @ColumnInfo(name = "quote_canonical_url") val quoteCanonicalUrl: String? = null,

    @ColumnInfo(name = "media_json") val mediaJson: String? = null,
    @ColumnInfo(name = "media_status") val mediaStatus: String? = null,

    @ColumnInfo(name = "views") val views: Long? = null,
    @ColumnInfo(name = "likes") val likes: Long? = null,
    @ColumnInfo(name = "retweets") val retweets: Long? = null,

    @ColumnInfo(name = "canonical_url") val canonicalUrl: String? = null,
    @ColumnInfo(name = "canonical_tweet_id") val canonicalTweetId: String? = null,
    @ColumnInfo(name = "reply_to_handle") val replyToHandle: String? = null,
    @ColumnInfo(name = "reply_to_status") val replyToStatus: String? = null,
    @ColumnInfo(name = "is_reply") val isReply: Boolean = false,
    @ColumnInfo(name = "is_ghost") val isGhost: Boolean = false,
    @ColumnInfo(name = "content_hash") val contentHash: String? = null,

    @ColumnInfo(name = "body_translation") val bodyTranslation: String? = null,
    @ColumnInfo(name = "body_source_lang") val bodySourceLang: String? = null,
    @ColumnInfo(name = "quote_translation") val quoteTranslation: String? = null,
    @ColumnInfo(name = "quote_source_lang") val quoteSourceLang: String? = null,

    @ColumnInfo(name = "published_at") val publishedAt: Long = 0,
    @ColumnInfo(name = "sync_seq") val syncSeq: Long = 0,
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
        Index(value = ["sync_seq"], orders = [Index.Order.DESC], name = "idx_videos_sync_seq"),
    ],
)
data class VideoEntity(
    @PrimaryKey @ColumnInfo(name = "video_id") val videoId: String,
    @ColumnInfo(name = "channel_id") val channelId: String,
    @ColumnInfo(name = "title") val title: String? = null,
    @ColumnInfo(name = "description") val description: String? = null,
    @ColumnInfo(name = "duration") val duration: Long? = null,
    @SerialName("duration_label")
    @ColumnInfo(name = "duration_label") val durationLabel: String = "",
    @ColumnInfo(name = "thumbnail_path") val thumbnailPath: String? = null,
    @ColumnInfo(name = "file_path") val filePath: String? = null,
    @ColumnInfo(name = "file_size") val fileSize: Long? = null,
    @ColumnInfo(name = "published_at") val publishedAt: Long = 0,
    @ColumnInfo(name = "downloaded_at") val downloadedAt: Long = 0,
    @ColumnInfo(name = "media_kind") val mediaKind: String? = null,
    @ColumnInfo(name = "media_mode") val mediaMode: String? = null,
    @ColumnInfo(name = "slide_count") val slideCount: Int = 0,
    @ColumnInfo(name = "source_kind") val sourceKind: String? = null,
    @ColumnInfo(name = "metadata_json") val metadataJson: String? = null,
    @ColumnInfo(name = "canonical_url") val canonicalUrl: String? = null,
    @ColumnInfo(name = "display_title") val displayTitle: String? = null,
    @ColumnInfo(name = "display_title_casual") val displayTitleCasual: String? = null,
    @ColumnInfo(name = "dearrow_title") val dearrowTitle: String? = null,
    @ColumnInfo(name = "dearrow_title_casual") val dearrowTitleCasual: String? = null,
    @ColumnInfo(name = "dearrow_thumb_path") val dearrowThumbPath: String? = null,
    @ColumnInfo(name = "dearrow_checked_at_ms") val dearrowCheckedAtMs: Long? = null,
    @ColumnInfo(name = "sync_seq") val syncSeq: Long = 0,
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
    @ColumnInfo(name = "avatar_url") val avatarUrl: String? = null,
    @ColumnInfo(name = "quality") val quality: String? = null,
    @ColumnInfo(name = "last_checked") val lastChecked: Long? = null,
    @ColumnInfo(name = "created_at") val createdAt: Long = 0,
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
    @SerialName("followers_label")
    @ColumnInfo(name = "followers_label") val followersLabel: String = "",
    @ColumnInfo(name = "following") val following: Int = 0,
    @SerialName("following_label")
    @ColumnInfo(name = "following_label") val followingLabel: String = "",
    @ColumnInfo(name = "verified") val verified: Boolean = false,
    @ColumnInfo(name = "verified_type") val verifiedType: String? = null,
    @SerialName("protected")
    @ColumnInfo(name = "protected") val isProtected: Boolean = false,
    @ColumnInfo(name = "avatar_url") val avatarUrl: String? = null,
    @ColumnInfo(name = "banner_url") val bannerUrl: String? = null,
    @SerialName("profile_url")
    @ColumnInfo(name = "profile_url") val profileUrl: String? = null,
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
    @ColumnInfo(name = "author_thumbnail") val authorThumbnail: String? = null,
    @ColumnInfo(name = "text") val text: String? = null,
    @ColumnInfo(name = "like_count") val likeCount: Long? = null,
    @ColumnInfo(name = "published_at") val publishedAt: Long = 0,
    @ColumnInfo(name = "platform") val platform: String = "youtube",
    @ColumnInfo(name = "fetched_at") val fetchedAt: Long = 0,
    @ColumnInfo(name = "thread_order") val threadOrder: Int = 0,
    @ColumnInfo(name = "thread_depth") val threadDepth: Int = 0,
    @ColumnInfo(name = "parent_order") val parentOrder: Int = 0,
    @ColumnInfo(name = "reply_to_author") val replyToAuthor: String = "",
    @SerialName("is_creator")
    @ColumnInfo(name = "is_creator") val isCreator: Boolean = false,
    @SerialName("like_count_label")
    @ColumnInfo(name = "like_count_label") val likeCountLabel: String = "",
)

@Serializable
@Entity(
    tableName = "retweet_sources",
    primaryKeys = ["content_hash", "retweeter_handle"],
    indices = [
        Index(value = ["tweet_id"], name = "idx_retweet_sources_tweet"),
    ],
)
data class RetweetSourceEntity(
    @ColumnInfo(name = "content_hash") val contentHash: String,
    @ColumnInfo(name = "retweeter_handle") val retweeterHandle: String,
    @ColumnInfo(name = "retweeter_display_name") val retweeterDisplayName: String? = null,
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
    @ColumnInfo(name = "reposter_handle") val reposterHandle: String = "",
    @ColumnInfo(name = "reposter_display_name") val reposterDisplayName: String? = null,
    @ColumnInfo(name = "repost_author_label") val repostAuthorLabel: String = "",
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

@Entity(tableName = "feed_likes")
data class FeedLikeEntity(
    @PrimaryKey @ColumnInfo(name = "tweet_id") val tweetId: String,
    @ColumnInfo(name = "liked_at") val likedAt: Long = 0,
)

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

@Entity(tableName = "bookmark_categories")
data class BookmarkCategoryEntity(
    @PrimaryKey @ColumnInfo(name = "category_id") val categoryId: Long,
    @ColumnInfo(name = "name") val name: String,
    @ColumnInfo(name = "archive_path") val archivePath: String? = null,
    @ColumnInfo(name = "created_at") val createdAt: Long = 0,
)

@Entity(tableName = "bookmark_labels")
data class BookmarkLabelEntity(
    @PrimaryKey @ColumnInfo(name = "label") val label: String,
    @ColumnInfo(name = "synced_at") val syncedAt: Long = 0,
)

@Entity(tableName = "feed_seen")
data class FeedSeenEntity(
    @PrimaryKey @ColumnInfo(name = "tweet_id") val tweetId: String,
    @ColumnInfo(name = "seen_at") val seenAt: Long = 0,
)

@Entity(tableName = "moment_views")
data class MomentViewEntity(
    @PrimaryKey @ColumnInfo(name = "video_id") val videoId: String,
    @ColumnInfo(name = "viewed_at") val viewedAt: Long = 0,
)

@Entity(tableName = "watch_history")
data class WatchHistoryEntity(
    @PrimaryKey @ColumnInfo(name = "video_id") val videoId: String,
    @ColumnInfo(name = "playback_position") val playbackPosition: Double = 0.0,
    @ColumnInfo(name = "duration") val duration: Double? = null,
    @ColumnInfo(name = "progress_updated_at_ms") val progressUpdatedAtMs: Long = 0,
    @ColumnInfo(name = "progress_source") val progressSource: String? = null,
    @ColumnInfo(name = "last_watched") val lastWatched: Long = 0,
)

@Entity(tableName = "muted_accounts")
data class MutedAccountEntity(
    @PrimaryKey @ColumnInfo(name = "handle") val handle: String,
    @ColumnInfo(name = "muted_at") val mutedAt: Long = 0,
)

@Entity(tableName = "channel_follows")
data class ChannelFollowEntity(
    @PrimaryKey @ColumnInfo(name = "channel_id") val channelId: String,
    @ColumnInfo(name = "followed_at") val followedAt: Long = 0,
)

@Entity(tableName = "channel_stars")
data class ChannelStarEntity(
    @PrimaryKey @ColumnInfo(name = "channel_id") val channelId: String,
    @ColumnInfo(name = "starred_at") val starredAt: Long = 0,
)

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

// ─── Android-only tables ──────────────────────────────────────────────────────────────

@Entity(
    tableName = "feed_timeline_entries",
    primaryKeys = ["surface", "position"],
    indices = [
        Index(
            value = ["surface", "tweet_id"],
            unique = true,
            name = "idx_feed_timeline_entries_surface_tweet",
        ),
        Index(value = ["surface", "captured_at_ms"], name = "idx_feed_timeline_entries_capture"),
    ],
)
data class FeedTimelineEntryEntity(
    @ColumnInfo(name = "surface") val surface: String,
    @ColumnInfo(name = "position") val position: Int,
    @ColumnInfo(name = "tweet_id") val tweetId: String,
    @ColumnInfo(name = "captured_at_ms") val capturedAtMs: Long,
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

@Entity(tableName = "cursors")
data class CursorEntity(
    @PrimaryKey @ColumnInfo(name = "stream") val stream: String,
    @ColumnInfo(name = "cursor") val cursor: String,
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

@Entity(
    tableName = "media_inventory",
    primaryKeys = ["asset_id", "asset_kind"],
    indices = [
        Index(value = ["state", "next_attempt_at_ms"], name = "idx_media_inventory_claim"),
        Index(value = ["scope"], name = "idx_media_inventory_scope"),
        Index(value = ["bucket"], name = "idx_media_inventory_bucket"),
        Index(value = ["owner_id"], name = "idx_media_inventory_owner"),
        Index(value = ["bucket", "effective_recency_ms"], name = "idx_media_inventory_recency"),
    ],
)
data class MediaInventoryEntity(
    @ColumnInfo(name = "asset_id") val assetId: String,
    @ColumnInfo(name = "asset_kind") val assetKind: String,
    @ColumnInfo(name = "scope") val scope: String,
    @ColumnInfo(name = "owner_id") val ownerId: String? = null,
    @ColumnInfo(name = "bucket") val bucket: String,
    @ColumnInfo(name = "server_url") val serverUrl: String,
    @ColumnInfo(name = "local_path") val localPath: String? = null,
    @ColumnInfo(name = "file_size") val fileSize: Long? = null,
    @ColumnInfo(name = "size_hint") val sizeHint: Long = 0,
    @ColumnInfo(name = "content_type") val contentType: String? = null,
    @ColumnInfo(name = "subtitle_is_auto") val subtitleIsAuto: Boolean = true,
    @ColumnInfo(name = "audio_language") val audioLanguage: String? = null,
    @ColumnInfo(name = "effective_recency_ms") val effectiveRecencyMs: Long = 0,
    @ColumnInfo(name = "state") val state: String = "pending",
    @ColumnInfo(name = "attempt_count") val attemptCount: Int = 0,
    @ColumnInfo(name = "next_attempt_at_ms") val nextAttemptAtMs: Long = 0,
    @ColumnInfo(name = "last_error") val lastError: String? = null,
    @ColumnInfo(name = "added_at_ms") val addedAtMs: Long = 0,
    @ColumnInfo(name = "cached_at_ms") val cachedAtMs: Long? = null,
)

@Entity(tableName = "android_sync_generations")
data class AndroidSyncGenerationEntity(
    @PrimaryKey @ColumnInfo(name = "generation_id") val generationId: String,
    @ColumnInfo(name = "created_at_ms") val createdAtMs: Long,
    @ColumnInfo(name = "status") val status: String,
    @ColumnInfo(name = "source_version") val sourceVersion: String,
    @ColumnInfo(name = "retention_json") val retentionJson: String,
    @ColumnInfo(name = "item_count") val itemCount: Int,
    @ColumnInfo(name = "asset_count") val assetCount: Int,
    @ColumnInfo(name = "ready_asset_count") val readyAssetCount: Int,
    @ColumnInfo(name = "server_missing_asset_count") val serverMissingAssetCount: Int,
    @ColumnInfo(name = "total_bytes") val totalBytes: Long,
    @ColumnInfo(name = "content_counts_json") val contentCountsJson: String = "{}",
    @ColumnInfo(name = "asset_counts_json") val assetCountsJson: String = "{}",
    @ColumnInfo(name = "items_imported_at_ms") val itemsImportedAtMs: Long? = null,
    @ColumnInfo(name = "assets_imported_at_ms") val assetsImportedAtMs: Long? = null,
)

@Entity(
    tableName = "android_sync_items",
    primaryKeys = ["generation_id", "seq"],
    indices = [
        Index(value = ["generation_id", "item_kind", "item_id"], unique = true, name = "idx_android_sync_items_identity"),
        Index(value = ["item_kind", "item_id", "generation_id"], name = "idx_android_sync_items_kind_identity"),
    ],
)
data class AndroidSyncItemEntity(
    @ColumnInfo(name = "generation_id") val generationId: String,
    @ColumnInfo(name = "seq") val seq: Long,
    @ColumnInfo(name = "item_kind") val itemKind: String,
    @ColumnInfo(name = "item_id") val itemId: String,
    @ColumnInfo(name = "payload_json") val payloadJson: String,
)

@Entity(
    tableName = "android_sync_assets",
    primaryKeys = ["generation_id", "asset_id", "asset_kind"],
    indices = [
        Index(value = ["generation_id", "seq"], name = "idx_android_sync_assets_page"),
        Index(value = ["generation_id", "state", "next_attempt_at_ms"], name = "idx_android_sync_assets_claim"),
        Index(value = ["generation_id", "bucket"], name = "idx_android_sync_assets_bucket"),
        Index(value = ["asset_id", "asset_kind", "server_state", "state"], name = "idx_android_sync_assets_identity_state"),
        Index(value = ["owner_id", "asset_kind", "server_state", "state", "verified_at_ms", "generation_id"], name = "idx_android_sync_assets_owner_kind_state"),
    ],
)
data class AndroidSyncAssetEntity(
    @ColumnInfo(name = "generation_id") val generationId: String,
    @ColumnInfo(name = "seq") val seq: Long,
    @ColumnInfo(name = "asset_id") val assetId: String,
    @ColumnInfo(name = "asset_kind") val assetKind: String,
    @ColumnInfo(name = "owner_id") val ownerId: String,
    @ColumnInfo(name = "owner_kind") val ownerKind: String,
    @ColumnInfo(name = "bucket") val bucket: String,
    @ColumnInfo(name = "server_url") val serverUrl: String,
    @ColumnInfo(name = "content_type") val contentType: String? = null,
    @ColumnInfo(name = "size_bytes") val sizeBytes: Long = 0,
    @ColumnInfo(name = "sha256") val sha256: String? = null,
    @ColumnInfo(name = "server_state") val serverState: String = "ready",
    @ColumnInfo(name = "required_reason") val requiredReason: String? = null,
    @ColumnInfo(name = "subtitle_is_auto") val subtitleIsAuto: Boolean = true,
    @ColumnInfo(name = "audio_language") val audioLanguage: String? = null,
    @ColumnInfo(name = "effective_recency_ms") val effectiveRecencyMs: Long = 0,
    @ColumnInfo(name = "state") val state: String = "desired",
    @ColumnInfo(name = "local_path") val localPath: String? = null,
    @ColumnInfo(name = "file_size") val fileSize: Long? = null,
    @ColumnInfo(name = "verified_at_ms") val verifiedAtMs: Long? = null,
    @ColumnInfo(name = "attempt_count") val attemptCount: Int = 0,
    @ColumnInfo(name = "next_attempt_at_ms") val nextAttemptAtMs: Long = 0,
    @ColumnInfo(name = "last_error") val lastError: String? = null,
    @ColumnInfo(name = "updated_at_ms") val updatedAtMs: Long = 0,
)
