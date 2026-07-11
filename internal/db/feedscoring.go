package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
)

// ScoringItem holds the minimal fields needed to compute algo_interest.
type ScoringItem struct {
	TweetID      string
	SourceHandle string
	AuthorHandle string
	BodyText     string
	MediaJSON    string
	IsRetweet    bool
	PublishedAt  string
}

// GetUnscoredFeedItems returns items where algo_scored_at = 0.
// limit caps the batch size; 0 means no limit.
func (db *DB) GetUnscoredFeedItems(limit int) ([]ScoringItem, error) {
	q := `SELECT tweet_id, COALESCE(source_handle,''), author_handle,
	             COALESCE(body_text,''), COALESCE(media_json,''),
	             COALESCE(is_retweet,0), COALESCE(published_at,'')
	      FROM feed_items_resolved WHERE algo_scored_at = 0`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := db.conn.Query(q)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var items []ScoringItem
	for rows.Next() {
		var si ScoringItem
		if err := rows.Scan(&si.TweetID, &si.SourceHandle, &si.AuthorHandle,
			&si.BodyText, &si.MediaJSON, &si.IsRetweet, &si.PublishedAt); err != nil {
			return nil, err
		}
		items = append(items, si)
	}
	return items, rows.Err()
}

// UpdateAlgoInterest sets algo_interest and algo_scored_at for a batch of items.
func (db *DB) UpdateAlgoInterest(scores map[string]float64) error {
	if len(scores) == 0 {
		return nil
	}
	now := time.Now().UnixMilli()
	return db.WithWrite(func(tx *sql.Tx) error {
		stmt, err := tx.Prepare("UPDATE feed_items SET algo_interest = ?, algo_scored_at = ? WHERE tweet_id = ?")
		if err != nil {
			return err
		}
		defer func() {
			_ = stmt.Close()
		}()
		for tweetID, score := range scores {
			if _, err := stmt.Exec(score, now, tweetID); err != nil {
				return err
			}
		}
		return nil
	})
}

// InvalidateAlgoScore resets algo_scored_at to 0 for the given tweet IDs,
// causing the scoring worker to recompute them on its next cycle.
func (db *DB) InvalidateAlgoScore(tweetIDs ...string) error {
	if len(tweetIDs) == 0 {
		return nil
	}
	ph := strings.Repeat("?,", len(tweetIDs))
	ph = ph[:len(ph)-1]
	args := make([]any, len(tweetIDs))
	for i, id := range tweetIDs {
		args[i] = id
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE feed_items SET algo_scored_at = 0 WHERE tweet_id IN ("+ph+")", args...)
		return err
	})
}

// InvalidateAlgoScoreByHandle resets algo_scored_at to 0 for all items
// matching the given source or author handle. Used when a channel is starred/unstarred.
func (db *DB) InvalidateAlgoScoreByHandle(handle string) error {
	channelID := model.TwitterChannelIDFromHandle(handle)
	if channelID == "" {
		return nil
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			"UPDATE feed_items SET algo_scored_at = 0 WHERE source_channel_id = ? OR channel_id = ?",
			channelID, channelID,
		)
		return err
	})
}

// CountUnscoredItems returns the number of feed items with algo_scored_at = 0.
func (db *DB) CountUnscoredItems() (int, error) {
	var count int
	err := db.conn.QueryRow("SELECT COUNT(*) FROM feed_items WHERE algo_scored_at = 0").Scan(&count)
	return count, err
}

