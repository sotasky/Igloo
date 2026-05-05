package com.screwy.igloo.data.dao

import androidx.room.Dao
import androidx.room.Query
import androidx.room.RewriteQueriesToDropUnusedColumns
import com.screwy.igloo.data.entity.ChannelDisplay
import kotlinx.coroutines.flow.Flow

/**
 * Drawer Accounts list — starred channels grouped first, then alphabetical.
 */
@Dao
interface ChannelReadDao {

    @RewriteQueriesToDropUnusedColumns
    @Query(
        """
        SELECT c.*,
               CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END AS is_starred,
               CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS is_followed,
               cp.handle       AS handle,
               cp.display_name AS display_name
        FROM channels c
        LEFT JOIN channel_stars    cs ON c.channel_id = cs.channel_id
        LEFT JOIN channel_follows  cf ON c.channel_id = cf.channel_id
        LEFT JOIN channel_profiles cp ON c.channel_id = cp.channel_id
        ORDER BY (cs.channel_id IS NOT NULL) DESC,
                 LOWER(COALESCE(NULLIF(cp.display_name, ''), c.name)) ASC
        """
    )
    fun allFlow(): Flow<List<ChannelDisplay>>
}
