package com.screwy.igloo.data.dao

import androidx.room.Dao
import androidx.room.Query
import androidx.room.Transaction
import androidx.room.Upsert
import com.screwy.igloo.data.entity.BookmarkCategoryEntity
import com.screwy.igloo.data.entity.ChannelEntity
import com.screwy.igloo.data.entity.ChannelFollowEntity
import com.screwy.igloo.data.entity.ChannelProfileEntity
import com.screwy.igloo.data.entity.ChannelSettingEntity
import com.screwy.igloo.data.entity.ChannelStarEntity
import com.screwy.igloo.data.entity.FeedItemEntity
import com.screwy.igloo.data.entity.FeedLikeEntity
import com.screwy.igloo.data.entity.FeedRankEntity
import com.screwy.igloo.data.entity.FeedSeenEntity
import com.screwy.igloo.data.entity.MomentViewEntity
import com.screwy.igloo.data.entity.MomentsCursorEntity
import com.screwy.igloo.data.entity.MutedChannelEntity
import com.screwy.igloo.data.entity.MutedChannelDisplay
import com.screwy.igloo.data.entity.RetweetSourceEntity
import com.screwy.igloo.data.entity.SponsorBlockCheckedEntity
import com.screwy.igloo.data.entity.SponsorBlockSegmentEntity
import com.screwy.igloo.data.entity.VideoCommentEntity
import com.screwy.igloo.data.entity.VideoEntity
import com.screwy.igloo.data.entity.VideoRepostSourceEntity
import com.screwy.igloo.data.entity.WatchHistoryEntity
import com.screwy.igloo.data.entity.BookmarkEntity
import kotlinx.coroutines.flow.Flow

/**
 * Per-entity CRUD DAOs. Composite read queries (joins across side tables) live in
 * FeedReadDao / MomentReadDao / VideoReadDao / BookmarkReadDao / ChannelReadDao —
 * sync + UI consume those for multi-table reads, and come here for raw row access.
 *
 * Conventions:
 *  - `upsert` takes either one row or a list (vararg would force boxed allocations).
 *  - `*Flow` methods return `Flow<...>` for reactive consumption. Room invalidates on
 *    any write to the table; UI re-emits automatically.
 *  - `deleteAll` supports exact table replacement where the caller owns the transaction.
 */

@Dao
interface FeedItemDao {
    @Upsert suspend fun upsert(rows: List<FeedItemEntity>)
    @Upsert suspend fun upsert(row: FeedItemEntity)

    @Query("SELECT * FROM feed_items WHERE tweet_id = :tweetId")
    fun getByIdFlow(tweetId: String): Flow<FeedItemEntity?>

    @Query("SELECT * FROM feed_items WHERE tweet_id = :tweetId")
    suspend fun getById(tweetId: String): FeedItemEntity?

    @Query("DELETE FROM feed_items WHERE tweet_id IN (:tweetIds)")
    suspend fun deleteByIds(tweetIds: List<String>)

    @Query("SELECT tweet_id FROM feed_items")
    suspend fun allIds(): List<String>

    @Query("DELETE FROM feed_items")
    suspend fun deleteAll()

    @Query("SELECT COUNT(*) FROM feed_items")
    suspend fun count(): Int

}

@Dao
interface VideoDao {
    @Upsert suspend fun upsert(rows: List<VideoEntity>)
    @Upsert suspend fun upsert(row: VideoEntity)

    @Query("SELECT * FROM videos WHERE video_id = :videoId")
    fun getByIdFlow(videoId: String): Flow<VideoEntity?>

    @Query("SELECT * FROM videos WHERE video_id = :videoId")
    suspend fun getById(videoId: String): VideoEntity?

    @Query("DELETE FROM videos WHERE video_id IN (:videoIds)")
    suspend fun deleteByIds(videoIds: List<String>)

    @Query("SELECT video_id FROM videos")
    suspend fun allIds(): List<String>

    @Query("DELETE FROM videos")
    suspend fun deleteAll()

    @Query("SELECT COUNT(*) FROM videos")
    suspend fun count(): Int

    @Query(
        """
        SELECT video_id FROM videos
        WHERE owner_kind = 'youtube_video'
          AND (
                published_at < (SELECT published_at FROM videos WHERE video_id = :videoId)
                OR (
                    published_at = (SELECT published_at FROM videos WHERE video_id = :videoId)
                    AND video_id < :videoId
                )
              )
        ORDER BY published_at DESC, video_id DESC
        LIMIT 1
        """
    )
    suspend fun getNextVideoId(videoId: String): String?

