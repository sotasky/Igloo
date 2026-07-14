package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
)

const (
	feedSnapshotHistoryRetention         = 6 * time.Hour
	feedRankingStarBoost                 = 25.0
	feedAbsenceBoostCapHoursSetting      = "feed_absence_boost_cap_hours"
	feedAbsenceBoostMaxStarFactorSetting = "feed_absence_boost_max_star_factor"
	feedNeverSeenBoostStarFactorSetting  = "feed_never_seen_boost_star_factor"
	defaultFeedAbsenceBoostCapHours      = 72.0
	defaultFeedAbsenceBoostMaxStarFactor = 0.5
	defaultFeedNeverSeenBoostStarFactor  = 1.0
	feedFreshnessBonusPeak               = 8.0
	feedFreshnessBonusWindowHours        = 6.0
	feedSeenRelatedContentPenalty        = 5.0
	feedRepeatedRelatedContentPenalty    = 12.0
	feedReplyPenalty                     = 4.0
)

var ErrFeedSnapshotExpired = errors.New("feed snapshot expired")

func (db *DB) feedAbsenceBoostConfig() (capHours, seenMaxBoost, neverSeenBoost float64) {
	capHours = db.FloatSetting(feedAbsenceBoostCapHoursSetting, defaultFeedAbsenceBoostCapHours)
	capHours = safeFeedAbsenceCapHours(capHours)

	seenFactor := db.FloatSetting(feedAbsenceBoostMaxStarFactorSetting, defaultFeedAbsenceBoostMaxStarFactor)
	if seenFactor < 0 {
		seenFactor = 0
	}
	neverFactor := db.FloatSetting(feedNeverSeenBoostStarFactorSetting, defaultFeedNeverSeenBoostStarFactor)
	if neverFactor < 0 {
		neverFactor = 0
	}

	seenMaxBoost = feedRankingStarBoost * seenFactor
	neverSeenBoost = feedRankingStarBoost * neverFactor
	return capHours, seenMaxBoost, neverSeenBoost
}

func feedPrioritySourceSQL(alias string) string {
	return fmt.Sprintf(`NULLIF(%[1]s.source_channel_id, '') IS NOT NULL
				 AND %[1]s.source_channel_id != COALESCE(%[1]s.channel_id, '')
				 AND (cf_source.channel_id IS NOT NULL OR cs_source.channel_id IS NOT NULL)`, alias)
}

func feedRankingAccountIDSQL(alias string) string {
	return fmt.Sprintf(`CASE
				WHEN %[2]s THEN %[1]s.source_channel_id
				ELSE %[1]s.channel_id
			END`, alias, feedPrioritySourceSQL(alias))
}

func feedRankingAccountIsPrioritySQL(alias string) string {
	return fmt.Sprintf(`CASE
				WHEN %[1]s THEN 1
				WHEN cf_author.channel_id IS NOT NULL OR cs_author.channel_id IS NOT NULL THEN 1
				ELSE 0
			END`, feedPrioritySourceSQL(alias))
}

func feedAbsenceBoostSelect(alias string) string {
	return fmt.Sprintf(`CASE
				WHEN NULLIF(%[2]s, '') IS NOT NULL
				 AND %[3]s = 1
				THEN CASE
					WHEN lps.last_seen_at IS NULL THEN ?
					ELSE ? * MIN(?, MAX(0, ((CAST(strftime('%%s','now') AS INTEGER) * 1000) - lps.last_seen_at) / 3600000.0)) / ?
				END
				ELSE 0
		END`, alias, feedRankingAccountIDSQL(alias), feedRankingAccountIsPrioritySQL(alias))
}

