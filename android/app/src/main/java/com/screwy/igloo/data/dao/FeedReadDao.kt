package com.screwy.igloo.data.dao

import androidx.room.Dao
import androidx.room.Query
import androidx.room.RewriteQueriesToDropUnusedColumns
import androidx.room.RoomWarnings
import com.screwy.igloo.data.entity.FeedHeadCandidate
import com.screwy.igloo.data.entity.FeedRow
import com.screwy.igloo.data.entity.FeedRowActionState
import kotlinx.coroutines.flow.Flow

/**
 * Feed read DAO. Single row shape, separate query methods for each feed surface.
 *
 *  - main feed applies visibility filters and `ORDER BY rank_position ASC`
 *  - liked feed requires a bookmark/like side-table row and orders by liked time
 *  - channel feed filters by channel id, falling back to the normalized author
 *    handle for older/local rows, and orders by publish time
 *
 * Room annotation processors can't conditionally rewrite WHERE/ORDER BY inside a single
 * `@Query`, so this DAO exposes one method per surface.
 *
 * `@RewriteQueriesToDropUnusedColumns` lets Room skip columns not present on the
 * projection, sidestepping the "unused column" warning on wide queries.
 */
@Dao
interface FeedReadDao {

    // Main feed applies visibility filters (seen + muted) and rank. Explicit library/detail
    // surfaces like Liked and channel pages intentionally do not hide seen/muted rows.

    @RewriteQueriesToDropUnusedColumns
    @SuppressWarnings(RoomWarnings.QUERY_MISMATCH)
    @Query(
        """
        WITH bookmark_state AS (
            SELECT
                fi2.tweet_id,
                b.video_id,
                b.category_id,
                b.custom_title,
                b.bookmarked_at,
                b.account_handles,
                b.media_indices,
                ROW_NUMBER() OVER (
                    PARTITION BY fi2.tweet_id
                    ORDER BY
                        CASE WHEN b.video_id = fi2.tweet_id THEN 0 ELSE 1 END,
                        b.bookmarked_at DESC,
                        b.video_id DESC
                ) AS bookmark_rank
            FROM feed_items fi2
            JOIN feed_items sibling
              ON sibling.tweet_id = fi2.tweet_id
              OR (
                  NULLIF(TRIM(COALESCE(fi2.content_hash, '')), '') IS NOT NULL
                  AND sibling.content_hash = fi2.content_hash
            )
            JOIN bookmarks b ON b.video_id = sibling.tweet_id
        ),
        seen_hashes AS (
            SELECT DISTINCT seen_fi.content_hash
            FROM feed_seen fs
            JOIN feed_items seen_fi ON seen_fi.tweet_id = fs.tweet_id
            WHERE NULLIF(TRIM(COALESCE(seen_fi.content_hash, '')), '') IS NOT NULL
        )
        SELECT
            fi.*,
            COALESCE(NULLIF(cp.display_name, ''), c.name) AS channel_name,
            c.avatar_url       AS channel_avatar_url,
            COALESCE(c.platform, cp.platform) AS channel_platform,

            CASE WHEN fl.tweet_id IS NOT NULL THEN 1 ELSE 0 END AS is_liked,
            fl.liked_at                                         AS liked_at,

            CASE WHEN bm.video_id IS NOT NULL THEN 1 ELSE 0 END AS is_bookmarked,
            bm.category_id                                      AS bookmark_category_id,
            bm.custom_title                                     AS bookmark_custom_title,
            bm.bookmarked_at                                    AS bookmarked_at,
            bm.account_handles                                  AS bookmark_account_handles,
            bm.media_indices                                    AS bookmark_media_indices,

            CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_followed,
            CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_starred,
            CASE
                WHEN NULLIF(TRIM(COALESCE(fi.quote_author_handle, '')), '') IS NOT NULL
                    THEN 'twitter_' || LOWER(LTRIM(TRIM(fi.quote_author_handle), '@'))
                ELSE NULL
            END AS quote_channel_id,
            CASE WHEN qcf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS quote_channel_is_followed
        FROM feed_items fi
        LEFT JOIN channels        c  ON fi.channel_id = c.channel_id
        LEFT JOIN channel_profiles cp ON fi.channel_id = cp.channel_id
        LEFT JOIN feed_likes      fl ON fi.tweet_id   = fl.tweet_id
        LEFT JOIN feed_rank       fr ON fi.tweet_id   = fr.tweet_id
        LEFT JOIN bookmark_state  bm ON bm.tweet_id = fi.tweet_id AND bm.bookmark_rank = 1
        LEFT JOIN channel_follows cf ON fi.channel_id = cf.channel_id
        LEFT JOIN channel_stars   cs ON fi.channel_id = cs.channel_id
        LEFT JOIN seen_hashes     sh ON sh.content_hash = fi.content_hash
        LEFT JOIN channel_follows qcf
          ON qcf.channel_id = CASE
              WHEN NULLIF(TRIM(COALESCE(fi.quote_author_handle, '')), '') IS NOT NULL
                  THEN 'twitter_' || LOWER(LTRIM(TRIM(fi.quote_author_handle), '@'))
              ELSE NULL
          END
        WHERE
            NOT EXISTS (SELECT 1 FROM feed_seen fs WHERE fs.tweet_id = fi.tweet_id)
            AND (
                NULLIF(TRIM(COALESCE(fi.content_hash, '')), '') IS NULL
                OR sh.content_hash IS NULL
            )
            AND
            fi.author_handle       NOT IN (SELECT handle FROM muted_accounts)
            AND (fi.retweeted_by_handle IS NULL
                 OR fi.retweeted_by_handle NOT IN (SELECT handle FROM muted_accounts))
            AND COALESCE(fi.is_ghost, 0) = 0
        ORDER BY COALESCE(fr.rank_position, 2147483647) ASC, fi.sync_seq DESC, fi.tweet_id DESC
        LIMIT :limit OFFSET :offset
        """
    )
    fun feedFlow(limit: Int = 40, offset: Int = 0): Flow<List<FeedRow>>

