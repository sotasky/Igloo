package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
)

// ChannelQueueRow is the DB-level representation of a channel download queue entry.
type ChannelQueueRow struct {
	ChannelID string
	Status    string
	Priority  int
	Platform  string // joined from channels table
}

// DownloadQueueRow is the DB-level representation of a video download queue entry.
type DownloadQueueRow struct {
	VideoID           string
	ChannelID         string
	Title             string
	Status            string
	Priority          int
	Error             string
	RetryCount        int
	PublishedAtMs     int64
	LastErrorKind     string
	LastErrorStrategy string
	LeaseOwner        string
	LeaseUntilMs      int64
	NextAttemptAtMs   int64
	Tool              string
	CookieLabel       string
}

// AddChannelToQueue inserts or updates a channel in the channel_queue with status='pending'.
func (db *DB) AddChannelToQueue(channelID string, priority int) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			INSERT INTO channel_queue (channel_id, status, priority, added_at)
			VALUES (?, 'pending', ?, CAST(strftime('%s','now') AS INTEGER) * 1000)
			ON CONFLICT(channel_id) DO UPDATE SET
				status='pending', priority=excluded.priority
		`, channelID, priority)
		return err
	})
}

// GetPendingChannelQueue returns up to limit pending channel queue entries joined with channels.platform.
func (db *DB) GetPendingChannelQueue(limit int) ([]ChannelQueueRow, error) {
	rows, err := db.conn.Query(`
		SELECT cq.channel_id, cq.status, cq.priority, COALESCE(c.platform, 'youtube')
		FROM channel_queue cq
		LEFT JOIN channels c ON c.channel_id = cq.channel_id
		WHERE cq.status = 'pending'
		ORDER BY cq.priority DESC, cq.added_at ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var result []ChannelQueueRow
	for rows.Next() {
		var r ChannelQueueRow
		if err := rows.Scan(&r.ChannelID, &r.Status, &r.Priority, &r.Platform); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// UpdateChannelQueueStatus updates the status of a channel_queue entry.
func (db *DB) UpdateChannelQueueStatus(channelID, status string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			"UPDATE channel_queue SET status=? WHERE channel_id=?",
			status, channelID,
		)
		return err
	})
}

// AddToDownloadQueue inserts a video into download_queue, ignoring duplicates.
func (db *DB) AddToDownloadQueue(videoID, channelID, title string) error {
	return db.AddToDownloadQueueWithPublishedAt(videoID, channelID, title, 0)
}

