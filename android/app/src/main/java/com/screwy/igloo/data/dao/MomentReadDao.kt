package com.screwy.igloo.data.dao

import androidx.room.Dao
import androidx.room.Query
import androidx.room.RewriteQueriesToDropUnusedColumns
import com.screwy.igloo.data.entity.MomentItem
import com.screwy.igloo.data.entity.StoryChannelItem
import kotlinx.coroutines.flow.Flow

/**
 * Moments tab + all-moments grid. TikTok + Instagram shorts live on `videos`; this DAO
 * joins each row against `moment_views` for the "already viewed?" flag.
 *
 * Twitter-as-moment (video-kind tweets) is served by FeedReadDao with a media_kind
 * filter. This DAO stays shorts-only.
 *
 * Moments player and grid lists are intentionally oldest -> newest: scrolling
 * forward moves through time, and new rows append at the end instead of shifting
 * the start of the list. Keep web /moments and /api/shorts/cards aligned.
 */
@Dao
interface MomentReadDao {

    @RewriteQueriesToDropUnusedColumns
    @Query(
        """
        WITH allowed_reposts AS (
            SELECT vrs.*,
                   COUNT(*) OVER (PARTITION BY vrs.video_id) AS repost_count,
                   ROW_NUMBER() OVER (
                       PARTITION BY vrs.video_id
                       ORDER BY COALESCE(NULLIF(vrs.reposted_at_ms, 0), vrs.first_seen_at_ms) DESC,
                                vrs.reposter_channel_id ASC
                   ) AS rn
            FROM video_repost_sources vrs
            INNER JOIN channel_follows rcf ON rcf.channel_id = vrs.reposter_channel_id
            LEFT JOIN channel_settings rcs ON rcs.channel_id = vrs.reposter_channel_id
            WHERE COALESCE(rcs.include_reposts, 1) != 0
        ),
        repost_heads AS (
            SELECT * FROM allowed_reposts WHERE rn = 1
        )
        SELECT v.*,
               CASE WHEN mv.video_id IS NOT NULL THEN 1 ELSE 0 END AS is_viewed,
               mv.viewed_at                                        AS viewed_at,
               c.name                                              AS channel_name,
               c.source_id                                         AS channel_source_id,
               CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_followed,
               COALESCE(rh.reposter_channel_id, '')                AS reposter_channel_id,
               COALESCE(rh.reposter_handle, '')                    AS reposter_handle,
               COALESCE(rh.reposter_display_name, '')              AS reposter_display_name,
               COALESCE(rh.repost_author_label, '')                AS repost_author_label,
               COALESCE(rh.repost_count, 0)                        AS repost_count,
               CASE WHEN rh.video_id IS NOT NULL THEN 1 ELSE 0 END AS repost_introduced,
               CASE WHEN rh.video_id IS NOT NULL
                    THEN COALESCE(NULLIF(rh.reposted_at_ms, 0), NULLIF(rh.first_seen_at_ms, 0), v.published_at)
                    ELSE COALESCE(v.published_at, 0)
                END AS effective_moment_at_ms
        FROM videos v
        LEFT JOIN moment_views mv ON v.video_id = mv.video_id
        LEFT JOIN channels c      ON c.channel_id = v.channel_id
        LEFT JOIN channel_follows cf ON cf.channel_id = v.channel_id
        LEFT JOIN repost_heads rh ON rh.video_id = v.video_id
        WHERE (v.channel_id LIKE 'tiktok_%' OR v.channel_id LIKE 'instagram_%')
          AND COALESCE(v.source_kind, '') != 'story'
          AND (cf.channel_id IS NOT NULL OR rh.video_id IS NOT NULL)
        ORDER BY effective_moment_at_ms ASC, v.video_id ASC
        """
    )
    fun momentsAllFlow(): Flow<List<MomentItem>>

