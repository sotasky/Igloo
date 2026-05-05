package com.screwy.igloo.data.dao

import androidx.room.Dao
import androidx.room.Query
import androidx.room.Upsert
import com.screwy.igloo.data.entity.MediaInventoryEntity
import kotlinx.coroutines.flow.Flow

/**
 * Read-side access for the retired media inventory table.
 *
 * Android sync owns current asset sync through android_sync_assets. This DAO remains
 * only for existing cached rows and the UI/player fallbacks that still read them.
 */
@Dao
interface MediaInventoryDao {

    @Upsert suspend fun upsert(rows: List<MediaInventoryEntity>)
    @Upsert suspend fun upsert(row: MediaInventoryEntity)

    @Query("SELECT * FROM media_inventory WHERE owner_id = :ownerId")
    suspend fun forOwner(ownerId: String): List<MediaInventoryEntity>

    @Query("SELECT * FROM media_inventory WHERE owner_id = :ownerId")
    fun forOwnerFlow(ownerId: String): Flow<List<MediaInventoryEntity>>

    @Query("DELETE FROM media_inventory WHERE owner_id = :ownerId")
    suspend fun deleteForOwner(ownerId: String): Int

    @Query("SELECT * FROM media_inventory WHERE owner_id = :ownerId AND asset_kind = :assetKind LIMIT 1")
    fun forOwnerAndKindFlow(ownerId: String, assetKind: String): Flow<MediaInventoryEntity?>

    @Query(
        """
        SELECT bucket,
               COUNT(*)                                                 AS entries,
               SUM(CASE WHEN state = 'cached' THEN 1 ELSE 0 END)        AS cached,
               COALESCE(SUM(CASE WHEN state = 'cached' THEN file_size ELSE 0 END), 0) AS bytes,
               SUM(CASE WHEN state IN ('failed', 'tombstoned') THEN 1 ELSE 0 END) AS failed
        FROM media_inventory
        GROUP BY bucket
        """
    )
    suspend fun statsByBucket(): List<BucketStats>

    data class BucketStats(
        val bucket: String,
        val entries: Int,
        val cached: Int,
        val bytes: Long,
        val failed: Int,
    )

    @Query("DELETE FROM media_inventory WHERE bucket = :bucket")
    suspend fun deleteBucket(bucket: String): Int

    @Query("DELETE FROM media_inventory")
    suspend fun deleteAll()

    @Query(
        """
        SELECT * FROM media_inventory
        WHERE owner_id = :ownerId AND asset_kind = :assetKind
        ORDER BY CASE state WHEN 'cached' THEN 0 ELSE 1 END
        LIMIT 1
        """
    )
    suspend fun resolveForOwner(ownerId: String, assetKind: String): MediaInventoryEntity?
}