// AddToDownloadQueueWithPublishedAt inserts a video into download_queue with
// source-discovered publish time metadata, ignoring duplicates.
func (db *DB) AddToDownloadQueueWithPublishedAt(videoID, channelID, title string, publishedAtMs int64) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			INSERT OR IGNORE INTO download_queue
				(video_id, channel_id, title, published_at_ms, status, added_at)
			VALUES (?, ?, ?, ?, 'pending', CAST(strftime('%s','now') AS INTEGER) * 1000)
		`, videoID, channelID, title, publishedAtMs)
		return err
	})
}

// ClaimDownloadBatch atomically selects up to limit pending download jobs and marks them processing.
func (db *DB) ClaimDownloadBatch(limit int) ([]DownloadQueueRow, error) {
	return db.ClaimDownloadBatchWithLease(LeaseOptions{
		Owner: "downloadpool:legacy",
		Limit: limit,
	})
}

// ClaimDownloadBatchWithLease claims pending or expired processing jobs with a durable lease.
func (db *DB) ClaimDownloadBatchWithLease(opts LeaseOptions) ([]DownloadQueueRow, error) {
	opts = normalizeLeaseOptions(opts, "pending", "processing")
	var claimed []DownloadQueueRow

	err := db.WithWrite(func(tx *sql.Tx) error {
		// Round-robin across platforms so a large TikTok backlog
		// doesn't starve YouTube (or vice versa). ROW_NUMBER per
		// platform interleaves: 1st from each platform, then 2nd, etc.
		query := `
			WITH eligible AS (
				SELECT video_id, channel_id, COALESCE(title,'') AS title, status,
				       COALESCE(priority,0) AS priority, COALESCE(error,'') AS error,
				       COALESCE(retry_count,0) AS retry_count,
				       COALESCE(published_at_ms,0) AS published_at_ms, added_at,
				       CASE WHEN channel_id LIKE 'tiktok_%' THEN 'tiktok'
				            WHEN channel_id LIKE 'youtube_%' THEN 'youtube'
				            WHEN channel_id LIKE 'instagram_%' THEN 'instagram'
				            ELSE 'other' END AS platform
				FROM download_queue
				WHERE ` + leaseEligibleSQL() + `
			),
			ranked AS (
				SELECT *, ROW_NUMBER() OVER (
					PARTITION BY platform
					ORDER BY priority DESC,
					         CASE WHEN video_id LIKE 'instagram_reel_%' THEN 0 ELSE 1 END,
					         added_at ASC
				) AS rn
				FROM eligible
			)
			SELECT video_id
			FROM ranked
			ORDER BY rn ASC, priority DESC,
			         CASE WHEN video_id LIKE 'instagram_reel_%' THEN 0 ELSE 1 END,
			         added_at ASC
			LIMIT ?
		`
		ids, err := claimLeasedIDs(tx, "download_queue", "video_id", query, []any{
			opts.NowMs, opts.StatusFrom, opts.NowMs, opts.StatusTo, opts.NowMs, opts.Limit,
		}, opts)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := tx.Exec(
				"UPDATE download_queue SET started_at=? WHERE video_id=?",
				opts.NowMs, id,
			); err != nil {
				return err
			}
			job, err := readDownloadQueueRowTx(tx, id)
			if err != nil {
				return err
			}
			claimed = append(claimed, job)
		}
		return nil
	})

	return claimed, err
}

func readDownloadQueueRowTx(tx *sql.Tx, videoID string) (DownloadQueueRow, error) {
	var r DownloadQueueRow
	err := tx.QueryRow(`
		SELECT video_id, channel_id, COALESCE(title,''), COALESCE(status,''),
		       COALESCE(priority,0), COALESCE(error,''), COALESCE(retry_count,0),
		       COALESCE(published_at_ms,0), COALESCE(last_error_kind,''),
		       COALESCE(last_error_strategy,''), COALESCE(lease_owner,''), COALESCE(lease_until_ms,0),
		       COALESCE(next_attempt_at_ms,0), COALESCE(tool,''),
		       COALESCE(cookie_label,'')
		FROM download_queue
		WHERE video_id=?
	`, videoID).Scan(
		&r.VideoID, &r.ChannelID, &r.Title, &r.Status,
		&r.Priority, &r.Error, &r.RetryCount,
		&r.PublishedAtMs, &r.LastErrorKind,
		&r.LastErrorStrategy, &r.LeaseOwner, &r.LeaseUntilMs,
		&r.NextAttemptAtMs, &r.Tool,
		&r.CookieLabel,
	)
	return r, err
}

// UpdateDownloadQueueStatus updates the status, error message, and retry count for a download job.
func (db *DB) UpdateDownloadQueueStatus(videoID, owner, status, errMsg string, retryCount int, errorKind, errorStrategy string, retryDelay time.Duration, nowMs int64) error {
	if nowMs == 0 {
		nowMs = time.Now().UnixMilli()
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		nextAttempt := int64(0)
		if status == "pending" && retryCount > 0 {
			if retryDelay <= 0 {
				retryDelay = jobRetryDelay(retryCount)
			}
			nextAttempt = nowMs + retryDelay.Milliseconds()
		}
		completedAt := int64(0)
		if status == "completed" || status == "failed" {
			completedAt = nowMs
		}
		if status == "completed" {
			errMsg = ""
			errorKind = ""
			errorStrategy = ""
		}
		res, err := tx.Exec(`
			UPDATE download_queue
			SET status=?,
			    error=?,
			    retry_count=?,
			    started_at=?,
			    completed_at=?,
			    next_attempt_at_ms=?,
			    last_error_kind=?,
			    last_error_strategy=?,
			    lease_owner='',
			    lease_until_ms=0
			WHERE video_id=?
			  AND status='processing'
			  AND lease_owner=?
		`, status, nilIfEmpty(errMsg), retryCount, nowMs, completedAt, nextAttempt, trimJobError(errorKind), trimJobError(errorStrategy), videoID, owner)
		if err != nil {
			return err
		}
		return requireQueueLeaseUpdate(res, "download_queue", videoID, owner)
	})
}

// RemoveFromDownloadQueue deletes a video from the download_queue.
func (db *DB) RemoveFromDownloadQueue(videoID, owner string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(
			"DELETE FROM download_queue WHERE video_id=? AND status='processing' AND lease_owner=?",
			videoID, owner,
		)
		if err != nil {
			return err
		}
		return requireQueueLeaseUpdate(res, "download_queue", videoID, owner)
	})
}

func (db *DB) RenewDownloadQueueLease(videoID, owner string, nowMs int64, lease time.Duration) error {
	if videoID == "" || owner == "" {
		return nil
	}
	if nowMs == 0 {
		nowMs = time.Now().UnixMilli()
	}
	if lease <= 0 {
		lease = defaultQueueLease
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			UPDATE download_queue
			   SET lease_until_ms=?
			 WHERE video_id=?
			   AND status='processing'
			   AND lease_owner=?
		`, nowMs+lease.Milliseconds(), videoID, owner)
		if err != nil {
			return err
		}
		return requireQueueLeaseUpdate(res, "download_queue", videoID, owner)
	})
}

