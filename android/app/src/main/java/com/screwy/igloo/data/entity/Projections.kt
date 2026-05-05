package com.screwy.igloo.data.entity

import androidx.room.ColumnInfo
import androidx.room.Embedded

/**
 * Typed projection classes returned by the composite read DAOs. Kept next to
 * entities because they are joined row shapes: data, not logic.
 *
 * `@Embedded(prefix = "")` wraps the base entity so Room can populate all of its columns
 * from the query result without re-enumerating every field. Joined columns live at the
 * top level of the projection.
 */

/** Feed query result row. */
data class FeedRow(
    @Embedded val item: FeedItemEntity,

    @ColumnInfo(name = "channel_name") val channelName: String?,
    @ColumnInfo(name = "channel_avatar_url") val channelAvatarUrl: String?,
    @ColumnInfo(name = "channel_platform") val channelPlatform: String?,

    @ColumnInfo(name = "is_liked") val isLiked: Int,
    @ColumnInfo(name = "liked_at") val likedAt: Long?,

    @ColumnInfo(name = "is_bookmarked") val isBookmarked: Int,
    @ColumnInfo(name = "bookmark_category_id") val bookmarkCategoryId: Long?,
    @ColumnInfo(name = "bookmark_custom_title") val bookmarkCustomTitle: String?,
    @ColumnInfo(name = "bookmarked_at") val bookmarkedAt: Long?,
    @ColumnInfo(name = "bookmark_account_handles") val bookmarkAccountHandles: String? = null,
    @ColumnInfo(name = "bookmark_media_indices") val bookmarkMediaIndices: String? = null,

    @ColumnInfo(name = "channel_is_followed") val channelIsFollowed: Int,
    @ColumnInfo(name = "channel_is_starred") val channelIsStarred: Int,

    @ColumnInfo(name = "quote_channel_id") val quoteChannelId: String? = null,
    @ColumnInfo(name = "quote_channel_is_followed") val quoteChannelIsFollowed: Int = 0,
)

/**
 * A feed row plus its inline conversation context. [chain] contains ancestors
 * ordered root -> parent; [row] is the leaf and is not repeated in [chain].
 */
data class ThreadedFeedRow(
    val row: FeedRow,
    val chain: List<FeedRow>,
)

/** Live side-table state for rows already held in a stable feed snapshot. */
data class FeedRowActionState(
    @ColumnInfo(name = "tweet_id") val tweetId: String,
    @ColumnInfo(name = "is_liked") val isLiked: Int,
    @ColumnInfo(name = "liked_at") val likedAt: Long?,
    @ColumnInfo(name = "is_bookmarked") val isBookmarked: Int,
    @ColumnInfo(name = "bookmark_category_id") val bookmarkCategoryId: Long?,
    @ColumnInfo(name = "bookmark_custom_title") val bookmarkCustomTitle: String?,
    @ColumnInfo(name = "bookmarked_at") val bookmarkedAt: Long?,
    @ColumnInfo(name = "bookmark_account_handles") val bookmarkAccountHandles: String? = null,
    @ColumnInfo(name = "bookmark_media_indices") val bookmarkMediaIndices: String? = null,
)

/** Lightweight ordered candidate used to compare the active main-feed snapshot head. */
data class FeedHeadCandidate(
    @ColumnInfo(name = "tweet_id") val tweetId: String,
    @ColumnInfo(name = "author_handle") val authorHandle: String,
    @ColumnInfo(name = "author_display_name") val authorDisplayName: String?,
    @ColumnInfo(name = "channel_id") val channelId: String?,
)

/** Shorts/moments grid item — TikTok + Instagram. */
data class MomentItem(
    @Embedded val video: VideoEntity,

    @ColumnInfo(name = "is_viewed") val isViewed: Int,
    @ColumnInfo(name = "viewed_at") val viewedAt: Long?,
    @ColumnInfo(name = "channel_name") val channelName: String?,
    @ColumnInfo(name = "channel_source_id") val channelSourceId: String?,
    @ColumnInfo(name = "channel_is_followed") val channelIsFollowed: Int = 0,
    @ColumnInfo(name = "reposter_channel_id") val reposterChannelId: String? = null,
    @ColumnInfo(name = "reposter_handle") val reposterHandle: String? = null,
    @ColumnInfo(name = "reposter_display_name") val reposterDisplayName: String? = null,
    @ColumnInfo(name = "repost_author_label") val repostAuthorLabel: String? = null,
    @ColumnInfo(name = "repost_count") val repostCount: Int = 0,
    @ColumnInfo(name = "repost_introduced") val repostIntroduced: Int = 0,
    @ColumnInfo(name = "effective_moment_at_ms") val effectiveMomentAtMs: Long = 0,
)

