package com.screwy.igloo.data.dao

import androidx.room.Dao
import androidx.room.Query
import androidx.room.Upsert
import com.screwy.igloo.data.entity.OfflineVideoDownloadEntity

@Dao
interface OfflineVideoDownloadDao {
    @Upsert suspend fun upsert(row: OfflineVideoDownloadEntity)

    @Query("SELECT * FROM offline_video_downloads WHERE video_id = :videoId")
    suspend fun get(videoId: String): OfflineVideoDownloadEntity?

    @Query("DELETE FROM offline_video_downloads WHERE video_id = :videoId")
    suspend fun delete(videoId: String)

    @Query(
        """
        DELETE FROM offline_video_downloads
        WHERE NOT EXISTS (SELECT 1 FROM videos WHERE videos.video_id = offline_video_downloads.video_id)
        """,
    )
    suspend fun deleteMissingVideos(): Int
}
