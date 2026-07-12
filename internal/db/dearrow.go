package db

import (
	"database/sql"
	"fmt"
	"strings"
)

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
