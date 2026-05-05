package db

import (
	"database/sql"
	"time"
)

// FeedMediaJobRow is the DB-level representation of a feed media download job.
type FeedMediaJobRow struct {
	TweetID      string
	TweetURL     string
	SourceHandle string
	Status       string // queued|processing|completed|failed|pruned
	MediaKind    string // video|image|unknown
	SlideCount   int
	RetryCount   int
	Priority     int
	LastError    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// EnqueueFeedMediaJobs inserts jobs with status='queued', ignoring duplicates.
func (db *DB) EnqueueFeedMediaJobs(jobs []FeedMediaJobRow) error {
	if len(jobs) == 0 {
		return nil
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(`
			INSERT OR IGNORE INTO feed_media_jobs
				(tweet_id, tweet_url, source_handle, status, media_kind, slide_count,
				 retry_count, priority, created_at, updated_at)
			VALUES (?, ?, ?, 'queued', ?, ?, ?, ?, CAST(strftime('%s','now') AS INTEGER) * 1000, CAST(strftime('%s','now') AS INTEGER) * 1000)
		`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, j := range jobs {
			if _, err := stmt.Exec(
				j.TweetID, nilIfEmpty(j.TweetURL), nilIfEmpty(j.SourceHandle),
				nilIfEmpty(j.MediaKind), j.SlideCount, j.RetryCount, j.Priority,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

// ClaimFeedMediaBatch atomically selects up to limit queued jobs and marks them processing.
// Returns the claimed jobs.
func (db *DB) ClaimFeedMediaBatch(limit int) ([]FeedMediaJobRow, error) {
	var claimed []FeedMediaJobRow

	err := db.WithWrite(func(tx *sql.Tx) error {
		rows, err := tx.Query(`
			SELECT tweet_id, COALESCE(tweet_url,''), COALESCE(source_handle,''),
			       COALESCE(status,''), COALESCE(media_kind,'unknown'),
			       COALESCE(slide_count,0), COALESCE(retry_count,0),
			       COALESCE(priority,0), COALESCE(last_error,'')
			FROM feed_media_jobs
			WHERE status = 'queued'
			  AND (retry_count = 0 OR updated_at + (30 * (1 << MIN(retry_count, 10)) * 1000) < CAST(strftime('%s','now') AS INTEGER) * 1000)
			ORDER BY priority DESC, updated_at ASC
			LIMIT ?
		`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var j FeedMediaJobRow
			if err := rows.Scan(
				&j.TweetID, &j.TweetURL, &j.SourceHandle,
				&j.Status, &j.MediaKind,
				&j.SlideCount, &j.RetryCount,
				&j.Priority, &j.LastError,
			); err != nil {
				return err
			}
			claimed = append(claimed, j)
		}
		if err := rows.Err(); err != nil {
			return err
		}

		if len(claimed) == 0 {
			return nil
		}

		// Mark all claimed rows as processing
		now := time.Now().UnixMilli()
		for _, j := range claimed {
			if _, err := tx.Exec(
				"UPDATE feed_media_jobs SET status='processing', updated_at=? WHERE tweet_id=?",
				now, j.TweetID,
			); err != nil {
				return err
			}
		}
		return nil
	})

	return claimed, err
}

// UpdateFeedMediaJobStatus updates the status, last_error, and retry_count for a job.
func (db *DB) UpdateFeedMediaJobStatus(tweetID, status, lastError string, retryCount int) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		now := time.Now().UnixMilli()
		_, err := tx.Exec(`
			UPDATE feed_media_jobs
			SET status=?, last_error=?, retry_count=?, updated_at=?
			WHERE tweet_id=?
		`, status, nilIfEmpty(lastError), retryCount, now, tweetID)
		return err
	})
}

// ResetStaleFeedMediaJobs resets feed_media_jobs left in processing state back to queued.
// Returns the number of rows updated.
func (db *DB) ResetStaleFeedMediaJobs() (int, error) {
	var affected int
	err := db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(
			"UPDATE feed_media_jobs SET status='queued', updated_at=? WHERE status='processing'",
			time.Now().UnixMilli(),
		)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		affected = int(n)
		return nil
	})
	return affected, err
}

// GetFeedMediaJobsByStatus returns all processing and queued jobs split into two lists.
func (db *DB) GetFeedMediaJobsByStatus() (processing, pending []FeedMediaJobRow, err error) {
	rows, qErr := db.conn.Query(`
		SELECT tweet_id, COALESCE(tweet_url,''), COALESCE(source_handle,''),
		       status, COALESCE(media_kind,'unknown'),
		       COALESCE(slide_count,0), COALESCE(retry_count,0),
		       COALESCE(priority,0), COALESCE(last_error,''),
		       created_at, updated_at
		FROM feed_media_jobs
		WHERE status IN ('processing','queued')
		ORDER BY priority DESC, updated_at ASC
	`)
	if qErr != nil {
		return nil, nil, qErr
	}
	defer rows.Close()

	for rows.Next() {
		var j FeedMediaJobRow
		var createdAt, updatedAt string
		if scanErr := rows.Scan(
			&j.TweetID, &j.TweetURL, &j.SourceHandle,
			&j.Status, &j.MediaKind,
			&j.SlideCount, &j.RetryCount,
			&j.Priority, &j.LastError,
			&createdAt, &updatedAt,
		); scanErr != nil {
			return nil, nil, scanErr
		}
		switch j.Status {
		case "processing":
			processing = append(processing, j)
		case "queued":
			pending = append(pending, j)
		}
	}
	return processing, pending, rows.Err()
}

// CountPendingFeedMediaJobs returns the number of queued and processing jobs.
func (db *DB) CountPendingFeedMediaJobs() (queued int, processing int, err error) {
	rows, err := db.conn.Query(`
		SELECT status, COUNT(*)
		FROM feed_media_jobs
		WHERE status IN ('queued','processing')
		GROUP BY status
	`)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return 0, 0, err
		}
		switch status {
		case "queued":
			queued = count
		case "processing":
			processing = count
		}
	}
	return queued, processing, rows.Err()
}

