package com.screwy.igloo.data.dao

import androidx.room.Dao
import androidx.room.Query
import androidx.room.Transaction
import androidx.room.Upsert
import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.data.entity.AndroidSyncGenerationEntity
import com.screwy.igloo.data.entity.AndroidSyncItemEntity
import kotlinx.coroutines.flow.Flow

data class AndroidSyncHealthCounts(
    val total: Int,
    val verified: Int,
    val pending: Int,
    val failed: Int,
    val missing: Int,
    val verifiedBytes: Long,
)

data class AndroidSyncContentPruneCounts(
    val videos: Int,
    val feedItems: Int,
    val channels: Int,
    val channelProfiles: Int,
    val legacyAssets: Int,
    val sideRows: Int,
)

@Dao
interface AndroidSyncDao {

    @Upsert suspend fun upsertItems(rows: List<AndroidSyncItemEntity>)

    @Query(
        """
        INSERT INTO android_sync_generations (
            generation_id, created_at_ms, status, source_version, retention_json,
            item_count, asset_count, ready_asset_count, server_missing_asset_count,
            total_bytes, content_counts_json, asset_counts_json,
            items_imported_at_ms, assets_imported_at_ms, items_importer_version
        ) VALUES (
            :generationId, :createdAtMs, :status, :sourceVersion, :retentionJson,
            :itemCount, :assetCount, :readyAssetCount, :serverMissingAssetCount,
            :totalBytes, :contentCountsJson, :assetCountsJson,
            :itemsImportedAtMs, :assetsImportedAtMs, :itemsImporterVersion
        )
        ON CONFLICT(generation_id) DO UPDATE SET
            created_at_ms = excluded.created_at_ms,
            status = excluded.status,
            source_version = excluded.source_version,
            retention_json = excluded.retention_json,
            item_count = excluded.item_count,
            asset_count = excluded.asset_count,
            ready_asset_count = excluded.ready_asset_count,
            server_missing_asset_count = excluded.server_missing_asset_count,
            total_bytes = excluded.total_bytes,
            content_counts_json = excluded.content_counts_json,
            asset_counts_json = excluded.asset_counts_json,
            items_imported_at_ms = COALESCE(android_sync_generations.items_imported_at_ms, excluded.items_imported_at_ms),
            assets_imported_at_ms = COALESCE(android_sync_generations.assets_imported_at_ms, excluded.assets_imported_at_ms),
            items_importer_version = android_sync_generations.items_importer_version
        """
    )
    suspend fun upsertGeneration(
        generationId: String,
        createdAtMs: Long,
        status: String,
        sourceVersion: String,
        retentionJson: String,
        itemCount: Int,
        assetCount: Int,
        readyAssetCount: Int,
        serverMissingAssetCount: Int,
        totalBytes: Long,
        contentCountsJson: String,
        assetCountsJson: String,
        itemsImportedAtMs: Long?,
        assetsImportedAtMs: Long?,
        itemsImporterVersion: Int,
    )

    suspend fun upsertGeneration(row: AndroidSyncGenerationEntity) {
        upsertGeneration(
            generationId = row.generationId,
            createdAtMs = row.createdAtMs,
            status = row.status,
            sourceVersion = row.sourceVersion,
            retentionJson = row.retentionJson,
            itemCount = row.itemCount,
            assetCount = row.assetCount,
            readyAssetCount = row.readyAssetCount,
            serverMissingAssetCount = row.serverMissingAssetCount,
            totalBytes = row.totalBytes,
            contentCountsJson = row.contentCountsJson,
            assetCountsJson = row.assetCountsJson,
            itemsImportedAtMs = row.itemsImportedAtMs,
            assetsImportedAtMs = row.assetsImportedAtMs,
            itemsImporterVersion = row.itemsImporterVersion,
        )
    }

    @Query(
        """
        SELECT cur.seq
        FROM android_sync_items cur
        WHERE cur.generation_id = :generationId
          AND cur.seq > :afterSeq
          AND cur.seq <= :toSeq
          AND NOT EXISTS (
              SELECT 1
              FROM android_sync_items prev
              JOIN android_sync_generations gen
                ON gen.generation_id = prev.generation_id
              WHERE prev.generation_id != :generationId
                AND gen.items_imported_at_ms IS NOT NULL
                AND gen.items_importer_version = :importerVersion
                AND prev.item_kind = cur.item_kind
                AND prev.item_id = cur.item_id
                AND prev.payload_json = cur.payload_json
              LIMIT 1
          )
        """
    )
    suspend fun changedItemSeqsFromPreviousImportedGenerations(
        generationId: String,
        afterSeq: Long,
        toSeq: Long,
        importerVersion: Int,
    ): List<Long>

