package com.screwy.igloo.sync

import androidx.room.withTransaction
import com.screwy.igloo.data.IglooDatabase
import com.screwy.igloo.data.dao.AndroidSyncDao
import com.screwy.igloo.data.dao.OfflineVideoDownloadDao
import com.screwy.igloo.data.entity.AndroidSyncAssetEntity
import com.screwy.igloo.data.entity.OfflineVideoDownloadEntity
import java.io.File

internal fun AndroidSyncAssetEntity.isYoutubeVideoPrimaryBinary(): Boolean =
    assetKind == "video_stream" ||
        (assetKind == "post_media" && contentType.orEmpty().startsWith("video/", ignoreCase = true)) ||
        (assetKind == "post_audio" && contentType.orEmpty().startsWith("audio/", ignoreCase = true))

/**
 * Device-owned actions for the primary YouTube binary.
 *
 * Video rows and auxiliary assets remain Android-sync data. This owner records
 * an explicit request so the normal sync drainer keeps only the primary binary
 * outside the automatic retention window.
 */
interface OfflineVideoActions {
    suspend fun requestDownload(videoId: String)

    suspend fun removeDownload(videoId: String)
}

class OfflineVideoDownloads(
    private val db: IglooDatabase,
    private val syncDao: AndroidSyncDao,
    private val downloads: OfflineVideoDownloadDao,
    mediaRoot: File,
    private val nowMsProvider: () -> Long,
    private val syncTrigger: () -> Unit,
) : OfflineVideoActions {
    private val syncRoot = File(mediaRoot, "sync").absoluteFile

    override suspend fun requestDownload(videoId: String) {
        val id = videoId.trim()
        if (id.isEmpty()) return

        db.withTransaction {
            if (!syncDao.hasVideo(id)) return@withTransaction
            val state =
                if (syncDao.localYoutubeVideoPrimaryAssets(id).isNotEmpty()) STATE_DOWNLOADED
                else STATE_REQUESTED
            downloads.upsert(
                OfflineVideoDownloadEntity(
                    videoId = id,
                    state = state,
                    updatedAtMs = nowMsProvider(),
                ),
            )
            syncDao.prioritizeYoutubeVideoPrimaryAssets(id)
        }
        syncTrigger()
    }

    override suspend fun removeDownload(videoId: String) {
        val id = videoId.trim()
        if (id.isEmpty()) return

        val files =
            db.withTransaction {
                val localPrimaryAssets = syncDao.localYoutubeVideoPrimaryAssets(id)
                downloads.upsert(
                    OfflineVideoDownloadEntity(
                        videoId = id,
                        state = STATE_REMOVED,
                        updatedAtMs = nowMsProvider(),
                    ),
                )
                syncDao.resetVerifiedLocalPathsForYoutubeVideoPrimaryAssets(id)
                localPrimaryAssets.mapNotNull { it.localPath }
            }
        files.forEach(::deleteSyncFile)
        syncTrigger()
    }

    private fun deleteSyncFile(path: String) {
        val file = File(path)
        val normalized = runCatching { file.absoluteFile.toPath().normalize() }.getOrNull() ?: return
        val root = runCatching { syncRoot.toPath().normalize() }.getOrNull() ?: return
        if (!normalized.startsWith(root) || !file.isFile) return
        file.delete()
    }

    private companion object {
        const val STATE_REQUESTED = "requested"
        const val STATE_DOWNLOADED = "downloaded"
        const val STATE_REMOVED = "removed"
    }
}
