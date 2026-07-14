package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
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

func (db *DB) GetUnscoredFeedItems(limit int) ([]ScoringItem, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("unscored feed limit must be positive")
	}
	rows, err := db.conn.Query(`
		WITH candidates(tweet_id) AS MATERIALIZED (
			SELECT candidate.tweet_id
			FROM feed_items candidate INDEXED BY idx_feed_items_unscored
			WHERE candidate.algo_scored_at = 0
			  AND `+feedPrimaryItemPredicate("candidate")+`
			  AND `+feedActiveOwnerPredicate("candidate")+`
			ORDER BY candidate.rowid DESC
			LIMIT ?
		)
		SELECT resolved.tweet_id, COALESCE(resolved.source_handle,''), resolved.author_handle,
		       COALESCE(resolved.body_text,''), COALESCE(resolved.media_json,''),
		       COALESCE(resolved.is_retweet,0), COALESCE(resolved.published_at,'')
		FROM candidates
		JOIN feed_items_resolved resolved ON resolved.tweet_id = candidates.tweet_id
	`, limit)
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

const feedWindowChannelCandidateLimit = 2000

func (db *DB) InvalidateFeedWindowByChannelID(channelID string) error {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil
	}
	query := fmt.Sprintf(`
		WITH candidates(tweet_id) AS MATERIALIZED (
			SELECT snapshot.tweet_id
			FROM feed_rank_snapshot snapshot
			JOIN feed_items fi ON fi.tweet_id = snapshot.tweet_id
			WHERE fi.channel_id = ?
			   OR fi.source_channel_id = ?
			   OR fi.reposter_channel_id = ?
			UNION
			SELECT tweet_id FROM (
				SELECT tweet_id
				FROM feed_items INDEXED BY idx_feed_items_author_fetched
				WHERE channel_id = ?
				  AND channel_id IS NOT NULL
				  AND channel_id != ''
				ORDER BY fetched_at DESC, published_at DESC, tweet_id DESC
				LIMIT %d
			)
			UNION
			SELECT tweet_id FROM (
				SELECT tweet_id
				FROM feed_items INDEXED BY idx_feed_items_source_channel
				WHERE source_channel_id = ?
				ORDER BY rowid DESC
				LIMIT %d
			)
			UNION
			SELECT tweet_id FROM (
				SELECT tweet_id
				FROM feed_items INDEXED BY idx_feed_items_reposter_channel
				WHERE reposter_channel_id = ?
				  AND reposter_channel_id IS NOT NULL
				  AND reposter_channel_id != ''
				ORDER BY published_at DESC, tweet_id DESC
				LIMIT %d
			)
		)
		UPDATE feed_items
		SET algo_scored_at = 0
		WHERE tweet_id IN (SELECT tweet_id FROM candidates)
	`, feedWindowChannelCandidateLimit, feedWindowChannelCandidateLimit, feedWindowChannelCandidateLimit)
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(query,
			channelID, channelID, channelID,
			channelID, channelID, channelID,
		)
		return err
	})
}
