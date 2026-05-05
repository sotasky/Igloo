package db

import "database/sql"

// dayMsDearrow is the millisecond duration used by the retry/recency windows.
// Package-scoped so tests can reference the same value and stay in sync.
const dayMsDearrow int64 = 86_400_000

// MarkDearrowChecked records that a DeArrow lookup was attempted for the given
// video without finding usable data. Used by the worker for retry accounting.
// Bumps videos.sync_seq so Android's videos delta re-emits the row.
func (db *DB) MarkDearrowChecked(videoID string, atMs int64) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			`UPDATE videos SET dearrow_checked_at = ?, sync_seq = ? WHERE video_id = ?`,
			atMs, db.NextSyncSeq(), videoID,
		)
		return err
	})
}

// SetDearrowData writes DeArrow branding results for the given video. Nil
// pointer arguments clear the corresponding column (models "original won the
// vote, don't override" semantics). Bumps videos.sync_seq so Android's videos
// delta re-emits the row with the new branding fields.
func (db *DB) SetDearrowData(videoID string, title, titleCasual, thumbPath *string, atMs int64) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			UPDATE videos
			SET dearrow_title = ?,
			    dearrow_title_casual = ?,
			    dearrow_thumb_path = ?,
			    dearrow_checked_at = ?,
			    sync_seq = ?
			WHERE video_id = ?`,
			title, titleCasual, thumbPath, atMs, db.NextSyncSeq(), videoID,
		)
		return err
	})
}

// ListVideosNeedingDearrow returns YouTube video IDs that need a DeArrow check.
// Same criteria as ListVideosNeedingYoutubeEnrichment's DeArrow branch; kept
// as a focused helper for the one-shot backfill script and DeArrow tests.
func (db *DB) ListVideosNeedingDearrow(nowMs int64, limit int) ([]string, error) {
	rows, err := db.conn.Query(`
		SELECT v.video_id
		FROM videos v
		JOIN channels c ON v.channel_id = c.channel_id
		WHERE c.platform = 'youtube'
		  AND (
		        v.dearrow_checked_at IS NULL
		     OR (
		           v.dearrow_title IS NULL
		       AND v.dearrow_title_casual IS NULL
		       AND v.dearrow_thumb_path IS NULL
		       AND v.published_at > ?
		       AND v.dearrow_checked_at < ?
		            )
		      )
		LIMIT ?`,
		nowMs-7*dayMsDearrow, nowMs-dayMsDearrow, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// YoutubeEnrichTask describes one video that needs an API-side catch-up on
// DeArrow branding and/or SponsorBlock segments. The worker uses this to
// co-fetch both against sponsor.ajay.app under a single rate limit.
type YoutubeEnrichTask struct {
	VideoID       string
	FilePath      string
	PublishedAtMs int64
	NeedsDearrow  bool
	NeedsSB       bool
}

// ListVideosNeedingYoutubeEnrichment returns YouTube videos that need a
// DeArrow check, a SponsorBlock check, or both.
//
// DeArrow criteria (matches legacy ListVideosNeedingDearrow):
//   - never checked, OR
//   - checked >1d ago, no data, published within the last 7d (still relevant).
//
// SponsorBlock criteria (matches web.sbShouldFetch):
//   - never checked, OR
//   - age=="young" at last check AND checked >24h ago.
func (db *DB) ListVideosNeedingYoutubeEnrichment(nowMs int64, limit int) ([]YoutubeEnrichTask, error) {
	rows, err := db.conn.Query(`
		SELECT v.video_id,
		       COALESCE(v.file_path, ''),
		       COALESCE(v.published_at, 0),
		       CASE WHEN v.dearrow_checked_at IS NULL
		              OR (v.dearrow_title IS NULL
		                  AND v.dearrow_title_casual IS NULL
		                  AND v.dearrow_thumb_path IS NULL
		                  AND v.published_at > ?
		                  AND v.dearrow_checked_at < ?)
		            THEN 1 ELSE 0 END AS needs_dearrow,
		       CASE WHEN sbc.video_id IS NULL
		              OR (sbc.video_age_at_check = 'young'
		                  AND sbc.checked_at < ?)
		            THEN 1 ELSE 0 END AS needs_sb
		FROM videos v
		JOIN channels c ON v.channel_id = c.channel_id
		LEFT JOIN sponsorblock_checked sbc ON sbc.video_id = v.video_id
		WHERE c.platform = 'youtube'
		  AND (
		         v.dearrow_checked_at IS NULL
		      OR (v.dearrow_title IS NULL
		          AND v.dearrow_title_casual IS NULL
		          AND v.dearrow_thumb_path IS NULL
		          AND v.published_at > ?
		          AND v.dearrow_checked_at < ?)
		      OR sbc.video_id IS NULL
		      OR (sbc.video_age_at_check = 'young' AND sbc.checked_at < ?)
		  )
		LIMIT ?`,
		nowMs-7*dayMsDearrow, nowMs-dayMsDearrow, nowMs-dayMsDearrow,
		nowMs-7*dayMsDearrow, nowMs-dayMsDearrow, nowMs-dayMsDearrow, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []YoutubeEnrichTask
	for rows.Next() {
		var t YoutubeEnrichTask
		var needsDA, needsSB int
		if err := rows.Scan(&t.VideoID, &t.FilePath, &t.PublishedAtMs, &needsDA, &needsSB); err != nil {
			return nil, err
		}
		t.NeedsDearrow = needsDA != 0
		t.NeedsSB = needsSB != 0
		out = append(out, t)
	}
	return out, rows.Err()
}
