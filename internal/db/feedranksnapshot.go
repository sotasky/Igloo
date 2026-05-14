package db

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
)

const (
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

func feedNormalizedTwitterHandleSQL(expr string) string {
	return fmt.Sprintf("LOWER(LTRIM(TRIM(COALESCE(%s, '')), '@'))", expr)
}

func feedPrioritySourceSQL(alias string) string {
	author := feedNormalizedTwitterHandleSQL(alias + ".author_handle")
	source := feedNormalizedTwitterHandleSQL(alias + ".source_handle")
	return fmt.Sprintf(`NULLIF(%[2]s, '') IS NOT NULL
				 AND %[2]s != %[1]s
				 AND (cf_source.channel_id IS NOT NULL OR cs_source.channel_id IS NOT NULL)`, author, source)
}

func feedRankingAccountHandleSQL(alias string) string {
	author := feedNormalizedTwitterHandleSQL(alias + ".author_handle")
	source := feedNormalizedTwitterHandleSQL(alias + ".source_handle")
	return fmt.Sprintf(`CASE
				WHEN %[3]s THEN %[2]s
				ELSE %[1]s
			END`, author, source, feedPrioritySourceSQL(alias))
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
				WHEN ? != ''
				 AND NULLIF(%[2]s, '') IS NOT NULL
				 AND %[3]s = 1
				THEN CASE
					WHEN lps.last_seen_at IS NULL THEN ?
					ELSE ? * MIN(?, MAX(0, ((CAST(strftime('%%s','now') AS INTEGER) * 1000) - lps.last_seen_at) / 3600000.0)) / ?
				END
				ELSE 0
		END`, alias, feedRankingAccountHandleSQL(alias), feedRankingAccountIsPrioritySQL(alias))
}

func feedRankingFromSQL(relatedSeenExpr, absenceExpr string) string {
	return fmt.Sprintf(`
				FROM (
				    SELECT fi.*,
				           (CAST(strftime('%%s','now') AS INTEGER) * 1000 - fi.published_at) / 3600000.0 AS age_h,
				           %s AS related_seen_count,
				           %s AS absence_boost
				    FROM feed_items fi
				    LEFT JOIN (
				        SELECT fs_related.username,
				               %s AS related_key,
				               COUNT(*) AS related_seen_count
				        FROM feed_seen fs_related
				        JOIN feed_items seen_fi ON seen_fi.tweet_id = fs_related.tweet_id
				        GROUP BY fs_related.username, %s
				    ) rsc ON rsc.username = NULLIF(?, '')
				        AND rsc.related_key = %s
		    LEFT JOIN channel_follows cf_author
		      ON cf_author.user_id = ''
		     AND cf_author.channel_id = 'twitter_' || %s
		    LEFT JOIN channel_stars cs_author
		      ON cs_author.user_id = ''
		     AND cs_author.channel_id = 'twitter_' || %s
		    LEFT JOIN channel_follows cf_source
		      ON cf_source.user_id = ''
		     AND cf_source.channel_id = 'twitter_' || %s
		    LEFT JOIN channel_stars cs_source
		      ON cs_source.user_id = ''
		     AND cs_source.channel_id = 'twitter_' || %s
				    LEFT JOIN (
				        SELECT handle, MAX(seen_at) AS last_seen_at
				        FROM (
				            SELECT %s AS handle, fs.seen_at
				            FROM feed_seen fs
				            JOIN feed_items parent ON parent.tweet_id = fs.tweet_id
				            WHERE fs.username = ?
				              AND NULLIF(%s, '') IS NOT NULL
				              AND COALESCE(parent.is_ghost, 0) = 0
				            UNION ALL
				            SELECT %s AS handle, fs.seen_at
				            FROM feed_seen fs
				            JOIN feed_items parent ON parent.tweet_id = fs.tweet_id
				            WHERE fs.username = ?
				              AND NULLIF(%s, '') IS NOT NULL
				              AND COALESCE(parent.is_ghost, 0) = 0
				        ) seen_handles
				        GROUP BY handle
				    ) lps ON lps.handle = %s
			) fi
			`, relatedSeenExpr, absenceExpr,
		feedRelatedContentKeySQL("seen_fi"),
		feedRelatedContentKeySQL("seen_fi"),
		feedRelatedContentKeySQL("fi"),
		feedNormalizedTwitterHandleSQL("fi.author_handle"),
		feedNormalizedTwitterHandleSQL("fi.author_handle"),
		feedNormalizedTwitterHandleSQL("fi.source_handle"),
		feedNormalizedTwitterHandleSQL("fi.source_handle"),
		feedNormalizedTwitterHandleSQL("parent.author_handle"),
		feedNormalizedTwitterHandleSQL("parent.author_handle"),
		feedNormalizedTwitterHandleSQL("parent.source_handle"),
		feedNormalizedTwitterHandleSQL("parent.source_handle"),
		feedRankingAccountHandleSQL("fi"),
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

func feedAbsenceBoostArgs(username string, capHours, seenMaxBoost, neverSeenBoost float64) []any {
	return []any{
		username,
		neverSeenBoost,
		seenMaxBoost, capHours, capHours,
	}
}

func feedRankingArgs(username string, capHours, seenMaxBoost, neverSeenBoost float64) []any {
	args := feedAbsenceBoostArgs(username, capHours, seenMaxBoost, neverSeenBoost)
	args = append(args, username, username, username)
	return args
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

// ReplaceFeedRankSnapshot replaces the snapshot for `username` atomically.
// All existing rows for the user are deleted and `rows` are inserted in one transaction.
// `computed_at` is recorded on every row so callers can see snapshot age.
//
// If `rows` is empty, the existing snapshot is preserved and nil is returned.
// This prevents a transient computation failure from wiping a good snapshot.
func (db *DB) ReplaceFeedRankSnapshot(username string, rows []SnapshotRow) error {
	if username == "" {
		return fmt.Errorf("ReplaceFeedRankSnapshot: empty username")
	}
	if len(rows) == 0 {
		return nil
	}
	now := time.Now().UnixMilli()
	return db.WithWrite(func(tx *sql.Tx) error {
		var previous sql.NullInt64
		if err := tx.QueryRow(
			"SELECT MAX(computed_at) FROM feed_rank_snapshot WHERE username = ?",
			username,
		).Scan(&previous); err != nil {
			return fmt.Errorf("read previous snapshot time: %w", err)
		}
		if previous.Valid && now <= previous.Int64 {
			now = previous.Int64 + 1
		}
		if _, err := tx.Exec("DELETE FROM feed_rank_snapshot WHERE username = ?", username); err != nil {
			return fmt.Errorf("delete old snapshot: %w", err)
		}
		stmt, err := tx.Prepare(`INSERT INTO feed_rank_snapshot
			(username, tweet_id, rank_position, base_score, decay_factor, freshness_bonus,
			 jitter, diversity_demoted_by, final_score, computed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return fmt.Errorf("prepare insert: %w", err)
		}
		defer func() {
			_ = stmt.Close()
		}()
		for _, r := range rows {
			if _, err := stmt.Exec(
				username, r.TweetID, r.RankPosition,
				r.BaseScore, r.DecayFactor, r.FreshnessBonus,
				r.Jitter, r.DiversityDemotedBy, r.FinalScore, now,
			); err != nil {
				return fmt.Errorf("insert row %s: %w", r.TweetID, err)
			}
		}
		return nil
	})
}

