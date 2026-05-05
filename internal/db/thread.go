package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/screwys/igloo/internal/model"
)

// GetFeedItemByTweetID fetches a single feed_items row by tweet_id, including
// ghost rows. Returns (nil, nil) if not found. Used by the reply resolver to
// detect whether a parent already exists in DB before fetching from fxtwitter,
// and by the thread API.
func (db *DB) GetFeedItemByTweetID(tweetID string) (*model.FeedItem, error) {
	const q = `
		SELECT tweet_id, COALESCE(source_handle,''), author_handle,
		       COALESCE(author_display_name, ''), COALESCE(author_avatar_url, ''),
		       COALESCE(body_text, ''), COALESCE(lang, ''),
		       COALESCE(is_retweet, 0),
		       COALESCE(retweeted_by_handle, ''), COALESCE(retweeted_by_display_name, ''),
		       COALESCE(quote_tweet_id, ''), COALESCE(quote_author_handle, ''),
		       COALESCE(quote_author_display_name, ''), COALESCE(quote_author_avatar_url, ''),
		       COALESCE(quote_body_text, ''), COALESCE(quote_lang, ''),
		       COALESCE(quote_media_json, ''),
		       COALESCE(media_json, ''),
		       COALESCE(canonical_url, ''), COALESCE(reply_to_handle, ''),
		       COALESCE(reply_to_status, ''),
		       COALESCE(is_reply, 0), COALESCE(is_ghost, 0),
		       COALESCE(quote_published_at, 0),
		       COALESCE(views, 0), COALESCE(likes, 0), COALESCE(retweets, 0),
		       COALESCE(published_at, 0), COALESCE(fetched_at, 0),
		       COALESCE(content_hash, '')
		FROM feed_items WHERE tweet_id = ?`

	var f model.FeedItem
	var quotePubMs, pubMs, fetchedMs int64
	err := db.conn.QueryRow(q, tweetID).Scan(
		&f.TweetID, &f.SourceHandle, &f.AuthorHandle,
		&f.AuthorDisplayName, &f.AuthorAvatarURL,
		&f.BodyText, &f.Lang,
		&f.IsRetweet,
		&f.RetweetedByHandle, &f.RetweetedByDisplayName,
		&f.QuoteTweetID, &f.QuoteAuthorHandle,
		&f.QuoteAuthorDisplayName, &f.QuoteAuthorAvatarURL,
		&f.QuoteBodyText, &f.QuoteLang,
		&f.QuoteMediaJSON,
		&f.MediaJSON,
		&f.CanonicalURL, &f.ReplyToHandle,
		&f.ReplyToStatus,
		&f.IsReply, &f.IsGhost,
		&quotePubMs,
		&f.Views, &f.Likes, &f.Retweets,
		&pubMs, &fetchedMs,
		&f.ContentHash,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("GetFeedItemByTweetID: %w", err)
	}
	if quotePubMs > 0 {
		t := time.UnixMilli(quotePubMs).UTC()
		f.QuotePublishedAt = &t
	}
	if pubMs > 0 {
		t := time.UnixMilli(pubMs).UTC()
		f.PublishedAt = &t
	}
	if fetchedMs > 0 {
		f.FetchedAt = time.UnixMilli(fetchedMs).UTC()
	}
	f.ParseMedia()
	return &f, nil
}

// UpsertGhostFeedItem stores a single feed_items row with is_ghost=1. The row
// represents a parent tweet fetched from fxtwitter to maintain thread continuity
// — the user does not follow this account, so we don't want it polluting feed
// listings, but we need it joinable via reply_to_status.
//
// If a row with the same tweet_id already exists and is NOT a ghost (i.e., we
// follow this account and ingested it normally), the ON CONFLICT clause keeps
// is_ghost=0 — the real row wins.
func (db *DB) UpsertGhostFeedItem(item model.FeedItem) error {
	item.IsGhost = true
	_, err := db.UpsertFeedItems([]model.FeedItem{item})
	return err
}

// UpdateReplyToStatus sets reply_to_status on an existing feed_items row.
// Idempotent — calling multiple times with the same value is a no-op.
func (db *DB) UpdateReplyToStatus(tweetID, parentTweetID string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			`UPDATE feed_items SET reply_to_status = ? WHERE tweet_id = ?`,
			parentTweetID, tweetID,
		)
		return err
	})
}

// GetThreadChain returns the conversation chain rooted at tweetID's earliest
// known ancestor and ending at tweetID, ordered root → leaf. If a parent in
// the chain is missing from the DB, the chain stops at the first orphan
// (the leaf row is always returned, even with no ancestors).
//
// Implementation uses a recursive CTE walking up via reply_to_status, then
// reverses to root → leaf order.
func (db *DB) GetThreadChain(tweetID string) ([]model.FeedItem, error) {
	const q = `
		WITH RECURSIVE chain(tweet_id, depth) AS (
			SELECT tweet_id, 0 FROM feed_items WHERE tweet_id = ?
			UNION ALL
			SELECT fi.reply_to_status, c.depth + 1
			FROM chain c
			JOIN feed_items fi ON fi.tweet_id = c.tweet_id
			WHERE fi.reply_to_status IS NOT NULL
			  AND fi.reply_to_status != ''
			  AND c.depth < 50
		)
		SELECT tweet_id FROM chain
		WHERE tweet_id IS NOT NULL AND tweet_id != ''
		ORDER BY depth DESC`

	rows, err := db.conn.Query(q, tweetID)
	if err != nil {
		return nil, fmt.Errorf("GetThreadChain query: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]model.FeedItem, 0, len(ids))
	for _, id := range ids {
		fi, err := db.GetFeedItemByTweetID(id)
		if err != nil {
			return nil, err
		}
		if fi != nil {
			out = append(out, *fi)
		}
	}
	return out, nil
}

// FindUnresolvedReplies returns leaf reply rows that still have empty
// reply_to_status — typically those whose per-cycle resolve failed (transient
// fxtwitter errors, deadline exceeded). Limit caps the sweep size so a
// long-running outage doesn't produce a huge batch.
func (db *DB) FindUnresolvedReplies(limit int) ([]model.FeedItem, error) {
	const q = `
		SELECT tweet_id, COALESCE(author_handle, ''), COALESCE(reply_to_handle, '')
		FROM feed_items
		WHERE COALESCE(is_reply, 0) = 1
		  AND COALESCE(reply_to_status, '') = ''
		  AND COALESCE(is_ghost, 0) = 0
		ORDER BY published_at DESC
		LIMIT ?`

	rows, err := db.conn.Query(q, limit)
	if err != nil {
		return nil, fmt.Errorf("FindUnresolvedReplies: %w", err)
	}
	defer rows.Close()

	var out []model.FeedItem
	for rows.Next() {
		var f model.FeedItem
		if err := rows.Scan(&f.TweetID, &f.AuthorHandle, &f.ReplyToHandle); err != nil {
			return nil, err
		}
		f.IsReply = true
		out = append(out, f)
	}
	return out, rows.Err()
}