func feedRankingFromSQL(relatedSeenExpr, absenceExpr, candidateJoin string) string {
	return fmt.Sprintf(`
				FROM (
				    SELECT fi.*,
				           (CAST(strftime('%%s','now') AS INTEGER) * 1000 - fi.published_at) / 3600000.0 AS age_h,
				           %s AS related_seen_count,
				           %s AS absence_boost
				    FROM feed_items fi
				    %s
				    LEFT JOIN (
				        SELECT %s AS related_key,
				               COUNT(*) AS related_seen_count
				        FROM seen_feed seen_fi
				        GROUP BY %s
				    ) rsc ON rsc.related_key = %s
			    LEFT JOIN channel_follows cf_author
			      ON cf_author.channel_id = fi.channel_id
			    LEFT JOIN channel_stars cs_author
			      ON cs_author.channel_id = fi.channel_id
			    LEFT JOIN channel_follows cf_source
			      ON cf_source.channel_id = fi.source_channel_id
			    LEFT JOIN channel_stars cs_source
			      ON cs_source.channel_id = fi.source_channel_id
				    LEFT JOIN (
				        SELECT channel_id, MAX(seen_at) AS last_seen_at
				        FROM (
			            SELECT parent.channel_id, parent.seen_at
			            FROM seen_feed parent
			            WHERE NULLIF(parent.channel_id, '') IS NOT NULL
				              AND COALESCE(parent.is_ghost, 0) = 0
				            UNION ALL
			            SELECT parent.source_channel_id, parent.seen_at
			            FROM seen_feed parent
			            WHERE NULLIF(parent.source_channel_id, '') IS NOT NULL
				              AND COALESCE(parent.is_ghost, 0) = 0
				        ) seen_channels
				        GROUP BY channel_id
				    ) lps ON lps.channel_id = %s
			) fi
				`, relatedSeenExpr, absenceExpr, candidateJoin,
		feedRelatedContentKeySQL("seen_fi"),
		feedRelatedContentKeySQL("seen_fi"),
		feedRelatedContentKeySQL("fi"),
		feedRankingAccountIDSQL("fi"),
	)
}

func feedRelatedContentKeySQL(alias string) string {
	return fmt.Sprintf(`CASE
		WHEN NULLIF(TRIM(COALESCE(%[1]s.quote_tweet_id, '')), '') IS NOT NULL
			THEN 'tweet:' || LOWER(TRIM(%[1]s.quote_tweet_id))
		WHEN NULLIF(TRIM(COALESCE(%[1]s.canonical_tweet_id, '')), '') IS NOT NULL
			THEN 'tweet:' || LOWER(TRIM(%[1]s.canonical_tweet_id))
		ELSE 'tweet:' || LOWER(TRIM(%[1]s.tweet_id))
	END`, alias)
}

func feedRelatedSeenCountSelect(alias string) string {
	_ = alias
	return `COALESCE(rsc.related_seen_count, 0)`
}

func feedRelatedContentPenaltySQL(alias string) string {
	return fmt.Sprintf(`CASE
		WHEN COALESCE(%s.related_seen_count, 0) >= 2 THEN %.1f
		WHEN COALESCE(%s.related_seen_count, 0) = 1 THEN %.1f
		ELSE 0
	END`, alias, feedRepeatedRelatedContentPenalty, alias, feedSeenRelatedContentPenalty)
}

func feedRankingBaseScoreSQL(alias string) string {
	return fmt.Sprintf("MAX(0, %[1]s.algo_interest + %[1]s.absence_boost - (%[2]s))",
		alias, feedRelatedContentPenaltySQL(alias))
}

func feedReplyPenaltySQL(alias string) string {
	return fmt.Sprintf(`CASE
		WHEN COALESCE(%s.is_reply, 0) = 1 THEN %.1f
		ELSE 0
	END`, alias, feedReplyPenalty)
}

func feedAbsenceBoostArgs(capHours, seenMaxBoost, neverSeenBoost float64) []any {
	return []any{
		neverSeenBoost,
		seenMaxBoost, capHours, capHours,
	}
}

func feedRankingArgs(capHours, seenMaxBoost, neverSeenBoost float64) []any {
	return feedAbsenceBoostArgs(capHours, seenMaxBoost, neverSeenBoost)
}

func safeFeedAbsenceCapHours(capHours float64) float64 {
	if math.IsNaN(capHours) || math.IsInf(capHours, 0) || capHours <= 0 {
		return defaultFeedAbsenceBoostCapHours
	}
	return capHours
}

