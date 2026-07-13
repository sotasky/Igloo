package db

import (
	"database/sql"
	"strings"
	"time"
)

// NextMediaWorkDelay returns the next due time across durable content assets
// and video work for the currently eligible download platforms. Enqueue
// signals may wake the executor sooner.
func (db *DB) NextMediaWorkDelay(nowMs int64, downloadPlatforms []string, includeTweets bool) (time.Duration, error) {
	if nowMs <= 0 {
		nowMs = time.Now().UnixMilli()
	}
	downloadPlatforms = normalizePlatforms(downloadPlatforms)
	query := `
		SELECT MIN(due_ms) FROM (
			SELECT CASE
			         WHEN mo.job_state = 'downloading' THEN mo.lease_until_ms
			         WHEN mo.next_attempt_at_ms > 0 THEN mo.next_attempt_at_ms
			         ELSE ?
			       END AS due_ms
			FROM media_objects mo
			WHERE mo.job_state IN ('queued', 'downloading')
			  AND EXISTS (
			    SELECT 1 FROM assets a
			    WHERE a.desired_object_id = mo.object_id AND a.lifecycle_state = 'active'
			      AND ((? AND a.owner_kind = 'tweet' AND a.asset_kind IN ('post_audio', 'post_media', 'post_thumbnail'))
			        OR (a.owner_kind = 'comment_author' AND a.asset_kind = 'avatar'))
			  )`
	args := []any{nowMs, includeTweets}
	if len(downloadPlatforms) > 0 {
		query += `
			UNION ALL
			SELECT CASE
			         WHEN dq.status = 'processing' THEN dq.lease_until_ms
			         WHEN dq.next_attempt_at_ms > 0 THEN dq.next_attempt_at_ms
			         ELSE ?
			       END
			FROM download_queue dq
			WHERE dq.status IN ('pending', 'processing')
			  AND EXISTS (SELECT 1 FROM video_desires d WHERE d.video_id = dq.video_id)
			  AND EXISTS (
			    SELECT 1 FROM channels owner_channel
			    WHERE owner_channel.channel_id = dq.owner_channel_id
			      AND ` + videoDownloadPlatformSQL("dq", "owner_channel") + ` IN (` + placeholders(len(downloadPlatforms)) + `)
			  )
			  AND NOT EXISTS (
			    SELECT 1 FROM videos v
			    WHERE v.video_id = dq.video_id
			      AND ` + readyVideoMediaExistsSQL("v") + `
			  )`
		args = append(args, nowMs)
		args = append(args, stringsToAny(downloadPlatforms)...)
	}
	query += `
		)`
	var due sql.NullInt64
	err := db.reader().QueryRow(query, args...).Scan(&due)
	if err != nil {
		return 0, err
	}
	if !due.Valid {
		return 5 * time.Minute, nil
	}
	delay := time.Duration(due.Int64-nowMs) * time.Millisecond
	if delay < 0 {
		return 0, nil
	}
	return delay, nil
}

func normalizePlatforms(platforms []string) []string {
	seen := make(map[string]struct{}, len(platforms))
	out := make([]string, 0, len(platforms))
	for _, platform := range platforms {
		platform = strings.ToLower(strings.TrimSpace(platform))
		if platform == "" {
			continue
		}
		if _, ok := seen[platform]; ok {
			continue
		}
		seen[platform] = struct{}{}
		out = append(out, platform)
	}
	return out
}

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
		       CASE WHEN SUM(CASE WHEN mo.job_state = 'downloading' THEN 1 ELSE 0 END) > 0
		            THEN 'processing' ELSE 'queued' END,
		       CASE WHEN SUM(CASE WHEN mo.content_type LIKE 'video/%' THEN 1 ELSE 0 END) > 0
		            THEN 'video' ELSE 'image' END,
		       SUM(CASE WHEN a.asset_kind = 'post_media' THEN 1 ELSE 0 END),
		       MAX(mo.attempts), MAX(mo.last_error)
		FROM media_objects mo
		CROSS JOIN assets a INDEXED BY idx_assets_desired_object
		LEFT JOIN feed_items_resolved fi ON fi.tweet_id = a.owner_id
		WHERE a.desired_object_id = mo.object_id
		  AND a.lifecycle_state = 'active'
		  AND a.owner_kind = 'tweet'
		  AND a.asset_kind IN ('post_audio', 'post_media', 'post_thumbnail')
		  AND mo.job_state IN ('queued', 'downloading')
		GROUP BY a.owner_id, fi.source_handle
		ORDER BY MAX(mo.updated_at_ms) ASC, a.owner_id ASC
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
			SELECT a.owner_id,
			       MAX(CASE WHEN mo.job_state = 'downloading' THEN 1 ELSE 0 END) AS processing
			FROM media_objects mo
			CROSS JOIN assets a INDEXED BY idx_assets_desired_object
			WHERE a.desired_object_id = mo.object_id
			  AND a.lifecycle_state = 'active'
			  AND a.owner_kind = 'tweet'
			  AND a.asset_kind IN ('post_audio', 'post_media', 'post_thumbnail')
			  AND mo.job_state IN ('queued', 'downloading')
			GROUP BY a.owner_id
		)
		SELECT COALESCE(SUM(CASE WHEN processing = 0 THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(processing), 0)
		FROM owner_state
	`).Scan(&queued, &processing)
	return queued, processing, err
}