    @Query("SELECT generation_id FROM android_sync_generations ORDER BY created_at_ms DESC LIMIT 1")
    suspend fun latestGenerationId(): String?

    @Query(
        """
        SELECT COUNT(*) FROM (
            SELECT items_imported_at_ms, assets_imported_at_ms, items_importer_version
            FROM android_sync_generations
            ORDER BY created_at_ms DESC
            LIMIT 1
        )
        WHERE items_imported_at_ms IS NULL
           OR items_importer_version != :itemImporterVersion
           OR assets_imported_at_ms IS NULL
        """
    )
    suspend fun countLatestIncompleteImports(itemImporterVersion: Int): Int

    @Query(
        """
        SELECT COUNT(*) FROM android_sync_generations
        WHERE generation_id = :generationId
          AND items_imported_at_ms IS NOT NULL
        """
    )
    suspend fun countItemsImportComplete(generationId: String): Int

    @Query(
        """
        SELECT COUNT(*) FROM android_sync_generations
        WHERE generation_id = :generationId
          AND items_imported_at_ms IS NOT NULL
          AND items_importer_version = :importerVersion
        """
    )
    suspend fun countItemsImportCompleteForImporter(generationId: String, importerVersion: Int): Int

    @Query("SELECT items_importer_version FROM android_sync_generations WHERE generation_id = :generationId")
    suspend fun itemImporterVersion(generationId: String): Int?

    @Query(
        """
        SELECT COUNT(*) FROM android_sync_generations
        WHERE generation_id = :generationId
          AND assets_imported_at_ms IS NOT NULL
        """
    )
    suspend fun countAssetsImportComplete(generationId: String): Int

    @Query(
        """
        SELECT COUNT(*) FROM android_sync_generations
        WHERE generation_id = :generationId
          AND assets_imported_at_ms IS NOT NULL
          AND NOT EXISTS (
              SELECT 1 FROM android_sync_assets
              WHERE generation_id = :generationId
                AND server_state = 'ready'
                AND server_url NOT LIKE '/api/android/sync/generation/%/assets/%'
          )
        """
    )
    suspend fun countAssetsImportCompleteForCurrentContract(generationId: String): Int

    @Query("SELECT COALESCE(MAX(seq), 0) FROM android_sync_items WHERE generation_id = :generationId")
    suspend fun maxImportedItemSeq(generationId: String): Long

    @Query("SELECT COALESCE(MAX(seq), 0) FROM android_sync_assets WHERE generation_id = :generationId")
    suspend fun maxImportedAssetSeq(generationId: String): Long

    @Query(
        """
        SELECT local_path FROM android_sync_assets
        WHERE owner_id = :ownerId
          AND asset_kind = :assetKind
          AND server_state = 'ready'
          AND state = 'verified'
          AND COALESCE(local_path, '') != ''
        ORDER BY COALESCE(verified_at_ms, 0) DESC, generation_id DESC
        LIMIT 1
        """
    )
    suspend fun latestVerifiedLocalPath(ownerId: String, assetKind: String): String?

    @Query(
        """
        SELECT local_path FROM android_sync_assets
        WHERE owner_id = :ownerId
          AND asset_kind = :assetKind
          AND server_state = 'ready'
          AND state = 'verified'
          AND COALESCE(local_path, '') != ''
        ORDER BY COALESCE(verified_at_ms, 0) DESC, generation_id DESC
        LIMIT 1
        """
    )
    fun latestVerifiedLocalPathFlow(ownerId: String, assetKind: String): Flow<String?>

    @Query(
        """
        SELECT * FROM android_sync_assets
        WHERE owner_id = :ownerId
          AND asset_kind IN (:assetKinds)
          AND server_state = 'ready'
          AND state = 'verified'
          AND COALESCE(local_path, '') != ''
        ORDER BY COALESCE(verified_at_ms, 0) DESC, generation_id DESC, seq ASC
        """
    )
    fun latestVerifiedAssetsForOwnerFlow(ownerId: String, assetKinds: List<String>): Flow<List<AndroidSyncAssetEntity>>

    @Query(
        """
        SELECT * FROM android_sync_assets
        WHERE owner_id = :ownerId
          AND asset_kind IN (:assetKinds)
          AND server_state = 'ready'
          AND state = 'verified'
          AND COALESCE(local_path, '') != ''
        ORDER BY COALESCE(verified_at_ms, 0) DESC, generation_id DESC, seq ASC
        """
    )
    suspend fun latestVerifiedAssetsForOwner(ownerId: String, assetKinds: List<String>): List<AndroidSyncAssetEntity>