    @RewriteQueriesToDropUnusedColumns
    @Query(
        """
        SELECT v.*,
               CASE WHEN mv.video_id IS NOT NULL THEN 1 ELSE 0 END AS is_viewed,
               mv.viewed_at                                        AS viewed_at,
               c.name                                              AS channel_name,
               c.source_id                                         AS channel_source_id,
               CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_followed,
               ''                                                  AS reposter_channel_id,
               ''                                                  AS reposter_handle,
               ''                                                  AS reposter_display_name,
               ''                                                  AS repost_author_label,
               0                                                   AS repost_count,
               0                                                   AS repost_introduced,
               COALESCE(v.published_at, 0)                         AS effective_moment_at_ms
        FROM videos v
        LEFT JOIN moment_views mv ON v.video_id = mv.video_id
        LEFT JOIN channels c      ON c.channel_id = v.channel_id
        INNER JOIN channel_follows cf ON cf.channel_id = v.channel_id
        WHERE (v.channel_id LIKE 'tiktok_%' OR v.channel_id LIKE 'instagram_%')
          AND COALESCE(v.source_kind, '') != 'story'
        ORDER BY v.published_at ASC, v.video_id ASC
        """
    )
    fun momentsFollowingFlow(): Flow<List<MomentItem>>

    /**
     * Player-only rows for the active Moments pager.
     *
     * This intentionally does not read `moment_views`: viewing a page writes that
     * side table, and the full-screen player should not rebuild its list while a
     * swipe is in progress just because it logged that the current video was seen.
     */
    @RewriteQueriesToDropUnusedColumns
    @Query(
        """
        WITH allowed_reposts AS (
            SELECT vrs.*,
                   COUNT(*) OVER (PARTITION BY vrs.video_id) AS repost_count,
                   ROW_NUMBER() OVER (
                       PARTITION BY vrs.video_id
                       ORDER BY COALESCE(NULLIF(vrs.reposted_at_ms, 0), vrs.first_seen_at_ms) DESC,
                                vrs.reposter_channel_id ASC
                   ) AS rn
            FROM video_repost_sources vrs
            INNER JOIN channel_follows rcf ON rcf.channel_id = vrs.reposter_channel_id
            LEFT JOIN channel_settings rcs ON rcs.channel_id = vrs.reposter_channel_id
            WHERE COALESCE(rcs.include_reposts, 1) != 0
        ),
        repost_heads AS (
            SELECT * FROM allowed_reposts WHERE rn = 1
        )
        SELECT v.*,
               0                                                 AS is_viewed,
               NULL                                              AS viewed_at,
               c.name                                            AS channel_name,
               c.source_id                                       AS channel_source_id,
               CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_followed,
               COALESCE(rh.reposter_channel_id, '')              AS reposter_channel_id,
               COALESCE(rh.reposter_handle, '')                  AS reposter_handle,
               COALESCE(rh.reposter_display_name, '')            AS reposter_display_name,
               COALESCE(rh.repost_author_label, '')              AS repost_author_label,
               COALESCE(rh.repost_count, 0)                      AS repost_count,
               CASE WHEN rh.video_id IS NOT NULL THEN 1 ELSE 0 END AS repost_introduced,
               CASE WHEN rh.video_id IS NOT NULL
                    THEN COALESCE(NULLIF(rh.reposted_at_ms, 0), NULLIF(rh.first_seen_at_ms, 0), v.published_at)
                    ELSE COALESCE(v.published_at, 0)
                END AS effective_moment_at_ms
        FROM videos v
        LEFT JOIN channels c ON c.channel_id = v.channel_id
        LEFT JOIN channel_follows cf ON cf.channel_id = v.channel_id
        LEFT JOIN repost_heads rh ON rh.video_id = v.video_id
        WHERE (v.channel_id LIKE 'tiktok_%' OR v.channel_id LIKE 'instagram_%')
          AND COALESCE(v.source_kind, '') != 'story'
          AND (cf.channel_id IS NOT NULL OR rh.video_id IS NOT NULL)
        ORDER BY effective_moment_at_ms ASC, v.video_id ASC
        """
    )
    fun playerMomentsAllFlow(): Flow<List<MomentItem>>