    @Query(
        """
        SELECT
            fi.tweet_id,
            fi.author_handle,
            fi.author_display_name,
            fi.channel_id
        FROM feed_items fi
        LEFT JOIN feed_rank fr ON fi.tweet_id = fr.tweet_id
        WHERE
            NOT EXISTS (SELECT 1 FROM muted_accounts ma WHERE ma.handle = fi.author_handle)
            AND (
                fi.retweeted_by_handle IS NULL
                OR NOT EXISTS (SELECT 1 FROM muted_accounts ma WHERE ma.handle = fi.retweeted_by_handle)
            )
            AND COALESCE(fi.is_ghost, 0) = 0
        ORDER BY COALESCE(fr.rank_position, 2147483647) ASC, fi.sync_seq DESC, fi.tweet_id DESC
        LIMIT :limit
        """
    )
    fun mainFeedHeadCandidatesFlow(limit: Int): Flow<List<FeedHeadCandidate>>

    @Query(
        """
        WITH bookmark_state AS (
            SELECT
                fi2.tweet_id,
                b.video_id,
                b.category_id,
                b.custom_title,
                b.bookmarked_at,
                b.account_handles,
                b.media_indices,
                ROW_NUMBER() OVER (
                    PARTITION BY fi2.tweet_id
                    ORDER BY
                        CASE WHEN b.video_id = fi2.tweet_id THEN 0 ELSE 1 END,
                        b.bookmarked_at DESC,
                        b.video_id DESC
                ) AS bookmark_rank
            FROM feed_items fi2
            JOIN feed_items sibling
              ON sibling.tweet_id = fi2.tweet_id
              OR (
                  NULLIF(TRIM(COALESCE(fi2.content_hash, '')), '') IS NOT NULL
                  AND sibling.content_hash = fi2.content_hash
              )
            JOIN bookmarks b ON b.video_id = sibling.tweet_id
            WHERE fi2.tweet_id IN (:tweetIds)
        )
        SELECT
            fi.tweet_id,
            CASE WHEN fl.tweet_id IS NOT NULL THEN 1 ELSE 0 END AS is_liked,
            fl.liked_at                                         AS liked_at,
            CASE WHEN bm.video_id IS NOT NULL THEN 1 ELSE 0 END AS is_bookmarked,
            bm.category_id                                      AS bookmark_category_id,
            bm.custom_title                                     AS bookmark_custom_title,
            bm.bookmarked_at                                    AS bookmarked_at,
            bm.account_handles                                  AS bookmark_account_handles,
            bm.media_indices                                    AS bookmark_media_indices
        FROM feed_items fi
        LEFT JOIN feed_likes     fl ON fi.tweet_id = fl.tweet_id
        LEFT JOIN bookmark_state bm ON bm.tweet_id = fi.tweet_id AND bm.bookmark_rank = 1
        WHERE fi.tweet_id IN (:tweetIds)
        """
    )
    fun actionStateFlow(tweetIds: List<String>): Flow<List<FeedRowActionState>>

