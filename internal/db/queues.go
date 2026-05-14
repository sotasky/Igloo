package db

import (
	"database/sql"
	"time"
)

// FeedMediaJobRow is the DB-level representation of a feed media download job.
type FeedMediaJobRow struct {
	TweetID         string
	TweetURL        string
	SourceHandle    string
	Status          string // queued|processing|completed|failed|pruned
	MediaKind       string // video|image|unknown
	SlideCount      int
	RetryCount      int
	Priority        int
	LastError       string
	LastErrorKind   string
	LeaseOwner      string
	LeaseUntilMs    int64
	NextAttemptAtMs int64
	Tool            string
	CookieLabel     string
	StartedAtMs     int64
	CompletedAtMs   int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
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
		defer func() {
			_ = stmt.Close()
		}()
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
	return db.ClaimFeedMediaBatchWithLease(LeaseOptions{
		Owner: "feedmedia:legacy",
		Limit: limit,
	})
}

// ClaimFeedMediaBatchWithLease claims queued or expired processing jobs with a durable lease.
func (db *DB) ClaimFeedMediaBatchWithLease(opts LeaseOptions) ([]FeedMediaJobRow, error) {
	opts = normalizeLeaseOptions(opts, "queued", "processing")
	var claimed []FeedMediaJobRow
	err := db.WithWrite(func(tx *sql.Tx) error {
		query := `
			SELECT tweet_id
			FROM feed_media_jobs
			WHERE ` + leaseEligibleSQL() + `
			ORDER BY priority DESC, retry_count ASC, updated_at DESC
			LIMIT ?`
		ids, err := claimLeasedIDs(tx, "feed_media_jobs", "tweet_id", query, []any{
			opts.NowMs, opts.StatusFrom, opts.NowMs, opts.StatusTo, opts.NowMs, opts.Limit,
		}, opts)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := tx.Exec(
				`UPDATE feed_media_jobs SET started_at_ms=?, updated_at=? WHERE tweet_id=?`,
				opts.NowMs, opts.NowMs, id,
			); err != nil {
				return err
			}
			job, err := readFeedMediaJobTx(tx, id)
			if err != nil {
				return err
			}
			claimed = append(claimed, job)
		}
		return nil
	})
	return claimed, err
}

func readFeedMediaJobTx(tx *sql.Tx, tweetID string) (FeedMediaJobRow, error) {
	var j FeedMediaJobRow
	err := tx.QueryRow(`
		SELECT tweet_id, COALESCE(tweet_url,''), COALESCE(source_handle,''),
		       COALESCE(status,''), COALESCE(media_kind,'unknown'),
		       COALESCE(slide_count,0), COALESCE(retry_count,0),
		       COALESCE(priority,0), COALESCE(last_error,''),
		       COALESCE(last_error_kind,''), COALESCE(lease_owner,''),
		       COALESCE(lease_until_ms,0), COALESCE(next_attempt_at_ms,0),
		       COALESCE(tool,''), COALESCE(cookie_label,''),
		       COALESCE(started_at_ms,0), COALESCE(completed_at_ms,0)
		FROM feed_media_jobs
		WHERE tweet_id=?
	`, tweetID).Scan(
		&j.TweetID, &j.TweetURL, &j.SourceHandle,
		&j.Status, &j.MediaKind,
		&j.SlideCount, &j.RetryCount,
		&j.Priority, &j.LastError,
		&j.LastErrorKind, &j.LeaseOwner,
		&j.LeaseUntilMs, &j.NextAttemptAtMs,
		&j.Tool, &j.CookieLabel,
		&j.StartedAtMs, &j.CompletedAtMs,
	)
	return j, err
}

// UpdateFeedMediaJobStatus updates the status, last_error, and retry_count for a job.
func (db *DB) UpdateFeedMediaJobStatus(tweetID, status, lastError string, retryCount int) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		now := time.Now().UnixMilli()
		completedAt := int64(0)
		if status == "completed" || status == "failed" || status == "pruned" {
			completedAt = now
		}
		_, err := tx.Exec(`
			UPDATE feed_media_jobs
			SET status=?,
			    last_error=?,
			    retry_count=?,
			    next_attempt_at_ms=0,
			    last_error_kind='',
			    lease_owner='',
			    lease_until_ms=0,
			    completed_at_ms=?,
			    updated_at=?
			WHERE tweet_id=?
		`, status, nilIfEmpty(lastError), retryCount, completedAt, now, tweetID)
		return err
	})
}

