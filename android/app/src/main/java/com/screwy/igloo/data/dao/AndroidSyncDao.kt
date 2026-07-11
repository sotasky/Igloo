package com.screwy.igloo.data.dao

import androidx.room.Dao
import androidx.room.Query
import androidx.room.Upsert
import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.data.entity.AndroidSyncHeadEntity
import com.screwy.igloo.data.entity.AndroidSyncStateEntity
import kotlinx.coroutines.flow.Flow

data class AndroidSyncHealthCounts(
    val total: Int,
    val verified: Int,
    val pending: Int,
    val missing: Int,
    val verifiedBytes: Long,
)

data class AndroidSyncBucketStats(
    val bucket: String,
    val entries: Int,
    val cached: Int,
    val bytes: Long,
)

@Dao
interface AndroidSyncDao {
    @Query("SELECT * FROM android_sync_state WHERE id = 1")
    suspend fun syncState(): AndroidSyncStateEntity?

    @Upsert suspend fun upsertSyncState(row: AndroidSyncStateEntity)

    @Upsert suspend fun upsertHead(row: AndroidSyncHeadEntity)

    @Query("UPDATE android_sync_heads SET bootstrap_seen = 0")
    suspend fun markHeadsUnseen()

    @Query("SELECT * FROM android_sync_heads WHERE bootstrap_seen = 0 ORDER BY owner_kind, owner_id")
    suspend fun unseenHeads(): List<AndroidSyncHeadEntity>

    @Query(
        """
        DELETE FROM android_sync_heads
        WHERE owner_kind = :ownerKind AND owner_id = :ownerId
        """,
    )
    suspend fun deleteHead(ownerKind: String, ownerId: String)

    @Query(
        """
        SELECT * FROM android_sync_heads
        WHERE (retention_bucket = 'feed' AND (:feedDays = 0 OR retain_at_ms <= :feedCutoffMs))
           OR (retention_bucket = 'youtube' AND (:youtubeDays = 0 OR retain_at_ms <= :youtubeCutoffMs))
           OR (retention_bucket = 'moments' AND (:momentsDays = 0 OR retain_at_ms <= :momentsCutoffMs))
           OR (retention_bucket = 'story' AND (:storyHours = 0 OR retain_at_ms <= :storyCutoffMs))
        ORDER BY owner_kind, owner_id
        """,
    )
    suspend fun expiredHeads(
        feedDays: Int,
        feedCutoffMs: Long,
        youtubeDays: Int,
        youtubeCutoffMs: Long,
        momentsDays: Int,
        momentsCutoffMs: Long,
        storyHours: Int,
        storyCutoffMs: Long,
    ): List<AndroidSyncHeadEntity>

    @Query("SELECT COUNT(*) FROM android_sync_heads")
    suspend fun headCount(): Int

    @Query("SELECT owner_id FROM android_sync_heads WHERE owner_kind = :ownerKind")
    suspend fun headIds(ownerKind: String): List<String>

    @Query(
        """
        DELETE FROM android_sync_heads
        WHERE (owner_kind = 'feed_rank'
               AND NOT EXISTS (SELECT 1 FROM feed_items WHERE tweet_id = android_sync_heads.owner_id))
           OR (owner_kind = 'retweet_sources'
               AND NOT EXISTS (SELECT 1 FROM feed_items WHERE content_hash = android_sync_heads.owner_id))
           OR (owner_kind = 'channel'
               AND NOT EXISTS (SELECT 1 FROM channels WHERE channel_id = android_sync_heads.owner_id)
               AND NOT EXISTS (SELECT 1 FROM channel_profiles WHERE channel_id = android_sync_heads.owner_id))
           OR (owner_kind = 'asset'
               AND NOT EXISTS (SELECT 1 FROM android_sync_assets WHERE asset_id = android_sync_heads.owner_id))
        """,
    )
    suspend fun deleteOrphanHeads(): Int

    @Query("SELECT EXISTS(SELECT 1 FROM feed_items WHERE tweet_id = :tweetId)")
    suspend fun hasFeed(tweetId: String): Boolean