func feedDecaySQL() string {
	return `(CASE
		           WHEN age_h <   2 THEN 1.0 - 0.3 * age_h / 2.0
		           WHEN age_h <   6 THEN 0.7 - 0.25 * (age_h - 2.0) / 4.0
		           WHEN age_h <  24 THEN 0.45 - 0.25 * (age_h - 6.0) / 18.0
		           WHEN age_h <  72 THEN 0.20 - 0.12 * (age_h - 24.0) / 48.0
		           WHEN age_h < 720 THEN 0.08 - 0.06 * (age_h - 72.0) / 648.0
		           ELSE 0.02
		       END)`
}

func feedFreshnessSQL() string {
	return fmt.Sprintf("MAX(0, %.1f * (1.0 - age_h / %.1f))", feedFreshnessBonusPeak, feedFreshnessBonusWindowHours)
}

// SnapshotRow is one row of a feed rank snapshot.
type SnapshotRow struct {
	TweetID            string
	RankPosition       int
	BaseScore          float64
	DecayFactor        float64
	FreshnessBonus     float64
	Jitter             float64
	DiversityDemotedBy float64
	FinalScore         float64
}

// ReplaceFeedRankSnapshot replaces the snapshot atomically.
// All existing rows are deleted and `rows` are inserted in one transaction.
// `computed_at` is recorded on every row so callers can see snapshot age.
func (db *DB) ReplaceFeedRankSnapshot(rows []SnapshotRow) error {
	now := time.Now().UnixMilli()
	return db.WithWrite(func(tx *sql.Tx) error {
		var previous sql.NullInt64
		if err := tx.QueryRow(
			"SELECT MAX(computed_at) FROM feed_rank_snapshot",
		).Scan(&previous); err != nil {
			return fmt.Errorf("read previous snapshot time: %w", err)
		}
		if previous.Valid && now <= previous.Int64 {
			now = previous.Int64 + 1
		}
		if _, err := tx.Exec("DELETE FROM feed_rank_snapshot"); err != nil {
			return fmt.Errorf("delete old snapshot: %w", err)
		}
		stmt, err := tx.Prepare(`INSERT INTO feed_rank_snapshot
			(tweet_id, rank_position, base_score, decay_factor, freshness_bonus,
			 jitter, diversity_demoted_by, final_score, computed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return fmt.Errorf("prepare insert: %w", err)
		}
		defer func() {
			_ = stmt.Close()
		}()
		historyStmt, err := tx.Prepare(`INSERT INTO feed_rank_snapshot_history
			(computed_at, tweet_id, rank_position, base_score, decay_factor,
			 freshness_bonus, jitter, diversity_demoted_by, final_score)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return fmt.Errorf("prepare snapshot history insert: %w", err)
		}
		defer func() { _ = historyStmt.Close() }()
		for _, r := range rows {
			if _, err := stmt.Exec(
				r.TweetID, r.RankPosition,
				r.BaseScore, r.DecayFactor, r.FreshnessBonus,
				r.Jitter, r.DiversityDemotedBy, r.FinalScore, now,
			); err != nil {
				return fmt.Errorf("insert row %s: %w", r.TweetID, err)
			}
			if _, err := historyStmt.Exec(
				now, r.TweetID, r.RankPosition, r.BaseScore, r.DecayFactor,
				r.FreshnessBonus, r.Jitter, r.DiversityDemotedBy, r.FinalScore,
			); err != nil {
				return fmt.Errorf("insert snapshot history row %s: %w", r.TweetID, err)
			}
		}
		if _, err := tx.Exec(
			"DELETE FROM feed_rank_snapshot_history WHERE computed_at < ?",
			now-feedSnapshotHistoryRetention.Milliseconds(),
		); err != nil {
			return fmt.Errorf("prune snapshot history: %w", err)
		}
		return nil
	})
}

