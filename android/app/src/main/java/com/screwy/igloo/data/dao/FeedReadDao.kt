package com.screwy.igloo.data.dao

import androidx.room.Dao
import androidx.room.Query
import androidx.room.RewriteQueriesToDropUnusedColumns
import androidx.room.RoomWarnings
import com.screwy.igloo.data.entity.FeedHeadCandidate
import com.screwy.igloo.data.entity.FeedRow
import com.screwy.igloo.data.entity.FeedRowActionState
import com.screwy.igloo.data.entity.FeedThreadContext
import kotlinx.coroutines.flow.Flow

private const val MAIN_FEED_REPOST_VISIBILITY =
    """
    NOT (
        COALESCE(fi.is_retweet, 0) = 1
        AND COALESCE(fi.source_channel_id, '') != ''
        AND fi.source_channel_id != COALESCE(fi.channel_id, '')
        AND EXISTS (
            SELECT 1 FROM channel_settings source_settings
            WHERE source_settings.channel_id = fi.source_channel_id
              AND source_settings.include_reposts = 0
        )
        AND NOT EXISTS (
            SELECT 1 FROM retweet_sources source
            LEFT JOIN channel_settings retweeter_settings
              ON retweeter_settings.channel_id = source.retweeter_channel_id
            WHERE source.content_hash = COALESCE(fi.content_hash, '')
              AND COALESCE(retweeter_settings.include_reposts, 1) != 0
        )
    )
    AND NOT (
        COALESCE(fi.quote_tweet_id, '') != ''
        AND COALESCE(fi.channel_id, '') != ''
        AND fi.channel_id != COALESCE(fi.quote_channel_id, '')
        AND EXISTS (
            SELECT 1 FROM channel_settings author_settings
            WHERE author_settings.channel_id = fi.channel_id
              AND author_settings.include_reposts = 0
        )
    )
    """

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
            COALESCE(c.platform, cp.platform) AS channel_platform,
            COALESCE(cp.handle, '') AS author_handle,
            COALESCE(NULLIF(cp.display_name, ''), c.name, '') AS author_display_name,
            COALESCE(sp.handle, '') AS source_handle,
            COALESCE(sp.display_name, '') AS source_display_name,
            COALESCE(qp.handle, '') AS quote_author_handle,
            COALESCE(qp.display_name, '') AS quote_author_display_name,
            COALESCE(replyp.handle, '') AS reply_handle,
            COALESCE(rp.handle, '') AS reposter_handle,
            COALESCE(rp.display_name, '') AS reposter_display_name,

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
            CASE WHEN qcf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS quote_channel_is_followed
        FROM feed_items fi
        LEFT JOIN channels        c  ON fi.channel_id = c.channel_id
        LEFT JOIN channel_profiles cp ON fi.channel_id = cp.channel_id
        LEFT JOIN channel_profiles sp ON fi.source_channel_id = sp.channel_id
        LEFT JOIN channel_profiles qp ON fi.quote_channel_id = qp.channel_id
        LEFT JOIN channel_profiles replyp ON fi.reply_channel_id = replyp.channel_id
        LEFT JOIN channel_profiles rp ON fi.reposter_channel_id = rp.channel_id
        LEFT JOIN feed_likes      fl ON fi.tweet_id   = fl.tweet_id
        LEFT JOIN feed_rank       fr ON fi.tweet_id   = fr.tweet_id
        LEFT JOIN bookmark_state  bm ON bm.tweet_id = fi.tweet_id AND bm.bookmark_rank = 1
        LEFT JOIN channel_follows cf ON fi.channel_id = cf.channel_id
        LEFT JOIN channel_stars   cs ON fi.channel_id = cs.channel_id
        LEFT JOIN seen_hashes     sh ON sh.content_hash = fi.content_hash
        LEFT JOIN channel_follows qcf ON qcf.channel_id = fi.quote_channel_id
        WHERE
            NOT EXISTS (SELECT 1 FROM feed_seen fs WHERE fs.tweet_id = fi.tweet_id)
            AND (
                NULLIF(TRIM(COALESCE(fi.content_hash, '')), '') IS NULL
                OR sh.content_hash IS NULL
            )
            AND NOT EXISTS (
                SELECT 1 FROM muted_channels mc WHERE mc.channel_id = fi.channel_id
            )
            AND NOT EXISTS (
                SELECT 1 FROM muted_channels mc WHERE mc.channel_id = fi.reposter_channel_id
            )
            AND COALESCE(fi.is_ghost, 0) = 0
            AND (
        """ + MAIN_FEED_REPOST_VISIBILITY + """
            )
        ORDER BY COALESCE(fr.rank_position, 2147483647) ASC, fi.published_at DESC, fi.tweet_id DESC
        LIMIT :limit OFFSET :offset
        """
    )
    fun feedFlow(limit: Int = 40, offset: Int = 0): Flow<List<FeedRow>>

    @Query(
        """
        SELECT
            fi.tweet_id,
            COALESCE(cp.handle, '') AS author_handle,
            COALESCE(NULLIF(cp.display_name, ''), c.name, '') AS author_display_name,
            fi.channel_id
        FROM feed_items fi
        LEFT JOIN feed_rank fr ON fi.tweet_id = fr.tweet_id
        LEFT JOIN channels c ON fi.channel_id = c.channel_id
        LEFT JOIN channel_profiles cp ON fi.channel_id = cp.channel_id
        WHERE
            NOT EXISTS (SELECT 1 FROM muted_channels mc WHERE mc.channel_id = fi.channel_id)
            AND NOT EXISTS (SELECT 1 FROM muted_channels mc WHERE mc.channel_id = fi.reposter_channel_id)
            AND COALESCE(fi.is_ghost, 0) = 0
            AND (
        """ + MAIN_FEED_REPOST_VISIBILITY + """
            )
        ORDER BY COALESCE(fr.rank_position, 2147483647) ASC, fi.published_at DESC, fi.tweet_id DESC
        LIMIT :limit
        """
    )
    fun mainFeedHeadCandidatesFlow(limit: Int): Flow<List<FeedHeadCandidate>>

    @Query(
        """
        WITH RECURSIVE reply_chain(leaf_tweet_id, tweet_id, reply_to_status, depth) AS (
            SELECT tweet_id, tweet_id, reply_to_status, 0
            FROM feed_items
            WHERE tweet_id IN (:tweetIds)
              AND COALESCE(reply_to_status, '') != ''

            UNION ALL

            SELECT rc.leaf_tweet_id, parent.tweet_id, parent.reply_to_status, rc.depth + 1
            FROM reply_chain rc
            JOIN feed_items parent ON parent.tweet_id = rc.reply_to_status
            WHERE rc.depth < 50
        ),
        depths AS (
            SELECT leaf_tweet_id, MAX(depth) AS max_depth
            FROM reply_chain
            GROUP BY leaf_tweet_id
        ),
        roots AS (
            SELECT rc.leaf_tweet_id, rc.tweet_id AS root_tweet_id
            FROM reply_chain rc
            JOIN depths d ON d.leaf_tweet_id = rc.leaf_tweet_id AND d.max_depth = rc.depth
        )
        SELECT rc.leaf_tweet_id,
               roots.root_tweet_id,
               rc.tweet_id AS ancestor_tweet_id,
               depths.max_depth - rc.depth AS ancestor_order
        FROM reply_chain rc
        JOIN depths ON depths.leaf_tweet_id = rc.leaf_tweet_id
        JOIN roots ON roots.leaf_tweet_id = rc.leaf_tweet_id
        WHERE rc.depth > 0
        ORDER BY rc.leaf_tweet_id ASC, ancestor_order ASC
        """
    )
    suspend fun getThreadContexts(tweetIds: List<String>): List<FeedThreadContext>

    @RewriteQueriesToDropUnusedColumns
    @SuppressWarnings(RoomWarnings.QUERY_MISMATCH)
    @Query(
        """
        SELECT
            fi.*,
            COALESCE(NULLIF(cp.display_name, ''), c.name) AS channel_name,
            COALESCE(c.platform, cp.platform) AS channel_platform,
            COALESCE(cp.handle, '') AS author_handle,
            COALESCE(NULLIF(cp.display_name, ''), c.name, '') AS author_display_name,
            COALESCE(sp.handle, '') AS source_handle,
            COALESCE(sp.display_name, '') AS source_display_name,
            COALESCE(qp.handle, '') AS quote_author_handle,
            COALESCE(qp.display_name, '') AS quote_author_display_name,
            COALESCE(replyp.handle, '') AS reply_handle,
            COALESCE(rp.handle, '') AS reposter_handle,
            COALESCE(rp.display_name, '') AS reposter_display_name,

            CASE WHEN fl.tweet_id IS NOT NULL THEN 1 ELSE 0 END AS is_liked,
            fl.liked_at                                         AS liked_at,

            CASE WHEN bm.video_id IS NOT NULL THEN 1 ELSE 0 END AS is_bookmarked,
            bm.category_id                                      AS bookmark_category_id,
            bm.custom_title                                     AS bookmark_custom_title,
            bm.bookmarked_at                                    AS bookmarked_at,

            CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_followed,
            CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_starred,
            CASE WHEN qcf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS quote_channel_is_followed
        FROM feed_items fi
        LEFT JOIN channels        c  ON fi.channel_id = c.channel_id
        LEFT JOIN channel_profiles cp ON fi.channel_id = cp.channel_id
        LEFT JOIN channel_profiles sp ON fi.source_channel_id = sp.channel_id
        LEFT JOIN channel_profiles qp ON fi.quote_channel_id = qp.channel_id
        LEFT JOIN channel_profiles replyp ON fi.reply_channel_id = replyp.channel_id
        LEFT JOIN channel_profiles rp ON fi.reposter_channel_id = rp.channel_id
        LEFT JOIN feed_likes      fl ON fi.tweet_id   = fl.tweet_id
        LEFT JOIN bookmarks       bm ON bm.video_id   = fi.tweet_id
        LEFT JOIN channel_follows cf ON fi.channel_id = cf.channel_id
        LEFT JOIN channel_stars   cs ON fi.channel_id = cs.channel_id
        LEFT JOIN channel_follows qcf ON qcf.channel_id = fi.quote_channel_id
        WHERE fi.tweet_id IN (:tweetIds)
        """
    )
    suspend fun getFeedRowsByTweetIds(tweetIds: List<String>): List<FeedRow>

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
            COALESCE(c.platform, cp.platform) AS channel_platform,
            COALESCE(cp.handle, '') AS author_handle,
            COALESCE(NULLIF(cp.display_name, ''), c.name, '') AS author_display_name,
            COALESCE(sp.handle, '') AS source_handle,
            COALESCE(sp.display_name, '') AS source_display_name,
            COALESCE(qp.handle, '') AS quote_author_handle,
            COALESCE(qp.display_name, '') AS quote_author_display_name,
            COALESCE(replyp.handle, '') AS reply_handle,
            COALESCE(rp.handle, '') AS reposter_handle,
            COALESCE(rp.display_name, '') AS reposter_display_name,

            CASE WHEN fl.tweet_id IS NOT NULL THEN 1 ELSE 0 END AS is_liked,
            fl.liked_at                                         AS liked_at,

            CASE WHEN bm.video_id IS NOT NULL THEN 1 ELSE 0 END AS is_bookmarked,
            bm.category_id                                      AS bookmark_category_id,
            bm.custom_title                                     AS bookmark_custom_title,
            bm.bookmarked_at                                    AS bookmarked_at,

            CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_followed,
            CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_starred,
            CASE WHEN qcf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS quote_channel_is_followed
        FROM feed_items fi
        LEFT JOIN channels        c  ON fi.channel_id = c.channel_id
        LEFT JOIN channel_profiles cp ON fi.channel_id = cp.channel_id
        LEFT JOIN channel_profiles sp ON fi.source_channel_id = sp.channel_id
        LEFT JOIN channel_profiles qp ON fi.quote_channel_id = qp.channel_id
        LEFT JOIN channel_profiles replyp ON fi.reply_channel_id = replyp.channel_id
        LEFT JOIN channel_profiles rp ON fi.reposter_channel_id = rp.channel_id
        INNER JOIN feed_likes     fl ON fi.tweet_id   = fl.tweet_id
        LEFT JOIN bookmark_state  bm ON bm.tweet_id = fi.tweet_id AND bm.bookmark_rank = 1
        LEFT JOIN channel_follows cf ON fi.channel_id = cf.channel_id
        LEFT JOIN channel_stars   cs ON fi.channel_id = cs.channel_id
        LEFT JOIN channel_follows qcf ON qcf.channel_id = fi.quote_channel_id
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
            fi.*,
            COALESCE(NULLIF(cp.display_name, ''), c.name) AS channel_name,
            COALESCE(c.platform, cp.platform) AS channel_platform,
            COALESCE(cp.handle, '') AS author_handle,
            COALESCE(NULLIF(cp.display_name, ''), c.name, '') AS author_display_name,
            COALESCE(sp.handle, '') AS source_handle,
            COALESCE(sp.display_name, '') AS source_display_name,
            COALESCE(qp.handle, '') AS quote_author_handle,
            COALESCE(qp.display_name, '') AS quote_author_display_name,
            COALESCE(replyp.handle, '') AS reply_handle,
            COALESCE(rp.handle, '') AS reposter_handle,
            COALESCE(rp.display_name, '') AS reposter_display_name,

            CASE WHEN fl.tweet_id IS NOT NULL THEN 1 ELSE 0 END AS is_liked,
            fl.liked_at                                         AS liked_at,

            CASE WHEN bm.video_id IS NOT NULL THEN 1 ELSE 0 END AS is_bookmarked,
            bm.category_id                                      AS bookmark_category_id,
            bm.custom_title                                     AS bookmark_custom_title,
            bm.bookmarked_at                                    AS bookmarked_at,

            CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_followed,
            CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_starred,
            CASE WHEN qcf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS quote_channel_is_followed
        FROM feed_items fi
        LEFT JOIN channels        c  ON c.channel_id = fi.channel_id
        LEFT JOIN channel_profiles cp ON cp.channel_id = fi.channel_id
        LEFT JOIN channel_profiles sp ON fi.source_channel_id = sp.channel_id
        LEFT JOIN channel_profiles qp ON fi.quote_channel_id = qp.channel_id
        LEFT JOIN channel_profiles replyp ON fi.reply_channel_id = replyp.channel_id
        LEFT JOIN channel_profiles rp ON fi.reposter_channel_id = rp.channel_id
        LEFT JOIN feed_likes      fl ON fi.tweet_id   = fl.tweet_id
        LEFT JOIN bookmark_state  bm ON bm.tweet_id = fi.tweet_id AND bm.bookmark_rank = 1
        LEFT JOIN channel_follows cf ON fi.channel_id = cf.channel_id
        LEFT JOIN channel_stars   cs ON fi.channel_id = cs.channel_id
        LEFT JOIN channel_follows qcf ON qcf.channel_id = fi.quote_channel_id
        WHERE
            (
                :channelId IN (fi.channel_id, fi.source_channel_id, fi.reposter_channel_id, fi.quote_channel_id)
                OR (
                    :channelHandle != ''
                    AND (
                        LOWER(LTRIM(COALESCE(cp.handle, ''), '@')) = LOWER(LTRIM(:channelHandle, '@'))
                        OR LOWER(LTRIM(COALESCE(sp.handle, ''), '@')) = LOWER(LTRIM(:channelHandle, '@'))
                        OR LOWER(LTRIM(COALESCE(rp.handle, ''), '@')) = LOWER(LTRIM(:channelHandle, '@'))
                        OR LOWER(LTRIM(COALESCE(qp.handle, ''), '@')) = LOWER(LTRIM(:channelHandle, '@'))
                    )
                )
            )
            AND COALESCE(fi.is_ghost, 0) = 0
        ORDER BY fi.published_at DESC, fi.tweet_id DESC
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
            COALESCE(c.platform, cp.platform) AS channel_platform,
            COALESCE(cp.handle, '') AS author_handle,
            COALESCE(NULLIF(cp.display_name, ''), c.name, '') AS author_display_name,
            COALESCE(sp.handle, '') AS source_handle,
            COALESCE(sp.display_name, '') AS source_display_name,
            COALESCE(qp.handle, '') AS quote_author_handle,
            COALESCE(qp.display_name, '') AS quote_author_display_name,
            COALESCE(replyp.handle, '') AS reply_handle,
            COALESCE(rp.handle, '') AS reposter_handle,
            COALESCE(rp.display_name, '') AS reposter_display_name,

            CASE WHEN fl.tweet_id IS NOT NULL THEN 1 ELSE 0 END AS is_liked,
            fl.liked_at                                         AS liked_at,

            CASE WHEN bm.video_id IS NOT NULL THEN 1 ELSE 0 END AS is_bookmarked,
            bm.category_id                                      AS bookmark_category_id,
            bm.custom_title                                     AS bookmark_custom_title,
            bm.bookmarked_at                                    AS bookmarked_at,

            CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_followed,
            CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_starred,
            CASE WHEN qcf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS quote_channel_is_followed
        FROM chain ch
        JOIN feed_items fi ON fi.tweet_id = ch.tweet_id
        LEFT JOIN channels        c  ON fi.channel_id = c.channel_id
        LEFT JOIN channel_profiles cp ON fi.channel_id = cp.channel_id
        LEFT JOIN channel_profiles sp ON fi.source_channel_id = sp.channel_id
        LEFT JOIN channel_profiles qp ON fi.quote_channel_id = qp.channel_id
        LEFT JOIN channel_profiles replyp ON fi.reply_channel_id = replyp.channel_id
        LEFT JOIN channel_profiles rp ON fi.reposter_channel_id = rp.channel_id
        LEFT JOIN feed_likes      fl ON fi.tweet_id   = fl.tweet_id
        LEFT JOIN bookmarks       bm ON bm.video_id   = fi.tweet_id
        LEFT JOIN channel_follows cf ON fi.channel_id = cf.channel_id
        LEFT JOIN channel_stars   cs ON fi.channel_id = cs.channel_id
        LEFT JOIN channel_follows qcf ON qcf.channel_id = fi.quote_channel_id
        WHERE ch.tweet_id IS NOT NULL AND ch.tweet_id != ''
        ORDER BY ch.depth DESC
        """
    )
    suspend fun getThreadChain(tweetId: String): List<FeedRow>

    /**
     * Return the conversation root followed by the selected top-level reply branch.
     * This mirrors the web thread route: sibling reply branches under the same root
     * remain separate, while descendants of the selected branch are included.
     */
    @RewriteQueriesToDropUnusedColumns
    @SuppressWarnings(RoomWarnings.QUERY_MISMATCH)
    @Query(
        """
        WITH RECURSIVE
        chain(tweet_id, depth) AS (
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
        ),
        root_depth(max_depth) AS (
            SELECT COALESCE(MAX(depth), 0) FROM chain
        ),
        root(root_tweet_id) AS (
            SELECT tweet_id
            FROM chain
            WHERE depth = (SELECT max_depth FROM root_depth)
            LIMIT 1
        ),
        branch(branch_tweet_id, branch_depth) AS (
            SELECT tweet_id,
                   CASE WHEN (SELECT max_depth FROM root_depth) > 0 THEN 1 ELSE 0 END
            FROM chain
            WHERE depth = CASE
                WHEN (SELECT max_depth FROM root_depth) > 0
                    THEN (SELECT max_depth FROM root_depth) - 1
                ELSE 0
            END
            LIMIT 1
        ),
        subtree(tweet_id, parent_id, depth, sort_path) AS (
            SELECT fi.tweet_id,
                   COALESCE(fi.reply_to_status, ''),
                   (SELECT branch_depth FROM branch),
                   printf('%020d:%s', COALESCE(fi.published_at, 0), fi.tweet_id)
            FROM feed_items fi
            WHERE fi.tweet_id = (SELECT branch_tweet_id FROM branch)
            UNION ALL
            SELECT child.tweet_id,
                   child.reply_to_status,
                   subtree.depth + 1,
                   subtree.sort_path || '/' || printf('%020d:%s', COALESCE(child.published_at, 0), child.tweet_id)
            FROM feed_items child
            JOIN subtree ON child.reply_to_status = subtree.tweet_id
            WHERE subtree.depth < 50
        ),
        selected(tweet_id, depth, sort_path) AS (
            SELECT fi.tweet_id, 0, ''
            FROM feed_items fi
            WHERE fi.tweet_id = (SELECT root_tweet_id FROM root)
              AND (SELECT root_tweet_id FROM root) != (SELECT branch_tweet_id FROM branch)
            UNION ALL
            SELECT tweet_id, depth, sort_path FROM subtree
        )
        SELECT
            fi.*,
            COALESCE(NULLIF(cp.display_name, ''), c.name) AS channel_name,
            COALESCE(c.platform, cp.platform) AS channel_platform,
            COALESCE(cp.handle, '') AS author_handle,
            COALESCE(NULLIF(cp.display_name, ''), c.name, '') AS author_display_name,
            COALESCE(sp.handle, '') AS source_handle,
            COALESCE(sp.display_name, '') AS source_display_name,
            COALESCE(qp.handle, '') AS quote_author_handle,
            COALESCE(qp.display_name, '') AS quote_author_display_name,
            COALESCE(replyp.handle, '') AS reply_handle,
            COALESCE(rp.handle, '') AS reposter_handle,
            COALESCE(rp.display_name, '') AS reposter_display_name,

            CASE WHEN fl.tweet_id IS NOT NULL THEN 1 ELSE 0 END AS is_liked,
            fl.liked_at                                         AS liked_at,

            CASE WHEN bm.video_id IS NOT NULL THEN 1 ELSE 0 END AS is_bookmarked,
            bm.category_id                                      AS bookmark_category_id,
            bm.custom_title                                     AS bookmark_custom_title,
            bm.bookmarked_at                                    AS bookmarked_at,

            CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_followed,
            CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_starred,
            CASE WHEN qcf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS quote_channel_is_followed
        FROM selected st
        JOIN feed_items fi ON fi.tweet_id = st.tweet_id
        LEFT JOIN channels        c  ON fi.channel_id = c.channel_id
        LEFT JOIN channel_profiles cp ON fi.channel_id = cp.channel_id
        LEFT JOIN channel_profiles sp ON fi.source_channel_id = sp.channel_id
        LEFT JOIN channel_profiles qp ON fi.quote_channel_id = qp.channel_id
        LEFT JOIN channel_profiles replyp ON fi.reply_channel_id = replyp.channel_id
        LEFT JOIN channel_profiles rp ON fi.reposter_channel_id = rp.channel_id
        LEFT JOIN feed_likes      fl ON fi.tweet_id   = fl.tweet_id
        LEFT JOIN bookmarks       bm ON bm.video_id   = fi.tweet_id
        LEFT JOIN channel_follows cf ON fi.channel_id = cf.channel_id
        LEFT JOIN channel_stars   cs ON fi.channel_id = cs.channel_id
        LEFT JOIN channel_follows qcf ON qcf.channel_id = fi.quote_channel_id
        WHERE st.tweet_id IS NOT NULL AND st.tweet_id != ''
        ORDER BY st.sort_path
        """
    )
    suspend fun getThreadTree(tweetId: String): List<FeedRow>

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
            COALESCE(c.platform, cp.platform) AS channel_platform,
            COALESCE(cp.handle, '') AS author_handle,
            COALESCE(NULLIF(cp.display_name, ''), c.name, '') AS author_display_name,
            COALESCE(sp.handle, '') AS source_handle,
            COALESCE(sp.display_name, '') AS source_display_name,
            COALESCE(qp.handle, '') AS quote_author_handle,
            COALESCE(qp.display_name, '') AS quote_author_display_name,
            COALESCE(replyp.handle, '') AS reply_handle,
            COALESCE(rp.handle, '') AS reposter_handle,
            COALESCE(rp.display_name, '') AS reposter_display_name,

            CASE WHEN fl.tweet_id IS NOT NULL THEN 1 ELSE 0 END AS is_liked,
            fl.liked_at                                         AS liked_at,

            CASE WHEN bm.video_id IS NOT NULL THEN 1 ELSE 0 END AS is_bookmarked,
            bm.category_id                                      AS bookmark_category_id,
            bm.custom_title                                     AS bookmark_custom_title,
            bm.bookmarked_at                                    AS bookmarked_at,

            CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_followed,
            CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END AS channel_is_starred,
            CASE WHEN qcf.channel_id IS NOT NULL THEN 1 ELSE 0 END AS quote_channel_is_followed
        FROM feed_items fi
        LEFT JOIN channels        c  ON fi.channel_id = c.channel_id
        LEFT JOIN channel_profiles cp ON fi.channel_id = cp.channel_id
        LEFT JOIN channel_profiles sp ON fi.source_channel_id = sp.channel_id
        LEFT JOIN channel_profiles qp ON fi.quote_channel_id = qp.channel_id
        LEFT JOIN channel_profiles replyp ON fi.reply_channel_id = replyp.channel_id
        LEFT JOIN channel_profiles rp ON fi.reposter_channel_id = rp.channel_id
        LEFT JOIN feed_likes      fl ON fi.tweet_id   = fl.tweet_id
        LEFT JOIN bookmarks       bm ON bm.video_id   = fi.tweet_id
        LEFT JOIN channel_follows cf ON fi.channel_id = cf.channel_id
        LEFT JOIN channel_stars   cs ON fi.channel_id = cs.channel_id
        LEFT JOIN channel_follows qcf ON qcf.channel_id = fi.quote_channel_id
        WHERE TRIM(COALESCE(fi.quote_tweet_id, '')) = :tweetId
          AND COALESCE(fi.is_ghost, 0) = 0
        ORDER BY fi.published_at DESC, fi.tweet_id DESC
        LIMIT 1
        """
    )
    suspend fun getQuoteFallbackSource(tweetId: String): FeedRow?
}
