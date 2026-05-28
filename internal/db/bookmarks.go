package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
)

// IsBookmarked checks if a video is bookmarked by a user.
// Returns (bookmarked, categoryID, error).
func (db *DB) IsBookmarked(videoID, userID string) (bool, int64, error) {
	var categoryID int64
	err := db.conn.QueryRow(
		"SELECT category_id FROM bookmarks WHERE video_id = ? AND user_id = ?",
		videoID, userID,
	).Scan(&categoryID)
	if err == sql.ErrNoRows {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	return true, categoryID, nil
}

// BookmarkCategoryRow represents a user's bookmark folder with count.
type BookmarkCategoryRow struct {
	ID            int64
	Name          string
	ArchivePath   string
	CreatedAtMs   int64
	BookmarkCount int
}

// BookmarkLabelCountRow represents one bookmark label filter option.
type BookmarkLabelCountRow struct {
	Label         string
	IsNoLabel     bool
	BookmarkCount int
}

// GetBookmarkCategories returns all categories for a user with bookmark counts.
func (db *DB) GetBookmarkCategories(userID string) ([]BookmarkCategoryRow, error) {
	rows, err := db.conn.Query(`
		SELECT bc.id, bc.name, COALESCE(bc.archive_path,''), COALESCE(bc.created_at, 0),
		       COUNT(b.video_id) AS bookmark_count
		FROM bookmark_categories bc
		LEFT JOIN bookmarks b ON bc.id = b.category_id AND b.user_id = ?
		WHERE bc.user_id = ?
		GROUP BY bc.id
		ORDER BY bc.id
	`, userID, userID)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var categories []BookmarkCategoryRow
	for rows.Next() {
		var c BookmarkCategoryRow
		if err := rows.Scan(&c.ID, &c.Name, &c.ArchivePath, &c.CreatedAtMs, &c.BookmarkCount); err != nil {
			return nil, err
		}
		categories = append(categories, c)
	}
	return categories, rows.Err()
}

// BookmarkLabelFilterMode controls label filtering for bookmark list queries.
type BookmarkLabelFilterMode int

const (
	BookmarkLabelFilterNone BookmarkLabelFilterMode = iota
	BookmarkLabelFilterExact
	BookmarkLabelFilterNoLabel
)

// GetBookmarksOpts holds filter options for bookmark queries.
type GetBookmarksOpts struct {
	CategoryID      int64
	LabelFilterMode BookmarkLabelFilterMode
	Label           string
	UserID          string
	Limit           int
	Offset          int
}

// GetBookmarks returns bookmarked videos with full metadata, newest first.
func (db *DB) GetBookmarks(opts GetBookmarksOpts) ([]model.Video, error) {
	if opts.Limit <= 0 {
		opts.Limit = 10000
	}

	var where []string
	var args []any

	where = append(where, "b.user_id = ?")
	args = append(args, opts.UserID)

	where, args = appendBookmarkFilterWhere(where, args, opts, "b.")

	whereClause := "WHERE " + strings.Join(where, " AND ")

	query := fmt.Sprintf(`
		SELECT v.video_id, v.channel_id,
		       CASE WHEN v.title LIKE 'X post %%' THEN COALESCE(fi.body_text,
		           (SELECT fp.quote_body_text
		            FROM feed_items fp
		            WHERE fp.quote_tweet_id = v.video_id
		              AND fp.quote_tweet_id IS NOT NULL
		              AND fp.quote_tweet_id != ''
		            LIMIT 1),
		           '') ELSE v.title END,
		       COALESCE(v.description,''),
		       v.duration, COALESCE(v.thumbnail_path,''), COALESCE(v.file_path,''),
		       COALESCE(v.file_size,0), COALESCE(NULLIF(v.published_at, 0), fi.published_at,
		           (SELECT COALESCE(fp.quote_published_at, fp.published_at)
		            FROM feed_items fp
		            WHERE fp.quote_tweet_id = v.video_id
		              AND fp.quote_tweet_id IS NOT NULL
		              AND fp.quote_tweet_id != ''
		            LIMIT 1)
		       ), v.downloaded_at,
		       COALESCE(v.watched,0), COALESCE(v.is_temp,0), COALESCE(v.is_pinned,0),
		       COALESCE(v.metadata_json,''),
		       COALESCE(c.name,''),
		       COALESCE(c.platform,
		           CASE
		               WHEN v.channel_id LIKE 'twitter_%%' THEN 'twitter'
		               WHEN v.channel_id LIKE 'x_%%' THEN 'twitter'
		               WHEN v.channel_id LIKE 'tiktok_%%' THEN 'tiktok'
		               WHEN v.channel_id LIKE 'instagram_%%' THEN 'instagram'
		               ELSE 'youtube'
		           END),
		       CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END,
		       b.category_id,
		       COALESCE((
		           SELECT COUNT(DISTINCT mf.media_index)
		           FROM media_files mf
		           WHERE mf.owner_type = 'feed_media'
		             AND mf.owner_id = v.video_id
		       ), 0),
		       COALESCE((
		           SELECT mf.media_type
		           FROM media_files mf
		           WHERE mf.owner_type = 'feed_media'
		             AND mf.owner_id = v.video_id
		           ORDER BY mf.media_index
		           LIMIT 1
		       ), ''),
		       COALESCE((
		           SELECT COUNT(DISTINCT mf.media_index)
		           FROM feed_items fi2
		           JOIN media_files mf ON mf.owner_type = 'quote_media' AND mf.owner_id = fi2.quote_tweet_id
		           WHERE fi2.tweet_id = v.video_id
		             AND fi2.quote_tweet_id IS NOT NULL
		             AND fi2.quote_tweet_id != ''
		       ), 0),
		       COALESCE((
		           SELECT mf.media_type
		           FROM feed_items fi2
		           JOIN media_files mf ON mf.owner_type = 'quote_media' AND mf.owner_id = fi2.quote_tweet_id
		           WHERE fi2.tweet_id = v.video_id
		             AND fi2.quote_tweet_id IS NOT NULL
		             AND fi2.quote_tweet_id != ''
		           ORDER BY mf.media_index
		           LIMIT 1
		       ), '')
		FROM bookmarks b
		JOIN videos v ON b.video_id = v.video_id
		LEFT JOIN channels c ON v.channel_id = c.channel_id
		LEFT JOIN channel_follows cf ON cf.channel_id = v.channel_id AND cf.user_id = ''
		LEFT JOIN feed_items fi ON fi.tweet_id = v.video_id
		%s
		ORDER BY b.bookmarked_at DESC
		LIMIT ? OFFSET ?
	`, whereClause)
	args = append(args, opts.Limit, opts.Offset)

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var videos []model.Video
	for rows.Next() {
		var v model.Video
		var pubAt, dlAt sql.NullInt64
		var catID int64
		var directMediaFileCount, quoteMediaFileCount int
		var directFirstMediaType, quoteFirstMediaType string
		err := rows.Scan(
			&v.VideoID, &v.ChannelID, &v.Title, &v.Description,
			&v.Duration, &v.ThumbnailPath, &v.FilePath,
			&v.FileSize, &pubAt, &dlAt,
			&v.Watched, &v.IsTemp, &v.IsPinned,
			&v.MetadataJSON,
			&v.ChannelName, &v.Platform, &v.IsSubscribed,
			&catID,
			&directMediaFileCount, &directFirstMediaType,
			&quoteMediaFileCount, &quoteFirstMediaType,
		)
		if err != nil {
			return nil, err
		}
		v.PublishedAt = millisToTimePtr(pubAt)
		if t := millisToTimePtr(dlAt); t != nil {
			v.DownloadedAt = *t
		}
		v.BookmarkCategoryID = &catID
		v.Platform = detectPlatform(v.Platform, "")
		db.resolveVideoPaths(&v)
		v.EnrichForCard()

		mediaFileCount := directMediaFileCount
		firstMediaType := directFirstMediaType
		if mediaFileCount == 0 {
			mediaFileCount = quoteMediaFileCount
			firstMediaType = quoteFirstMediaType
		}

		// Bookmark stubs often lack metadata_json/file_path, and media_type can be
		// misleading for slideshow sidecars (for example TikTok MP3s marked "video").
		if mediaFileCount > 0 && (v.FilePath == "" || v.MetadataJSON == "" || v.MediaKind == "" || v.MediaSlideCount == 0) {
			if shape, err := db.deriveBookmarkMediaShape(v.VideoID); err == nil && shape.Kind != "" {
				v.MediaKind = shape.Kind
				v.MediaSlideCount = shape.SlideCount
				v.MediaTypes = shape.MediaTypes
			} else {
				switch firstMediaType {
				case "photo":
					if mediaFileCount > 1 {
						v.MediaKind = "slideshow"
						v.MediaSlideCount = mediaFileCount
					} else {
						v.MediaKind = "image"
						v.MediaSlideCount = 1
					}
				case "video", "gif":
					v.MediaKind = "video"
				}
			}
		}

		videos = append(videos, v)
	}
	return videos, rows.Err()
}

// GetQuoteTweetMediaPath returns the file_path of the first media file for a
// video's quote tweet (looked up via feed_items.quote_tweet_id).
func (db *DB) GetQuoteTweetMediaPath(videoID string) (string, error) {
	var path string
	err := db.conn.QueryRow(`
		SELECT mf.file_path FROM feed_items fi
		JOIN media_files mf ON mf.owner_id = fi.quote_tweet_id
		WHERE fi.tweet_id = ? AND fi.quote_tweet_id IS NOT NULL
		ORDER BY mf.media_index LIMIT 1
	`, videoID).Scan(&path)
	if err != nil {
		return "", err
	}
	return path, nil
}

// GetBookmarkCount returns total bookmarks matching the filter.
func (db *DB) GetBookmarkCount(opts GetBookmarksOpts) (int, error) {
	var where []string
	var args []any

	where = append(where, "user_id = ?")
	args = append(args, opts.UserID)

	where, args = appendBookmarkFilterWhere(where, args, opts, "")

	var count int
	err := db.conn.QueryRow(
		"SELECT COUNT(*) FROM bookmarks WHERE "+strings.Join(where, " AND "),
		args...,
	).Scan(&count)
	return count, err
}

func appendBookmarkFilterWhere(where []string, args []any, opts GetBookmarksOpts, prefix string) ([]string, []any) {
	switch opts.LabelFilterMode {
	case BookmarkLabelFilterExact:
		label := strings.TrimSpace(opts.Label)
		if label == "" {
			where = append(where, "NULLIF(TRIM(COALESCE("+prefix+"custom_title, '')), '') IS NULL")
			return where, args
		}
		where = append(where, "TRIM(COALESCE("+prefix+"custom_title, '')) = ?")
		args = append(args, label)
	case BookmarkLabelFilterNoLabel:
		where = append(where, "NULLIF(TRIM(COALESCE("+prefix+"custom_title, '')), '') IS NULL")
	default:
		if opts.CategoryID > 0 {
			where = append(where, prefix+"category_id = ?")
			args = append(args, opts.CategoryID)
		}
	}
	return where, args
}

// GetBookmarkLabelCounts returns bookmark label filters ordered by frequency.
func (db *DB) GetBookmarkLabelCounts(userID string) ([]BookmarkLabelCountRow, error) {
	rows, err := db.conn.Query(`
		SELECT label, COUNT(*) AS bookmark_count
		FROM (
			SELECT
				CASE
					WHEN NULLIF(TRIM(COALESCE(custom_title, '')), '') IS NULL THEN ''
					ELSE TRIM(custom_title)
				END AS label
			FROM bookmarks
			WHERE user_id = ?
		)
		GROUP BY label
		ORDER BY bookmark_count DESC, LOWER(label) ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var labels []BookmarkLabelCountRow
	for rows.Next() {
		var row BookmarkLabelCountRow
		if err := rows.Scan(&row.Label, &row.BookmarkCount); err != nil {
			return nil, err
		}
		row.IsNoLabel = row.Label == ""
		labels = append(labels, row)
	}
	if labels == nil {
		labels = []BookmarkLabelCountRow{}
	}
	return labels, rows.Err()
}

// AddBookmark creates or updates a bookmark.
func (db *DB) AddBookmark(userID, videoID string, categoryID int64, customTitle, accountHandles, mediaIndices string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		// Ensure a videos row exists for this bookmark. GetBookmarks inner-joins
		// videos, so mobile and web bookmark writes share the same scoped stub
		// materialization as the startup repair.
		if err := db.ensureBookmarkTargetStubsTx(tx, videoID); err != nil {
			return err
		}

		_, err := tx.Exec(`
			INSERT INTO bookmarks (user_id, video_id, category_id, custom_title, account_handles, media_indices, bookmarked_at)
			VALUES (?, ?, ?, ?, ?, ?, CAST(strftime('%s','now') AS INTEGER) * 1000)
			ON CONFLICT(user_id, video_id) DO UPDATE SET
				category_id = excluded.category_id,
				custom_title = COALESCE(NULLIF(excluded.custom_title,''), bookmarks.custom_title),
				account_handles = COALESCE(NULLIF(excluded.account_handles,''), bookmarks.account_handles),
				media_indices = COALESCE(NULLIF(excluded.media_indices,''), bookmarks.media_indices),
				bookmarked_at = CAST(strftime('%s','now') AS INTEGER) * 1000
		`, userID, videoID, categoryID, customTitle, accountHandles, mediaIndices)
		if err != nil {
			return err
		}
		if err := db.bumpBookmarkTargetSyncSeqTx(tx, videoID); err != nil {
			return err
		}
		return db.recordBookmarkCurrentSyncChangeTx(tx, userID, videoID)
	})
}

// RemoveBookmark deletes a bookmark.
func (db *DB) RemoveBookmark(userID, videoID string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec("DELETE FROM bookmarks WHERE user_id = ? AND video_id = ?", userID, videoID)
		if err != nil {
			return err
		}
		if err := db.bumpBookmarkTargetSyncSeqTx(tx, videoID); err != nil {
			return err
		}
		return db.recordSyncChangeTx(tx, "bookmark", videoID, `{"bookmarked":false}`)
	})
}

// CreateBookmarkCategory creates a new bookmark category, returns its ID.
func (db *DB) CreateBookmarkCategory(userID, name, archivePath string) (int64, error) {
	var id int64
	createdAt := time.Now().UnixMilli()
	err := db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(
			"INSERT INTO bookmark_categories (user_id, name, archive_path, created_at) VALUES (?, ?, ?, ?)",
			userID, name, archivePath, createdAt,
		)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		if err != nil {
			return err
		}
		return db.recordBookmarkCategorySyncChangeTx(tx, userID, "set", id, name, archivePath, createdAt)
	})
	return id, err
}

// DeleteBookmarkCategory deletes a category and moves its bookmarks to uncategorized.
func (db *DB) DeleteBookmarkCategory(userID string, categoryID int64) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE bookmarks SET category_id = 0 WHERE user_id = ? AND category_id = ?", userID, categoryID)
		if err != nil {
			return err
		}
		_, err = tx.Exec("DELETE FROM bookmark_categories WHERE id = ? AND user_id = ?", categoryID, userID)
		if err != nil {
			return err
		}
		return db.recordBookmarkCategorySyncChangeTx(tx, userID, "clear", categoryID, "", "", 0)
	})
}

// UpdateBookmarkCategory updates a category's name and/or archive path.
func (db *DB) UpdateBookmarkCategory(userID string, categoryID int64, name, archivePath string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			"UPDATE bookmark_categories SET name = ?, archive_path = ? WHERE id = ? AND user_id = ?",
			name, archivePath, categoryID, userID,
		)
		if err != nil {
			return err
		}
		var createdAt int64
		if err := tx.QueryRow(
			"SELECT COALESCE(created_at, 0) FROM bookmark_categories WHERE id = ? AND user_id = ?",
			categoryID, userID,
		).Scan(&createdAt); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return err
		}
		return db.recordBookmarkCategorySyncChangeTx(tx, userID, "set", categoryID, name, archivePath, createdAt)
	})
}

// GetBookmarkedHandles returns all unique account handles from bookmarks for a user.
func (db *DB) GetBookmarkedHandles(userID string) ([]string, error) {
	rows, err := db.conn.Query(
		"SELECT account_handles FROM bookmarks WHERE user_id = ? AND account_handles IS NOT NULL AND account_handles != ''",
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	seen := map[string]bool{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		var handles []string
		if err := json.Unmarshal([]byte(raw), &handles); err != nil {
			continue
		}
		for _, h := range handles {
			h = strings.TrimSpace(strings.ToLower(h))
			if h != "" {
				seen[h] = true
			}
		}
	}
	result := make([]string, 0, len(seen))
	for h := range seen {
		result = append(result, h)
	}
	return result, nil
}

// GetBookmarkLabels returns distinct custom_title values for autocomplete.
func (db *DB) GetBookmarkLabels(userID, categoryID string) ([]string, error) {
	var rows *sql.Rows
	var err error
	if categoryID != "" {
		rows, err = db.conn.Query(
			"SELECT DISTINCT TRIM(custom_title) FROM bookmarks WHERE user_id = ? AND category_id = ? AND NULLIF(TRIM(COALESCE(custom_title, '')), '') IS NOT NULL ORDER BY LOWER(TRIM(custom_title))",
			userID, categoryID,
		)
	} else {
		rows, err = db.conn.Query(
			"SELECT DISTINCT TRIM(custom_title) FROM bookmarks WHERE user_id = ? AND NULLIF(TRIM(COALESCE(custom_title, '')), '') IS NOT NULL ORDER BY LOWER(TRIM(custom_title))",
			userID,
		)
	}
	if err != nil {
		return []string{}, err
	}
	defer func() {
		_ = rows.Close()
	}()
	var labels []string
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err == nil {
			labels = append(labels, label)
		}
	}
	if labels == nil {
		labels = []string{}
	}
	return labels, nil
}

// ClearBookmarkLabel removes a custom_title from all bookmarks that use it.
func (db *DB) ClearBookmarkLabel(userID, label string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		rows, err := tx.Query(
			"SELECT video_id FROM bookmarks WHERE user_id = ? AND custom_title = ? ORDER BY video_id",
			userID, label,
		)
		if err != nil {
			return err
		}
		var videoIDs []string
		for rows.Next() {
			var videoID string
			if err := rows.Scan(&videoID); err != nil {
				_ = rows.Close()
				return err
			}
			videoIDs = append(videoIDs, videoID)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		_ = rows.Close()

		if _, err := tx.Exec(
			"UPDATE bookmarks SET custom_title = '' WHERE user_id = ? AND custom_title = ?",
			userID, label,
		); err != nil {
			return err
		}
		for _, videoID := range videoIDs {
			if err := db.bumpBookmarkTargetSyncSeqTx(tx, videoID); err != nil {
				return err
			}
			if err := db.recordBookmarkCurrentSyncChangeTx(tx, userID, videoID); err != nil {
				return err
			}
		}
		return nil
	})
}

func (db *DB) recordBookmarkCurrentSyncChangeTx(tx *sql.Tx, userID, videoID string) error {
	var categoryID int64
	var customTitle, accountHandles, mediaIndices sql.NullString
	var bookmarkedAt int64
	err := tx.QueryRow(`
		SELECT COALESCE(category_id, 0), custom_title, account_handles, media_indices, COALESCE(bookmarked_at, 0)
		FROM bookmarks
		WHERE user_id = ? AND video_id = ?
	`, userID, videoID).Scan(&categoryID, &customTitle, &accountHandles, &mediaIndices, &bookmarkedAt)
	if err != nil {
		return err
	}
	value := map[string]any{
		"video_id":        videoID,
		"action":          "set",
		"bookmarked":      true,
		"category_id":     categoryID,
		"custom_title":    nullStringValue(customTitle),
		"account_handles": nullStringValue(accountHandles),
		"media_indices":   nullStringValue(mediaIndices),
		"bookmarked_at":   bookmarkedAt,
		"updated_at_ms":   time.Now().UnixMilli(),
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return db.recordSyncChangeTx(tx, "bookmark", videoID, string(raw))
}

func (db *DB) recordBookmarkCategorySyncChangeTx(tx *sql.Tx, userID, action string, categoryID int64, name, archivePath string, createdAt int64) error {
	value := map[string]any{
		"action":        action,
		"category_id":   categoryID,
		"user_id":       userID,
		"updated_at_ms": time.Now().UnixMilli(),
	}
	if action != "clear" {
		value["name"] = name
		value["archive_path"] = archivePath
		value["created_at"] = createdAt
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return db.recordSyncChangeTx(tx, "bookmark_category", fmt.Sprintf("%d", categoryID), string(raw))
}

func nullStringValue(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}