    @Query("SELECT EXISTS(SELECT 1 FROM videos WHERE video_id = :videoId)")
    suspend fun hasVideo(videoId: String): Boolean

    @Query("SELECT EXISTS(SELECT 1 FROM feed_items WHERE content_hash = :contentHash)")
    suspend fun hasContentHash(contentHash: String): Boolean

    @Upsert suspend fun upsertAsset(row: AndroidSyncAssetEntity)

    @Query("SELECT * FROM android_sync_assets WHERE asset_id = :assetId")
    suspend fun asset(assetId: String): AndroidSyncAssetEntity?

    @Query("SELECT COUNT(*) FROM android_sync_assets")
    suspend fun assetCount(): Int

    @Query("SELECT * FROM android_sync_assets")
    suspend fun allAssets(): List<AndroidSyncAssetEntity>

    @Query("DELETE FROM android_sync_assets WHERE asset_id = :assetId")
    suspend fun deleteAsset(assetId: String)

    @Query("DELETE FROM android_sync_assets WHERE asset_id IN (:assetIds)")
    suspend fun deleteAssets(assetIds: List<String>)

    @Query("SELECT * FROM android_sync_assets WHERE asset_id IN (:assetIds)")
    suspend fun assets(assetIds: List<String>): List<AndroidSyncAssetEntity>

    @Query(
        """
        SELECT * FROM android_sync_assets
        WHERE owner_kind = :ownerKind AND owner_id = :ownerId
          AND (state != 'server_missing' OR local_path IS NOT NULL)
        ORDER BY media_index, asset_id
        """,
    )
    fun assetsForOwnerFlow(ownerKind: String, ownerId: String): Flow<List<AndroidSyncAssetEntity>>

    @Query(
        """
        SELECT * FROM android_sync_assets
        WHERE owner_kind = :ownerKind AND owner_id IN (:ownerIds)
          AND (state != 'server_missing' OR local_path IS NOT NULL)
        ORDER BY owner_id, media_index, asset_id
        """,
    )
    fun assetsForOwnersFlow(
        ownerKind: String,
        ownerIds: List<String>,
    ): Flow<List<AndroidSyncAssetEntity>>

    @Query("SELECT DISTINCT local_path FROM android_sync_assets WHERE local_path IS NOT NULL")
    suspend fun verifiedLocalPaths(): List<String>

    @Query(
        """
        SELECT DISTINCT local_path FROM android_sync_assets
        WHERE local_path IS NOT NULL AND (:bucket IS NULL OR bucket = :bucket)
        """,
    )
    suspend fun verifiedLocalPaths(bucket: String?): List<String>

    @Query(
        """
        SELECT DISTINCT local_path FROM android_sync_assets
        WHERE owner_kind = :ownerKind AND owner_id = :ownerId AND local_path IS NOT NULL
        """,
    )
    suspend fun verifiedLocalPathsForOwner(ownerKind: String, ownerId: String): List<String>

    @Query(
        """
        UPDATE android_sync_assets
        SET local_path = NULL, verified_at_ms = NULL, next_attempt_at_ms = 0
        WHERE owner_kind = :ownerKind AND owner_id = :ownerId AND local_path IS NOT NULL
        """,
    )
    suspend fun resetVerifiedLocalPathsForOwner(ownerKind: String, ownerId: String): Int

    @Query(
        """
        UPDATE android_sync_assets
        SET local_path = NULL, verified_at_ms = NULL, next_attempt_at_ms = 0
        WHERE local_path IS NOT NULL AND (:bucket IS NULL OR bucket = :bucket)
        """,
    )
    suspend fun resetVerifiedLocalPaths(bucket: String?): Int

    @Query(
        """
        SELECT * FROM android_sync_assets
        WHERE state = 'ready' AND local_path IS NULL AND next_attempt_at_ms <= :nowMs
        ORDER BY asset_id LIMIT :limit
        """,
    )
    suspend fun claimableAssets(nowMs: Long, limit: Int): List<AndroidSyncAssetEntity>

    @Query(
        """
        UPDATE android_sync_assets
        SET local_path = :localPath, verified_at_ms = :nowMs, next_attempt_at_ms = 0
        WHERE asset_id = :assetId AND revision = :revision AND state = 'ready'
        """,
    )
    suspend fun markVerified(assetId: String, revision: Long, localPath: String, nowMs: Long)