    @RewriteQueriesToDropUnusedColumns
    @Query(
        """
        SELECT v.*,
               0                                                 AS is_viewed,
               NULL                                              AS viewed_at,
               c.name                                            AS channel_name,
               c.source_id                                       AS channel_source_id,
               CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_followed,
               ''                                                AS reposter_channel_id,
               ''                                                AS reposter_handle,
               ''                                                AS reposter_display_name,
               ''                                                AS repost_author_label,
               0                                                 AS repost_count,
               0                                                 AS repost_introduced,
               COALESCE(v.published_at, 0)                       AS effective_moment_at_ms
        FROM videos v
        LEFT JOIN channels c ON c.channel_id = v.channel_id
        INNER JOIN channel_follows cf ON cf.channel_id = v.channel_id
        WHERE (v.channel_id LIKE 'tiktok_%' OR v.channel_id LIKE 'instagram_%')
          AND COALESCE(v.source_kind, '') != 'story'
        ORDER BY v.published_at ASC, v.video_id ASC
        """
    )
    fun playerMomentsFollowingFlow(): Flow<List<MomentItem>>

    @Query(
        """
        SELECT COALESCE(published_at, 0)
        FROM videos
        WHERE video_id = :videoId
        LIMIT 1
        """
    )
    fun momentSortAtFlow(videoId: String): Flow<Long?>

    @RewriteQueriesToDropUnusedColumns
    @Query(
        """
        WITH active AS (
            SELECT v.video_id,
                   v.channel_id,
                   COALESCE(v.published_at, 0) AS published_at,
                   CASE WHEN mv.video_id IS NULL THEN 1 ELSE 0 END AS unseen
            FROM videos v
            INNER JOIN channels c ON c.channel_id = v.channel_id
            INNER JOIN channel_follows cf ON cf.channel_id = v.channel_id
            LEFT JOIN moment_views mv ON mv.video_id = v.video_id
            WHERE COALESCE(c.platform, '') IN ('tiktok', 'instagram')
              AND COALESCE(v.source_kind, '') = 'story'
              AND (:cutoffMs <= 0 OR COALESCE(v.published_at, 0) >= :cutoffMs)
        )
        SELECT a.channel_id,
               COALESCE(NULLIF(cp.display_name, ''), NULLIF(c.name, ''), NULLIF(cp.handle, ''), a.channel_id) AS channel_name,
               COALESCE(NULLIF(cp.handle, ''), NULLIF(c.source_id, ''), '') AS channel_source_id,
               COUNT(*) AS story_count,
               COALESCE(SUM(a.unseen), 0) AS unseen_count,
               COALESCE(MAX(a.published_at), 0) AS latest_at_ms,
               COALESCE((
                   SELECT ax.video_id
                   FROM active ax
                   WHERE ax.channel_id = a.channel_id
                   ORDER BY ax.published_at ASC, ax.video_id ASC
                   LIMIT 1
               ), '') AS first_video_id,
               COALESCE((
                   SELECT ax.video_id
                   FROM active ax
                   WHERE ax.channel_id = a.channel_id AND ax.unseen = 1
                   ORDER BY ax.published_at ASC, ax.video_id ASC
                   LIMIT 1
               ), '') AS first_unseen_video_id,
               CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END AS is_starred
        FROM active a
        INNER JOIN channels c ON c.channel_id = a.channel_id
        LEFT JOIN channel_profiles cp ON cp.channel_id = a.channel_id
        LEFT JOIN channel_stars cs ON cs.channel_id = a.channel_id
        GROUP BY a.channel_id, c.name, c.source_id, cp.display_name, cp.handle, cs.channel_id
        ORDER BY CASE WHEN COALESCE(SUM(a.unseen), 0) > 0 THEN 0 ELSE 1 END,
                 CASE WHEN cs.channel_id IS NOT NULL THEN 0 ELSE 1 END,
                 latest_at_ms DESC,
                 channel_name COLLATE NOCASE ASC
        """
    )
    fun storyChannelsFlow(cutoffMs: Long): Flow<List<StoryChannelItem>>