// SnapshotComputedAt returns the most recent computed_at (unix ms),
// or 0 if no snapshot exists.
func (db *DB) SnapshotComputedAt() (int64, error) {
	var at sql.NullInt64
	err := db.reader().QueryRow(
		"SELECT MAX(computed_at) FROM feed_rank_snapshot",
	).Scan(&at)
	if err != nil {
		return 0, err
	}
	if !at.Valid {
		return 0, nil
	}
	return at.Int64, nil
}

// PreDiversitySnapshotRow holds one item with its score breakdown,
// before diversity MMR and jitter are applied in Go.
type PreDiversitySnapshotRow struct {
	TweetID                  string
	ChannelID                string
	SourceChannelID          string
	RelatedContentKey        string
	ContentHash              string
	IsRetweet                bool
	IsReply                  bool
	QuoteTweetID             string
	ThreadRootID             string
	RepostTargetThreadRootID string
	PublishedAtMs            int64
	BaseScore                float64
	DecayFactor              float64
	FreshnessBonus           float64
	ReplyPenalty             float64
}

const feedSnapshotMaxItems = 2000

func (db *DB) ListPreDiversityRankedCandidatesContext(
	ctx context.Context,
	dirtyTweetIDs []string,
	refillLimit int,
) ([]PreDiversitySnapshotRow, error) {
	if refillLimit < 0 {
		refillLimit = 0
	}
	if refillLimit > feedSnapshotMaxItems {
		refillLimit = feedSnapshotMaxItems
	}
	if dirtyTweetIDs == nil {
		dirtyTweetIDs = []string{}
	}
	dirtyJSON, err := json.Marshal(dirtyTweetIDs)
	if err != nil {
		return nil, fmt.Errorf("encode dirty feed candidates: %w", err)
	}

	var where []string
	var whereArgs []any
	where = append(where, feedPrimaryItemPredicate("fi"))
	where = append(where, feedActiveOwnerPredicate("fi"))

	muted, _ := db.GetMutedChannelIDs()
	if len(muted) > 0 {
		ph := strings.Repeat("?,", len(muted))
		ph = ph[:len(ph)-1]
		where = append(where, "fi.channel_id NOT IN ("+ph+")")
		for _, channelID := range muted {
			whereArgs = append(whereArgs, channelID)
		}
		where = append(where, "COALESCE(fi.source_channel_id,'') NOT IN ("+ph+")")
		for _, channelID := range muted {
			whereArgs = append(whereArgs, channelID)
		}
	}

	where = append(where, feedUnseenPredicate("fi"))

	where = append(where, `(
		fi.canonical_tweet_id IS NULL
		OR fi.canonical_tweet_id = ''
		OR fi.canonical_tweet_id = fi.tweet_id
		OR (
			COALESCE(fi.is_retweet,0) = 1
			AND COALESCE(fi.quote_tweet_id,'') = ''
		)
	)`)
	where = append(where, retweetFilterClause("fi"))
	// Rows without published_at produce NULL age_h and hence NULL decay/freshness,
	// which breaks the SQL → Go scan. Items without a published_at can't meaningfully
	// rank against time-decayed items anyway, so exclude them.
	where = append(where, "fi.published_at > 0")
	where = append(where, fmt.Sprintf("(%s > 0 OR fi.published_at > (CAST(strftime('%%s','now') AS INTEGER) - %.1f*3600) * 1000)", feedRankingBaseScoreSQL("fi"), feedFreshnessBonusWindowHours))

	whereClause := "WHERE " + strings.Join(where, " AND ")

	capHours, seenMaxBoost, neverSeenBoost := db.feedAbsenceBoostConfig()
	absenceExpr := feedAbsenceBoostSelect("fi")
	relatedSeenExpr := feedRelatedSeenCountSelect("fi")
	fromSQL := feedRankingFromSQL(
		relatedSeenExpr,
		absenceExpr,
		"JOIN candidate_ids candidate ON candidate.tweet_id = fi.tweet_id",
	)
	args := []any{string(dirtyJSON), refillLimit, refillLimit}
	args = append(args, feedRankingArgs(capHours, seenMaxBoost, neverSeenBoost)...)
	args = append(args, whereArgs...)
	decaySQL := feedDecaySQL()
	freshnessSQL := feedFreshnessSQL()
	refillWhere := strings.Join([]string{
		feedPrimaryItemPredicate("refill"),
		feedActiveOwnerPredicate("refill"),
		feedUnseenPredicate("refill"),
		"refill.published_at > 0",
	}, " AND ")

	query := fmt.Sprintf(`
			WITH seen_feed AS MATERIALIZED (
				SELECT fs.tweet_id, fs.seen_at,
				       fi.quote_tweet_id, fi.canonical_tweet_id,
				       fi.channel_id, fi.source_channel_id, fi.is_ghost
				FROM feed_seen fs
				JOIN feed_items fi INDEXED BY idx_feed_items_seen_cover
				  ON fi.tweet_id = fs.tweet_id
			),
			dirty_ids(tweet_id) AS MATERIALIZED (
				SELECT CAST(value AS TEXT)
				FROM json_each(?)
				WHERE TRIM(CAST(value AS TEXT)) != ''
			),
			recent_refill(tweet_id) AS MATERIALIZED (
				SELECT refill.tweet_id
				FROM feed_items refill INDEXED BY idx_feed_items_published
				WHERE %s
				ORDER BY refill.published_at DESC, refill.tweet_id DESC
				LIMIT ?
			),
			interest_refill(tweet_id) AS MATERIALIZED (
				SELECT refill.tweet_id
				FROM feed_items refill INDEXED BY idx_feed_items_algo
				WHERE %s
				ORDER BY refill.algo_interest DESC, refill.published_at DESC
				LIMIT ?
			),
			candidate_ids(tweet_id) AS MATERIALIZED (
				SELECT tweet_id FROM feed_rank_snapshot
				UNION SELECT tweet_id FROM dirty_ids
				UNION SELECT tweet_id FROM recent_refill
				UNION SELECT tweet_id FROM interest_refill
			)
			SELECT fi.tweet_id,
				       COALESCE(fi.channel_id,''),
				       COALESCE(fi.source_channel_id,''),
				       %s AS related_content_key,
				       COALESCE(fi.content_hash, '') AS content_hash,
				       COALESCE(fi.is_retweet, 0) AS is_retweet,
				       COALESCE(fi.is_reply, 0) AS is_reply,
				       COALESCE(fi.quote_tweet_id, '') AS quote_tweet_id,
				       COALESCE(fi.canonical_url, '') AS canonical_url,
				       fi.published_at,
				       %s AS base,
				       %s AS decay,
				       %s AS freshness,
				       %s AS reply_penalty
				%s
			%s
			ORDER BY MAX(0, base * decay + freshness - reply_penalty) DESC, fi.tweet_id DESC
			LIMIT %d
			`, refillWhere, refillWhere,
		feedRelatedContentKeySQL("fi"), feedRankingBaseScoreSQL("fi"), decaySQL, freshnessSQL,
		feedReplyPenaltySQL("fi"), fromSQL, whereClause, feedSnapshotMaxItems)

	rows, err := db.reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	out := make([]PreDiversitySnapshotRow, 0, feedSnapshotMaxItems)
	repostTargetByTweetID := make(map[string]string)
	for rows.Next() {
		var r PreDiversitySnapshotRow
		var isRetweet, isReply int
		var canonicalURL string
		if err := rows.Scan(&r.TweetID, &r.ChannelID, &r.SourceChannelID,
			&r.RelatedContentKey, &r.ContentHash, &isRetweet, &isReply, &r.QuoteTweetID, &canonicalURL, &r.PublishedAtMs,
			&r.BaseScore, &r.DecayFactor, &r.FreshnessBonus, &r.ReplyPenalty); err != nil {
			return nil, err
		}
		r.IsRetweet = isRetweet != 0
		r.IsReply = isReply != 0
		if r.IsRetweet && r.QuoteTweetID == "" {
			if targetID := model.TwitterStatusIDFromURL(canonicalURL); targetID != "" && targetID != r.TweetID {
				repostTargetByTweetID[r.TweetID] = targetID
			}
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out))
	seenIDs := make(map[string]bool, len(out)+len(repostTargetByTweetID))
	for _, row := range out {
		if !seenIDs[row.TweetID] {
			seenIDs[row.TweetID] = true
			ids = append(ids, row.TweetID)
		}
		if targetID := repostTargetByTweetID[row.TweetID]; targetID != "" && !seenIDs[targetID] {
			seenIDs[targetID] = true
			ids = append(ids, targetID)
		}
	}
	roots, err := db.threadRootIDsForTweetIDsContext(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if rootID := roots[out[i].TweetID]; rootID != "" {
			out[i].ThreadRootID = rootID
		}
		if targetID := repostTargetByTweetID[out[i].TweetID]; targetID != "" {
			if rootID := roots[targetID]; rootID != "" {
				out[i].RepostTargetThreadRootID = rootID
			}
		}
	}
	return out, nil
}