    @Query(
        """
        SELECT local_path FROM android_sync_assets
        WHERE owner_id = :ownerId
          AND asset_kind IN ('video_stream', 'post_media')
          AND server_state = 'ready'
          AND state = 'verified'
          AND COALESCE(local_path, '') != ''
          AND LOWER(COALESCE(content_type, '')) LIKE 'video/%'
        ORDER BY
          CASE WHEN asset_kind = 'video_stream' THEN 0 ELSE 1 END,
          COALESCE(verified_at_ms, 0) DESC,
          generation_id DESC
        LIMIT 1
        """
    )
    suspend fun latestVerifiedVideoLocalPath(ownerId: String): String?

    @Query(
        """
        SELECT local_path FROM android_sync_assets
        WHERE owner_id = :ownerId
          AND asset_kind IN ('video_stream', 'post_media')
          AND server_state = 'ready'
          AND state = 'verified'
          AND COALESCE(local_path, '') != ''
          AND LOWER(COALESCE(content_type, '')) LIKE 'video/%'
        ORDER BY
          CASE WHEN asset_kind = 'video_stream' THEN 0 ELSE 1 END,
          COALESCE(verified_at_ms, 0) DESC,
          generation_id DESC
        LIMIT 1
        """
    )
    fun latestVerifiedVideoLocalPathFlow(ownerId: String): Flow<String?>

    @Query(
        """
        UPDATE android_sync_generations
        SET items_imported_at_ms = :nowMs,
            items_importer_version = :importerVersion
        WHERE generation_id = :generationId
        """
    )
    suspend fun markItemsImported(generationId: String, nowMs: Long, importerVersion: Int)

    @Query("UPDATE android_sync_generations SET assets_imported_at_ms = :nowMs WHERE generation_id = :generationId")
    suspend fun markAssetsImported(generationId: String, nowMs: Long)

    @Transaction
    suspend fun pruneGenerationsExcept(generationId: String): Int {
        deleteItemsForOtherGenerations(generationId)
        deleteAssetsForOtherGenerations(generationId)
        return deleteOtherGenerations(generationId)
    }

    @Transaction
    suspend fun pruneContentOutsideGeneration(generationId: String): AndroidSyncContentPruneCounts {
        val videos = deleteVideosOutsideGeneration(generationId)
        val feedItems = deleteFeedItemsOutsideGeneration(generationId)
        val sideRows = deleteVideoCommentsWithoutVideo() +
            deleteVideoRepostSourcesWithoutVideo() +
            deleteSponsorBlockSegmentsWithoutVideo() +
            deleteSponsorBlockChecksWithoutVideo() +
            deleteWatchHistoryWithoutVideo() +
            deleteMomentViewsWithoutVideo() +
            deleteFeedSeenWithoutFeedItem() +
            deleteFeedRankWithoutFeedItem() +
            deleteFeedThreadContextWithoutFeedItem() +
            deleteRetweetSourcesWithoutFeedItem() +
            deleteChannelFollowsOutsideGeneration(generationId) +
            deleteChannelStarsOutsideGeneration(generationId) +
            deleteChannelSettingsOutsideGeneration(generationId)
        val channels = deleteChannelsOutsideGeneration(generationId)
        val channelProfiles = deleteChannelProfilesWithoutChannel(generationId)
        val legacyAssets = deleteLegacyAssetsWithoutOwner()
        return AndroidSyncContentPruneCounts(
            videos = videos,
            feedItems = feedItems,
            channels = channels,
            channelProfiles = channelProfiles,
            legacyAssets = legacyAssets,
            sideRows = sideRows,
        )
    }

    @Query(
        """
        DELETE FROM videos
        WHERE NOT EXISTS (
            SELECT 1
            FROM android_sync_items cur
            WHERE cur.generation_id = :generationId
              AND cur.item_kind = 'videos'
              AND cur.item_id = videos.video_id
        )
          AND NOT EXISTS (SELECT 1 FROM bookmarks WHERE video_id = videos.video_id)
          AND NOT EXISTS (SELECT 1 FROM feed_likes WHERE tweet_id = videos.video_id)
        """
    )
    suspend fun deleteVideosOutsideGeneration(generationId: String): Int

    @Query(
        """
        DELETE FROM feed_items
        WHERE NOT EXISTS (
            SELECT 1
            FROM android_sync_items cur
            WHERE cur.generation_id = :generationId
              AND cur.item_kind = 'feed_items'
              AND cur.item_id = feed_items.tweet_id
        )
          AND NOT EXISTS (SELECT 1 FROM bookmarks WHERE video_id = feed_items.tweet_id)
          AND NOT EXISTS (SELECT 1 FROM feed_likes WHERE tweet_id = feed_items.tweet_id)
        """
    )
    suspend fun deleteFeedItemsOutsideGeneration(generationId: String): Int