/** Active story channel row for recent own TikTok/Instagram moments. */
data class StoryChannelItem(
    @ColumnInfo(name = "channel_id") val channelId: String,
    @ColumnInfo(name = "channel_name") val channelName: String?,
    @ColumnInfo(name = "channel_source_id") val channelSourceId: String?,
    @ColumnInfo(name = "story_count") val storyCount: Int,
    @ColumnInfo(name = "unseen_count") val unseenCount: Int,
    @ColumnInfo(name = "latest_at_ms") val latestAtMs: Long,
    @ColumnInfo(name = "first_video_id") val firstVideoId: String,
    @ColumnInfo(name = "first_unseen_video_id") val firstUnseenVideoId: String,
    @ColumnInfo(name = "is_starred") val isStarred: Int = 0,
)

/**
 * YouTube Videos tab row — long-form only, with resume progress. `wh_*` aliasing keeps
 * the joined `watch_history` columns from colliding with embedded `VideoEntity` names
 * (both tables carry `duration`).
 */
data class VideoGridItem(
    @Embedded val video: VideoEntity,

    @ColumnInfo(name = "wh_playback_position") val playbackPosition: Double?,
    @ColumnInfo(name = "wh_duration") val watchDuration: Double?,
    @ColumnInfo(name = "wh_last_watched") val lastWatched: Long?,

    @ColumnInfo(name = "channel_name") val channelName: String?,
    @ColumnInfo(name = "channel_source_id") val channelSourceId: String?,
)

/**
 * Bookmarks tab row — LEFT JOINs both `feed_items` and `videos`; exactly one side matches
 * per row (or both, in the astronomically-unlikely tweet_id/video_id collision case, in
 * which case UI renders as video per §7). Consumers dispatch on which side is non-null.
 */
data class BookmarkItem(
    @Embedded val bookmark: BookmarkEntity,

    @Embedded(prefix = "tw_") val feedItem: FeedItemEntity?,
    @Embedded(prefix = "vd_") val video: VideoEntity?,
    @ColumnInfo(name = "resolved_channel_id") val resolvedChannelId: String?,
    @ColumnInfo(name = "resolved_channel_name") val resolvedChannelName: String?,
    @ColumnInfo(name = "resolved_channel_source_id") val resolvedChannelSourceId: String?,
    @ColumnInfo(name = "resolved_channel_is_followed") val resolvedChannelIsFollowed: Int = 0,
)

/** Channel drawer row — starred-first ordering. */
data class ChannelDisplay(
    @Embedded val channel: ChannelEntity,

    @ColumnInfo(name = "is_starred") val isStarred: Int,
    @ColumnInfo(name = "is_followed") val isFollowed: Int,
    // Joined from channel_profiles — Twitter/TikTok near-100% coverage, YouTube ~95%.
    // Used for sidebar search so Latin queries can find unicode display names.
    @ColumnInfo(name = "handle") val handle: String? = null,
    // Pretty, human-facing name from channel_profiles. Twitter ingest stores
    // the handle in `channels.name` for ~82% of rows — display_name is how we
    // recover "Example Display Name" for @sample_handle_ja instead of showing
    // "sample_handle_ja". Use [ChannelDisplay.displayOrName] at render time.
    @ColumnInfo(name = "display_name") val displayName: String? = null,
)

/** Prefer channel_profiles.display_name when set, fall back to channels.name. */
val ChannelDisplay.displayOrName: String
    get() {
        val primary = displayName?.trim().orEmpty()
        if (primary.isBlank()) return channel.name
        val normalizedPrimary = primary.removePrefix("@").trim()
        val normalizedHandle = handle?.trim()?.removePrefix("@")?.trim().orEmpty()
        val normalizedName = channel.name.trim().removePrefix("@").trim()
        if (normalizedHandle.isNotBlank() &&
            normalizedPrimary == normalizedHandle &&
            normalizedName.isNotBlank() &&
            normalizedName != normalizedHandle
        ) {
            return channel.name
        }
        return primary
    }