// GetQueuedVideoCount returns the count of pending and processing entries for a channel.
func (db *DB) GetQueuedVideoCount(channelID string) (int, error) {
	var count int
	err := db.conn.QueryRow(`
		SELECT COUNT(*) FROM download_queue
		WHERE channel_id=? AND status IN ('pending','processing')
	`, channelID).Scan(&count)
	return count, err
}

// ResetStaleDownloadQueueItems resets processing items back to pending.
// Returns the number of rows updated.
func (db *DB) ResetStaleDownloadQueueItems() (int, error) {
	return db.ResetStaleDownloadQueueItemsAt(time.Now().UnixMilli())
}

func (db *DB) ResetStaleDownloadQueueItemsAt(nowMs int64) (int, error) {
	var affected int
	err := db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			UPDATE download_queue
			   SET status='pending',
			       lease_owner='',
			       lease_until_ms=0,
			       started_at=0
			 WHERE status='processing'
			   AND COALESCE(lease_until_ms,0) > 0
			   AND lease_until_ms <= ?
		`, nowMs)
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

// ClearFailedDownloadQueueItems resets failed items back to pending where retry_count < 5.
// Returns the number of rows updated.
func (db *DB) ClearFailedDownloadQueueItems() (int, error) {
	var affected int
	err := db.WithWrite(func(tx *sql.Tx) error {
		now := time.Now().UnixMilli()
		res, err := tx.Exec(`
			UPDATE download_queue
			   SET status='pending',
			       started_at=0,
			       lease_owner='',
			       lease_until_ms=0
			 WHERE status='failed'
			   AND retry_count < 5
			   AND next_attempt_at_ms > 0
			   AND next_attempt_at_ms <= ?
		`, now)
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

// ResetDownloadAuthFailuresForPlatform resets terminal auth failures after a
// platform's credential source changes, so rows failed by stale cookies can be
// retried with the new source.
func (db *DB) ResetDownloadAuthFailuresForPlatform(platform string) (int, error) {
	platform = strings.TrimSpace(platform)
	if platform == "" {
		return 0, nil
	}
	var affected int
	err := db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			UPDATE download_queue
			   SET status='pending',
			       error='',
			       retry_count=0,
			       started_at=0,
			       completed_at=0,
			       lease_owner='',
			       lease_until_ms=0,
			       next_attempt_at_ms=0,
			       last_error_kind='',
			       last_error_strategy='',
			       tool='',
			       cookie_label=''
			 WHERE status='failed'
			   AND last_error_kind='auth'
			   AND channel_id IN (
			       SELECT channel_id
			         FROM channels
			        WHERE platform=?
			   )
		`, platform)
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