func (db *DB) CountVisibleFeedRankSnapshotContext(ctx context.Context) (int, error) {
	muted, _ := db.GetMutedChannelIDs()
	where := []string{
		feedPrimaryItemPredicate("fi"),
		feedActiveOwnerPredicate("fi"),
		feedUnseenPredicate("fi"),
	}
	args := make([]any, 0, len(muted)*2)
	if len(muted) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(muted)), ",")
		where = append(where, "fi.channel_id NOT IN ("+ph+")")
		for _, channelID := range muted {
			args = append(args, channelID)
		}
		where = append(where, "COALESCE(fi.source_channel_id,'') NOT IN ("+ph+")")
		for _, channelID := range muted {
			args = append(args, channelID)
		}
	}
	var count int
	err := db.reader().QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM feed_rank_snapshot snapshot
		JOIN feed_items fi ON fi.tweet_id = snapshot.tweet_id
		WHERE `+strings.Join(where, " AND "), args...).Scan(&count)
	return count, err
}

func (db *DB) threadRootIDsForTweetIDsContext(ctx context.Context, tweetIDs []string) (map[string]string, error) {
	if len(tweetIDs) == 0 {
		return map[string]string{}, nil
	}
	placeholders := strings.Repeat("?,", len(tweetIDs))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(tweetIDs))
	for _, id := range tweetIDs {
		args = append(args, id)
	}

	rows, err := db.reader().QueryContext(ctx, `
		WITH RECURSIVE chain(seed_id, tweet_id, reply_to_status, depth) AS (
			SELECT tweet_id, tweet_id, COALESCE(reply_to_status, ''), 0
			FROM feed_items
			WHERE tweet_id IN (`+placeholders+`)
			UNION ALL
			SELECT chain.seed_id, parent.tweet_id, COALESCE(parent.reply_to_status, ''), chain.depth + 1
			FROM chain
			JOIN feed_items parent ON parent.tweet_id = chain.reply_to_status
			WHERE chain.reply_to_status != ''
			  AND chain.depth < 50
		),
		root_depth AS (
			SELECT seed_id, MAX(depth) AS max_depth
			FROM chain
			GROUP BY seed_id
		)
		SELECT chain.seed_id, chain.tweet_id
		FROM chain
		JOIN root_depth
		  ON root_depth.seed_id = chain.seed_id
		 AND root_depth.max_depth = chain.depth
	`, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	roots := make(map[string]string, len(tweetIDs))
	for rows.Next() {
		var tweetID, rootID string
		if err := rows.Scan(&tweetID, &rootID); err != nil {
			return nil, err
		}
		roots[tweetID] = rootID
	}
	return roots, rows.Err()
}

// SnapshotPageItem joins a snapshot row with the underlying feed_item.
// rank_position from the snapshot becomes the ordering key on the client.
type SnapshotPageItem struct {
	Item               model.FeedItem
	RankPosition       int
	FinalScore         float64
	BaseScore          float64
	DecayFactor        float64
	FreshnessBonus     float64
	Jitter             float64
	DiversityDemotedBy float64
	ComputedAt         int64
}

// ListSnapshotPage returns up to `limit` snapshot rows with rank_position strictly
// greater than `afterPos`, joined with their feed_item content. afterPos < 1
// returns from the start. The result is ordered by rank_position ASC.
//
// Items present in feed_seen are excluded at query time. The
// snapshot builder also excludes seen items, but items marked seen between
// rebuilds would otherwise surface at their stale rank position and break
// pagination (a caller fetching limit+1 to detect hasMore can't distinguish
// "no more items" from "some items were filtered").
func (db *DB) ListSnapshotPage(snapshotAt int64, afterPos int, limit int) ([]SnapshotPageItem, error) {
	if limit <= 0 || limit > 200 {
		limit = 40
	}
	if afterPos < 0 {
		afterPos = 0
	}

	rows, err := db.reader().Query(`
		SELECT s.rank_position, s.final_score, s.base_score,
		       s.decay_factor, s.freshness_bonus, s.jitter, s.diversity_demoted_by,
		       `+feedItemSelectSQL("fi")+`
		FROM feed_rank_snapshot_history s
		JOIN feed_items_resolved fi ON fi.tweet_id = s.tweet_id
		WHERE s.computed_at = ?
		  AND s.rank_position > ?
		  AND `+feedPrimaryItemPredicate("fi")+`
		  AND `+feedActiveOwnerPredicate("fi")+`
		  AND `+feedUnseenPredicate("fi")+`
		ORDER BY s.rank_position ASC
		LIMIT ?
	`, snapshotAt, afterPos, limit)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var out []SnapshotPageItem
	for rows.Next() {
		var p SnapshotPageItem
		var quotePubAt, pubAt, fetchedAt sql.NullInt64
		if err := rows.Scan(
			&p.RankPosition, &p.FinalScore, &p.BaseScore,
			&p.DecayFactor, &p.FreshnessBonus, &p.Jitter, &p.DiversityDemotedBy,
			&p.Item.TweetID,
			&p.Item.SourceHandle, &p.Item.AuthorHandle,
			&p.Item.AuthorDisplayName, &p.Item.AuthorAvatarURL,
			&p.Item.BodyText, &p.Item.Lang,
			&p.Item.IsRetweet, &p.Item.RetweetedByHandle,
			&p.Item.RetweetedByDisplayName,
			&p.Item.QuoteTweetID, &p.Item.QuoteAuthorHandle,
			&p.Item.QuoteAuthorDisplayName, &p.Item.QuoteAuthorAvatarURL,
			&p.Item.QuoteBodyText, &p.Item.QuoteLang,
			&p.Item.QuoteMediaJSON, &p.Item.MediaJSON,
			&p.Item.CanonicalURL, &p.Item.ReplyToHandle,
			&p.Item.ReplyToStatus,
			&p.Item.IsReply, &p.Item.IsGhost,
			&quotePubAt,
			&p.Item.Views, &p.Item.Likes, &p.Item.Retweets,
			&pubAt, &fetchedAt,
			&p.Item.ContentHash, &p.Item.CanonicalTweetID,
			&p.Item.SourceChannelID, &p.Item.ChannelID, &p.Item.QuoteChannelID,
			&p.Item.ReplyChannelID, &p.Item.ReposterChannelID,
		); err != nil {
			return nil, err
		}
		p.Item.QuotePublishedAt = millisToTimePtr(quotePubAt)
		p.Item.PublishedAt = millisToTimePtr(pubAt)
		if t := millisToTimePtr(fetchedAt); t != nil {
			p.Item.FetchedAt = *t
		}
		p.Item.AlgoInterestScore = p.FinalScore
		p.ComputedAt = snapshotAt
		p.Item.ParseMedia()
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		var exists bool
		if err := db.reader().QueryRow(
			"SELECT EXISTS(SELECT 1 FROM feed_rank_snapshot_history WHERE computed_at = ?)",
			snapshotAt,
		).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, ErrFeedSnapshotExpired
		}
	}
	return out, nil
}