    @Query(
        """
        SELECT video_id FROM videos
        WHERE owner_kind = 'youtube_video'
          AND (
                published_at > (SELECT published_at FROM videos WHERE video_id = :videoId)
                OR (
                    published_at = (SELECT published_at FROM videos WHERE video_id = :videoId)
                    AND video_id > :videoId
                )
              )
        ORDER BY published_at ASC, video_id ASC
        LIMIT 1
        """
    )
    suspend fun getPreviousVideoId(videoId: String): String?

}

@Dao
interface ChannelDao {
    @Upsert suspend fun upsert(rows: List<ChannelEntity>)
    @Upsert suspend fun upsert(row: ChannelEntity)

    @Query("SELECT * FROM channels WHERE channel_id = :channelId")
    fun getByIdFlow(channelId: String): Flow<ChannelEntity?>

    @Query("SELECT * FROM channels WHERE channel_id = :channelId")
    suspend fun getById(channelId: String): ChannelEntity?

    @Query("SELECT channel_id FROM channels")
    suspend fun allIds(): List<String>

    @Query("DELETE FROM channels WHERE channel_id IN (:channelIds)")
    suspend fun deleteByIds(channelIds: List<String>)

    @Query("SELECT * FROM channels WHERE LOWER(COALESCE(source_id, '')) = LOWER(:sourceId) LIMIT 1")
    suspend fun findBySourceId(sourceId: String): ChannelEntity?

    @Query(
        """
        SELECT * FROM channels
        WHERE LOWER(COALESCE(source_id, '')) = LOWER(:sourceId)
          AND LOWER(COALESCE(platform, '')) = LOWER(:platform)
        LIMIT 1
        """
    )
    suspend fun findBySourceIdAndPlatform(sourceId: String, platform: String): ChannelEntity?

    @Query("DELETE FROM channels")
    suspend fun deleteAll()
}

@Dao
interface ChannelProfileDao {
    @Upsert suspend fun upsert(rows: List<ChannelProfileEntity>)
    @Upsert suspend fun upsert(row: ChannelProfileEntity)

    @Query("DELETE FROM channel_profiles WHERE channel_id = :channelId")
    suspend fun delete(channelId: String)

    @Query("DELETE FROM channel_profiles WHERE channel_id IN (:channelIds)")
    suspend fun deleteByIds(channelIds: List<String>)

    @Query(
        """
        DELETE FROM channel_profiles
        WHERE channel_id NOT IN (
            SELECT channel_id FROM channels
            UNION SELECT channel_id FROM channel_follows
            UNION SELECT channel_id FROM channel_stars
            UNION SELECT channel_id FROM muted_channels
            UNION SELECT channel_id FROM channel_settings
                  WHERE media_only IS NOT NULL
                     OR include_reposts IS NOT NULL
                     OR media_download_limit IS NOT NULL
                     OR max_videos IS NOT NULL
                     OR download_subtitles IS NOT NULL
            UNION SELECT channel_id FROM videos
            UNION SELECT channel_id FROM feed_items WHERE channel_id IS NOT NULL
            UNION SELECT source_channel_id FROM feed_items WHERE source_channel_id IS NOT NULL
            UNION SELECT quote_channel_id FROM feed_items WHERE quote_channel_id IS NOT NULL
            UNION SELECT reply_channel_id FROM feed_items WHERE reply_channel_id IS NOT NULL
            UNION SELECT reposter_channel_id FROM feed_items WHERE reposter_channel_id IS NOT NULL
            UNION SELECT reposter_channel_id FROM video_repost_sources
            UNION SELECT retweeter_channel_id FROM retweet_sources
        )
        """,
    )
    suspend fun deleteUnreferenced()

    @Query("SELECT * FROM channel_profiles WHERE channel_id = :channelId")
    suspend fun getById(channelId: String): ChannelProfileEntity?

    @Query("SELECT * FROM channel_profiles WHERE channel_id = :channelId")
    fun getByIdFlow(channelId: String): Flow<ChannelProfileEntity?>