    @Query(
        """
        DELETE FROM video_comments
        WHERE NOT EXISTS (SELECT 1 FROM videos WHERE videos.video_id = video_comments.video_id)
        """
    )
    suspend fun deleteVideoCommentsWithoutVideo(): Int

    @Query(
        """
        DELETE FROM video_repost_sources
        WHERE NOT EXISTS (SELECT 1 FROM videos WHERE videos.video_id = video_repost_sources.video_id)
        """
    )
    suspend fun deleteVideoRepostSourcesWithoutVideo(): Int

    @Query(
        """
        DELETE FROM sponsorblock_segments
        WHERE NOT EXISTS (SELECT 1 FROM videos WHERE videos.video_id = sponsorblock_segments.video_id)
        """
    )
    suspend fun deleteSponsorBlockSegmentsWithoutVideo(): Int

    @Query(
        """
        DELETE FROM sponsorblock_checked
        WHERE NOT EXISTS (SELECT 1 FROM videos WHERE videos.video_id = sponsorblock_checked.video_id)
        """
    )
    suspend fun deleteSponsorBlockChecksWithoutVideo(): Int

    @Query(
        """
        DELETE FROM watch_history
        WHERE NOT EXISTS (SELECT 1 FROM videos WHERE videos.video_id = watch_history.video_id)
        """
    )
    suspend fun deleteWatchHistoryWithoutVideo(): Int

    @Query(
        """
        DELETE FROM moment_views
        WHERE NOT EXISTS (SELECT 1 FROM videos WHERE videos.video_id = moment_views.video_id)
        """
    )
    suspend fun deleteMomentViewsWithoutVideo(): Int

    @Query(
        """
        DELETE FROM feed_seen
        WHERE NOT EXISTS (SELECT 1 FROM feed_items WHERE feed_items.tweet_id = feed_seen.tweet_id)
        """
    )
    suspend fun deleteFeedSeenWithoutFeedItem(): Int

    @Query(
        """
        DELETE FROM feed_rank
        WHERE NOT EXISTS (SELECT 1 FROM feed_items WHERE feed_items.tweet_id = feed_rank.tweet_id)
        """
    )
    suspend fun deleteFeedRankWithoutFeedItem(): Int

    @Query(
        """
        DELETE FROM feed_thread_context
        WHERE NOT EXISTS (SELECT 1 FROM feed_items WHERE feed_items.tweet_id = feed_thread_context.leaf_tweet_id)
           OR NOT EXISTS (SELECT 1 FROM feed_items WHERE feed_items.tweet_id = feed_thread_context.ancestor_tweet_id)
        """
    )
    suspend fun deleteFeedThreadContextWithoutFeedItem(): Int

    @Query(
        """
        DELETE FROM retweet_sources
        WHERE NOT EXISTS (
            SELECT 1
            FROM feed_items
            WHERE COALESCE(feed_items.content_hash, '') = retweet_sources.content_hash
              AND COALESCE(feed_items.content_hash, '') != ''
        )
        """
    )
    suspend fun deleteRetweetSourcesWithoutFeedItem(): Int

    @Query(
        """
        DELETE FROM channels
        WHERE NOT EXISTS (
            SELECT 1
            FROM android_sync_items cur
            WHERE cur.generation_id = :generationId
              AND cur.item_kind = 'channels'
              AND cur.item_id = channels.channel_id
        )
          AND NOT EXISTS (SELECT 1 FROM channel_follows WHERE channel_id = channels.channel_id)
          AND NOT EXISTS (SELECT 1 FROM videos WHERE videos.channel_id = channels.channel_id)
          AND NOT EXISTS (SELECT 1 FROM feed_items WHERE feed_items.channel_id = channels.channel_id)
        """
    )
    suspend fun deleteChannelsOutsideGeneration(generationId: String): Int

    @Query(
        """
        DELETE FROM channel_follows
        WHERE NOT EXISTS (
            SELECT 1
            FROM android_sync_items cur
            WHERE cur.generation_id = :generationId
              AND cur.item_kind = 'channels'
              AND cur.item_id = channel_follows.channel_id
        )
          AND NOT EXISTS (SELECT 1 FROM videos WHERE videos.channel_id = channel_follows.channel_id)
          AND NOT EXISTS (SELECT 1 FROM feed_items WHERE feed_items.channel_id = channel_follows.channel_id)
          AND NOT EXISTS (
            SELECT 1 FROM outbox
            WHERE state = 'pending'
              AND kind = 'follow'
              AND item_id = channel_follows.channel_id
          )
        """
    )
    suspend fun deleteChannelFollowsOutsideGeneration(generationId: String): Int

