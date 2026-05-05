package com.screwy.igloo.data.dao

import androidx.room.Dao
import androidx.room.Query
import androidx.room.RewriteQueriesToDropUnusedColumns
import com.screwy.igloo.data.entity.VideoGridItem
import kotlinx.coroutines.flow.Flow

/**
 * Videos tab — YouTube long-form only, with resume-progress annotations.
 * No pagination: bookmarks-style single-page load from Room.
 */
@Dao
interface VideoReadDao {

    @RewriteQueriesToDropUnusedColumns
    @Query(
        """
        SELECT v.*,
               wh.playback_position AS wh_playback_position,
               wh.duration          AS wh_duration,
               wh.last_watched      AS wh_last_watched,
               c.name               AS channel_name,
               c.source_id          AS channel_source_id
        FROM videos v
        LEFT JOIN watch_history wh ON v.video_id = wh.video_id
        LEFT JOIN channels      c  ON v.channel_id = c.channel_id
        WHERE v.channel_id LIKE 'youtube_%'
        ORDER BY v.published_at DESC
        """
    )
    fun videosFlow(): Flow<List<VideoGridItem>>

    /** Channel-scoped YouTube list. */
    @RewriteQueriesToDropUnusedColumns
    @Query(
        """
        SELECT v.*,
               wh.playback_position AS wh_playback_position,
               wh.duration          AS wh_duration,
               wh.last_watched      AS wh_last_watched,
               c.name               AS channel_name,
               c.source_id          AS channel_source_id
        FROM videos v
        LEFT JOIN watch_history wh ON v.video_id = wh.video_id
        LEFT JOIN channels      c  ON v.channel_id = c.channel_id
        WHERE v.channel_id = :channelId
        ORDER BY v.published_at DESC
        """
    )
    fun channelVideosFlow(channelId: String): Flow<List<VideoGridItem>>
}
