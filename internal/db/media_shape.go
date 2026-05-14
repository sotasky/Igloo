package db

import (
	"database/sql"
	"log"
	"path/filepath"
	"slices"
	"strings"
)

type derivedMediaShape struct {
	Kind       string
	SlideCount int
	HasAudio   bool
}

func deriveMediaShapeFromPaths(paths []string) derivedMediaShape {
	imageCount := 0
	videoCount := 0
	hasAudio := false

	for _, path := range paths {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".jpg", ".jpeg", ".png", ".webp":
			imageCount++
		case ".mp3", ".m4a", ".ogg", ".aac", ".wav":
			hasAudio = true
		case ".mp4", ".webm", ".mkv", ".mov", ".m4v", ".gif":
			videoCount++
		}
	}

	switch {
	case imageCount > 1:
		return derivedMediaShape{Kind: "slideshow", SlideCount: imageCount, HasAudio: hasAudio}
	case imageCount == 1 && videoCount == 0:
		return derivedMediaShape{Kind: "image", SlideCount: 1, HasAudio: hasAudio}
	case videoCount > 0:
		return derivedMediaShape{Kind: "video", SlideCount: 0, HasAudio: hasAudio}
	default:
		return derivedMediaShape{}
	}
}

func (db *DB) deriveFeedMediaShape(ownerType, ownerID string) (derivedMediaShape, error) {
	rows, err := db.conn.Query(`
		SELECT COALESCE(file_path, '')
		FROM media_files
		WHERE owner_type = ? AND owner_id = ?
		ORDER BY media_index ASC, id ASC
	`, ownerType, ownerID)
	if err != nil {
		return derivedMediaShape{}, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return derivedMediaShape{}, err
		}
		if strings.TrimSpace(path) != "" {
			paths = append(paths, path)
		}
	}
	if err := rows.Err(); err != nil {
		return derivedMediaShape{}, err
	}
	return deriveMediaShapeFromPaths(paths), nil
}

func (db *DB) deriveBookmarkMediaShape(videoID string) (derivedMediaShape, error) {
	shape, err := db.deriveFeedMediaShape("feed_media", videoID)
	if err != nil {
		return derivedMediaShape{}, err
	}
	if shape.Kind != "" {
		return shape, nil
	}

	var quoteTweetID string
	err = db.conn.QueryRow(`
		SELECT COALESCE(quote_tweet_id, '')
		FROM feed_items
		WHERE tweet_id = ?
		LIMIT 1
	`, videoID).Scan(&quoteTweetID)
	if err != nil && err != sql.ErrNoRows {
		return derivedMediaShape{}, err
	}
	if strings.TrimSpace(quoteTweetID) == "" {
		return derivedMediaShape{}, nil
	}
	return db.deriveFeedMediaShape("quote_media", quoteTweetID)
}

func (db *DB) repairVideoMediaShapesForIDs(videoIDs []string) error {
	unique := make([]string, 0, len(videoIDs))
	seen := make(map[string]struct{}, len(videoIDs))
	for _, videoID := range videoIDs {
		videoID = strings.TrimSpace(videoID)
		if videoID == "" {
			continue
		}
		if _, ok := seen[videoID]; ok {
			continue
		}
		seen[videoID] = struct{}{}
		unique = append(unique, videoID)
	}
	if len(unique) == 0 {
		return nil
	}
	slices.Sort(unique)

	type repairRow struct {
		videoID    string
		mediaKind  string
		slideCount int
	}
	repairs := make([]repairRow, 0, len(unique))
	for _, videoID := range unique {
		shape, err := db.deriveFeedMediaShape("feed_media", videoID)
		if err != nil {
			return err
		}
		if shape.Kind == "" {
			continue
		}

		var currentKind string
		var currentSlideCount int
		err = db.conn.QueryRow(`
			SELECT COALESCE(media_kind, ''), COALESCE(slide_count, 0)
			FROM videos
			WHERE video_id = ?
		`, videoID).Scan(&currentKind, &currentSlideCount)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		if currentKind == shape.Kind && currentSlideCount == shape.SlideCount {
			continue
		}
		repairs = append(repairs, repairRow{
			videoID:    videoID,
			mediaKind:  shape.Kind,
			slideCount: shape.SlideCount,
		})
	}
	if len(repairs) == 0 {
		return nil
	}

	return db.WithWrite(func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(`
			UPDATE videos
			SET media_kind = ?, slide_count = ?, sync_seq = ?
			WHERE video_id = ?
		`)
		if err != nil {
			return err
		}
		defer func() {
			_ = stmt.Close()
		}()

		for _, repair := range repairs {
			if _, err := stmt.Exec(
				repair.mediaKind,
				repair.slideCount,
				db.NextSyncSeq(),
				repair.videoID,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func (db *DB) RepairVideoMediaShapes() error {
	rows, err := db.conn.Query(`
		SELECT video_id
		FROM videos
		WHERE EXISTS (
			SELECT 1
			FROM media_files mf
			WHERE mf.owner_type = 'feed_media' AND mf.owner_id = videos.video_id
		)
		  AND (
			COALESCE(media_kind, '') = ''
			OR COALESCE(slide_count, 0) = 0
			OR COALESCE(media_kind, '') = 'video'
		  )
	`)
	if err != nil {
		return err
	}
	defer func() {
		_ = rows.Close()
	}()

	var videoIDs []string
	for rows.Next() {
		var videoID string
		if err := rows.Scan(&videoID); err != nil {
			return err
		}
		videoIDs = append(videoIDs, videoID)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return db.repairVideoMediaShapesForIDs(videoIDs)
}

func (db *DB) RepairVideoMediaShapesOnce() error {
	return db.runStartupMigrationOnce(
		"repair_video_media_shapes",
		db.RepairVideoMediaShapes,
		db.warnVideoMediaShapesNeedRepair,
	)
}

func (db *DB) warnVideoMediaShapesNeedRepair() error {
	var count int
	if err := db.conn.QueryRow(`
		SELECT COUNT(*)
		FROM videos
		WHERE EXISTS (
			SELECT 1
			FROM media_files mf
			WHERE mf.owner_type = 'feed_media' AND mf.owner_id = videos.video_id
		)
		  AND (
			media_kind IS NULL
			OR media_kind = ''
			OR (media_kind IN ('image', 'slideshow') AND (slide_count IS NULL OR slide_count = 0))
		  )
	`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		log.Printf("schema migration repair_video_media_shapes already applied, but %d videos still match the repair condition; leaving them for investigation", count)
	}
	return nil
}