    @Query(
        """
        UPDATE android_sync_assets SET next_attempt_at_ms = :nextAttemptAtMs
        WHERE asset_id = :assetId AND revision = :revision
        """,
    )
    suspend fun deferAsset(assetId: String, revision: Long, nextAttemptAtMs: Long)

    @Query(
        """
        SELECT bucket,
               COUNT(*) AS entries,
               SUM(CASE WHEN local_path IS NOT NULL THEN 1 ELSE 0 END) AS cached,
               COALESCE(SUM(CASE WHEN local_path IS NOT NULL THEN size_bytes ELSE 0 END), 0) AS bytes
        FROM android_sync_assets
        GROUP BY bucket ORDER BY bucket
        """,
    )
    suspend fun cacheStatsByBucket(): List<AndroidSyncBucketStats>

    @Query(
        """
        SELECT COUNT(*) AS total,
               SUM(CASE WHEN state = 'ready' AND local_path IS NOT NULL THEN 1 ELSE 0 END) AS verified,
               SUM(CASE WHEN state = 'ready' AND local_path IS NULL THEN 1 ELSE 0 END) AS pending,
               SUM(CASE WHEN state = 'server_missing' THEN 1 ELSE 0 END) AS missing,
               COALESCE(SUM(CASE WHEN local_path IS NOT NULL THEN size_bytes ELSE 0 END), 0) AS verifiedBytes
        FROM android_sync_assets
        """,
    )
    suspend fun healthCounts(): AndroidSyncHealthCounts

    @Query(
        """
        WITH RECURSIVE protected(owner_id) AS (
            SELECT tweet_id FROM feed_likes
            UNION SELECT video_id FROM bookmarks
            UNION
            SELECT fi.reply_to_status
            FROM feed_items fi
            JOIN protected p ON p.owner_id = fi.tweet_id
            WHERE COALESCE(fi.reply_to_status, '') != ''
        )
        SELECT owner_id FROM protected
        """,
    )
    suspend fun protectedContentIds(): List<String>

    @Query(
        """
        SELECT channel_id FROM channel_follows
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
        """,
    )
    suspend fun protectedChannelIds(): List<String>

    @Query(
        """
        SELECT tweet_id AS owner_id FROM feed_items
        UNION SELECT tweet_id FROM feed_likes
        UNION SELECT video_id FROM bookmarks
        UNION SELECT quote_tweet_id FROM feed_items WHERE quote_tweet_id IS NOT NULL
        UNION SELECT canonical_tweet_id FROM feed_items WHERE canonical_tweet_id IS NOT NULL
        UNION SELECT video_id FROM videos
        UNION SELECT channel_id FROM channels
        UNION SELECT channel_id FROM channel_profiles
        UNION SELECT channel_id FROM feed_items WHERE channel_id IS NOT NULL
        UNION SELECT source_channel_id FROM feed_items WHERE source_channel_id IS NOT NULL
        UNION SELECT quote_channel_id FROM feed_items WHERE quote_channel_id IS NOT NULL
        UNION SELECT reply_channel_id FROM feed_items WHERE reply_channel_id IS NOT NULL
        UNION SELECT reposter_channel_id FROM feed_items WHERE reposter_channel_id IS NOT NULL
        UNION SELECT reposter_channel_id FROM video_repost_sources
        UNION SELECT retweeter_channel_id FROM retweet_sources
        UNION SELECT author_id FROM video_comments WHERE author_id IS NOT NULL
        UNION SELECT channel_id FROM channel_follows
        UNION SELECT channel_id FROM channel_stars
        UNION SELECT channel_id FROM muted_channels
        UNION SELECT channel_id FROM channel_settings
              WHERE media_only IS NOT NULL
                 OR include_reposts IS NOT NULL
                 OR media_download_limit IS NOT NULL
                 OR max_videos IS NOT NULL
                 OR download_subtitles IS NOT NULL
        """,
    )
    suspend fun retainedAssetOwnerIds(): List<String>

}
