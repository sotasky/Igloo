package com.screwy.igloo.data.dao

import androidx.room.Dao
import androidx.room.Query
import androidx.room.RewriteQueriesToDropUnusedColumns
import com.screwy.igloo.data.entity.BookmarkItem
import kotlinx.coroutines.flow.Flow

/**
 * Bookmarks tab mixed-platform list. Orders by `bookmarked_at DESC` and LEFT JOINs both content sources — exactly one matches per
 * row outside a cross-namespace identifier collision.
 *
 * The projection uses `@Embedded(prefix = ...)` on both sides so Room can disambiguate
 * the column collisions (both tables have `video_id`/`tweet_id`-ish keys).
 */
@Dao
interface BookmarkReadDao {

    @RewriteQueriesToDropUnusedColumns
    @Query(
        """
        WITH bookmark_candidates AS (
            SELECT
                b.video_id AS candidate_video_id,
                CASE
                    WHEN fi.tweet_id IS NOT NULL
                         AND NULLIF(TRIM(COALESCE(fi.quote_tweet_id, '')), '') IS NULL
                         AND NULLIF(TRIM(COALESCE(fi.canonical_tweet_id, '')), '') IS NOT NULL
                        THEN 'twitter:' || fi.canonical_tweet_id
                    WHEN fi.tweet_id IS NOT NULL
                         AND NULLIF(TRIM(COALESCE(fi.quote_tweet_id, '')), '') IS NULL
                         AND COALESCE(fi.is_retweet, 0) != 0
                         AND NULLIF(TRIM(COALESCE(fi.content_hash, '')), '') IS NOT NULL
                        THEN 'twitter-hash:' || fi.content_hash
                    ELSE 'item:' || b.video_id
                END AS cluster_key,
                CASE
                    WHEN fi.tweet_id IS NOT NULL
                         AND NULLIF(TRIM(COALESCE(fi.canonical_tweet_id, '')), '') IS NOT NULL
                         AND fi.tweet_id = fi.canonical_tweet_id
                        THEN 0
                    WHEN fi.tweet_id IS NOT NULL
                         AND COALESCE(fi.is_retweet, 0) = 0
                        THEN 1
                    ELSE 2
                END AS representative_rank,
                b.bookmarked_at AS candidate_bookmarked_at,
                COALESCE(fi.published_at, v.published_at, 0) AS candidate_published_at
            FROM bookmarks b
            LEFT JOIN feed_items fi ON b.video_id = fi.tweet_id
            LEFT JOIN videos     v  ON b.video_id = v.video_id
            WHERE fi.tweet_id IS NULL
               OR NULLIF(TRIM(COALESCE(fi.media_json, '')), '') IS NOT NULL
               OR NULLIF(TRIM(COALESCE(fi.quote_media_json, '')), '') IS NOT NULL
        ),
        ranked_bookmarks AS (
            SELECT
                candidate_video_id,
                MAX(candidate_bookmarked_at) OVER (PARTITION BY cluster_key) AS cluster_bookmarked_at,
                ROW_NUMBER() OVER (
                    PARTITION BY cluster_key
                    ORDER BY
                        representative_rank ASC,
                        candidate_bookmarked_at DESC,
                        candidate_published_at DESC,
                        candidate_video_id DESC
                ) AS cluster_rank
            FROM bookmark_candidates
        )
        SELECT
            b.*,

            fi.tweet_id                    AS tw_tweet_id,
            fi.source_channel_id           AS tw_source_channel_id,
            fi.body_text                   AS tw_body_text,
            fi.lang                        AS tw_lang,
            fi.is_retweet                  AS tw_is_retweet,
            fi.reposter_channel_id         AS tw_reposter_channel_id,
            fi.quote_tweet_id              AS tw_quote_tweet_id,
            fi.quote_channel_id            AS tw_quote_channel_id,
            fi.quote_body_text             AS tw_quote_body_text,
            fi.quote_lang                  AS tw_quote_lang,
            fi.quote_media_json            AS tw_quote_media_json,
            fi.quote_published_at          AS tw_quote_published_at,
            fi.quote_canonical_url         AS tw_quote_canonical_url,
            fi.media_json                  AS tw_media_json,
            fi.views                       AS tw_views,
            fi.likes                       AS tw_likes,
            fi.retweets                    AS tw_retweets,
            fi.canonical_url               AS tw_canonical_url,
            fi.canonical_tweet_id          AS tw_canonical_tweet_id,
            fi.reply_channel_id            AS tw_reply_channel_id,
            fi.reply_to_status             AS tw_reply_to_status,
            fi.is_reply                    AS tw_is_reply,
            fi.is_ghost                    AS tw_is_ghost,
            fi.content_hash                AS tw_content_hash,
            fi.body_translation            AS tw_body_translation,
            fi.body_source_lang            AS tw_body_source_lang,
            fi.quote_translation           AS tw_quote_translation,
            fi.quote_source_lang           AS tw_quote_source_lang,
            fi.published_at                AS tw_published_at,
            fi.channel_id                  AS tw_channel_id,
            cp.handle                      AS feed_author_handle,
            COALESCE(NULLIF(cp.display_name, ''), fc.name) AS feed_author_display_name,
            sp.handle                      AS feed_source_handle,
            qp.handle                      AS feed_quote_author_handle,

            v.video_id                     AS vd_video_id,
            v.channel_id                   AS vd_channel_id,
            v.owner_kind                   AS vd_owner_kind,
            v.title                        AS vd_title,
            v.description                  AS vd_description,
            v.duration                     AS vd_duration,
            v.published_at                 AS vd_published_at,
            v.media_kind                   AS vd_media_kind,
            v.slide_count                  AS vd_slide_count,
            v.source_kind                  AS vd_source_kind,
            v.metadata_json                AS vd_metadata_json,
            v.canonical_url                AS vd_canonical_url,
            v.dearrow_title                AS vd_dearrow_title,
            v.dearrow_title_casual         AS vd_dearrow_title_casual,

            COALESCE((
                SELECT COUNT(DISTINCT asa.media_index)
                FROM android_sync_assets asa
                WHERE asa.owner_id = b.video_id
                  AND asa.owner_kind = COALESCE(
                      CASE WHEN fi.tweet_id IS NOT NULL THEN 'tweet' END,
                      v.owner_kind,
                      ''
                  )
                  AND asa.asset_kind = 'post_media'
                  AND asa.state != 'server_missing'
            ), 0) AS asset_media_count,
            COALESCE((
                SELECT COUNT(DISTINCT asa.media_index)
                FROM android_sync_assets asa
                WHERE asa.owner_id = b.video_id
                  AND asa.owner_kind = COALESCE(
                      CASE WHEN fi.tweet_id IS NOT NULL THEN 'tweet' END,
                      v.owner_kind,
                      ''
                  )
                  AND asa.asset_kind = 'post_media'
                  AND asa.state != 'server_missing'
                  AND LOWER(COALESCE(asa.content_type, '')) LIKE 'video/%'
            ), 0) AS asset_video_count,
            COALESCE((
                SELECT COUNT(DISTINCT asa.media_index)
                FROM android_sync_assets asa
                WHERE asa.owner_id = b.video_id
                  AND asa.owner_kind = COALESCE(
                      CASE WHEN fi.tweet_id IS NOT NULL THEN 'tweet' END,
                      v.owner_kind,
                      ''
                  )
                  AND asa.asset_kind = 'post_media'
                  AND asa.state != 'server_missing'
                  AND LOWER(COALESCE(asa.content_type, '')) LIKE 'image/%'
            ), 0) AS asset_image_count,

            COALESCE(fi.channel_id, v.channel_id) AS resolved_channel_id,
            COALESCE(fc.name, vc.name)            AS resolved_channel_name,
            COALESCE(fc.source_id, vc.source_id)  AS resolved_channel_source_id,
            CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS resolved_channel_is_followed
        FROM ranked_bookmarks rb
        INNER JOIN bookmarks b ON b.video_id = rb.candidate_video_id
        LEFT JOIN feed_items fi ON b.video_id = fi.tweet_id
        LEFT JOIN videos     v  ON b.video_id = v.video_id
        LEFT JOIN channels   fc ON fc.channel_id = fi.channel_id
        LEFT JOIN channels   vc ON vc.channel_id = v.channel_id
		LEFT JOIN channel_profiles cp ON cp.channel_id = fi.channel_id
		LEFT JOIN channel_profiles sp ON sp.channel_id = fi.source_channel_id
		LEFT JOIN channel_profiles qp ON qp.channel_id = fi.quote_channel_id
		LEFT JOIN channel_follows cf ON cf.channel_id = COALESCE(
			NULLIF(fi.channel_id, ''),
			NULLIF(v.channel_id, '')
		)
        WHERE rb.cluster_rank = 1
        ORDER BY rb.cluster_bookmarked_at DESC, b.bookmarked_at DESC, b.video_id DESC
        """
    )
    fun bookmarksFlow(): Flow<List<BookmarkItem>>
}
