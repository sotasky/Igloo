package db

import (
	"database/sql"
	"strings"
	"time"
)

// XContentDownloadOwner is the compact owner-level status projection used by
// queue UIs. Individual asset rows remain the only durable work records.
type XContentDownloadOwner struct {
	TweetID      string
	SourceHandle string
	MediaKind    string
	AssetCount   int
	Attempts     int
	LastError    string
}

// RetryXContentForTweet makes the direct and quoted canonical assets for one
// stored post eligible for a user-requested retry.
func (db *DB) RetryXContentForTweet(tweetID string) (bool, error) {
	ownerIDs, err := db.xContentOwnerIDsForTweet(tweetID)
	if err != nil {
		return false, err
	}
	n, err := db.RequeueXContentAssets(ownerIDs, true, "manual", time.Now().UnixMilli())
	return n > 0, err
}

func (db *DB) xContentOwnerIDsForTweet(tweetID string) ([]string, error) {
	tweetID = strings.TrimSpace(tweetID)
	if tweetID == "" {
		return nil, nil
	}
	ids := []string{tweetID}
	var quoteID string
	err := db.conn.QueryRow(`SELECT COALESCE(quote_tweet_id, '') FROM feed_items WHERE tweet_id = ?`, tweetID).Scan(&quoteID)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if quoteID != "" {
		ids = append(ids, quoteID)
	}
	return uniqueStrings(ids), nil
}

// ListPendingXContentDownloads projects owner-level queue state from assets.
func (db *DB) ListPendingXContentDownloads() (processing, pending []XContentDownloadOwner, err error) {
	rows, err := db.conn.Query(`
		SELECT a.owner_id,
		       COALESCE(fi.source_handle, ''),
		       CASE WHEN SUM(CASE WHEN a.state = 'downloading' THEN 1 ELSE 0 END) > 0
		            THEN 'processing' ELSE 'queued' END,
		       CASE WHEN SUM(CASE WHEN a.content_type LIKE 'video/%' THEN 1 ELSE 0 END) > 0
		            THEN 'video' ELSE 'image' END,
		       SUM(CASE WHEN a.asset_kind = 'post_media' THEN 1 ELSE 0 END),
		       MAX(a.attempts), MAX(a.last_error)
		FROM assets a
		LEFT JOIN feed_items_resolved fi ON fi.tweet_id = a.owner_id
		WHERE a.owner_kind = 'tweet'
		  AND a.asset_kind IN ('post_audio', 'post_media', 'post_thumbnail')
		  AND a.state IN ('queued', 'downloading')
		GROUP BY a.owner_id, fi.source_handle
		ORDER BY MAX(a.updated_at_ms) ASC, a.owner_id ASC
	`)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var owner XContentDownloadOwner
		var state string
		if err := rows.Scan(
			&owner.TweetID, &owner.SourceHandle, &state,
			&owner.MediaKind, &owner.AssetCount, &owner.Attempts,
			&owner.LastError,
		); err != nil {
			return nil, nil, err
		}
		if state == "processing" {
			processing = append(processing, owner)
		} else {
			pending = append(pending, owner)
		}
	}
	return processing, pending, rows.Err()
}

func (db *DB) CountPendingXContentDownloads() (queued int, processing int, err error) {
	err = db.conn.QueryRow(`
		WITH owner_state AS (
			SELECT owner_id,
			       MAX(CASE WHEN state = 'downloading' THEN 1 ELSE 0 END) AS processing
			FROM assets
			WHERE owner_kind = 'tweet'
			  AND asset_kind IN ('post_audio', 'post_media', 'post_thumbnail')
			  AND state IN ('queued', 'downloading')
			GROUP BY owner_id
		)
		SELECT COALESCE(SUM(CASE WHEN processing = 0 THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(processing), 0)
		FROM owner_state
	`).Scan(&queued, &processing)
	return queued, processing, err
}