// EnqueueMissingBookmarkLikeMedia creates feed_media_jobs for bookmarked and
// liked tweets that have media (direct or quote) but no local media_files yet
// (and no existing job). Returns the number of jobs enqueued.
func (db *DB) EnqueueMissingBookmarkLikeMedia() (int, error) {
	var count int
	err := db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			INSERT OR IGNORE INTO feed_media_jobs
				(tweet_id, tweet_url, source_handle, status, media_kind, slide_count,
				 retry_count, priority, created_at, updated_at)
			SELECT
				fi.tweet_id,
				fi.canonical_url,
				fi.source_handle,
				'queued',
				CASE
					WHEN COALESCE(fi.media_json, fi.quote_media_json) LIKE '%"video"%'
					  OR COALESCE(fi.media_json, fi.quote_media_json) LIKE '%"gif"%' THEN 'video'
					ELSE 'image'
				END,
				0,
				0,
				1,
				CAST(strftime('%s','now') AS INTEGER) * 1000,
				CAST(strftime('%s','now') AS INTEGER) * 1000
			FROM feed_items fi
			WHERE (
			      (fi.media_json IS NOT NULL AND fi.media_json != '')
			   OR (fi.quote_media_json IS NOT NULL AND fi.quote_media_json != '')
			  )
			  AND (
			      EXISTS (SELECT 1 FROM bookmarks b WHERE b.video_id = fi.tweet_id)
			   OR EXISTS (SELECT 1 FROM feed_likes fl WHERE fl.tweet_id = fi.tweet_id)
			  )
			  AND NOT EXISTS (SELECT 1 FROM media_files mf WHERE mf.owner_id = fi.tweet_id)
			  AND NOT EXISTS (SELECT 1 FROM media_files mf WHERE mf.owner_type = 'quote_media' AND mf.owner_id = fi.quote_tweet_id)
			  AND NOT EXISTS (SELECT 1 FROM feed_media_jobs j WHERE j.tweet_id = fi.tweet_id)
		`)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		count = int(n)
		return nil
	})
	return count, err
}

// EnsureBookmarkVideoStubs creates stub video entries for bookmarked feed items
// that are missing from the videos table (so they appear in the bookmarks page).
// Also normalizes any existing stubs with mixed-case channel_id to lowercase
// so they match channels table conventions.
// Returns the number of stubs created.
func (db *DB) EnsureBookmarkVideoStubs() (int, error) {
	var count int
	err := db.WithWrite(func(tx *sql.Tx) error {
		// Fix existing stubs with mixed-case channel_id (channels use lowercase).
		_, _ = tx.Exec(`
			UPDATE videos SET channel_id = LOWER(channel_id)
			WHERE channel_id LIKE 'twitter_%' AND channel_id != LOWER(channel_id)
		`)

		// Fix stubs with bare "twitter_" channel_id — these are bookmarked quote
		// tweets whose author was not resolved on initial insert.
		_, _ = tx.Exec(`
			UPDATE videos SET channel_id = 'twitter_' || LOWER(fi.quote_author_handle)
			FROM feed_items fi
			WHERE fi.quote_tweet_id = videos.video_id
			  AND videos.channel_id = 'twitter_'
			  AND fi.quote_author_handle != ''
		`)

		res, err := tx.Exec(`
			INSERT OR IGNORE INTO videos (video_id, channel_id, title, duration, file_path)
			SELECT
				b.video_id,
				COALESCE(
					'twitter_' || LOWER(NULLIF(
						COALESCE(direct.author_handle, quoted.quote_author_handle),
					'')),
					'twitter_'
				),
				'X post ' || b.video_id,
				0,
				''
			FROM bookmarks b
			LEFT JOIN feed_items direct ON direct.tweet_id = b.video_id
			                           AND direct.author_handle != ''
			LEFT JOIN feed_items quoted ON quoted.quote_tweet_id = b.video_id
			                           AND quoted.quote_author_handle != ''
			WHERE NOT EXISTS (SELECT 1 FROM videos v WHERE v.video_id = b.video_id)
		`)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		count = int(n)

		// Any zero-valued sync_seq rows are invisible to the bundle-delta streams once
		// the Android cursor has advanced. Give each stub a real global sequence.
		rows, err := tx.Query(`SELECT video_id FROM videos WHERE COALESCE(sync_seq, 0) = 0`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var videoID string
			if err := rows.Scan(&videoID); err != nil {
				return err
			}
			if err := db.bumpVideoSyncSeqTx(tx, videoID); err != nil {
				return err
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		return nil
	})
	return count, err
}