// SnapshotComputedAt returns the most recent computed_at for `username` (unix ms),
// or 0 if no snapshot exists.
func (db *DB) SnapshotComputedAt(username string) (int64, error) {
	var at sql.NullInt64
	err := db.conn.QueryRow(
		"SELECT MAX(computed_at) FROM feed_rank_snapshot WHERE username = ?",
		username,
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
	TweetID           string
	AuthorHandle      string
	SourceHandle      string
	RelatedContentKey string
	BaseScore         float64
	DecayFactor       float64
	FreshnessBonus    float64
	ReplyPenalty      float64
}

// ListPreDiversityRanked returns every eligible feed item with its score
// breakdown, ordered by raw (base*decay + freshness) DESC. This is the input
// to the Go-side diversity + jitter pass that produces the snapshot.
//
// Filters mirror ListRankedFeedItems: muted accounts excluded, seen items
// excluded, canonical items only, retweet/quote dedup applied, and zero-interest
// items past the freshness window dropped.
func (db *DB) ListPreDiversityRanked(username string) ([]PreDiversitySnapshotRow, error) {
	return db.ListPreDiversityRankedContext(context.Background(), username)
}

func (db *DB) ListPreDiversityRankedContext(ctx context.Context, username string) ([]PreDiversitySnapshotRow, error) {
	var where []string
	var args []any

	muted, _ := db.GetMutedAccounts()
	if len(muted) > 0 {
		ph := strings.Repeat("?,", len(muted))
		ph = ph[:len(ph)-1]
		where = append(where, "fi.author_handle NOT IN ("+ph+")")
		for _, h := range muted {
			args = append(args, h)
		}
		where = append(where, "COALESCE(fi.source_handle,'') NOT IN ("+ph+")")
		for _, h := range muted {
			args = append(args, h)
		}
	}

	if username != "" {
		where = append(where, feedUnseenPredicate("fi"))
		args = append(args, feedUnseenPredicateArgs(username)...)
	}

	where = append(where, "(fi.canonical_tweet_id IS NULL OR fi.canonical_tweet_id = '' OR fi.canonical_tweet_id = fi.tweet_id)")
	where = append(where, retweetFilterClause("fi"))
	// Rows without published_at produce NULL age_h and hence NULL decay/freshness,
	// which breaks the SQL → Go scan. Items without a published_at can't meaningfully
	// rank against time-decayed items anyway, so exclude them.
	where = append(where, "fi.published_at > 0")
	where = append(where, fmt.Sprintf("(%s > 0 OR fi.published_at > (CAST(strftime('%%s','now') AS INTEGER) - %.1f*3600) * 1000)", feedRankingBaseScoreSQL("fi"), feedFreshnessBonusWindowHours))

	whereClause := "WHERE " + strings.Join(where, " AND ")

	// Cap the snapshot size. The exact greedy diversity pass prunes candidates
	// by score bounds, but the ranking query and snapshot write still do work
	// proportional to the candidate set. 2000 covers ~50 pages of 40.
	const snapshotMaxItems = 2000

	// Recency ladder: the first two hours stay very competitive, then the
	// multiplier falls quickly so medium-interest new posts can surface over
	// older high-affinity items. The long tail remains non-zero for starred
	// and high-affinity items, but should not pin the top of the feed.
	capHours, seenMaxBoost, neverSeenBoost := db.feedAbsenceBoostConfig()
	absenceExpr := feedAbsenceBoostSelect("fi")
	relatedSeenExpr := feedRelatedSeenCountSelect("fi")
	fromSQL := feedRankingFromSQL(relatedSeenExpr, absenceExpr)
	args = append(feedRankingArgs(username, capHours, seenMaxBoost, neverSeenBoost), args...)
	decaySQL := feedDecaySQL()
	freshnessSQL := feedFreshnessSQL()

	query := fmt.Sprintf(`
			SELECT fi.tweet_id,
				       fi.author_handle,
				       COALESCE(fi.source_handle,''),
				       %s AS related_content_key,
				       %s AS base,
				       %s AS decay,
				       %s AS freshness,
				       %s AS reply_penalty
				%s
			%s
			ORDER BY MAX(0, base * decay + freshness - reply_penalty) DESC, fi.tweet_id DESC
			LIMIT %d
			`, feedRelatedContentKeySQL("fi"), feedRankingBaseScoreSQL("fi"), decaySQL, freshnessSQL, feedReplyPenaltySQL("fi"), fromSQL, whereClause, snapshotMaxItems)

	rows, err := db.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	out := make([]PreDiversitySnapshotRow, 0, snapshotMaxItems)
	for rows.Next() {
		var r PreDiversitySnapshotRow
		if err := rows.Scan(&r.TweetID, &r.AuthorHandle, &r.SourceHandle,
			&r.RelatedContentKey, &r.BaseScore, &r.DecayFactor, &r.FreshnessBonus, &r.ReplyPenalty); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
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
// Items present in feed_seen for `username` are excluded at query time. The
// snapshot builder also excludes seen items, but items marked seen between
// rebuilds would otherwise surface at their stale rank position and break
// pagination (a caller fetching limit+1 to detect hasMore can't distinguish
// "no more items" from "some items were filtered").
func (db *DB) ListSnapshotPage(username string, afterPos int, limit int) ([]SnapshotPageItem, error) {
	if limit <= 0 || limit > 200 {
		limit = 40
	}
	if afterPos < 0 {
		afterPos = 0
	}

	rows, err := db.conn.Query(`
		SELECT s.tweet_id, s.rank_position, s.final_score, s.base_score,
		       s.decay_factor, s.freshness_bonus, s.jitter, s.diversity_demoted_by,
		       s.computed_at,
		       COALESCE(fi.source_handle,''), fi.author_handle,
		       COALESCE(fi.author_display_name,''), COALESCE(fi.author_avatar_url,''),
		       COALESCE(fi.body_text,''), COALESCE(fi.lang,''),
		       COALESCE(fi.is_retweet,0), COALESCE(fi.retweeted_by_handle,''),
		       COALESCE(fi.retweeted_by_display_name,''),
		       COALESCE(fi.quote_tweet_id,''), COALESCE(fi.quote_author_handle,''),
		       COALESCE(fi.quote_author_display_name,''), COALESCE(fi.quote_author_avatar_url,''),
		       COALESCE(fi.quote_body_text,''), COALESCE(fi.quote_lang,''),
		       COALESCE(fi.quote_media_json,''), COALESCE(fi.media_json,''),
		       COALESCE(fi.canonical_url,''), COALESCE(fi.reply_to_handle,''),
		       COALESCE(fi.reply_to_status,''),
		       COALESCE(fi.is_reply,0), COALESCE(fi.is_ghost,0),
		       fi.quote_published_at,
		       COALESCE(fi.views,0), COALESCE(fi.likes,0), COALESCE(fi.retweets,0),
		       fi.published_at, fi.fetched_at,
		       COALESCE(fi.content_hash,''), COALESCE(fi.canonical_tweet_id,'')
		FROM feed_rank_snapshot s
		JOIN feed_items fi ON fi.tweet_id = s.tweet_id
		WHERE s.username = ?
		  AND s.rank_position > ?
		  AND `+feedUnseenPredicateForUser("fi", "s.username")+`
		ORDER BY s.rank_position ASC
		LIMIT ?
	`, username, afterPos, limit)
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
			&p.Item.TweetID, &p.RankPosition, &p.FinalScore, &p.BaseScore,
			&p.DecayFactor, &p.FreshnessBonus, &p.Jitter, &p.DiversityDemotedBy,
			&p.ComputedAt,
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
		); err != nil {
			return nil, err
		}
		p.Item.QuotePublishedAt = millisToTimePtr(quotePubAt)
		p.Item.PublishedAt = millisToTimePtr(pubAt)
		if t := millisToTimePtr(fetchedAt); t != nil {
			p.Item.FetchedAt = *t
		}
		p.Item.AlgoInterestScore = p.FinalScore
		p.Item.ParseMedia()
		out = append(out, p)
	}
	return out, rows.Err()
}