    @RewriteQueriesToDropUnusedColumns
    @Query(
        """
        WITH eligible_repost_channels AS (
            SELECT DISTINCT v.channel_id
            FROM video_repost_sources vrs
            INNER JOIN videos v ON v.video_id = vrs.video_id
            INNER JOIN channel_follows rcf ON rcf.channel_id = vrs.reposter_channel_id
            LEFT JOIN channel_settings rcs ON rcs.channel_id = vrs.reposter_channel_id
            WHERE COALESCE(v.channel_id, '') != ''
              AND COALESCE(v.source_kind, '') != 'story'
              AND COALESCE(rcs.include_reposts, 1) != 0
              AND (
                :cutoffMs <= 0 OR
                COALESCE(NULLIF(vrs.updated_at_ms, 0), NULLIF(vrs.first_seen_at_ms, 0), NULLIF(vrs.reposted_at_ms, 0), COALESCE(v.published_at, 0)) >= :cutoffMs
              )
        ),
        active AS (
            SELECT sv.video_id,
                   sv.channel_id,
                   COALESCE(sv.published_at, 0) AS published_at,
                   CASE WHEN mv.video_id IS NULL THEN 1 ELSE 0 END AS unseen
            FROM videos sv
            INNER JOIN channels c ON c.channel_id = sv.channel_id
            LEFT JOIN channel_follows cf ON cf.channel_id = sv.channel_id
            LEFT JOIN moment_views mv ON mv.video_id = sv.video_id
            WHERE COALESCE(c.platform, '') IN ('tiktok', 'instagram')
              AND COALESCE(sv.source_kind, '') = 'story'
              AND (:cutoffMs <= 0 OR COALESCE(sv.published_at, 0) >= :cutoffMs)
              AND (
                cf.channel_id IS NOT NULL
                OR EXISTS (SELECT 1 FROM eligible_repost_channels erc WHERE erc.channel_id = sv.channel_id)
              )
        )
        SELECT a.channel_id,
               COALESCE(NULLIF(cp.display_name, ''), NULLIF(c.name, ''), NULLIF(cp.handle, ''), a.channel_id) AS channel_name,
               COALESCE(NULLIF(cp.handle, ''), NULLIF(c.source_id, ''), '') AS channel_source_id,
               COUNT(*) AS story_count,
               COALESCE(SUM(a.unseen), 0) AS unseen_count,
               COALESCE(MAX(a.published_at), 0) AS latest_at_ms,
               COALESCE((
                   SELECT ax.video_id
                   FROM active ax
                   WHERE ax.channel_id = a.channel_id
                   ORDER BY ax.published_at ASC, ax.video_id ASC
                   LIMIT 1
               ), '') AS first_video_id,
               COALESCE((
                   SELECT ax.video_id
                   FROM active ax
                   WHERE ax.channel_id = a.channel_id AND ax.unseen = 1
                   ORDER BY ax.published_at ASC, ax.video_id ASC
                   LIMIT 1
               ), '') AS first_unseen_video_id,
               CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END AS is_starred
        FROM active a
        INNER JOIN channels c ON c.channel_id = a.channel_id
        LEFT JOIN channel_profiles cp ON cp.channel_id = a.channel_id
        LEFT JOIN channel_stars cs ON cs.channel_id = a.channel_id
        GROUP BY a.channel_id, c.name, c.source_id, cp.display_name, cp.handle, cs.channel_id
        """
    )
    fun storyStatusesFlow(cutoffMs: Long): Flow<List<StoryChannelItem>>

    /**
     * Channel-scoped moments (TikTok or Instagram channel page).
     *
     * Channel profiles show content newest -> oldest, matching Twitter and
     * YouTube profile pages rather than the main Moments player direction.
     */
    @RewriteQueriesToDropUnusedColumns
    @Query(
        """
        SELECT v.*,
               CASE WHEN mv.video_id IS NOT NULL THEN 1 ELSE 0 END AS is_viewed,
               mv.viewed_at                                        AS viewed_at,
               c.name                                              AS channel_name,
               c.source_id                                         AS channel_source_id,
               CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_followed,
               ''                                                  AS reposter_channel_id,
               ''                                                  AS reposter_handle,
               ''                                                  AS reposter_display_name,
               ''                                                  AS repost_author_label,
               0                                                   AS repost_count,
               0                                                   AS repost_introduced,
               COALESCE(v.published_at, 0)                         AS effective_moment_at_ms
        FROM videos v
        LEFT JOIN moment_views mv ON v.video_id = mv.video_id
        LEFT JOIN channels c      ON c.channel_id = v.channel_id
        LEFT JOIN channel_follows cf ON cf.channel_id = v.channel_id
        WHERE v.channel_id = :channelId
          AND COALESCE(v.source_kind, '') != 'story'
        ORDER BY v.published_at DESC, v.video_id DESC
        """
    )
    fun channelMomentsFlow(channelId: String): Flow<List<MomentItem>>

