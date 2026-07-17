package com.screwy.igloo.media

import com.screwy.igloo.data.dao.AndroidSyncDao
import com.screwy.igloo.log.Logger
import java.io.File
import java.io.IOException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

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
)

interface CacheActions {
    suspend fun stats(): List<CacheStats>
    suspend fun clearCache(bucket: String? = null)
    suspend fun clearCaches(buckets: Collection<String>)
    suspend fun clearOwner(ownerKind: String, ownerId: String)
}

class CacheOps(
    private val syncDao: AndroidSyncDao,
    private val mediaRoot: File,
    private val logger: Logger,
    private val syncTrigger: () -> Unit = {},
    private val nowMsProvider: () -> Long = { System.currentTimeMillis() },
) : CacheActions {

    /** Returns per-bucket stats from the current canonical sync inventory. */
    override suspend fun stats(): List<CacheStats> =
        syncDao.cacheStatsByBucket().map { row ->
            CacheStats(
                bucket = row.bucket,
                entries = row.entries,
                cached = row.cached,
                bytes = row.bytes,
            )
        }

    /**
     * Clears cached paths and files, then requests asset sync.
     *
     * @param bucket If null, clears all buckets. Otherwise clears only the named bucket
     *               (e.g., "shorts_videos", "youtube_videos", "avatars").
     */
    override suspend fun clearCache(bucket: String?) {
        var demoted = false
        try {
            val deletionFailure = if (bucket == null) {
                clearAllBuckets().also { demoted = true }
            } else {
                clearBucket(bucket).also { demoted = true }
            }
            logger.info(
                event = "cache_cleared",
                fields = mapOf("bucket" to (bucket ?: "all")),
            )
            if (deletionFailure != null) throw deletionFailure
        } finally {
            if (demoted) syncTrigger()
        }
    }

    override suspend fun clearCaches(buckets: Collection<String>) {
        val normalized = buckets.map { it.trim() }.filter { it.isNotEmpty() }.distinct()
        if (normalized.isEmpty()) return
        var demoted = false
        var deletionFailure: IOException? = null
        try {
            normalized.forEach { bucket ->
                val failure = clearBucket(bucket)
                demoted = true
                if (deletionFailure == null) deletionFailure = failure
            }
            logger.info(
                event = "cache_cleared",
                fields = mapOf("buckets" to normalized.joinToString(",")),
            )
            deletionFailure?.let { throw it }
        } finally {
            if (demoted) syncTrigger()
        }
    }

    override suspend fun clearOwner(ownerKind: String, ownerId: String) {
        var demoted = false
        try {
            val paths = syncDao.verifiedLocalPathsForOwner(ownerKind, ownerId)
            if (ownerKind == "youtube_video") {
                syncDao.markOfflineYoutubeDownloadRemoved(ownerId, nowMsProvider())
            }
            syncDao.resetVerifiedLocalPathsForOwner(ownerKind, ownerId)
            demoted = true
            val deletionFailure = deleteRecordedFilesOnIo(paths)
            logger.info(
                event = "cache_cleared",
                fields = mapOf("owner_kind" to ownerKind, "owner_id" to ownerId),
            )
            deletionFailure?.let { throw it }
        } finally {
            if (demoted) syncTrigger()
        }
    }

    private suspend fun clearAllBuckets(): IOException? {
        val paths = syncDao.verifiedLocalPaths(bucket = null)
        syncDao.markOfflineYoutubeDownloadsRemovedForPrimaryAssets(bucket = null, nowMs = nowMsProvider())
        syncDao.resetVerifiedLocalPaths(bucket = null)
        return deleteRecordedFilesOnIo(paths)
    }

    private suspend fun clearBucket(bucket: String): IOException? {
        val paths = syncDao.verifiedLocalPaths(bucket)
        syncDao.markOfflineYoutubeDownloadsRemovedForPrimaryAssets(bucket, nowMsProvider())
        syncDao.resetVerifiedLocalPaths(bucket = bucket)
        return deleteRecordedFilesOnIo(paths)
    }

    private suspend fun deleteRecordedFilesOnIo(paths: List<String>): IOException? =
        withContext(Dispatchers.IO) {
            try {
                deleteRecordedFiles(paths)
                null
            } catch (e: Exception) {
                if (e is IOException) e else IOException("failed to clear recorded asset", e)
            }
        }

    private fun deleteRecordedFiles(paths: List<String>) {
        val rootPath = mediaRoot.absoluteFile.toPath().normalize()
        paths.distinct().forEach { path ->
            val file = File(path).absoluteFile
            val normalized = file.toPath().normalize()
            if (!normalized.startsWith(rootPath)) {
                throw IOException("refusing to clear asset outside media root")
            }
            deleteFile(file)
            deleteFile(File(file.parentFile, file.name + ".part"))
        }
    }

    private fun deleteFile(file: File) {
        if (file.exists() && (!file.isFile || !file.delete())) {
            throw IOException("failed to clear recorded asset")
        }
    }
}