    @Query(
        """
        SELECT * FROM channel_profiles
        WHERE LOWER(COALESCE(platform, '')) = LOWER(:platform)
          AND LOWER(REPLACE(COALESCE(handle, ''), '@', '')) = LOWER(:handle)
        ORDER BY channel_id ASC
        LIMIT 1
        """
    )
    suspend fun findByHandleAndPlatform(handle: String, platform: String): ChannelProfileEntity?

    @Query(
        """
        SELECT * FROM channel_profiles
        WHERE LOWER(REPLACE(COALESCE(handle, ''), '@', '')) = LOWER(:handle)
        ORDER BY CASE WHEN LOWER(COALESCE(platform, '')) IN ('twitter', 'x') THEN 0 ELSE 1 END,
                 channel_id ASC
        LIMIT 1
        """
    )
    suspend fun findByHandle(handle: String): ChannelProfileEntity?

    @Query("DELETE FROM channel_profiles")
    suspend fun deleteAll()
}

@Dao
interface VideoCommentDao {
    @Upsert suspend fun upsert(rows: List<VideoCommentEntity>)

    @Query(
        """
        SELECT * FROM video_comments
        WHERE video_id = :videoId
        ORDER BY published_at DESC, comment_id ASC
        """
    )
    fun forVideoFlow(videoId: String): Flow<List<VideoCommentEntity>>

    @Query("DELETE FROM video_comments WHERE video_id = :videoId")
    suspend fun deleteForVideo(videoId: String)

    @Query("DELETE FROM video_comments WHERE video_id IN (:videoIds)")
    suspend fun deleteForVideos(videoIds: List<String>)

    @Query("DELETE FROM video_comments WHERE video_id NOT IN (SELECT video_id FROM videos)")
    suspend fun deleteOrphans()

    @Query("DELETE FROM video_comments")
    suspend fun deleteAll()
}

@Dao
interface RetweetSourceDao {
    @Upsert suspend fun upsert(rows: List<RetweetSourceEntity>)

    @Query(
        """
        SELECT * FROM retweet_sources
        WHERE content_hash = :contentHash
        ORDER BY published_at DESC
        LIMIT :limit
        """
    )
    suspend fun forContentHash(contentHash: String, limit: Int = 5): List<RetweetSourceEntity>

    @Query("SELECT COUNT(*) FROM retweet_sources WHERE content_hash = :contentHash")
    suspend fun countForContentHash(contentHash: String): Int

    @Query("DELETE FROM retweet_sources WHERE content_hash = :contentHash")
    suspend fun deleteForContentHash(contentHash: String)

    @Query("DELETE FROM retweet_sources WHERE content_hash IN (:contentHashes)")
    suspend fun deleteForContentHashes(contentHashes: List<String>)

    @Query("DELETE FROM retweet_sources WHERE content_hash NOT IN (SELECT content_hash FROM feed_items WHERE content_hash IS NOT NULL)")
    suspend fun deleteOrphans()

    @Query("DELETE FROM retweet_sources")
    suspend fun deleteAll()
}

@Dao
interface VideoRepostSourceDao {
    @Upsert suspend fun upsert(rows: List<VideoRepostSourceEntity>)

    @Query("DELETE FROM video_repost_sources WHERE video_id = :videoId")
    suspend fun deleteForVideo(videoId: String)

    @Query("DELETE FROM video_repost_sources WHERE video_id IN (:videoIds)")
    suspend fun deleteForVideos(videoIds: List<String>)

    @Query("DELETE FROM video_repost_sources WHERE video_id NOT IN (SELECT video_id FROM videos)")
    suspend fun deleteOrphans()

    @Query("SELECT * FROM video_repost_sources WHERE video_id = :videoId ORDER BY reposted_at_ms DESC, first_seen_at_ms DESC, reposter_channel_id ASC")
    suspend fun forVideo(videoId: String): List<VideoRepostSourceEntity>

    @Transaction
    suspend fun replaceForVideo(videoId: String, rows: List<VideoRepostSourceEntity>) {
        deleteForVideo(videoId)
        if (rows.isNotEmpty()) upsert(rows)
    }

    @Query("DELETE FROM video_repost_sources")
    suspend fun deleteAll()
}

@Dao
interface SponsorBlockSegmentDao {
    @Upsert suspend fun upsert(rows: List<SponsorBlockSegmentEntity>)

    @Query("SELECT * FROM sponsorblock_segments WHERE video_id = :videoId ORDER BY start_time")
    suspend fun forVideo(videoId: String): List<SponsorBlockSegmentEntity>

