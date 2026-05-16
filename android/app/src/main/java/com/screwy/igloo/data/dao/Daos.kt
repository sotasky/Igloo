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
import com.screwy.igloo.data.entity.BookmarkEntity
import com.screwy.igloo.data.entity.BookmarkLabelEntity
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
 *  - `deleteAll` is the logout/wipe primitive — belt-and-suspenders on top of the
 *    per-user DB file delete in DatabaseHolder.
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

    @Query("DELETE FROM feed_items")
    suspend fun deleteAll()

    @Query("SELECT COUNT(*) FROM feed_items")
    suspend fun count(): Int

    @Query("SELECT COALESCE(MAX(sync_seq), 0) FROM feed_items")
    suspend fun maxSyncSeq(): Long

    /**
     * Prune Twitter posts past retention, respecting side-table protection.
     * Feed likes OR bookmarks save the row. Retention cutoff is server-signaled via the inbound delta; the client
     * just applies this predicate.
     */
    @Query(
        """
        DELETE FROM feed_items
        WHERE published_at < :cutoffMs
          AND NOT EXISTS (SELECT 1 FROM feed_likes WHERE tweet_id = feed_items.tweet_id)
          AND NOT EXISTS (SELECT 1 FROM bookmarks  WHERE video_id = feed_items.tweet_id)
        """
    )
    suspend fun pruneExpired(cutoffMs: Long): Int
}

@Dao
interface FeedThreadContextDao {
    @Upsert suspend fun upsert(rows: List<FeedThreadContextEntity>)

    @Query("DELETE FROM feed_thread_context WHERE leaf_tweet_id = :leafTweetId")
    suspend fun deleteForLeaf(leafTweetId: String)

    @Query("DELETE FROM feed_thread_context")
    suspend fun deleteAll()

    @Transaction
    suspend fun replaceForLeaf(leafTweetId: String, rows: List<FeedThreadContextEntity>) {
        deleteForLeaf(leafTweetId)
        if (rows.isNotEmpty()) upsert(rows)
    }
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

    @Query("DELETE FROM videos")
    suspend fun deleteAll()

    @Query("SELECT COUNT(*) FROM videos")
    suspend fun count(): Int

    @Query("SELECT COALESCE(MAX(sync_seq), 0) FROM videos WHERE channel_id LIKE 'youtube_%'")
    suspend fun maxYoutubeSyncSeq(): Long

    @Query("SELECT COALESCE(MAX(sync_seq), 0) FROM videos WHERE channel_id NOT LIKE 'youtube_%'")
    suspend fun maxShortsSyncSeq(): Long

    @Query(
        """
        SELECT video_id FROM videos
        WHERE channel_id LIKE 'youtube_%'
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
        WHERE channel_id LIKE 'youtube_%'
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

    /**
     * Prune shorts rows (TikTok / Instagram) past retention, respecting side-table
     * protection. YouTube rows are excluded so the Videos tab can render faded
     * placeholders.
     */
    @Query(
        """
        DELETE FROM videos
        WHERE channel_id NOT LIKE 'youtube_%'
          AND published_at < :cutoffMs
          AND NOT EXISTS (SELECT 1 FROM bookmarks    WHERE video_id = videos.video_id)
          AND NOT EXISTS (SELECT 1 FROM moment_views WHERE video_id = videos.video_id)
        """
    )
    suspend fun pruneShorts(cutoffMs: Long): Int
}

@Dao
interface ChannelDao {
    @Upsert suspend fun upsert(rows: List<ChannelEntity>)
    @Upsert suspend fun upsert(row: ChannelEntity)

    @Query("SELECT * FROM channels WHERE channel_id = :channelId")
    fun getByIdFlow(channelId: String): Flow<ChannelEntity?>

    @Query("SELECT * FROM channels WHERE channel_id = :channelId")
    suspend fun getById(channelId: String): ChannelEntity?

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
    @Upsert suspend fun upsert(row: ChannelProfileEntity)

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
        ORDER BY
            CASE WHEN thread_order > 0 THEN 0 ELSE 1 END ASC,
            thread_order ASC,
            published_at DESC
        """
    )
    fun forVideoFlow(videoId: String): Flow<List<VideoCommentEntity>>

    @Query("DELETE FROM video_comments WHERE video_id = :videoId")
    suspend fun deleteForVideo(videoId: String)

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

    @Query("DELETE FROM retweet_sources")
    suspend fun deleteAll()
}

@Dao
interface VideoRepostSourceDao {
    @Upsert suspend fun upsert(rows: List<VideoRepostSourceEntity>)

    @Query("DELETE FROM video_repost_sources WHERE video_id = :videoId")
    suspend fun deleteForVideo(videoId: String)

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

    @Query("DELETE FROM sponsorblock_segments")
    suspend fun deleteAll()
}