    @RewriteQueriesToDropUnusedColumns
    @SuppressWarnings(RoomWarnings.QUERY_MISMATCH)
    @Query(
        """
        WITH bookmark_state AS (
            SELECT
                fi2.tweet_id,
                b.video_id,
                b.category_id,
                b.custom_title,
                b.bookmarked_at,
                ROW_NUMBER() OVER (
                    PARTITION BY fi2.tweet_id
                    ORDER BY
                        CASE WHEN b.video_id = fi2.tweet_id THEN 0 ELSE 1 END,
                        b.bookmarked_at DESC,
                        b.video_id DESC
                ) AS bookmark_rank
            FROM feed_items fi2
            JOIN feed_items sibling
              ON sibling.tweet_id = fi2.tweet_id
              OR (
                  NULLIF(TRIM(COALESCE(fi2.content_hash, '')), '') IS NOT NULL
                  AND sibling.content_hash = fi2.content_hash
              )
            JOIN bookmarks b ON b.video_id = sibling.tweet_id
        )
        SELECT
            fi.*,
            COALESCE(NULLIF(cp.display_name, ''), c.name) AS channel_name,
            c.avatar_url       AS channel_avatar_url,
            COALESCE(c.platform, cp.platform) AS channel_platform,

            CASE WHEN fl.tweet_id IS NOT NULL THEN 1 ELSE 0 END AS is_liked,
            fl.liked_at                                         AS liked_at,

            CASE WHEN bm.video_id IS NOT NULL THEN 1 ELSE 0 END AS is_bookmarked,
            bm.category_id                                      AS bookmark_category_id,
            bm.custom_title                                     AS bookmark_custom_title,
            bm.bookmarked_at                                    AS bookmarked_at,

            CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_followed,
            CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_starred,
            CASE
                WHEN NULLIF(TRIM(COALESCE(fi.quote_author_handle, '')), '') IS NOT NULL
                    THEN 'twitter_' || LOWER(LTRIM(TRIM(fi.quote_author_handle), '@'))
                ELSE NULL
            END AS quote_channel_id,
            CASE WHEN qcf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS quote_channel_is_followed
        FROM feed_items fi
        LEFT JOIN channels        c  ON fi.channel_id = c.channel_id
        LEFT JOIN channel_profiles cp ON fi.channel_id = cp.channel_id
        INNER JOIN feed_likes     fl ON fi.tweet_id   = fl.tweet_id
        LEFT JOIN bookmark_state  bm ON bm.tweet_id = fi.tweet_id AND bm.bookmark_rank = 1
        LEFT JOIN channel_follows cf ON fi.channel_id = cf.channel_id
        LEFT JOIN channel_stars   cs ON fi.channel_id = cs.channel_id
        LEFT JOIN channel_follows qcf
          ON qcf.channel_id = CASE
              WHEN NULLIF(TRIM(COALESCE(fi.quote_author_handle, '')), '') IS NOT NULL
                  THEN 'twitter_' || LOWER(LTRIM(TRIM(fi.quote_author_handle), '@'))
              ELSE NULL
          END
        WHERE COALESCE(fi.is_ghost, 0) = 0
        ORDER BY fl.liked_at DESC, fi.tweet_id DESC
        LIMIT :limit OFFSET :offset
        """
    )
    fun likedFlow(limit: Int = 40, offset: Int = 0): Flow<List<FeedRow>>

