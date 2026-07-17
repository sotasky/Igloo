package com.screwy.igloo.data.dao

import androidx.room.Dao
import androidx.room.Query
import androidx.room.RewriteQueriesToDropUnusedColumns
import com.screwy.igloo.data.entity.VideoGridItem
import kotlinx.coroutines.flow.Flow

/** YouTube long-form video lists, read exclusively from the local Room mirror. */
@Dao
interface VideoReadDao {

    @RewriteQueriesToDropUnusedColumns
    @Query(
        """
        SELECT v.*,
               wh.playback_position AS wh_playback_position,
               wh.duration          AS wh_duration,
               c.name               AS channel_name,
               c.source_id          AS channel_source_id
        FROM videos v
        LEFT JOIN watch_history wh ON v.video_id = wh.video_id
        LEFT JOIN channels      c  ON v.channel_id = c.channel_id
        WHERE v.owner_kind = 'youtube_video'
        ORDER BY v.published_at DESC, v.video_id DESC
        LIMIT :limit
        """
    )
    fun youtubeVideosPageFlow(limit: Int): Flow<List<VideoGridItem>>

    /**
     * Device-owned completed downloads and server-temporary videos, all projected from the same
     * local video mirror as the main list. Temporary server videos deliberately stay visible even
     * without a local primary binary, so the grid can show them as playable/downloadable faded rows.
     */
    @RewriteQueriesToDropUnusedColumns
    @Query(
        """
        SELECT v.*,
               wh.playback_position AS wh_playback_position,
               wh.duration          AS wh_duration,
               c.name               AS channel_name,
               c.source_id          AS channel_source_id
        FROM videos v
        LEFT JOIN offline_video_downloads saved ON saved.video_id = v.video_id
        LEFT JOIN watch_history wh ON v.video_id = wh.video_id
        LEFT JOIN channels      c  ON v.channel_id = c.channel_id
        WHERE v.owner_kind = 'youtube_video'
          AND (
            COALESCE(v.is_temp, 0) = 1
            OR (
              saved.state = 'downloaded'
              AND EXISTS (
                SELECT 1 FROM android_sync_assets asa
                WHERE asa.owner_kind = 'youtube_video'
                  AND asa.owner_id = v.video_id
                  AND ${youtubeVideoPrimaryAssetSql}
                  AND asa.local_path IS NOT NULL
              )
            )
          )
        ORDER BY v.published_at DESC, v.video_id DESC
        LIMIT :limit
        """
    )
    fun downloadedVideosPageFlow(limit: Int): Flow<List<VideoGridItem>>

    /** Channel-scoped YouTube list. */
    @RewriteQueriesToDropUnusedColumns
    @Query(
        """
        SELECT v.*,
               wh.playback_position AS wh_playback_position,
               wh.duration          AS wh_duration,
               c.name               AS channel_name,
               c.source_id          AS channel_source_id
        FROM videos v
        LEFT JOIN watch_history wh ON v.video_id = wh.video_id
        LEFT JOIN channels      c  ON v.channel_id = c.channel_id
        WHERE v.channel_id = :channelId
        ORDER BY v.published_at DESC, v.video_id DESC
        """
    )
    fun channelVideosFlow(channelId: String): Flow<List<VideoGridItem>>
}