    @Query("SELECT * FROM sponsorblock_segments WHERE video_id = :videoId ORDER BY start_time")
    fun forVideoFlow(videoId: String): Flow<List<SponsorBlockSegmentEntity>>

    @Query("DELETE FROM sponsorblock_segments WHERE video_id = :videoId")
    suspend fun deleteForVideo(videoId: String)

    @Query("DELETE FROM sponsorblock_segments WHERE video_id IN (:videoIds)")
    suspend fun deleteForVideos(videoIds: List<String>)

    @Query("DELETE FROM sponsorblock_segments WHERE video_id NOT IN (SELECT video_id FROM videos)")
    suspend fun deleteOrphans()

    @Query("DELETE FROM sponsorblock_segments")
    suspend fun deleteAll()
}

@Dao
interface SponsorBlockCheckedDao {
    @Upsert suspend fun upsert(rows: List<SponsorBlockCheckedEntity>)
    @Upsert suspend fun upsert(row: SponsorBlockCheckedEntity)

    @Query("SELECT * FROM sponsorblock_checked WHERE video_id = :videoId")
    suspend fun forVideo(videoId: String): SponsorBlockCheckedEntity?

    @Query("DELETE FROM sponsorblock_checked WHERE video_id = :videoId")
    suspend fun deleteForVideo(videoId: String)

    @Query("DELETE FROM sponsorblock_checked WHERE video_id IN (:videoIds)")
    suspend fun deleteForVideos(videoIds: List<String>)

    @Query("DELETE FROM sponsorblock_checked WHERE video_id NOT IN (SELECT video_id FROM videos)")
    suspend fun deleteOrphans()

    @Query("DELETE FROM sponsorblock_checked")
    suspend fun deleteAll()
}

@Dao
interface FeedLikeDao {
    @Upsert suspend fun upsert(rows: List<FeedLikeEntity>)
    @Upsert suspend fun upsert(row: FeedLikeEntity)

    @Query("DELETE FROM feed_likes WHERE tweet_id = :tweetId")
    suspend fun delete(tweetId: String)

    @Query("SELECT EXISTS(SELECT 1 FROM feed_likes WHERE tweet_id = :tweetId)")
    suspend fun exists(tweetId: String): Boolean

    @Query("SELECT * FROM feed_likes WHERE tweet_id = :tweetId")
    suspend fun getById(tweetId: String): FeedLikeEntity?

    @Query("SELECT * FROM feed_likes WHERE tweet_id = :tweetId")
    fun getByIdFlow(tweetId: String): Flow<FeedLikeEntity?>

    @Query("DELETE FROM feed_likes")
    suspend fun deleteAll()
}

@Dao
interface FeedRankDao {
    @Upsert suspend fun upsert(rows: List<FeedRankEntity>)

    @Query("SELECT COALESCE(MAX(snapshot_at), 0) FROM feed_rank")
    suspend fun currentSnapshotAt(): Long

    @Query("SELECT COUNT(*) FROM feed_rank")
    suspend fun count(): Int

    @Query("DELETE FROM feed_rank WHERE tweet_id IN (:tweetIds)")
    suspend fun deleteForTweets(tweetIds: List<String>)

    @Query("DELETE FROM feed_rank WHERE tweet_id NOT IN (SELECT tweet_id FROM feed_items)")
    suspend fun deleteOrphans()

    @Query("DELETE FROM feed_rank")
    suspend fun deleteAll()
}

@Dao
interface BookmarkDao {
    @Upsert suspend fun upsert(rows: List<BookmarkEntity>)
    @Upsert suspend fun upsert(row: BookmarkEntity)

    @Query("DELETE FROM bookmarks WHERE video_id = :videoId")
    suspend fun delete(videoId: String)

    @Query("SELECT * FROM bookmarks WHERE video_id = :videoId")
    suspend fun getById(videoId: String): BookmarkEntity?

    @Query("SELECT * FROM bookmarks WHERE category_id = :categoryId")
    suspend fun forCategory(categoryId: Long): List<BookmarkEntity>

    @Query("SELECT * FROM bookmarks WHERE video_id = :videoId")
    fun getByIdFlow(videoId: String): Flow<BookmarkEntity?>

    @Query("UPDATE bookmarks SET category_id = :newId WHERE category_id = :oldId")
    suspend fun remapCategory(oldId: Long, newId: Long)

