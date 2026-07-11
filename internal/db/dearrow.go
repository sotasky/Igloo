package db

import (
	"database/sql"
	"fmt"
	"strings"
)

// dayMsDearrow is the millisecond duration used by the retry/recency windows.
// Package-scoped so tests can reference the same value and stay in sync.
const dayMsDearrow int64 = 86_400_000

// MarkDearrowChecked records that a DeArrow lookup was attempted for the given
// video without finding usable data. Used by the worker for retry accounting.
func (db *DB) MarkDearrowChecked(videoID string, atMs int64) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			`UPDATE videos SET dearrow_checked_at = ? WHERE video_id = ?`,
			atMs, videoID,
		)
		return err
	})
}

// SetDearrowTitles records a partial branding result without changing the
// currently published thumbnail asset.
func (db *DB) SetDearrowTitles(videoID string, title, titleCasual *string, atMs int64) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		result, err := tx.Exec(`
			UPDATE videos
			SET dearrow_title = COALESCE(?, dearrow_title),
			    dearrow_title_casual = COALESCE(?, dearrow_title_casual),
			    dearrow_checked_at = ?
			WHERE video_id = ?
		`, title, titleCasual, atMs, videoID)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed == 0 {
			return fmt.Errorf("video not found: %s", videoID)
		}
		return nil
	})
}

// SetDearrowData writes DeArrow titles and atomically replaces the canonical
// thumbnail asset. Nil title pointers clear those title values; a nil thumbnail
// removes the published DeArrow asset.
func (db *DB) SetDearrowData(videoID string, title, titleCasual, thumbPath *string, atMs int64) error {
	videoID = strings.TrimSpace(videoID)
	if videoID == "" {
		return fmt.Errorf("video id is empty")
	}
	var ownerKind string
	if err := db.conn.QueryRow(`SELECT owner_kind FROM videos WHERE video_id = ?`, videoID).Scan(&ownerKind); err != nil {
		return err
	}
	platform, ok := videoPlatformForOwnerKind(ownerKind)
	if !ok || ownerKind == "tweet" {
		return fmt.Errorf("video %s has invalid non-X owner kind %q", videoID, ownerKind)
	}

	var prepared []Asset
	var keep map[string]struct{}
	if thumbPath != nil && strings.TrimSpace(*thumbPath) == "" {
		thumbPath = nil
	}
	if thumbPath != nil {
		key := strings.TrimSpace(*thumbPath)
		thumbPath = &key
		asset := Asset{
			AssetID:        BuildAssetID(platform, ownerKind, videoID, "dearrow_thumbnail", 0),
			AssetKind:      "dearrow_thumbnail",
			OwnerKind:      ownerKind,
			OwnerID:        videoID,
			FilePath:       key,
			ContentType:    "image/jpeg",
			State:          AssetStateReady,
			RequiredReason: "retention",
		}
		ready, err := db.prepareReadyAssetMetadata(asset, atMs)
		if err != nil {
			return fmt.Errorf("fingerprint DeArrow thumbnail %s: %w", videoID, err)
		}
		ready = normalizeAsset(ready, atMs)
		prepared = []Asset{ready}
		keep = map[string]struct{}{key: {}}
	}

	var retired []string
	err := db.WithWrite(func(tx *sql.Tx) error {
		result, err := tx.Exec(`
			UPDATE videos
			SET dearrow_title = ?,
			    dearrow_title_casual = ?,
			    dearrow_checked_at = ?
			WHERE video_id = ?`,
			title, titleCasual, atMs, videoID,
		)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed == 0 {
			return fmt.Errorf("video not found: %s", videoID)
		}
		retired, err = replaceVideoAssetsTx(
			tx, ownerKind, videoID, []string{"dearrow_thumbnail"}, prepared, "",
		)
		return err
	})
	if err != nil {
		return err
	}
	db.removeRetiredCanonicalFiles(retired, keep)
	return nil
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
		       AND NOT EXISTS (
		             SELECT 1 FROM assets da
		             WHERE da.owner_id = v.video_id
		               AND da.owner_kind = 'youtube_video'
		               AND da.asset_kind = 'dearrow_thumbnail'
		               AND da.state = 'ready' AND da.file_path != ''
		           )
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
	defer func() {
		_ = rows.Close()
	}()
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
		       COALESCE((
		         SELECT stream.file_path
		         FROM assets stream
		         WHERE stream.owner_id = v.video_id
		           AND stream.owner_kind = 'youtube_video'
		           AND stream.asset_kind = 'video_stream'
		           AND stream.media_index = 0
		           AND stream.state = 'ready'
		         LIMIT 1
		       ), ''),
		       COALESCE(v.published_at, 0),
		       CASE WHEN v.dearrow_checked_at IS NULL
		              OR (v.dearrow_title IS NULL
		                  AND v.dearrow_title_casual IS NULL
		                  AND NOT EXISTS (
		                        SELECT 1 FROM assets da
		                        WHERE da.owner_id = v.video_id
		                          AND da.owner_kind = 'youtube_video'
		                          AND da.asset_kind = 'dearrow_thumbnail'
		                          AND da.state = 'ready' AND da.file_path != ''
		                      )
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
		          AND NOT EXISTS (
		                SELECT 1 FROM assets da
		                WHERE da.owner_id = v.video_id
		                  AND da.owner_kind = 'youtube_video'
		                  AND da.asset_kind = 'dearrow_thumbnail'
		                  AND da.state = 'ready' AND da.file_path != ''
		              )
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
	defer func() {
		_ = rows.Close()
	}()
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