// PruneDownloadQueue deletes pending entries for a channel whose video_id is not in allowedIDs.
// Returns the number of rows deleted.
func (db *DB) PruneDownloadQueue(channelID string, allowedIDs []string) (int, error) {
	if len(allowedIDs) == 0 {
		// If no allowed IDs, delete all pending for this channel
		var affected int
		err := db.WithWrite(func(tx *sql.Tx) error {
			res, err := tx.Exec(
				"DELETE FROM download_queue WHERE channel_id=? AND status='pending'",
				channelID,
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

	// Build NOT IN placeholders
	placeholders := make([]byte, 0, len(allowedIDs)*2)
	args := make([]any, 0, len(allowedIDs)+1)
	args = append(args, channelID)
	for i, id := range allowedIDs {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args = append(args, id)
	}

	var affected int
	err := db.WithWrite(func(tx *sql.Tx) error {
		query := "DELETE FROM download_queue WHERE channel_id=? AND status='pending' AND video_id NOT IN (" + string(placeholders) + ")"
		res, err := tx.Exec(query, args...)
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

// PruneSourceWindowDownloadQueue deletes pending jobs that belonged to a
// followed account's combined source window, including introduced rows queued
// under the original owner channel.
func (db *DB) PruneSourceWindowDownloadQueue(sourceChannelID string, allowedIDs []string) (int, error) {
	sourceChannelID = strings.TrimSpace(sourceChannelID)
	if sourceChannelID == "" {
		return 0, nil
	}
	args := []any{sourceChannelID, sourceChannelID}
	allowedClause := ""
	if len(allowedIDs) > 0 {
		allowedClause = " AND video_id NOT IN (" + placeholders(len(allowedIDs)) + ")"
		for _, id := range allowedIDs {
			args = append(args, id)
		}
	}
	var affected int
	err := db.WithWrite(func(tx *sql.Tx) error {
		query := `
			WITH source_queue(video_id) AS (
				SELECT dq.video_id
				FROM download_queue dq
				WHERE dq.channel_id = ?

				UNION
				SELECT dq.video_id
				FROM download_queue dq
				INNER JOIN video_repost_sources vrs ON vrs.video_id = dq.video_id
				WHERE vrs.reposter_channel_id = ?
			)
			DELETE FROM download_queue
			WHERE status = 'pending'
			  AND video_id IN (SELECT video_id FROM source_queue)
		` + allowedClause
		res, err := tx.Exec(query, args...)
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

// IsVideoDownloaded returns true if the video exists in the videos table with a non-empty file_path.
func (db *DB) IsVideoDownloaded(videoID string) (bool, error) {
	var exists int
	err := db.conn.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM videos
			WHERE video_id=? AND file_path IS NOT NULL AND file_path <> ''
		)
	`, videoID).Scan(&exists)
	return exists == 1, err
}

// ClearChannelChecked sets last_checked = 0 so the scheduler considers it due immediately.
func (db *DB) ClearChannelChecked(channelID string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE channels SET last_checked=0 WHERE channel_id=?", channelID)
		return err
	})
}

// ClearPlatformChecked sets last_checked = 0 for followed channels on a platform.
func (db *DB) ClearPlatformChecked(platform string) (int, error) {
	var affected int
	err := db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			UPDATE channels
			SET last_checked = 0
			WHERE platform = ?
			  AND channel_id IN (
			      SELECT channel_id
			      FROM channel_follows
			      WHERE user_id = ''
			  )
		`, platform)
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

// UpdateChannelChecked sets last_checked = CAST(strftime('%s','now') AS INTEGER) * 1000 for a channel.
func (db *DB) UpdateChannelChecked(channelID string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			"UPDATE channels SET last_checked=CAST(strftime('%s','now') AS INTEGER) * 1000 WHERE channel_id=?",
			channelID,
		)
		return err
	})
}

// GetExcessVideoIDs returns video IDs exceeding the limit for a channel.
// Excludes temp, pinned, and bookmarked. Returns oldest first.
func (db *DB) GetExcessVideoIDs(channelID string, limit int) ([]string, error) {
	repostProtection := ""
	includeTikTokSources := db.MomentsIncludeRepostsEnabled()
	includeInstagramSources := db.InstagramIncludeTaggedEnabled()
	if includeTikTokSources || includeInstagramSources {
		repostProtection = activeRepostProtectionClause("v", includeTikTokSources, includeInstagramSources)
	}
	query := `
		SELECT v.video_id FROM videos v
		WHERE v.channel_id = ?
		  AND COALESCE(v.is_temp, 0) = 0
		  AND COALESCE(v.is_pinned, 0) = 0
		  AND COALESCE(v.source_kind, '') != 'story'
		  AND v.video_id NOT IN (SELECT video_id FROM bookmarks)
		  ` + repostProtection + `
		ORDER BY v.published_at ASC, v.id ASC
		LIMIT MAX(0, (
			SELECT COUNT(*) FROM videos v
			WHERE v.channel_id = ?
			  AND COALESCE(v.is_temp, 0) = 0
			  AND COALESCE(v.is_pinned, 0) = 0
			  AND COALESCE(v.source_kind, '') != 'story'
			  AND v.video_id NOT IN (SELECT video_id FROM bookmarks)
			  ` + repostProtection + `
		) - ?)
	`
	rows, err := db.conn.Query(query, channelID, channelID, limit)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetSourceWindowPrunableVideoIDs returns downloaded videos associated with a
// source account that are no longer in that account's combined retained window.
func (db *DB) GetSourceWindowPrunableVideoIDs(sourceChannelID string, allowedIDs []string) ([]string, error) {
	sourceChannelID = strings.TrimSpace(sourceChannelID)
	if sourceChannelID == "" {
		return nil, nil
	}
	args := []any{sourceChannelID, sourceChannelID}
	allowedClause := ""
	if len(allowedIDs) > 0 {
		allowedClause = " AND v.video_id NOT IN (" + placeholders(len(allowedIDs)) + ")"
		for _, id := range allowedIDs {
			args = append(args, id)
		}
	}
	otherSourceProtection := ""
	includeTikTokSources := db.MomentsIncludeRepostsEnabled()
	includeInstagramSources := db.InstagramIncludeTaggedEnabled()
	if includeTikTokSources || includeInstagramSources {
		otherSourceProtection = `
		  AND NOT EXISTS (
		      SELECT 1
		      FROM video_repost_sources other
		      INNER JOIN channel_follows cf ON cf.channel_id = other.reposter_channel_id AND cf.user_id = ''
		      LEFT JOIN channel_settings cs ON cs.channel_id = other.reposter_channel_id
		      WHERE other.video_id = v.video_id
		        AND other.reposter_channel_id != ?
		        AND COALESCE(cs.include_reposts, 1) != 0
		        AND ` + sourceWindowPlatformEnabledClause("v", includeTikTokSources, includeInstagramSources) + `
		  )`
		args = append(args, sourceChannelID)
	}
	args = append(args, sourceChannelID)
	rows, err := db.conn.Query(`
		WITH candidate(video_id) AS (
			SELECT v.video_id
			FROM videos v
			WHERE v.channel_id = ?

			UNION
			SELECT v.video_id
			FROM videos v
			INNER JOIN video_repost_sources vrs ON vrs.video_id = v.video_id
			WHERE vrs.reposter_channel_id = ?
		)
		SELECT v.video_id
		FROM videos v
		INNER JOIN candidate c ON c.video_id = v.video_id
		WHERE COALESCE(v.is_temp, 0) = 0
		  AND COALESCE(v.is_pinned, 0) = 0
		  AND COALESCE(v.source_kind, '') != 'story'
		  AND v.video_id NOT IN (SELECT video_id FROM bookmarks)
		  AND v.video_id NOT IN (SELECT tweet_id FROM feed_likes)
		  `+allowedClause+`
		  `+otherSourceProtection+`
		  AND NOT EXISTS (
		      SELECT 1
		      FROM channel_follows owner_follow
		      WHERE owner_follow.channel_id = v.channel_id
		        AND owner_follow.user_id = ''
		        AND v.channel_id != ?
		  )
		ORDER BY COALESCE(v.published_at, 0) ASC, v.id ASC
	`, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func activeRepostProtectionClause(videoAlias string, includeTikTokSources, includeInstagramSources bool) string {
	if strings.TrimSpace(videoAlias) == "" {
		videoAlias = "v"
	}
	return `
		  AND NOT EXISTS (
		      SELECT 1
		      FROM video_repost_sources vrs
		      INNER JOIN channel_follows cf ON cf.channel_id = vrs.reposter_channel_id AND cf.user_id = ''
		      LEFT JOIN channel_settings cs ON cs.channel_id = vrs.reposter_channel_id
		      WHERE vrs.video_id = ` + videoAlias + `.video_id
		        AND COALESCE(cs.include_reposts, 1) != 0
		        AND ` + sourceWindowPlatformEnabledClause(videoAlias, includeTikTokSources, includeInstagramSources) + `
		  )`
}

// DeleteVideoWithFile deletes a video record and its associated files from disk.
func (db *DB) DeleteVideoWithFile(videoID, dataDir string) error {
	v, err := db.GetVideo(videoID)
	if err != nil || v == nil {
		return db.DeleteVideo(videoID)
	}
	if v.FilePath != "" {
		absPath := v.FilePath
		if !filepath.IsAbs(absPath) {
			absPath = filepath.Join(dataDir, absPath)
		}
		_ = os.Remove(absPath)
		// Remove sibling files (thumbnail, info.json).
		dir := filepath.Dir(absPath)
		base := strings.TrimSuffix(filepath.Base(absPath), filepath.Ext(absPath))
		if entries, err := os.ReadDir(dir); err == nil {
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), base) && e.Name() != filepath.Base(absPath) {
					_ = os.Remove(filepath.Join(dir, e.Name()))
				}
			}
		}
	}
	return db.DeleteVideo(videoID)
}

// GetPinnedVideos returns temp videos that are pinned (kept indefinitely),
// each annotated with its playback position + duration for a progress indicator.
func (db *DB) GetPinnedVideos() ([]model.Video, error) {
	return db.queryTempVideosByPin(true)
}

// GetCurrentlyAvailableVideos returns temp videos that will auto-expire at 24h,
// each annotated with its playback position + duration for a progress indicator.
func (db *DB) GetCurrentlyAvailableVideos() ([]model.Video, error) {
	return db.queryTempVideosByPin(false)
}

func (db *DB) queryTempVideosByPin(pinned bool) ([]model.Video, error) {
	flag := 0
	if pinned {
		flag = 1
	}
	rows, err := db.conn.Query(`
		SELECT v.video_id, v.title,
		       COALESCE(v.duration, 0),
		       COALESCE(wh.playback_position, 0)
		FROM videos v
		LEFT JOIN watch_history wh ON wh.video_id = v.video_id
		WHERE v.is_temp = 1 AND COALESCE(v.is_pinned, 0) = ?
		ORDER BY v.downloaded_at DESC
	`, flag)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	var videos []model.Video
	for rows.Next() {
		var v model.Video
		if err := rows.Scan(&v.VideoID, &v.Title, &v.Duration, &v.PlaybackPosition); err != nil {
			return nil, err
		}
		videos = append(videos, v)
	}
	return videos, rows.Err()
}

// GetCurrentlyWatchingVideos returns the most recently watched videos with
// in-progress watch history (not yet finished), ordered by last_watched DESC.
// Each video is annotated with playback position + duration.
func (db *DB) GetCurrentlyWatchingVideos(limit int) ([]model.Video, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := db.conn.Query(`
		SELECT v.video_id, v.title,
		       COALESCE(v.duration, 0),
		       wh.playback_position
		FROM watch_history wh
		JOIN videos v ON v.video_id = wh.video_id
		WHERE wh.playback_position > 0
		  AND (wh.duration = 0 OR wh.playback_position < wh.duration * 0.95)
		ORDER BY wh.last_watched DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	var videos []model.Video
	for rows.Next() {
		var v model.Video
		if err := rows.Scan(&v.VideoID, &v.Title, &v.Duration, &v.PlaybackPosition); err != nil {
			return nil, err
		}
		videos = append(videos, v)
	}
	return videos, rows.Err()
}

// GetTempVideos returns all temp video records.
func (db *DB) GetTempVideos() ([]model.Video, error) {
	rows, err := db.conn.Query(`
		SELECT video_id, COALESCE(file_path,''), COALESCE(is_pinned,0), downloaded_at
		FROM videos WHERE is_temp = 1
	`)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var videos []model.Video
	for rows.Next() {
		var v model.Video
		var downloadedAt sql.NullInt64
		if err := rows.Scan(&v.VideoID, &v.FilePath, &v.IsPinned, &downloadedAt); err != nil {
			return nil, err
		}
		if t := millisToTimePtr(downloadedAt); t != nil {
			v.DownloadedAt = *t
		}
		videos = append(videos, v)
	}
	return videos, rows.Err()
}

// Ensure time package is used.
var _ = time.Now