    @Query(
        """
        SELECT account_handles
        FROM bookmarks
        WHERE account_handles IS NOT NULL
          AND TRIM(account_handles) != ''
        """
    )
    suspend fun accountHandleSelections(): List<String>

    @Query("DELETE FROM bookmarks")
    suspend fun deleteAll()

    @Query(
        """
        SELECT DISTINCT TRIM(custom_title)
        FROM bookmarks
        WHERE NULLIF(TRIM(COALESCE(custom_title, '')), '') IS NOT NULL
        ORDER BY LOWER(TRIM(custom_title)) ASC
        """
    )
    fun labelSuggestionsFlow(): Flow<List<String>>
}

@Dao
interface BookmarkCategoryDao {
    @Upsert suspend fun upsert(row: BookmarkCategoryEntity)
    @Upsert suspend fun upsert(rows: List<BookmarkCategoryEntity>)

    @Query("SELECT * FROM bookmark_categories ORDER BY created_at ASC, category_id ASC")
    suspend fun all(): List<BookmarkCategoryEntity>

    @Query("SELECT * FROM bookmark_categories ORDER BY created_at ASC, category_id ASC")
    fun allFlow(): Flow<List<BookmarkCategoryEntity>>

    @Query("SELECT * FROM bookmark_categories WHERE category_id = :id")
    suspend fun getById(id: Long): BookmarkCategoryEntity?

    @Query("DELETE FROM bookmark_categories WHERE category_id = :id")
    suspend fun delete(id: Long)

    @Query("DELETE FROM bookmark_categories WHERE category_id > 0")
    suspend fun deletePositive()

    @Query("DELETE FROM bookmark_categories")
    suspend fun deleteAll()
}

@Dao
interface FeedSeenDao {
    @Upsert suspend fun upsert(rows: List<FeedSeenEntity>)
    @Upsert suspend fun upsert(row: FeedSeenEntity)

    @Query("SELECT EXISTS(SELECT 1 FROM feed_seen WHERE tweet_id = :tweetId)")
    suspend fun exists(tweetId: String): Boolean

    @Query("SELECT * FROM feed_seen WHERE tweet_id = :tweetId")
    suspend fun getById(tweetId: String): FeedSeenEntity?

    @Query("DELETE FROM feed_seen WHERE tweet_id = :tweetId")
    suspend fun delete(tweetId: String)

    @Query("DELETE FROM feed_seen")
    suspend fun deleteAll()
}

@Dao
interface MomentViewDao {
    @Upsert suspend fun upsert(rows: List<MomentViewEntity>)
    @Upsert suspend fun upsert(row: MomentViewEntity)

    @Query("SELECT EXISTS(SELECT 1 FROM moment_views WHERE video_id = :videoId)")
    suspend fun exists(videoId: String): Boolean

    @Query("SELECT * FROM moment_views WHERE video_id = :videoId")
    suspend fun getById(videoId: String): MomentViewEntity?

    @Query("DELETE FROM moment_views WHERE video_id = :videoId")
    suspend fun delete(videoId: String)

    @Query("DELETE FROM moment_views")
    suspend fun deleteAll()
}

@Dao
interface WatchHistoryDao {
    @Upsert suspend fun upsert(rows: List<WatchHistoryEntity>)
    @Upsert suspend fun upsert(row: WatchHistoryEntity)

    @Query("SELECT * FROM watch_history WHERE video_id = :videoId")
    suspend fun getById(videoId: String): WatchHistoryEntity?

    @Query("SELECT * FROM watch_history WHERE video_id = :videoId")
    fun getByIdFlow(videoId: String): Flow<WatchHistoryEntity?>

    @Query("DELETE FROM watch_history WHERE video_id = :videoId")
    suspend fun delete(videoId: String)

    @Query("DELETE FROM watch_history")
    suspend fun deleteAll()
}

@Dao
interface MomentsCursorDao {
    @Upsert suspend fun upsert(row: MomentsCursorEntity)
    @Upsert suspend fun upsert(rows: List<MomentsCursorEntity>)

    @Query("SELECT * FROM moments_cursors WHERE scope = :scope")
    suspend fun get(scope: String): MomentsCursorEntity?

    @Query("SELECT * FROM moments_cursors WHERE scope = :scope")
    fun flow(scope: String): Flow<MomentsCursorEntity?>