func (db *DB) CompleteFeedMediaJob(tweetID, owner string, nowMs int64) error {
	if nowMs == 0 {
		nowMs = time.Now().UnixMilli()
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			UPDATE feed_media_jobs
			   SET status='completed',
			       retry_count=0,
			       next_attempt_at_ms=0,
			       last_error_kind='',
			       last_error=NULL,
			       lease_owner='',
			       lease_until_ms=0,
			       completed_at_ms=?,
			       updated_at=?
			 WHERE tweet_id=?
			   AND status='processing'
			   AND lease_owner=?
		`, nowMs, nowMs, tweetID, owner)
		if err != nil {
			return err
		}
		return requireQueueLeaseUpdate(res, "feed_media_jobs", tweetID, owner)
	})
}

func (db *DB) RetryFeedMediaJob(tweetID, owner, kind, message string, delay time.Duration, nowMs int64) error {
	if nowMs == 0 {
		nowMs = time.Now().UnixMilli()
	}
	nextMs := nowMs + delay.Milliseconds()
	if delay < 0 {
		nextMs = nowMs
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			UPDATE feed_media_jobs
			   SET status='queued',
			       retry_count=retry_count+1,
			       next_attempt_at_ms=?,
			       last_error_kind=?,
			       last_error=?,
			       lease_owner='',
			       lease_until_ms=0,
			       completed_at_ms=0,
			       updated_at=?
			 WHERE tweet_id=?
			   AND status='processing'
			   AND lease_owner=?
		`, nextMs, trimJobError(kind), trimJobError(message), nowMs, tweetID, owner)
		if err != nil {
			return err
		}
		return requireQueueLeaseUpdate(res, "feed_media_jobs", tweetID, owner)
	})
}

func (db *DB) FailFeedMediaJob(tweetID, owner, kind, message string, nowMs int64) error {
	if nowMs == 0 {
		nowMs = time.Now().UnixMilli()
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			UPDATE feed_media_jobs
			   SET status='failed',
			       retry_count=retry_count+1,
			       next_attempt_at_ms=0,
			       last_error_kind=?,
			       last_error=?,
			       lease_owner='',
			       lease_until_ms=0,
			       completed_at_ms=?,
			       updated_at=?
			 WHERE tweet_id=?
			   AND status='processing'
			   AND lease_owner=?
		`, trimJobError(kind), trimJobError(message), nowMs, nowMs, tweetID, owner)
		if err != nil {
			return err
		}
		return requireQueueLeaseUpdate(res, "feed_media_jobs", tweetID, owner)
	})
}

func (db *DB) PruneFeedMediaJob(tweetID, owner, kind, message string, retryCount int, nowMs int64) error {
	if nowMs == 0 {
		nowMs = time.Now().UnixMilli()
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			UPDATE feed_media_jobs
			   SET status='pruned',
			       retry_count=?,
			       next_attempt_at_ms=0,
			       last_error_kind=?,
			       last_error=?,
			       lease_owner='',
			       lease_until_ms=0,
			       completed_at_ms=?,
			       updated_at=?
			 WHERE tweet_id=?
			   AND status='processing'
			   AND lease_owner=?
		`, retryCount, trimJobError(kind), trimJobError(message), nowMs, nowMs, tweetID, owner)
		if err != nil {
			return err
		}
		return requireQueueLeaseUpdate(res, "feed_media_jobs", tweetID, owner)
	})
}

func (db *DB) RenewFeedMediaJobLease(tweetID, owner string, nowMs int64, lease time.Duration) error {
	if tweetID == "" || owner == "" {
		return nil
	}
	if nowMs == 0 {
		nowMs = time.Now().UnixMilli()
	}
	if lease <= 0 {
		lease = defaultQueueLease
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			UPDATE feed_media_jobs
			   SET lease_until_ms=?, updated_at=?
			 WHERE tweet_id=?
			   AND status='processing'
			   AND lease_owner=?
		`, nowMs+lease.Milliseconds(), nowMs, tweetID, owner)
		return err
	})
}

// PromoteFeedMediaJobForTweet makes an existing feed media job urgent.
func (db *DB) PromoteFeedMediaJobForTweet(tweetID string, priority int) (bool, error) {
	if tweetID == "" {
		return false, nil
	}
	var changed bool
	err := db.WithWrite(func(tx *sql.Tx) error {
		now := time.Now().UnixMilli()
		res, err := tx.Exec(`
			UPDATE feed_media_jobs
			   SET status = 'queued',
			       priority = MAX(COALESCE(priority, 0), ?),
			       retry_count = 0,
			       last_error = NULL,
			       last_error_kind = '',
			       next_attempt_at_ms = 0,
			       lease_owner = '',
			       lease_until_ms = 0,
			       completed_at_ms = 0,
			       updated_at = ?
			 WHERE tweet_id = ?
			   AND status IN ('queued', 'failed', 'pruned')
		`, priority, now, tweetID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		changed = n > 0
		return nil
	})
	return changed, err
}

// ResetStaleFeedMediaJobs resets feed_media_jobs left in processing state back to queued.
// Returns the number of rows updated.
func (db *DB) ResetStaleFeedMediaJobs() (int, error) {
	return db.ResetStaleFeedMediaJobsAt(time.Now().UnixMilli())
}

func (db *DB) ResetStaleFeedMediaJobsAt(nowMs int64) (int, error) {
	var affected int
	err := db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(
			`UPDATE feed_media_jobs
			    SET status='queued',
			        lease_owner='',
			        lease_until_ms=0,
			        updated_at=?
			  WHERE status='processing'
			    AND COALESCE(lease_until_ms,0) > 0
			    AND lease_until_ms <= ?`,
			nowMs, nowMs,
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
	defer func() {
		_ = rows.Close()
	}()

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
	defer func() {
		_ = rows.Close()
	}()

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
		defer func() {
			_ = rows.Close()
		}()
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