    @RewriteQueriesToDropUnusedColumns
    @SuppressWarnings(RoomWarnings.QUERY_MISMATCH)
    @Query(
        """
        WITH bookmark_state AS (
            SELECT
                fi2.tweet_id,
                b.video_id,
                b.category_id,
                b.custom_title,
                b.bookmarked_at,
                ROW_NUMBER() OVER (
                    PARTITION BY fi2.tweet_id
                    ORDER BY
                        CASE WHEN b.video_id = fi2.tweet_id THEN 0 ELSE 1 END,
                        b.bookmarked_at DESC,
                        b.video_id DESC
                ) AS bookmark_rank
            FROM feed_items fi2
            JOIN feed_items sibling
              ON sibling.tweet_id = fi2.tweet_id
              OR (
                  NULLIF(TRIM(COALESCE(fi2.content_hash, '')), '') IS NOT NULL
                  AND sibling.content_hash = fi2.content_hash
              )
            JOIN bookmarks b ON b.video_id = sibling.tweet_id
        )
        SELECT
            fi.tweet_id,
            fi.source_handle,
            fi.author_handle,
            fi.author_display_name,
            fi.author_avatar_url,
            fi.body_text,
            fi.lang,
            fi.is_retweet,
            fi.retweeted_by_handle,
            fi.retweeted_by_display_name,
            fi.quote_tweet_id,
            fi.quote_author_handle,
            fi.quote_author_display_name,
            fi.quote_author_avatar_url,
            fi.quote_body_text,
            fi.quote_lang,
            fi.quote_media_json,
            fi.quote_published_at,
            fi.quote_canonical_url,
            fi.media_json,
            fi.media_status,
            fi.views,
            fi.likes,
            fi.retweets,
            fi.canonical_url,
            fi.canonical_tweet_id,
            fi.reply_to_handle,
            fi.reply_to_status,
            fi.is_reply,
            fi.is_ghost,
            fi.content_hash,
            fi.body_translation,
            fi.quote_translation,
            fi.published_at,
            fi.sync_seq,
            COALESCE(NULLIF(fi.channel_id, ''), :channelId) AS channel_id,
            COALESCE(NULLIF(cp.display_name, ''), c.name) AS channel_name,
            c.avatar_url       AS channel_avatar_url,
            COALESCE(c.platform, cp.platform) AS channel_platform,

            CASE WHEN fl.tweet_id IS NOT NULL THEN 1 ELSE 0 END AS is_liked,
            fl.liked_at                                         AS liked_at,

            CASE WHEN bm.video_id IS NOT NULL THEN 1 ELSE 0 END AS is_bookmarked,
            bm.category_id                                      AS bookmark_category_id,
            bm.custom_title                                     AS bookmark_custom_title,
            bm.bookmarked_at                                    AS bookmarked_at,

            CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_followed,
            CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_starred,
            CASE
                WHEN NULLIF(TRIM(COALESCE(fi.quote_author_handle, '')), '') IS NOT NULL
                    THEN 'twitter_' || LOWER(LTRIM(TRIM(fi.quote_author_handle), '@'))
                ELSE NULL
            END AS quote_channel_id,
            CASE WHEN qcf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS quote_channel_is_followed
        FROM feed_items fi
        LEFT JOIN channels        c  ON c.channel_id = COALESCE(NULLIF(fi.channel_id, ''), :channelId)
        LEFT JOIN channel_profiles cp ON cp.channel_id = COALESCE(NULLIF(fi.channel_id, ''), :channelId)
        LEFT JOIN feed_likes      fl ON fi.tweet_id   = fl.tweet_id
        LEFT JOIN bookmark_state  bm ON bm.tweet_id = fi.tweet_id AND bm.bookmark_rank = 1
        LEFT JOIN channel_follows cf ON COALESCE(NULLIF(fi.channel_id, ''), :channelId) = cf.channel_id
        LEFT JOIN channel_stars   cs ON COALESCE(NULLIF(fi.channel_id, ''), :channelId) = cs.channel_id
        LEFT JOIN channel_follows qcf
          ON qcf.channel_id = CASE
              WHEN NULLIF(TRIM(COALESCE(fi.quote_author_handle, '')), '') IS NOT NULL
                  THEN 'twitter_' || LOWER(LTRIM(TRIM(fi.quote_author_handle), '@'))
              ELSE NULL
          END
        WHERE
            (
                fi.channel_id = :channelId
                OR (
                    :channelHandle != ''
                    AND LOWER(LTRIM(fi.author_handle, '@')) = LOWER(LTRIM(:channelHandle, '@'))
                )
            )
            AND COALESCE(fi.is_ghost, 0) = 0
        ORDER BY fi.published_at DESC, fi.sync_seq DESC, fi.tweet_id DESC
        LIMIT :limit OFFSET :offset
        """
    )
    fun channelFeedFlow(
        channelId: String,
        channelHandle: String = "",
        limit: Int = 40,
        offset: Int = 0,
    ): Flow<List<FeedRow>>