    @Query(
        """
        DELETE FROM channel_stars
        WHERE NOT EXISTS (
            SELECT 1
            FROM android_sync_items cur
            WHERE cur.generation_id = :generationId
              AND cur.item_kind = 'channels'
              AND cur.item_id = channel_stars.channel_id
        )
          AND NOT EXISTS (SELECT 1 FROM videos WHERE videos.channel_id = channel_stars.channel_id)
          AND NOT EXISTS (SELECT 1 FROM feed_items WHERE feed_items.channel_id = channel_stars.channel_id)
          AND NOT EXISTS (
            SELECT 1 FROM outbox
            WHERE state = 'pending'
              AND kind = 'star'
              AND item_id = channel_stars.channel_id
          )
        """
    )
    suspend fun deleteChannelStarsOutsideGeneration(generationId: String): Int

    @Query(
        """
        DELETE FROM channel_settings
        WHERE NOT EXISTS (
            SELECT 1
            FROM android_sync_items cur
            WHERE cur.generation_id = :generationId
              AND cur.item_kind = 'channels'
              AND cur.item_id = channel_settings.channel_id
        )
          AND NOT EXISTS (SELECT 1 FROM videos WHERE videos.channel_id = channel_settings.channel_id)
          AND NOT EXISTS (SELECT 1 FROM feed_items WHERE feed_items.channel_id = channel_settings.channel_id)
          AND NOT EXISTS (
            SELECT 1 FROM outbox
            WHERE state = 'pending'
              AND kind = 'channel_setting'
              AND item_id = channel_settings.channel_id
          )
        """
    )
    suspend fun deleteChannelSettingsOutsideGeneration(generationId: String): Int

    @Query(
        """
        DELETE FROM channel_profiles
        WHERE NOT EXISTS (SELECT 1 FROM channels WHERE channels.channel_id = channel_profiles.channel_id)
          AND NOT EXISTS (
            SELECT 1
            FROM android_sync_items cur
            WHERE cur.generation_id = :generationId
              AND cur.item_kind = 'channel_profiles'
              AND cur.item_id = channel_profiles.channel_id
          )
        """
    )
    suspend fun deleteChannelProfilesWithoutChannel(generationId: String): Int

    @Query(
        """
        DELETE FROM media_inventory
        WHERE COALESCE(owner_id, '') != ''
          AND NOT EXISTS (SELECT 1 FROM videos WHERE videos.video_id = media_inventory.owner_id)
          AND NOT EXISTS (SELECT 1 FROM feed_items WHERE feed_items.tweet_id = media_inventory.owner_id)
          AND NOT EXISTS (SELECT 1 FROM channels WHERE channels.channel_id = media_inventory.owner_id)
        """
    )
    suspend fun deleteLegacyAssetsWithoutOwner(): Int

    @Query(
        """
        SELECT DISTINCT local_path
        FROM android_sync_assets
        WHERE generation_id = :generationId
          AND state = 'verified'
          AND COALESCE(local_path, '') != ''
        """
    )
    suspend fun verifiedLocalPaths(generationId: String): List<String>

    @Query("DELETE FROM android_sync_items WHERE generation_id != :generationId")
    suspend fun deleteItemsForOtherGenerations(generationId: String): Int

    @Query("DELETE FROM android_sync_assets WHERE generation_id != :generationId")
    suspend fun deleteAssetsForOtherGenerations(generationId: String): Int

    @Query("DELETE FROM android_sync_generations WHERE generation_id != :generationId")
    suspend fun deleteOtherGenerations(generationId: String): Int

    @Transaction
    suspend fun importAssets(rows: List<AndroidSyncAssetEntity>, nowMs: Long) {
        rows.forEach { row ->
            insertOrRefreshAsset(
                generationId = row.generationId,
                seq = row.seq,
                assetId = row.assetId,
                assetKind = row.assetKind,
                mediaIndex = row.mediaIndex,
                ownerId = row.ownerId,
                ownerKind = row.ownerKind,
                bucket = row.bucket,
                serverUrl = row.serverUrl,
                contentType = row.contentType,
                sizeBytes = row.sizeBytes,
                sha256 = row.sha256,
                serverState = row.serverState,
                requiredReason = row.requiredReason,
                subtitleIsAuto = row.subtitleIsAuto,
                audioLanguage = row.audioLanguage,
                effectiveRecencyMs = row.effectiveRecencyMs,
                nowMs = nowMs,
            )
        }
    }

