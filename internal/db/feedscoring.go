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
	      FROM feed_items WHERE algo_scored_at = 0`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := db.conn.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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
	now := time.Now().Unix()
	return db.WithWrite(func(tx *sql.Tx) error {
		stmt, err := tx.Prepare("UPDATE feed_items SET algo_interest = ?, algo_scored_at = ? WHERE tweet_id = ?")
		if err != nil {
			return err
		}
		defer stmt.Close()
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
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			"UPDATE feed_items SET algo_scored_at = 0 WHERE LOWER(source_handle) = ? OR LOWER(author_handle) = ?",
			strings.ToLower(handle), strings.ToLower(handle),
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
func (db *DB) ListRankedFeedItems(username string, limit int, offset int) ([]model.FeedItem, error) {
	if limit <= 0 {
		limit = 41
	}
	if offset < 0 {
		offset = 0
	}

	var where []string
	var args []any

	// Exclude muted accounts
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

	// Exclude seen items
	if username != "" {
		where = append(where, feedUnseenPredicate("fi"))
		args = append(args, feedUnseenPredicateArgs(username)...)
	}

	// Canonical items only
	where = append(where, "(fi.canonical_tweet_id IS NULL OR fi.canonical_tweet_id = '' OR fi.canonical_tweet_id = fi.tweet_id)")

	// Apply the shared retweet/quote filter (dedup-aware, self-pass).
	where = append(where, retweetFilterClause("fi"))

	// Skip items that can't possibly rank: zero interest/absence AND past the fresh bonus window.
	where = append(where, fmt.Sprintf("((fi.algo_interest + fi.absence_boost) > 0 OR fi.published_at > (CAST(strftime('%%s','now') AS INTEGER) - %.1f*3600) * 1000)", feedFreshnessBonusWindowHours))

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	capHours, seenMaxBoost, neverSeenBoost, starredMaxBoost := db.feedAbsenceBoostConfig()
	absenceExpr := feedAbsenceBoostSelect("fi")
	starredAbsenceExpr := feedStarredAbsenceBoostSelect("fi")
	fromSQL := feedRankingFromSQL(absenceExpr, starredAbsenceExpr)
	args = append(feedAbsenceBoostArgs(username, capHours, seenMaxBoost, neverSeenBoost, starredMaxBoost), args...)
	decaySQL := feedDecaySQL()
	freshnessSQL := feedFreshnessSQL()

	query := fmt.Sprintf(`
		SELECT fi.tweet_id, COALESCE(fi.source_handle,''), fi.author_handle,
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
			       COALESCE(fi.content_hash,''), COALESCE(fi.canonical_tweet_id,''),
			       (fi.algo_interest + fi.absence_boost) * %s
			       + %s
			       + fi.starred_absence_boost
			       AS final_score
			%s
			%s
			ORDER BY final_score DESC, fi.tweet_id DESC
			LIMIT ? OFFSET ?
		`, decaySQL, freshnessSQL, fromSQL, whereClause)
	args = append(args, limit, offset)

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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
