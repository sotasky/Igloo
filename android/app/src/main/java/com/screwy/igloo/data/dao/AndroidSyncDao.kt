package com.screwy.igloo.data.dao

import androidx.room.Dao
import androidx.room.Query
import androidx.room.Upsert
import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.data.entity.AndroidSyncHeadEntity
import com.screwy.igloo.data.entity.AndroidSyncStateEntity
import kotlinx.coroutines.flow.Flow

/** SQL predicate for the locally playable primary binary of a YouTube video. */
internal const val youtubeVideoPrimaryAssetSql =
    """(
       asset_kind = 'video_stream'
       OR (asset_kind = 'post_media' AND LOWER(COALESCE(content_type, '')) LIKE 'video/%')
       OR (asset_kind = 'post_audio' AND LOWER(COALESCE(content_type, '')) LIKE 'audio/%')
    )"""

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

    @Query("DELETE FROM android_sync_heads WHERE owner_id = :ownerId")
    suspend fun deleteHeadsForOwnerId(ownerId: String)

    @Query(
        """
        SELECT * FROM android_sync_heads
        WHERE (retention_bucket = 'feed' AND (:feedDays = 0 OR retain_at_ms <= :feedCutoffMs))
           OR (
                retention_bucket = 'youtube'
                AND owner_kind != 'video'
                AND (:youtubeDays = 0 OR retain_at_ms <= :youtubeCutoffMs)
              )
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
        WHERE owner_kind = :ownerKind AND owner_id = :ownerId
        ORDER BY media_index, asset_id
        """,
    )
    suspend fun assetsForOwner(ownerKind: String, ownerId: String): List<AndroidSyncAssetEntity>

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

    @Query(
        """
        SELECT * FROM android_sync_assets
        WHERE owner_kind = 'youtube_video'
          AND owner_id = :videoId
          AND ${youtubeVideoPrimaryAssetSql}
          AND local_path IS NOT NULL
        ORDER BY media_index, asset_id
        """,
    )
    suspend fun localYoutubeVideoPrimaryAssets(videoId: String): List<AndroidSyncAssetEntity>

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
        WHERE owner_kind = 'youtube_video'
          AND owner_id = :videoId
          AND ${youtubeVideoPrimaryAssetSql}
          AND local_path IS NOT NULL
        """,
    )
    suspend fun resetVerifiedLocalPathsForYoutubeVideoPrimaryAssets(videoId: String): Int

    @Query(
        """
        UPDATE android_sync_assets
        SET local_path = NULL, verified_at_ms = NULL, next_attempt_at_ms = 0
        WHERE asset_id IN (:assetIds) AND local_path IS NOT NULL
        """,
    )
    suspend fun resetVerifiedLocalPaths(assetIds: List<String>): Int

    @Query(
        """
        UPDATE android_sync_assets
        SET next_attempt_at_ms = 0
        WHERE owner_kind = 'youtube_video'
          AND owner_id = :videoId
          AND ${youtubeVideoPrimaryAssetSql}
          AND state = 'ready'
        """,
    )
    suspend fun prioritizeYoutubeVideoPrimaryAssets(videoId: String): Int

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
        SELECT local_path FROM android_sync_assets
        WHERE owner_kind = 'youtube_video'
          AND ${youtubeVideoPrimaryAssetSql}
          AND local_path IS NOT NULL
          AND bucket IN (:buckets)
        """,
    )
    suspend fun verifiedLocalPathsForYoutubeVideoPrimaryAssets(buckets: List<String>): List<String>

    @Query(
        """
        UPDATE android_sync_assets
        SET local_path = NULL, verified_at_ms = NULL, next_attempt_at_ms = 0
        WHERE owner_kind = 'youtube_video'
          AND ${youtubeVideoPrimaryAssetSql}
          AND local_path IS NOT NULL
          AND bucket IN (:buckets)
        """,
    )
    suspend fun resetVerifiedLocalPathsForYoutubeVideoPrimaryAssets(buckets: List<String>): Int

    @Query(
        """
        SELECT asa.*
        FROM android_sync_assets asa
        WHERE asa.state = 'ready'
          AND asa.local_path IS NULL
          AND asa.next_attempt_at_ms <= :nowMs
          AND (
            NOT ${youtubeVideoPrimaryAssetSql}
            OR asa.owner_kind != 'youtube_video'
            OR EXISTS (
              SELECT 1 FROM offline_video_downloads saved
              WHERE saved.video_id = asa.owner_id
                AND saved.state IN ('requested', 'downloaded')
            )
            OR (
              NOT EXISTS (
                SELECT 1 FROM offline_video_downloads removed
                WHERE removed.video_id = asa.owner_id AND removed.state = 'removed'
              )
              AND EXISTS (
                SELECT 1
                FROM videos v
                WHERE v.video_id = asa.owner_id
                  AND COALESCE(v.is_temp, 0) = 0
                  AND v.published_at >= :youtubeCutoffMs
              )
            )
          )
        ORDER BY
          CASE WHEN asa.owner_kind = 'youtube_video'
                  AND ${youtubeVideoPrimaryAssetSql}
                  AND EXISTS (
                    SELECT 1 FROM offline_video_downloads requested
                    WHERE requested.video_id = asa.owner_id AND requested.state = 'requested'
                  )
               THEN 0 ELSE 1 END,
          asa.asset_id
        LIMIT :limit
        """,
    )
    suspend fun claimableAssets(
        nowMs: Long,
        youtubeCutoffMs: Long,
        limit: Int,
    ): List<AndroidSyncAssetEntity>

    @Query(
        """
        SELECT asa.*
        FROM android_sync_assets asa
        JOIN videos v ON v.video_id = asa.owner_id
        WHERE asa.owner_kind = 'youtube_video'
          AND ${youtubeVideoPrimaryAssetSql}
          AND asa.local_path IS NOT NULL
          AND NOT EXISTS (
            SELECT 1 FROM offline_video_downloads saved
            WHERE saved.video_id = asa.owner_id
              AND saved.state IN ('requested', 'downloaded')
          )
          AND (
            COALESCE(v.is_temp, 0) = 1
            OR v.published_at < :youtubeCutoffMs
          )
        ORDER BY asa.asset_id
        """,
    )
    suspend fun expiredAutomaticYoutubeVideoPrimaryAssets(youtubeCutoffMs: Long): List<AndroidSyncAssetEntity>

    @Query(
        """
        UPDATE android_sync_assets
        SET local_path = :localPath, verified_at_ms = :nowMs, next_attempt_at_ms = 0
        WHERE asset_id = :assetId AND revision = :revision AND state = 'ready'
          AND NOT (
            owner_kind = 'youtube_video'
            AND ${youtubeVideoPrimaryAssetSql}
            AND EXISTS (
              SELECT 1 FROM offline_video_downloads removed
              WHERE removed.video_id = android_sync_assets.owner_id
                AND removed.state = 'removed'
            )
          )
        """,
    )
    suspend fun markVerified(assetId: String, revision: Long, localPath: String, nowMs: Long): Int

    @Query(
        """
        UPDATE offline_video_downloads
        SET state = 'downloaded', updated_at_ms = :nowMs
        WHERE video_id = :videoId AND state = 'requested'
        """,
    )
    suspend fun markOfflineYoutubeVideoDownloaded(videoId: String, nowMs: Long): Int

    @Query(
        """
        UPDATE offline_video_downloads
        SET state = 'removed', updated_at_ms = :nowMs
        WHERE state IN ('requested', 'downloaded')
          AND video_id IN (
            SELECT owner_id
            FROM android_sync_assets
            WHERE owner_kind = 'youtube_video'
              AND ${youtubeVideoPrimaryAssetSql}
              AND (:bucket IS NULL OR bucket = :bucket)
          )
        """,
    )
    suspend fun markOfflineYoutubeDownloadsRemovedForPrimaryAssets(bucket: String?, nowMs: Long): Int

    @Query(
        """
        UPDATE offline_video_downloads
        SET state = 'removed', updated_at_ms = :nowMs
        WHERE video_id = :videoId AND state IN ('requested', 'downloaded')
        """,
    )
    suspend fun markOfflineYoutubeDownloadRemoved(videoId: String, nowMs: Long): Int

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
            SELECT saved.video_id
            FROM offline_video_downloads saved
            JOIN videos v ON v.video_id = saved.video_id
            JOIN android_sync_assets asa
              ON asa.owner_id = saved.video_id
             AND asa.owner_kind = 'youtube_video'
             AND ${youtubeVideoPrimaryAssetSql}
             AND asa.local_path IS NOT NULL
            WHERE saved.state = 'downloaded' AND COALESCE(v.is_temp, 0) = 0
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