// ListRankedFeedItems returns feed items ranked by algo_interest with piecewise
// linear time decay applied in SQL. Seen items are excluded. Muted accounts filtered.
// offset skips the first N ranked items for infinite-scroll pagination.
func (db *DB) ListRankedFeedItems(limit int, offset int) ([]model.FeedItem, error) {
	if limit <= 0 {
		limit = 41
	}
	if offset < 0 {
		offset = 0
	}

	var where []string
	var args []any
	where = append(where, feedPrimaryItemPredicate("fi"))

	// Exclude muted accounts
	muted, _ := db.GetMutedChannelIDs()
	if len(muted) > 0 {
		ph := strings.Repeat("?,", len(muted))
		ph = ph[:len(ph)-1]
		where = append(where, "fi.channel_id NOT IN ("+ph+")")
		for _, channelID := range muted {
			args = append(args, channelID)
		}
		where = append(where, "COALESCE(fi.source_channel_id,'') NOT IN ("+ph+")")
		for _, channelID := range muted {
			args = append(args, channelID)
		}
	}

	// Exclude seen items
	where = append(where, feedUnseenPredicate("fi"))

	// Canonical items only
	where = append(where, "(fi.canonical_tweet_id IS NULL OR fi.canonical_tweet_id = '' OR fi.canonical_tweet_id = fi.tweet_id)")

	// Apply the shared retweet/quote filter (dedup-aware, self-pass).
	where = append(where, retweetFilterClause("fi"))

	// Skip items that can't possibly rank: zero post-penalty interest/absence AND past the fresh bonus window.
	where = append(where, fmt.Sprintf("(%s > 0 OR fi.published_at > (CAST(strftime('%%s','now') AS INTEGER) - %.1f*3600) * 1000)", feedRankingBaseScoreSQL("fi"), feedFreshnessBonusWindowHours))

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	capHours, seenMaxBoost, neverSeenBoost := db.feedAbsenceBoostConfig()
	absenceExpr := feedAbsenceBoostSelect("fi")
	relatedSeenExpr := feedRelatedSeenCountSelect("fi")
	fromSQL := feedRankingFromSQL(relatedSeenExpr, absenceExpr)
	identityJoin := `JOIN feed_items_resolved resolved ON resolved.tweet_id = fi.tweet_id`
	args = append(feedRankingArgs(capHours, seenMaxBoost, neverSeenBoost), args...)
	decaySQL := feedDecaySQL()
	freshnessSQL := feedFreshnessSQL()

	query := fmt.Sprintf(`
		SELECT %s,
				       MAX(0, %s * %s
				       + %s
				       - %s)
				       AS final_score
			%s
			%s
			%s
			ORDER BY final_score DESC, fi.tweet_id DESC
			LIMIT ? OFFSET ?
			`, feedItemSelectSQL("resolved"), feedRankingBaseScoreSQL("fi"), decaySQL, freshnessSQL, feedReplyPenaltySQL("fi"), fromSQL, identityJoin, whereClause)
	args = append(args, limit, offset)

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var items []model.FeedItem
	for rows.Next() {
		var f model.FeedItem
		var quotePubAt, pubAt, fetchedAt sql.NullInt64
		var finalScore float64
		err := rows.Scan(
			&f.TweetID, &f.SourceHandle, &f.AuthorHandle,
			&f.AuthorDisplayName, &f.AuthorAvatarURL,
			&f.BodyText, &f.Lang,
			&f.IsRetweet, &f.RetweetedByHandle,
			&f.RetweetedByDisplayName,
			&f.QuoteTweetID, &f.QuoteAuthorHandle,
			&f.QuoteAuthorDisplayName, &f.QuoteAuthorAvatarURL,
			&f.QuoteBodyText, &f.QuoteLang,
			&f.QuoteMediaJSON, &f.MediaJSON,
			&f.CanonicalURL, &f.ReplyToHandle,
			&f.ReplyToStatus,
			&f.IsReply, &f.IsGhost,
			&quotePubAt,
			&f.Views, &f.Likes, &f.Retweets,
			&pubAt, &fetchedAt,
			&f.ContentHash, &f.CanonicalTweetID,
			&f.SourceChannelID, &f.ChannelID, &f.QuoteChannelID,
			&f.ReplyChannelID, &f.ReposterChannelID,
			&finalScore,
		)
		if err != nil {
			return nil, err
		}
		f.QuotePublishedAt = millisToTimePtr(quotePubAt)
		f.PublishedAt = millisToTimePtr(pubAt)
		if t := millisToTimePtr(fetchedAt); t != nil {
			f.FetchedAt = *t
		}
		f.AlgoInterestScore = finalScore
		f.ParseMedia()
		items = append(items, f)
	}
	return items, rows.Err()
}