    @Query(
        """
        INSERT INTO android_sync_assets (
            generation_id, seq, asset_id, asset_kind, media_index, owner_id, owner_kind,
            bucket, server_url, content_type, size_bytes, sha256, server_state,
            required_reason, subtitle_is_auto, audio_language, effective_recency_ms, state, local_path, file_size,
            verified_at_ms, attempt_count, next_attempt_at_ms, last_error, updated_at_ms
        ) VALUES (
            :generationId, :seq, :assetId, :assetKind, :mediaIndex, :ownerId, :ownerKind,
            :bucket, :serverUrl, :contentType, :sizeBytes, :sha256, :serverState,
            :requiredReason, :subtitleIsAuto, :audioLanguage, :effectiveRecencyMs,
            CASE WHEN :serverState = 'server_missing' THEN 'server_missing' ELSE 'desired' END,
            NULL, NULL, NULL, 0,
            CASE WHEN :serverState = 'server_missing' THEN 9223372036854775807 ELSE 0 END,
            NULL, :nowMs
        )
        ON CONFLICT(generation_id, asset_id, asset_kind) DO UPDATE SET
            seq = excluded.seq,
            media_index = excluded.media_index,
            owner_id = excluded.owner_id,
            owner_kind = excluded.owner_kind,
            bucket = excluded.bucket,
            server_url = excluded.server_url,
            content_type = excluded.content_type,
            size_bytes = excluded.size_bytes,
            sha256 = excluded.sha256,
            server_state = excluded.server_state,
            required_reason = excluded.required_reason,
            subtitle_is_auto = excluded.subtitle_is_auto,
            audio_language = excluded.audio_language,
            effective_recency_ms = excluded.effective_recency_ms,
            state = CASE
                WHEN excluded.server_state = 'server_missing' THEN 'server_missing'
                WHEN android_sync_assets.state = 'verified'
                     AND COALESCE(android_sync_assets.sha256, '') = COALESCE(excluded.sha256, '')
                     AND COALESCE(android_sync_assets.file_size, 0) = excluded.size_bytes THEN 'verified'
                WHEN android_sync_assets.state = 'failed'
                     AND COALESCE(android_sync_assets.sha256, '') = COALESCE(excluded.sha256, '')
                     AND COALESCE(android_sync_assets.server_url, '') = COALESCE(excluded.server_url, '') THEN 'failed'
                ELSE 'desired'
            END,
            local_path = CASE
                WHEN android_sync_assets.state = 'verified'
                     AND COALESCE(android_sync_assets.sha256, '') = COALESCE(excluded.sha256, '')
                     AND COALESCE(android_sync_assets.file_size, 0) = excluded.size_bytes THEN android_sync_assets.local_path
                ELSE NULL
            END,
            file_size = CASE
                WHEN android_sync_assets.state = 'verified'
                     AND COALESCE(android_sync_assets.sha256, '') = COALESCE(excluded.sha256, '')
                     AND COALESCE(android_sync_assets.file_size, 0) = excluded.size_bytes THEN android_sync_assets.file_size
                ELSE NULL
            END,
            verified_at_ms = CASE
                WHEN android_sync_assets.state = 'verified'
                     AND COALESCE(android_sync_assets.sha256, '') = COALESCE(excluded.sha256, '')
                     AND COALESCE(android_sync_assets.file_size, 0) = excluded.size_bytes THEN android_sync_assets.verified_at_ms
                ELSE NULL
            END,
            attempt_count = CASE
                WHEN excluded.server_state = 'server_missing' THEN android_sync_assets.attempt_count
                WHEN COALESCE(android_sync_assets.sha256, '') = COALESCE(excluded.sha256, '')
                     AND COALESCE(android_sync_assets.server_url, '') = COALESCE(excluded.server_url, '') THEN android_sync_assets.attempt_count
                ELSE 0
            END,
            next_attempt_at_ms = CASE
                WHEN excluded.server_state = 'server_missing' THEN 9223372036854775807
                WHEN COALESCE(android_sync_assets.sha256, '') = COALESCE(excluded.sha256, '')
                     AND COALESCE(android_sync_assets.server_url, '') = COALESCE(excluded.server_url, '') THEN android_sync_assets.next_attempt_at_ms
                ELSE 0
            END,
            last_error = CASE
                WHEN excluded.server_state = 'server_missing' THEN 'server_missing'
                WHEN COALESCE(android_sync_assets.sha256, '') = COALESCE(excluded.sha256, '')
                     AND COALESCE(android_sync_assets.server_url, '') = COALESCE(excluded.server_url, '') THEN android_sync_assets.last_error
                ELSE NULL
            END,
            updated_at_ms = :nowMs
        """
    )
    suspend fun insertOrRefreshAsset(
        generationId: String,
        seq: Long,
        assetId: String,
        assetKind: String,
        mediaIndex: Int,
        ownerId: String,
        ownerKind: String,
        bucket: String,
        serverUrl: String,
        contentType: String?,
        sizeBytes: Long,
        sha256: String?,
        serverState: String,
        requiredReason: String?,
        subtitleIsAuto: Boolean,
        audioLanguage: String?,
        effectiveRecencyMs: Long,
        nowMs: Long,
    )