    @RewriteQueriesToDropUnusedColumns
    @Query(
        """
        SELECT v.*,
               CASE WHEN mv.video_id IS NOT NULL THEN 1 ELSE 0 END AS is_viewed,
               mv.viewed_at                                        AS viewed_at,
               c.name                                              AS channel_name,
               c.source_id                                         AS channel_source_id,
               CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_followed,
               ''                                                  AS reposter_channel_id,
               ''                                                  AS reposter_handle,
               ''                                                  AS reposter_display_name,
               ''                                                  AS repost_author_label,
               0                                                   AS repost_count,
               0                                                   AS repost_introduced,
               COALESCE(v.published_at, 0)                         AS effective_moment_at_ms
        FROM videos v
        LEFT JOIN moment_views mv ON v.video_id = mv.video_id
        LEFT JOIN channels c      ON c.channel_id = v.channel_id
        LEFT JOIN channel_follows cf ON cf.channel_id = v.channel_id
        WHERE v.channel_id = :channelId
          AND COALESCE(v.source_kind, '') = 'story'
          AND (:cutoffMs <= 0 OR COALESCE(v.published_at, 0) >= :cutoffMs)
        ORDER BY v.published_at ASC, v.video_id ASC
        """
    )
    fun channelStoriesFlow(channelId: String, cutoffMs: Long): Flow<List<MomentItem>>

    @RewriteQueriesToDropUnusedColumns
    @Query(
        """
        WITH active AS (
            SELECT v.video_id,
                   v.channel_id,
                   COALESCE(v.published_at, 0) AS published_at,
                   CASE WHEN mv.video_id IS NULL THEN 1 ELSE 0 END AS unseen
            FROM videos v
            INNER JOIN channels c ON c.channel_id = v.channel_id
            LEFT JOIN channel_follows cf ON cf.channel_id = v.channel_id
            LEFT JOIN moment_views mv ON mv.video_id = v.video_id
            WHERE COALESCE(c.platform, '') IN ('tiktok', 'instagram')
              AND COALESCE(v.source_kind, '') = 'story'
              AND (:cutoffMs <= 0 OR COALESCE(v.published_at, 0) >= :cutoffMs)
              AND v.channel_id = :channelId
        ),
        channel_order AS (
            SELECT a.channel_id,
                   COALESCE(MAX(a.published_at), 0) AS latest_at_ms,
                   COALESCE(SUM(a.unseen), 0) AS unseen_count,
                   CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END AS is_starred,
                   COALESCE(NULLIF(cp.display_name, ''), NULLIF(c.name, ''), NULLIF(cp.handle, ''), a.channel_id) AS channel_name
            FROM active a
            INNER JOIN channels c ON c.channel_id = a.channel_id
            LEFT JOIN channel_profiles cp ON cp.channel_id = a.channel_id
            LEFT JOIN channel_stars cs ON cs.channel_id = a.channel_id
            GROUP BY a.channel_id, c.name, cp.display_name, cp.handle, cs.channel_id
        )
        SELECT v.*,
               CASE WHEN mv.video_id IS NOT NULL THEN 1 ELSE 0 END AS is_viewed,
               mv.viewed_at                                        AS viewed_at,
               c.name                                              AS channel_name,
               c.source_id                                         AS channel_source_id,
               CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_followed,
               ''                                                  AS reposter_channel_id,
               ''                                                  AS reposter_handle,
               ''                                                  AS reposter_display_name,
               ''                                                  AS repost_author_label,
               0                                                   AS repost_count,
               0                                                   AS repost_introduced,
               COALESCE(v.published_at, 0)                         AS effective_moment_at_ms
        FROM active a
        INNER JOIN videos v ON v.video_id = a.video_id
        INNER JOIN channel_order co ON co.channel_id = a.channel_id
        LEFT JOIN moment_views mv ON v.video_id = mv.video_id
        LEFT JOIN channels c      ON c.channel_id = v.channel_id
        LEFT JOIN channel_follows cf ON cf.channel_id = v.channel_id
        ORDER BY CASE WHEN co.unseen_count > 0 THEN 0 ELSE 1 END,
                 CASE WHEN co.is_starred = 1 THEN 0 ELSE 1 END,
                 co.latest_at_ms DESC,
                 co.channel_name COLLATE NOCASE ASC,
                 a.published_at ASC,
                 a.video_id ASC
        """
    )
    fun storyPlaylistFlow(channelId: String, cutoffMs: Long): Flow<List<MomentItem>>