@Dao
interface SponsorBlockCheckedDao {
    @Upsert suspend fun upsert(row: SponsorBlockCheckedEntity)

    @Query("SELECT * FROM sponsorblock_checked WHERE video_id = :videoId")
    suspend fun forVideo(videoId: String): SponsorBlockCheckedEntity?

    @Query("DELETE FROM sponsorblock_checked")
    suspend fun deleteAll()
}

@Dao
interface FeedLikeDao {
    @Upsert suspend fun upsert(row: FeedLikeEntity)

    @Query("DELETE FROM feed_likes WHERE tweet_id = :tweetId")
    suspend fun delete(tweetId: String)

    @Query("SELECT EXISTS(SELECT 1 FROM feed_likes WHERE tweet_id = :tweetId)")
    suspend fun exists(tweetId: String): Boolean

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

    @Query("DELETE FROM feed_rank")
    suspend fun deleteAll()
}

@Dao
interface BookmarkDao {
    @Upsert suspend fun upsert(row: BookmarkEntity)

    @Query("DELETE FROM bookmarks WHERE video_id = :videoId")
    suspend fun delete(videoId: String)

    @Query("SELECT * FROM bookmarks WHERE video_id = :videoId")
    suspend fun getById(videoId: String): BookmarkEntity?

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
interface BookmarkLabelDao {
    @Upsert suspend fun upsert(row: BookmarkLabelEntity)
    @Upsert suspend fun upsert(rows: List<BookmarkLabelEntity>)

    @Query(
        """
        WITH merged_labels AS (
            SELECT label
            FROM bookmark_labels
            WHERE TRIM(label) != ''
            UNION
            SELECT custom_title AS label
            FROM bookmarks
            WHERE custom_title IS NOT NULL
              AND TRIM(custom_title) != ''
        )
        SELECT label
        FROM merged_labels
        ORDER BY LOWER(label) ASC
        """
    )
    fun labelSuggestionsFlow(): Flow<List<String>>

    @Query("SELECT * FROM bookmark_labels ORDER BY LOWER(label) ASC")
    suspend fun all(): List<BookmarkLabelEntity>

    @Query("DELETE FROM bookmark_labels")
    suspend fun deleteAll()
}

@Dao
interface FeedSeenDao {
    @Upsert suspend fun upsert(row: FeedSeenEntity)

    @Query("SELECT EXISTS(SELECT 1 FROM feed_seen WHERE tweet_id = :tweetId)")
    suspend fun exists(tweetId: String): Boolean

    @Query("DELETE FROM feed_seen")
    suspend fun deleteAll()
}

@Dao
interface MomentViewDao {
    @Upsert suspend fun upsert(row: MomentViewEntity)

    @Query("SELECT EXISTS(SELECT 1 FROM moment_views WHERE video_id = :videoId)")
    suspend fun exists(videoId: String): Boolean

    @Query("DELETE FROM moment_views")
    suspend fun deleteAll()
}

@Dao
interface WatchHistoryDao {
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
interface MutedAccountDao {
    @Upsert suspend fun upsert(row: MutedAccountEntity)

    @Query("DELETE FROM muted_accounts WHERE handle = :handle")
    suspend fun delete(handle: String)

    @Query("SELECT EXISTS(SELECT 1 FROM muted_accounts WHERE handle = :handle)")
    suspend fun exists(handle: String): Boolean

    @Query("SELECT * FROM muted_accounts ORDER BY handle ASC")
    fun allFlow(): Flow<List<MutedAccountEntity>>

    @Query("DELETE FROM muted_accounts")
    suspend fun deleteAll()
}

@Dao
interface ChannelFollowDao {
    @Upsert suspend fun upsert(row: ChannelFollowEntity)

    @Query("DELETE FROM channel_follows WHERE channel_id = :channelId")
    suspend fun delete(channelId: String)

    @Query("SELECT EXISTS(SELECT 1 FROM channel_follows WHERE channel_id = :channelId)")
    suspend fun exists(channelId: String): Boolean

    @Query("SELECT * FROM channel_follows")
    fun allFlow(): Flow<List<ChannelFollowEntity>>

    @Query("DELETE FROM channel_follows")
    suspend fun deleteAll()
}

@Dao
interface ChannelStarDao {
    @Upsert suspend fun upsert(row: ChannelStarEntity)

    @Query("DELETE FROM channel_stars WHERE channel_id = :channelId")
    suspend fun delete(channelId: String)

    @Query("SELECT EXISTS(SELECT 1 FROM channel_stars WHERE channel_id = :channelId)")
    suspend fun exists(channelId: String): Boolean

    @Query("SELECT * FROM channel_stars")
    fun allFlow(): Flow<List<ChannelStarEntity>>

    @Query("DELETE FROM channel_stars")
    suspend fun deleteAll()
}

@Dao
interface ChannelSettingDao {
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