    @Query(
        """
        UPDATE android_sync_assets
        SET state = 'verified',
            local_path = (
                SELECT prev.local_path
                FROM android_sync_assets prev
                WHERE prev.generation_id != :generationId
                  AND prev.asset_id = android_sync_assets.asset_id
                  AND prev.asset_kind = android_sync_assets.asset_kind
                  AND prev.server_state = 'ready'
                  AND prev.state = 'verified'
                  AND COALESCE(prev.local_path, '') != ''
                  AND COALESCE(prev.sha256, '') = COALESCE(android_sync_assets.sha256, '')
                  AND COALESCE(prev.file_size, 0) = android_sync_assets.size_bytes
                ORDER BY COALESCE(prev.verified_at_ms, 0) DESC, prev.generation_id DESC
                LIMIT 1
            ),
            file_size = size_bytes,
            verified_at_ms = (
                SELECT prev.verified_at_ms
                FROM android_sync_assets prev
                WHERE prev.generation_id != :generationId
                  AND prev.asset_id = android_sync_assets.asset_id
                  AND prev.asset_kind = android_sync_assets.asset_kind
                  AND prev.server_state = 'ready'
                  AND prev.state = 'verified'
                  AND COALESCE(prev.local_path, '') != ''
                  AND COALESCE(prev.sha256, '') = COALESCE(android_sync_assets.sha256, '')
                  AND COALESCE(prev.file_size, 0) = android_sync_assets.size_bytes
                ORDER BY COALESCE(prev.verified_at_ms, 0) DESC, prev.generation_id DESC
                LIMIT 1
            ),
            attempt_count = 0,
            next_attempt_at_ms = 0,
            last_error = NULL,
            updated_at_ms = :nowMs
        WHERE generation_id = :generationId
          AND server_state = 'ready'
          AND state IN ('desired', 'failed')
          AND EXISTS (
                SELECT 1
                FROM android_sync_assets prev
                WHERE prev.generation_id != :generationId
                  AND prev.asset_id = android_sync_assets.asset_id
                  AND prev.asset_kind = android_sync_assets.asset_kind
                  AND prev.server_state = 'ready'
                  AND prev.state = 'verified'
                  AND COALESCE(prev.local_path, '') != ''
                  AND COALESCE(prev.sha256, '') = COALESCE(android_sync_assets.sha256, '')
                  AND COALESCE(prev.file_size, 0) = android_sync_assets.size_bytes
          )
        """
    )
    suspend fun adoptVerifiedAssetsFromPreviousGenerations(generationId: String, nowMs: Long): Int

    @Query(
        """
        SELECT * FROM android_sync_assets
        WHERE generation_id = :generationId
          AND server_state = 'ready'
          AND state IN ('desired', 'failed')
          AND next_attempt_at_ms <= :nowMs
        ORDER BY
            CASE
                WHEN asset_kind = 'post_thumbnail' THEN 0
                WHEN asset_kind = 'banner' AND LOWER(COALESCE(required_reason, '')) != 'profile' THEN 1
                WHEN asset_kind = 'avatar' AND LOWER(COALESCE(required_reason, '')) != 'profile' THEN 2
                WHEN asset_kind = 'post_media'     THEN 3
                WHEN asset_kind = 'post_audio'     THEN 4
                WHEN asset_kind = 'video_stream'   THEN 5
                WHEN asset_kind = 'subtitle'       THEN 6
                WHEN asset_kind = 'dearrow_thumbnail' THEN 7
                WHEN asset_kind = 'banner'         THEN 8
                WHEN asset_kind = 'avatar'         THEN 9
                WHEN asset_kind = 'preview_track_json' THEN 10
                WHEN asset_kind = 'preview_sprite' THEN 11
                ELSE 12
            END ASC,
            effective_recency_ms DESC,
            seq ASC
        LIMIT :limit
        """
    )
    suspend fun claimableAssets(generationId: String, nowMs: Long, limit: Int): List<AndroidSyncAssetEntity>

    @Query(
        """
        UPDATE android_sync_assets
        SET state = 'downloading',
            next_attempt_at_ms = 9223372036854775806,
            updated_at_ms = :nowMs
        WHERE generation_id = :generationId
          AND asset_id = :assetId
          AND asset_kind = :assetKind
          AND state IN ('desired', 'failed')
        """
    )
    suspend fun markDownloading(generationId: String, assetId: String, assetKind: String, nowMs: Long)