    @RewriteQueriesToDropUnusedColumns
    @Query(
        """
        WITH eligible_repost_channels AS (
            SELECT DISTINCT v.channel_id
            FROM video_repost_sources vrs
            INNER JOIN videos v ON v.video_id = vrs.video_id
            INNER JOIN channel_follows rcf ON rcf.channel_id = vrs.reposter_channel_id
            LEFT JOIN channel_settings rcs ON rcs.channel_id = vrs.reposter_channel_id
            WHERE COALESCE(v.channel_id, '') != ''
              AND COALESCE(v.source_kind, '') != 'story'
              AND COALESCE(rcs.include_reposts, 1) != 0
              AND (
                :cutoffMs <= 0 OR
                COALESCE(NULLIF(vrs.updated_at_ms, 0), NULLIF(vrs.first_seen_at_ms, 0), NULLIF(vrs.reposted_at_ms, 0), COALESCE(v.published_at, 0)) >= :cutoffMs
              )
        ),
        active AS (
            SELECT v.video_id,
                   v.channel_id,
                   COALESCE(v.published_at, 0) AS published_at,
                   CASE WHEN mv.video_id IS NULL THEN 1 ELSE 0 END AS unseen
            FROM videos v
            INNER JOIN channels c ON c.channel_id = v.channel_id
            LEFT JOIN channel_follows cf ON cf.channel_id = v.channel_id
            LEFT JOIN moment_views mv ON mv.video_id = v.video_id
            WHERE COALESCE(c.platform, '') IN ('tiktok', 'instagram')
              AND COALESCE(v.source_kind, '') = 'story'
              AND (:cutoffMs <= 0 OR COALESCE(v.published_at, 0) >= :cutoffMs)
              AND (
                cf.channel_id IS NOT NULL
                OR EXISTS (SELECT 1 FROM eligible_repost_channels erc WHERE erc.channel_id = v.channel_id)
              )
        ),
        channel_order AS (
            SELECT a.channel_id,
                   COALESCE(MAX(a.published_at), 0) AS latest_at_ms,
                   COALESCE(SUM(a.unseen), 0) AS unseen_count,
                   CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END AS is_starred,
                   COALESCE(NULLIF(cp.display_name, ''), NULLIF(c.name, ''), NULLIF(cp.handle, ''), a.channel_id) AS channel_name
            FROM active a
            INNER JOIN channels c ON c.channel_id = a.channel_id
            LEFT JOIN channel_profiles cp ON cp.channel_id = a.channel_id
            LEFT JOIN channel_stars cs ON cs.channel_id = a.channel_id
            GROUP BY a.channel_id, c.name, cp.display_name, cp.handle, cs.channel_id
        )
        SELECT v.*,
               CASE WHEN mv.video_id IS NOT NULL THEN 1 ELSE 0 END AS is_viewed,
               mv.viewed_at                                        AS viewed_at,
               c.name                                              AS channel_name,
               c.source_id                                         AS channel_source_id,
               CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_followed,
               ''                                                  AS reposter_channel_id,
               ''                                                  AS reposter_handle,
               ''                                                  AS reposter_display_name,
               ''                                                  AS repost_author_label,
               0                                                   AS repost_count,
               0                                                   AS repost_introduced,
               COALESCE(v.published_at, 0)                         AS effective_moment_at_ms
        FROM active a
        INNER JOIN videos v ON v.video_id = a.video_id
        INNER JOIN channel_order co ON co.channel_id = a.channel_id
        LEFT JOIN moment_views mv ON v.video_id = mv.video_id
        LEFT JOIN channels c      ON c.channel_id = v.channel_id
        LEFT JOIN channel_follows cf ON cf.channel_id = v.channel_id
        ORDER BY CASE WHEN co.unseen_count > 0 THEN 0 ELSE 1 END,
                 CASE WHEN co.is_starred = 1 THEN 0 ELSE 1 END,
                 co.latest_at_ms DESC,
                 co.channel_name COLLATE NOCASE ASC,
                 a.published_at ASC,
                 a.video_id ASC
        """
    )
    fun storyTrayPlaylistFlow(cutoffMs: Long): Flow<List<MomentItem>>
}