    /**
     * Walk up a conversation chain through reply_to_status and return rows root -> leaf.
     * Ghost rows are intentionally included here; they are hidden from list surfaces but
     * remain joinable context for replies.
     */
    @RewriteQueriesToDropUnusedColumns
    @SuppressWarnings(RoomWarnings.QUERY_MISMATCH)
    @Query(
        """
        WITH RECURSIVE chain(tweet_id, depth) AS (
            SELECT tweet_id, 0
            FROM feed_items
            WHERE tweet_id = :tweetId
            UNION ALL
            SELECT fi.reply_to_status, c.depth + 1
            FROM chain c
            JOIN feed_items fi ON fi.tweet_id = c.tweet_id
            WHERE fi.reply_to_status IS NOT NULL
              AND fi.reply_to_status != ''
              AND c.depth < 50
        )
        SELECT
            fi.*,
            COALESCE(NULLIF(cp.display_name, ''), c.name) AS channel_name,
            c.avatar_url       AS channel_avatar_url,
            COALESCE(c.platform, cp.platform) AS channel_platform,

            CASE WHEN fl.tweet_id IS NOT NULL THEN 1 ELSE 0 END AS is_liked,
            fl.liked_at                                         AS liked_at,

            CASE WHEN bm.video_id IS NOT NULL THEN 1 ELSE 0 END AS is_bookmarked,
            bm.category_id                                      AS bookmark_category_id,
            bm.custom_title                                     AS bookmark_custom_title,
            bm.bookmarked_at                                    AS bookmarked_at,

            CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_followed,
            CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_starred,
            CASE
                WHEN NULLIF(TRIM(COALESCE(fi.quote_author_handle, '')), '') IS NOT NULL
                    THEN 'twitter_' || LOWER(LTRIM(TRIM(fi.quote_author_handle), '@'))
                ELSE NULL
            END AS quote_channel_id,
            CASE WHEN qcf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS quote_channel_is_followed
        FROM chain ch
        JOIN feed_items fi ON fi.tweet_id = ch.tweet_id
        LEFT JOIN channels        c  ON fi.channel_id = c.channel_id
        LEFT JOIN channel_profiles cp ON fi.channel_id = cp.channel_id
        LEFT JOIN feed_likes      fl ON fi.tweet_id   = fl.tweet_id
        LEFT JOIN bookmarks       bm ON bm.video_id   = fi.tweet_id
        LEFT JOIN channel_follows cf ON fi.channel_id = cf.channel_id
        LEFT JOIN channel_stars   cs ON fi.channel_id = cs.channel_id
        LEFT JOIN channel_follows qcf
          ON qcf.channel_id = CASE
              WHEN NULLIF(TRIM(COALESCE(fi.quote_author_handle, '')), '') IS NOT NULL
                  THEN 'twitter_' || LOWER(LTRIM(TRIM(fi.quote_author_handle), '@'))
              ELSE NULL
          END
        WHERE ch.tweet_id IS NOT NULL AND ch.tweet_id != ''
        ORDER BY ch.depth DESC
        """
    )
    suspend fun getThreadChain(tweetId: String): List<FeedRow>