    @Query(
        """
        UPDATE android_sync_assets
        SET state = 'verified',
            local_path = :localPath,
            file_size = :fileSize,
            verified_at_ms = :nowMs,
            attempt_count = 0,
            next_attempt_at_ms = 0,
            last_error = NULL,
            updated_at_ms = :nowMs
        WHERE generation_id = :generationId
          AND asset_id = :assetId
          AND asset_kind = :assetKind
        """
    )
    suspend fun markVerified(
        generationId: String,
        assetId: String,
        assetKind: String,
        localPath: String,
        fileSize: Long,
        nowMs: Long,
    )

    @Query(
        """
        UPDATE android_sync_assets
        SET state = 'failed',
            attempt_count = attempt_count + 1,
            next_attempt_at_ms = :nextAttemptAtMs,
            last_error = :lastError,
            updated_at_ms = :nowMs
        WHERE generation_id = :generationId
          AND asset_id = :assetId
          AND asset_kind = :assetKind
        """
    )
    suspend fun markFailed(
        generationId: String,
        assetId: String,
        assetKind: String,
        nextAttemptAtMs: Long,
        lastError: String,
        nowMs: Long,
    )

    @Query(
        """
        UPDATE android_sync_assets
        SET state = 'desired',
            next_attempt_at_ms = :nextAttemptAtMs,
            last_error = :lastError,
            updated_at_ms = :nowMs
        WHERE generation_id = :generationId
          AND asset_id = :assetId
          AND asset_kind = :assetKind
        """
    )
    suspend fun deferAsset(
        generationId: String,
        assetId: String,
        assetKind: String,
        nextAttemptAtMs: Long,
        lastError: String,
        nowMs: Long,
    )

    @Query(
        """
        UPDATE android_sync_assets
        SET state = 'server_missing',
            next_attempt_at_ms = 9223372036854775807,
            last_error = :lastError,
            updated_at_ms = :nowMs
        WHERE generation_id = :generationId
          AND asset_id = :assetId
          AND asset_kind = :assetKind
        """
    )
    suspend fun markServerMissing(
        generationId: String,
        assetId: String,
        assetKind: String,
        lastError: String,
        nowMs: Long,
    )

    @Query("SELECT COUNT(*) FROM android_sync_assets WHERE generation_id = :generationId AND state IN ('desired', 'failed')")
    suspend fun countPending(generationId: String): Int

    @Query(
        """
        SELECT COUNT(*) FROM android_sync_assets
        WHERE generation_id = :generationId
          AND server_state = 'ready'
          AND (
              state = 'downloading'
              OR (
                  state IN ('desired', 'failed')
                  AND next_attempt_at_ms <= :nowMs
              )
          )
        """
    )
    suspend fun countActiveOrEligiblePending(generationId: String, nowMs: Long): Int

    @Query(
        """
        UPDATE android_sync_assets
        SET state = 'failed',
            next_attempt_at_ms = :nowMs,
            last_error = 'interrupted_download',
            updated_at_ms = :nowMs
        WHERE generation_id = :generationId
          AND state = 'downloading'
        """
    )
    suspend fun resetDownloading(generationId: String, nowMs: Long)

    @Query(
        """
        UPDATE android_sync_assets
        SET state = 'desired',
            local_path = NULL,
            file_size = NULL,
            verified_at_ms = NULL,
            attempt_count = 0,
            next_attempt_at_ms = 0,
            last_error = :lastError,
            updated_at_ms = :nowMs
        WHERE state = 'verified'
          AND (:bucket IS NULL OR bucket = :bucket)
        """
    )
    suspend fun resetVerifiedLocalPaths(bucket: String?, nowMs: Long, lastError: String = "local_cache_cleared"): Int

    @Query(
        """
        SELECT
            COUNT(*) AS total,
            SUM(CASE WHEN state = 'verified' THEN 1 ELSE 0 END) AS verified,
            SUM(CASE WHEN state IN ('desired', 'downloading') THEN 1 ELSE 0 END) AS pending,
            SUM(CASE WHEN state = 'failed' THEN 1 ELSE 0 END) AS failed,
            SUM(CASE WHEN state = 'server_missing' THEN 1 ELSE 0 END) AS missing,
            COALESCE(SUM(CASE WHEN state = 'verified' THEN COALESCE(file_size, 0) ELSE 0 END), 0) AS verifiedBytes
        FROM android_sync_assets
        WHERE generation_id = :generationId
        """
    )
    suspend fun healthCounts(generationId: String): AndroidSyncHealthCounts
}
