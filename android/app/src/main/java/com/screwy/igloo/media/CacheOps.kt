package com.screwy.igloo.media

import com.screwy.igloo.data.dao.AndroidSyncDao
import com.screwy.igloo.data.dao.MediaInventoryDao
import com.screwy.igloo.log.Logger
import java.io.File

/**
 * Cache stats aggregation + clear-cache operations for the Settings screen.
 * [mediaRoot] is the root of the media directory (`<appFilesDir>/media/`). Per-bucket
 * directories live directly under it: `<mediaRoot>/avatars/`, `<mediaRoot>/twitter_media/`, etc.
 * Android sync owns media re-materialization after a clear.
 */

data class CacheStats(
    val bucket: String,
    val entries: Int,
    val cached: Int,
    val bytes: Long,
    val failed: Int,
)

interface CacheActions {
    suspend fun stats(): List<CacheStats>
    suspend fun clearCache(bucket: String? = null)
    suspend fun clearCaches(buckets: Collection<String>)
}

class CacheOps(
    private val dao: MediaInventoryDao,
    private val syncDao: AndroidSyncDao,
    private val mediaRoot: File,
    private val logger: Logger,
    private val syncTrigger: () -> Unit = {},
    private val nowMsProvider: () -> Long = { System.currentTimeMillis() },
) : CacheActions {

    /**
     * Returns per-bucket stats with bytes measured from disk.
     *
     * Android sync stores verified assets under `media/sync/<bucket>/`, while the legacy
     * inventory stores files directly under `media/<bucket>/`. Row sums alone miss
     * Android sync, and sync rows repeat across generations, so disk is the only honest byte
     * source for the Settings screen.
     */
    override suspend fun stats(): List<CacheStats> {
        val legacyRows = dao.statsByBucket().associateBy { it.bucket }
        val diskBytes = diskBytesByBucket()
        val buckets = (legacyRows.keys + diskBytes.keys).sorted()
        return buckets.map { bucket ->
            val row = legacyRows[bucket]
            CacheStats(
                bucket = bucket,
                entries = row?.entries ?: 0,
                cached = row?.cached ?: 0,
                bytes = diskBytes[bucket] ?: row?.bytes ?: 0L,
                failed = row?.failed ?: 0,
            )
        }
    }

    /**
     * Clears the cache — inventory rows + on-disk files — then triggers Android sync to
     * re-materialize from the latest server-owned generation.
     *
     * @param bucket If null, clears all buckets. Otherwise clears only the named bucket
     *               (e.g., "shorts_videos", "youtube_videos", "avatars").
     */
    override suspend fun clearCache(bucket: String?) {
        if (bucket == null) {
            clearAllBuckets()
            logger.info(
                event = "cache_cleared",
                fields = mapOf("bucket" to "all"),
            )
        } else {
            clearBucket(bucket)
            logger.info(
                event = "cache_cleared",
                fields = mapOf("bucket" to bucket),
            )
        }
        syncTrigger()
    }

    override suspend fun clearCaches(buckets: Collection<String>) {
        val normalized = buckets.map { it.trim() }.filter { it.isNotEmpty() }.distinct()
        if (normalized.isEmpty()) return
        normalized.forEach { bucket -> clearBucket(bucket) }
        logger.info(
            event = "cache_cleared",
            fields = mapOf("buckets" to normalized.joinToString(",")),
        )
        syncTrigger()
    }

    private suspend fun clearAllBuckets() {
        dao.deleteAll()
        syncDao.resetVerifiedLocalPaths(bucket = null, nowMs = nowMsProvider())
        // Wipe every file under mediaRoot but leave the directory itself so subsequent
        // downloads can proceed without needing to recreate it.
        mediaRoot.listFiles()?.forEach { it.deleteRecursively() }
    }

    private suspend fun clearBucket(bucket: String) {
        dao.deleteBucket(bucket)
        syncDao.resetVerifiedLocalPaths(bucket = bucket, nowMs = nowMsProvider())
        File(mediaRoot, bucket).deleteRecursively()
        File(File(mediaRoot, SYNC_DIR), bucket).deleteRecursively()
    }

    private fun diskBytesByBucket(): Map<String, Long> {
        val totals = linkedMapOf<String, Long>()
        mediaRoot.listFiles()?.forEach { child ->
            if (child.name == SYNC_DIR && child.isDirectory) {
                child.listFiles()?.forEach { syncBucket ->
                    totals[syncBucket.name] = (totals[syncBucket.name] ?: 0L) + syncBucket.totalFileBytes()
                }
            } else {
                totals[child.name] = (totals[child.name] ?: 0L) + child.totalFileBytes()
            }
        }
        return totals
    }

    private fun File.totalFileBytes(): Long {
        if (!exists()) return 0L
        if (isFile) return length()
        var total = 0L
        walkTopDown().forEach { file ->
            if (file.isFile) total += file.length()
        }
        return total
    }

    private companion object {
        const val SYNC_DIR = "sync"
    }
}