    @Query("DELETE FROM moments_cursors WHERE scope = :scope")
    suspend fun delete(scope: String)

    @Query("DELETE FROM moments_cursors WHERE video_id = :videoId")
    suspend fun deleteForVideo(videoId: String)

    @Query("DELETE FROM moments_cursors")
    suspend fun deleteAll()
}

@Dao
interface MutedChannelDao {
    @Upsert suspend fun upsert(rows: List<MutedChannelEntity>)
    @Upsert suspend fun upsert(row: MutedChannelEntity)

    @Query("DELETE FROM muted_channels WHERE channel_id = :channelId")
    suspend fun delete(channelId: String)

    @Query("SELECT EXISTS(SELECT 1 FROM muted_channels WHERE channel_id = :channelId)")
    suspend fun exists(channelId: String): Boolean

    @Query("SELECT * FROM muted_channels WHERE channel_id = :channelId")
    suspend fun getById(channelId: String): MutedChannelEntity?

    @Query("SELECT * FROM muted_channels WHERE channel_id = :channelId")
    fun getByIdFlow(channelId: String): Flow<MutedChannelEntity?>

    @Query("SELECT * FROM muted_channels ORDER BY channel_id ASC")
    fun allFlow(): Flow<List<MutedChannelEntity>>

    @Query(
        """
        SELECT mc.*, cp.handle, COALESCE(NULLIF(cp.display_name, ''), c.name) AS display_name
        FROM muted_channels mc
        LEFT JOIN channel_profiles cp ON cp.channel_id = mc.channel_id
        LEFT JOIN channels c ON c.channel_id = mc.channel_id
        ORDER BY COALESCE(NULLIF(cp.display_name, ''), cp.handle, c.name, mc.channel_id) COLLATE NOCASE
        """
    )
    fun displayFlow(): Flow<List<MutedChannelDisplay>>

    @Query("DELETE FROM muted_channels")
    suspend fun deleteAll()
}

@Dao
interface ChannelFollowDao {
    @Upsert suspend fun upsert(rows: List<ChannelFollowEntity>)
    @Upsert suspend fun upsert(row: ChannelFollowEntity)

    @Query("DELETE FROM channel_follows WHERE channel_id = :channelId")
    suspend fun delete(channelId: String)

    @Query("SELECT EXISTS(SELECT 1 FROM channel_follows WHERE channel_id = :channelId)")
    suspend fun exists(channelId: String): Boolean

    @Query("SELECT * FROM channel_follows WHERE channel_id = :channelId")
    suspend fun getById(channelId: String): ChannelFollowEntity?

    @Query("SELECT * FROM channel_follows")
    fun allFlow(): Flow<List<ChannelFollowEntity>>

    @Query("DELETE FROM channel_follows")
    suspend fun deleteAll()
}

@Dao
interface ChannelStarDao {
    @Upsert suspend fun upsert(rows: List<ChannelStarEntity>)
    @Upsert suspend fun upsert(row: ChannelStarEntity)

    @Query("DELETE FROM channel_stars WHERE channel_id = :channelId")
    suspend fun delete(channelId: String)

    @Query("SELECT EXISTS(SELECT 1 FROM channel_stars WHERE channel_id = :channelId)")
    suspend fun exists(channelId: String): Boolean

    @Query("SELECT * FROM channel_stars WHERE channel_id = :channelId")
    suspend fun getById(channelId: String): ChannelStarEntity?

    @Query("SELECT * FROM channel_stars")
    fun allFlow(): Flow<List<ChannelStarEntity>>

    @Query("DELETE FROM channel_stars")
    suspend fun deleteAll()
}

@Dao
interface ChannelSettingDao {
    @Upsert suspend fun upsert(rows: List<ChannelSettingEntity>)
    @Upsert suspend fun upsert(row: ChannelSettingEntity)

    @Query("SELECT * FROM channel_settings WHERE channel_id = :channelId")
    suspend fun getById(channelId: String): ChannelSettingEntity?

    @Query("SELECT * FROM channel_settings WHERE channel_id = :channelId")
    fun getByIdFlow(channelId: String): Flow<ChannelSettingEntity?>

    @Query("DELETE FROM channel_settings WHERE channel_id = :channelId")
    suspend fun delete(channelId: String)

    @Query("DELETE FROM channel_settings")
    suspend fun deleteAll()
}