    /**
     * Quotes can be synced as embedded payloads on a parent feed row without a separate
     * feed_items row for the quoted tweet. The thread screen uses this source only as a
     * fallback when the quoted tweet id itself is not present locally.
     */
    @RewriteQueriesToDropUnusedColumns
    @SuppressWarnings(RoomWarnings.QUERY_MISMATCH)
    @Query(
        """
        SELECT
            fi.*,
            COALESCE(NULLIF(cp.display_name, ''), c.name) AS channel_name,
            c.avatar_url       AS channel_avatar_url,
            COALESCE(c.platform, cp.platform) AS channel_platform,

            CASE WHEN fl.tweet_id IS NOT NULL THEN 1 ELSE 0 END AS is_liked,
            fl.liked_at                                         AS liked_at,

            CASE WHEN bm.video_id IS NOT NULL THEN 1 ELSE 0 END AS is_bookmarked,
            bm.category_id                                      AS bookmark_category_id,
            bm.custom_title                                     AS bookmark_custom_title,
            bm.bookmarked_at                                    AS bookmarked_at,

            CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_followed,
            CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_starred,
            CASE
                WHEN NULLIF(TRIM(COALESCE(fi.quote_author_handle, '')), '') IS NOT NULL
                    THEN 'twitter_' || LOWER(LTRIM(TRIM(fi.quote_author_handle), '@'))
                ELSE NULL
            END AS quote_channel_id,
            CASE WHEN qcf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS quote_channel_is_followed
        FROM feed_items fi
        LEFT JOIN channels        c  ON fi.channel_id = c.channel_id
        LEFT JOIN channel_profiles cp ON fi.channel_id = cp.channel_id
        LEFT JOIN feed_likes      fl ON fi.tweet_id   = fl.tweet_id
        LEFT JOIN bookmarks       bm ON bm.video_id   = fi.tweet_id
        LEFT JOIN channel_follows cf ON fi.channel_id = cf.channel_id
        LEFT JOIN channel_stars   cs ON fi.channel_id = cs.channel_id
        LEFT JOIN channel_follows qcf
          ON qcf.channel_id = CASE
              WHEN NULLIF(TRIM(COALESCE(fi.quote_author_handle, '')), '') IS NOT NULL
                  THEN 'twitter_' || LOWER(LTRIM(TRIM(fi.quote_author_handle), '@'))
              ELSE NULL
          END
        WHERE TRIM(COALESCE(fi.quote_tweet_id, '')) = :tweetId
          AND COALESCE(fi.is_ghost, 0) = 0
        ORDER BY fi.published_at DESC, fi.sync_seq DESC, fi.tweet_id DESC
        LIMIT 1
        """
    )
    suspend fun getQuoteFallbackSource(tweetId: String): FeedRow?
}
