package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
)

// InsertMediaFile inserts a single media file record, ignoring duplicates.
func (db *DB) InsertMediaFile(mf model.MediaFile) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			INSERT OR IGNORE INTO media_files
				(owner_type, owner_id, media_index, file_path, media_type, source_url, file_size)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, mf.OwnerType, mf.OwnerID, mf.MediaIndex, mf.FilePath,
			nilIfEmpty(mf.MediaType), nilIfEmpty(mf.SourceURL),
			nilIfZero(mf.FileSize),
		)
		return err
	})
}

// InsertMediaFileBatch inserts multiple media file records in a single transaction, ignoring duplicates.
func (db *DB) InsertMediaFileBatch(files []model.MediaFile) error {
	if len(files) == 0 {
		return nil
	}
	repairOwners := make([]string, 0, len(files))
	if err := db.WithWrite(func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(`
			INSERT OR IGNORE INTO media_files
				(owner_type, owner_id, media_index, file_path, media_type, source_url, file_size)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, mf := range files {
			if mf.OwnerType == "feed_media" && strings.TrimSpace(mf.OwnerID) != "" {
				repairOwners = append(repairOwners, mf.OwnerID)
			}
			if _, err := stmt.Exec(
				mf.OwnerType, mf.OwnerID, mf.MediaIndex, mf.FilePath,
				nilIfEmpty(mf.MediaType), nilIfEmpty(mf.SourceURL),
				nilIfZero(mf.FileSize),
			); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return db.repairVideoMediaShapesForIDs(repairOwners)
}

// GetMediaFilePath returns the file_path for a single media file by (ownerType, ownerID, index).
// Returns an error if not found.
func (db *DB) GetMediaFilePath(ownerType, ownerID string, index int) (string, error) {
	var path string
	err := db.conn.QueryRow(
		"SELECT file_path FROM media_files WHERE owner_type=? AND owner_id=? AND media_index=?",
		ownerType, ownerID, index,
	).Scan(&path)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("media file not found: %s/%s[%d]", ownerType, ownerID, index)
	}
	return path, err
}

// GetMediaFileVideoPath returns the file_path of the first video-typed media
// file for (ownerType, ownerID), ordered by media_index. Used by the stream
// endpoint when a tweet has mixed media (e.g. photo at index 0, video at index 1).
func (db *DB) GetMediaFileVideoPath(ownerType, ownerID string) (string, error) {
	var path string
	err := db.conn.QueryRow(
		`SELECT file_path FROM media_files
		 WHERE owner_type=? AND owner_id=? AND media_type IN ('video','gif')
		 ORDER BY media_index ASC LIMIT 1`,
		ownerType, ownerID,
	).Scan(&path)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("video media file not found: %s/%s", ownerType, ownerID)
	}
	return path, err
}

// GetMediaFileAudioPath returns the file_path of the first audio file for
// (ownerType, ownerID), ordered by media_index. Some slideshow downloaders mark
// the companion MP3 as media_type=video, so extension sniffing is authoritative.
func (db *DB) GetMediaFileAudioPath(ownerType, ownerID string) (string, error) {
	var path string
	err := db.conn.QueryRow(
		`SELECT file_path FROM media_files
		 WHERE owner_type = ? AND owner_id = ?
		   AND (
			 lower(file_path) LIKE '%.mp3'
			 OR lower(file_path) LIKE '%.m4a'
			 OR lower(file_path) LIKE '%.ogg'
			 OR lower(file_path) LIKE '%.aac'
			 OR lower(file_path) LIKE '%.wav'
		   )
		 ORDER BY media_index ASC, id ASC
		 LIMIT 1`,
		ownerType, ownerID,
	).Scan(&path)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("audio media file not found: %s/%s", ownerType, ownerID)
	}
	return path, err
}

// GetMediaFileCreatedAt returns the created_at timestamp for a media file entry.
func (db *DB) GetMediaFileCreatedAt(ownerType, ownerID string) (time.Time, error) {
	var ts time.Time
	err := db.conn.QueryRow(
		"SELECT created_at FROM media_files WHERE owner_type=? AND owner_id=? LIMIT 1",
		ownerType, ownerID,
	).Scan(&ts)
	return ts, err
}

// GetMediaFilesByOwnerType returns all media_files rows for a given owner_type.
func (db *DB) GetMediaFilesByOwnerType(ownerType string) ([]model.MediaFile, error) {
	rows, err := db.conn.Query(`
		SELECT owner_type, owner_id, media_index, file_path,
		       COALESCE(media_type,''), COALESCE(source_url,''), COALESCE(file_size,0)
		FROM media_files
		WHERE owner_type = ?
	`, ownerType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []model.MediaFile
	for rows.Next() {
		var mf model.MediaFile
		if err := rows.Scan(
			&mf.OwnerType, &mf.OwnerID, &mf.MediaIndex, &mf.FilePath,
			&mf.MediaType, &mf.SourceURL, &mf.FileSize,
		); err != nil {
			return nil, err
		}
		files = append(files, mf)
	}
	return files, rows.Err()
}

// MediaFilePathUpdate holds a new file_path for a media_files row identified by
// (owner_type, owner_id, media_index).
type MediaFilePathUpdate struct {
	OwnerType  string
	OwnerID    string
	MediaIndex int
	NewPath    string
}

// BatchUpdateMediaFilePaths updates the file_path for each entry in a single transaction.
func (db *DB) BatchUpdateMediaFilePaths(updates []MediaFilePathUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(`
			UPDATE media_files SET file_path=?
			WHERE owner_type=? AND owner_id=? AND media_index=?
		`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, u := range updates {
			if _, err := stmt.Exec(u.NewPath, u.OwnerType, u.OwnerID, u.MediaIndex); err != nil {
				return err
			}
		}
		return nil
	})
}

// MediaHealthStats reports media download completeness for a scope.
type MediaHealthStats struct {
	TotalPosts     int      `json:"total_posts"`
	MediaReady     int      `json:"media_ready"`
	MediaPending   int      `json:"media_pending"`
	MediaFailed    int      `json:"media_failed"`
	FailedTweetIDs []string `json:"failed_tweet_ids"`
}

// manifestScopeSubquery returns a SQL subquery for tweet IDs in the given scope,
// along with the args needed for the subquery.
func manifestScopeSubquery(scope, username string) (string, []any, error) {
	switch scope {
	case "subscriptions":
		return "SELECT tweet_id FROM feed_items", nil, nil
	case "liked":
		return "SELECT fl.tweet_id FROM feed_likes fl WHERE fl.username = ?", []any{username}, nil
	case "bookmarked":
		return "SELECT b.video_id FROM bookmarks b", nil, nil
	default:
		return "", nil, fmt.Errorf("unknown manifest scope: %s", scope)
	}
}

// GetMediaHealth returns download health stats for feed posts in the given scope.
func (db *DB) GetMediaHealth(scope, username string) (MediaHealthStats, error) {
	scopeSQL, scopeArgs, err := manifestScopeSubquery(scope, username)
	if err != nil {
		return MediaHealthStats{}, err
	}

	// Count posts that have media (media_json is not empty/null)
	totalQuery := fmt.Sprintf(`
		SELECT COUNT(*) FROM feed_items fi
		WHERE fi.media_json IS NOT NULL AND fi.media_json != '' AND fi.media_json != '[]'
		  AND fi.tweet_id IN (%s)
	`, scopeSQL)

	var stats MediaHealthStats
	if err := db.conn.QueryRow(totalQuery, scopeArgs...).Scan(&stats.TotalPosts); err != nil {
		return stats, fmt.Errorf("GetMediaHealth total: %w", err)
	}

	// Count completed jobs in scope
	readyQuery := fmt.Sprintf(`
		SELECT COUNT(*) FROM feed_media_jobs fmj
		WHERE fmj.status = 'completed'
		  AND fmj.tweet_id IN (%s)
	`, scopeSQL)
	if err := db.conn.QueryRow(readyQuery, scopeArgs...).Scan(&stats.MediaReady); err != nil {
		return stats, fmt.Errorf("GetMediaHealth ready: %w", err)
	}

	// Count pending (queued/processing) jobs in scope
	pendingQuery := fmt.Sprintf(`
		SELECT COUNT(*) FROM feed_media_jobs fmj
		WHERE fmj.status IN ('queued', 'processing')
		  AND fmj.tweet_id IN (%s)
	`, scopeSQL)
	if err := db.conn.QueryRow(pendingQuery, scopeArgs...).Scan(&stats.MediaPending); err != nil {
		return stats, fmt.Errorf("GetMediaHealth pending: %w", err)
	}

	// Count and list failed jobs in scope
	failedQuery := fmt.Sprintf(`
		SELECT fmj.tweet_id FROM feed_media_jobs fmj
		WHERE fmj.status = 'failed'
		  AND fmj.tweet_id IN (%s)
	`, scopeSQL)
	rows, err := db.conn.Query(failedQuery, scopeArgs...)
	if err != nil {
		return stats, fmt.Errorf("GetMediaHealth failed: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var tid string
		if err := rows.Scan(&tid); err != nil {
			return stats, err
		}
		stats.FailedTweetIDs = append(stats.FailedTweetIDs, tid)
	}
	stats.MediaFailed = len(stats.FailedTweetIDs)
	return stats, rows.Err()
}

// nilIfEmpty returns nil for empty strings, otherwise returns the string.
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nilIfZero returns nil for zero int64, otherwise returns the value.
func nilIfZero(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

// boolToInt converts a bool to 0 or 1.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// nilIfFalse returns 1 for true, nil for false (SQL NULL = inherit from global).
func nilIfFalse(b bool) any {
	if b {
		return 1
	}
	return nil
}
