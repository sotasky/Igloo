package db

import (
	"database/sql"

	"github.com/screwys/igloo/internal/model"
)

// IsVideoDownloaded returns true when the video has canonical ready media.
func (db *DB) IsVideoDownloaded(videoID string) (bool, error) {
	var exists int
	err := db.conn.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM videos v
			WHERE v.video_id = ? AND `+readyVideoMediaExistsSQL("v")+`
		)
	`, videoID).Scan(&exists)
	return exists == 1, err
}

func (db *DB) ClearChannelChecked(channelID string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE channels SET last_checked=0 WHERE channel_id=?", channelID)
		return err
	})
}

func (db *DB) ClearPlatformChecked(platform string) (int, error) {
	var affected int
	err := db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(`
			UPDATE channels
			SET last_checked = 0
			WHERE platform = ?
			  AND channel_id IN (SELECT channel_id FROM channel_follows)
		`, platform)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		affected = int(n)
		return err
	})
	return affected, err
}

func (db *DB) UpdateChannelChecked(channelID string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			"UPDATE channels SET last_checked=CAST(strftime('%s','now') AS INTEGER) * 1000 WHERE channel_id=?",
			channelID,
		)
		return err
	})
}

func (db *DB) DeleteVideoWithFile(videoID string) error {
	keys, err := db.DeleteVideoAssetsTx(videoID)
	if err != nil {
		return err
	}
	db.removeRetiredCanonicalFiles(keys, nil)
	return nil
}

func (db *DB) GetPinnedVideos() ([]model.Video, error) {
	return db.queryTempVideosByPin(true)
}

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
	defer func() { _ = rows.Close() }()
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
		  AND (COALESCE(wh.duration, 0) = 0 OR wh.playback_position < wh.duration * 0.95)
		ORDER BY wh.updated_at_ms DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
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
