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
func (db *DB) IsBookmarked(videoID string) (bool, int64, error) {
	var err error
	videoID, err = db.ResolveFeedStateID(videoID)
	if err != nil {
		return false, 0, err
	}
	var categoryID int64
	err = db.conn.QueryRow("SELECT category_id FROM bookmarks WHERE video_id = ?", videoID).Scan(&categoryID)
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
func (db *DB) GetBookmarkCategories() ([]BookmarkCategoryRow, error) {
	rows, err := db.conn.Query(`
		SELECT bc.id, bc.name, COALESCE(bc.archive_path,''), COALESCE(bc.created_at, 0),
		       COUNT(b.video_id) AS bookmark_count
		FROM bookmark_categories bc
		LEFT JOIN bookmarks b ON bc.id = b.category_id
		GROUP BY bc.id
		ORDER BY bc.id
	`)
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

	where, args = appendBookmarkFilterWhere(where, args, opts, "b.")

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT v.video_id, v.channel_id, v.owner_kind,
		       CASE WHEN v.title LIKE 'X post %%' THEN COALESCE(fi.body_text,
		           (SELECT fp.quote_body_text
		            FROM feed_items fp
		            WHERE fp.quote_tweet_id = v.video_id
		              AND fp.quote_tweet_id IS NOT NULL
		              AND fp.quote_tweet_id != ''
		            LIMIT 1),
		           '') ELSE v.title END,
		       COALESCE(v.description,''),
		       COALESCE(v.duration,0), COALESCE(NULLIF(v.published_at, 0), fi.published_at,
		           (SELECT COALESCE(fp.quote_published_at, fp.published_at)
		            FROM feed_items fp
		            WHERE fp.quote_tweet_id = v.video_id
		              AND fp.quote_tweet_id IS NOT NULL
		              AND fp.quote_tweet_id != ''
		            LIMIT 1)
		       ), v.downloaded_at,
		       CASE WHEN %s THEN 1 ELSE 0 END,
		       COALESCE(v.is_temp,0), COALESCE(v.is_pinned,0),
		       COALESCE(v.metadata_json,''),
		       COALESCE(cp.display_name,''),
		       COALESCE(c.platform,
		           CASE
		               WHEN v.channel_id LIKE 'twitter_%%' THEN 'twitter'
		               WHEN v.channel_id LIKE 'x_%%' THEN 'twitter'
		               WHEN v.channel_id LIKE 'tiktok_%%' THEN 'tiktok'
		               WHEN v.channel_id LIKE 'instagram_%%' THEN 'instagram'
		               ELSE 'youtube'
		           END),
		       CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END,
		       b.category_id, COALESCE(fi.quote_tweet_id,''),
		       COALESCE((
		           SELECT GROUP_CONCAT(media_type, ',')
		           FROM (
		               SELECT CASE
			                   WHEN a.asset_kind = 'video_stream' OR mo.content_type LIKE 'video/%%' THEN 'video'
		                   ELSE 'image'
			               END AS media_type
			               FROM assets a
			               JOIN media_objects mo ON mo.object_id = a.object_id
			               WHERE a.owner_id = v.video_id
			                 AND a.owner_kind = %s
		                 AND mo.published_revision > 0 AND mo.file_path != ''
		                 AND a.asset_kind IN ('post_media', 'video_stream')
		               ORDER BY a.media_index, a.id
		           )
		       ), ''),
		       COALESCE((
		           SELECT GROUP_CONCAT(media_type, ',')
		           FROM (
		               SELECT CASE
			                   WHEN a.asset_kind = 'video_stream' OR mo.content_type LIKE 'video/%%' THEN 'video'
		                   ELSE 'image'
		               END AS media_type
			               FROM assets a
			               JOIN media_objects mo ON mo.object_id = a.object_id
		               WHERE a.owner_kind = 'tweet'
		                 AND a.owner_id = NULLIF(fi.quote_tweet_id, '')
			                 AND mo.published_revision > 0 AND mo.file_path != ''
		                 AND a.asset_kind IN ('post_media', 'video_stream')
		               ORDER BY a.media_index, a.id
		           )
		       ), '')
		FROM bookmarks b
		JOIN videos v ON b.video_id = v.video_id
		LEFT JOIN channels c ON v.channel_id = c.channel_id
		LEFT JOIN channel_profiles cp ON cp.channel_id = v.channel_id
		LEFT JOIN channel_follows cf ON cf.channel_id = v.channel_id
		LEFT JOIN feed_items fi ON fi.tweet_id = v.video_id
		%s
		ORDER BY b.bookmarked_at DESC
		LIMIT ? OFFSET ?
	`, videoFullyWatchedSQL("v"), "v.owner_kind", whereClause)
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
		var directMediaTypes, quoteMediaTypes, quoteTweetID string
		err := rows.Scan(
			&v.VideoID, &v.ChannelID, &v.OwnerKind, &v.Title, &v.Description,
			&v.Duration, &pubAt, &dlAt,
			&v.Watched, &v.IsTemp, &v.IsPinned,
			&v.MetadataJSON,
			&v.ChannelName, &v.Platform, &v.IsSubscribed,
			&catID, &quoteTweetID,
			&directMediaTypes, &quoteMediaTypes,
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
		v.EnrichForCard()

		mediaTypes := splitBookmarkMediaTypes(directMediaTypes)
		if len(mediaTypes) == 0 {
			mediaTypes = splitBookmarkMediaTypes(quoteMediaTypes)
		}
		v.MediaTypes = mediaTypes
		if len(mediaTypes) > 1 {
			v.MediaKind = "slideshow"
			v.MediaSlideCount = len(mediaTypes)
		} else if len(mediaTypes) == 1 {
			switch mediaTypes[0] {
			case "image":
				v.MediaKind = "image"
				v.MediaSlideCount = 1
			case "video":
				v.MediaKind = "video"
				v.MediaSlideCount = 0
			}
		}
		if v.OwnerKind == "tweet" {
			thumbnailOwnerID := v.VideoID
			if len(directMediaTypes) == 0 && len(quoteMediaTypes) > 0 && quoteTweetID != "" {
				thumbnailOwnerID = quoteTweetID
			}
			v.ThumbnailURL = "/api/media/thumbnail/" + thumbnailOwnerID + "?owner_kind=tweet"
		}

		videos = append(videos, v)
	}
	return videos, rows.Err()
}

func splitBookmarkMediaTypes(raw string) []string {
	var out []string
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

// GetBookmarkCount returns total bookmarks matching the filter.
func (db *DB) GetBookmarkCount(opts GetBookmarksOpts) (int, error) {
	var where []string
	var args []any

	where, args = appendBookmarkFilterWhere(where, args, opts, "")

	var count int
	query := "SELECT COUNT(*) FROM bookmarks"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	err := db.conn.QueryRow(query, args...).Scan(&count)
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
func (db *DB) GetBookmarkLabelCounts() ([]BookmarkLabelCountRow, error) {
	rows, err := db.conn.Query(`
		SELECT label, COUNT(*) AS bookmark_count
		FROM (
			SELECT
				CASE
					WHEN NULLIF(TRIM(COALESCE(custom_title, '')), '') IS NULL THEN ''
					ELSE TRIM(custom_title)
				END AS label
			FROM bookmarks
		)
		GROUP BY label
		ORDER BY bookmark_count DESC, LOWER(label) ASC
	`)
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
func (db *DB) AddBookmark(videoID string, categoryID int64, customTitle, accountHandles, mediaIndices string) error {
	mutation := BookmarkMutation{VideoID: videoID, Action: "set", CategoryID: &categoryID}
	if customTitle != "" {
		mutation.CustomTitle = &customTitle
	}
	if accountHandles != "" {
		mutation.AccountHandles = &accountHandles
	}
	if mediaIndices != "" {
		mutation.MediaIndices = &mediaIndices
	}
	_, err := db.MutateBookmark(mutation)
	return err
}

// RemoveBookmark deletes a bookmark.
func (db *DB) RemoveBookmark(videoID string) error {
	_, err := db.MutateBookmark(BookmarkMutation{VideoID: videoID, Action: "clear"})
	return err
}

// CreateBookmarkCategory creates a new bookmark category, returns its ID.
func (db *DB) CreateBookmarkCategory(name, archivePath string) (int64, error) {
	var id int64
	createdAt := time.Now().UnixMilli()
	err := db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(
			"INSERT INTO bookmark_categories (name, archive_path, created_at) VALUES (?, ?, ?)",
			name, archivePath, createdAt,
		)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		if err != nil {
			return err
		}
		return nil
	})
	return id, err
}

// DeleteBookmarkCategory deletes a category and moves its bookmarks to uncategorized.
func (db *DB) DeleteBookmarkCategory(categoryID int64) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		nowMs := time.Now().UnixMilli()
		if err := advanceMutationClocksTx(tx, "bookmark", "set", `
			SELECT video_id AS item_key, ? AS updated_at_ms
			FROM bookmarks WHERE category_id = ?
		`, nowMs, categoryID); err != nil {
			return err
		}
		if _, err := tx.Exec(`
			UPDATE bookmarks
			SET category_id = 0,
			    bookmarked_at = (
			      SELECT updated_at_ms FROM mutation_clocks
			      WHERE kind = 'bookmark' AND item_key = bookmarks.video_id
			    )
			WHERE category_id = ?
		`, categoryID); err != nil {
			return err
		}
		_, err := tx.Exec("DELETE FROM bookmark_categories WHERE id = ?", categoryID)
		if err != nil {
			return err
		}
		return nil
	})
}

// UpdateBookmarkCategory updates a category's name and/or archive path.
func (db *DB) UpdateBookmarkCategory(categoryID int64, name, archivePath string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			"UPDATE bookmark_categories SET name = ?, archive_path = ? WHERE id = ?",
			name, archivePath, categoryID,
		)
		if err != nil {
			return err
		}
		var createdAt int64
		if err := tx.QueryRow(
			"SELECT COALESCE(created_at, 0) FROM bookmark_categories WHERE id = ?",
			categoryID,
		).Scan(&createdAt); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return err
		}
		return nil
	})
}

// GetBookmarkedHandles returns all unique account handles from bookmarks for a user.
func (db *DB) GetBookmarkedHandles() ([]string, error) {
	rows, err := db.conn.Query(
		"SELECT account_handles FROM bookmarks WHERE account_handles IS NOT NULL AND account_handles != ''",
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
func (db *DB) GetBookmarkLabels(categoryID string) ([]string, error) {
	var rows *sql.Rows
	var err error
	if categoryID != "" {
		rows, err = db.conn.Query(
			"SELECT DISTINCT TRIM(custom_title) FROM bookmarks WHERE category_id = ? AND NULLIF(TRIM(COALESCE(custom_title, '')), '') IS NOT NULL ORDER BY LOWER(TRIM(custom_title))",
			categoryID,
		)
	} else {
		rows, err = db.conn.Query(
			"SELECT DISTINCT TRIM(custom_title) FROM bookmarks WHERE NULLIF(TRIM(COALESCE(custom_title, '')), '') IS NOT NULL ORDER BY LOWER(TRIM(custom_title))",
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
func (db *DB) ClearBookmarkLabel(label string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		nowMs := time.Now().UnixMilli()
		if err := advanceMutationClocksTx(tx, "bookmark", "set", `
			SELECT video_id AS item_key, ? AS updated_at_ms
			FROM bookmarks WHERE custom_title = ?
		`, nowMs, label); err != nil {
			return err
		}
		if _, err := tx.Exec(
			`UPDATE bookmarks
			 SET custom_title = '',
			     bookmarked_at = (
			       SELECT updated_at_ms FROM mutation_clocks
			       WHERE kind = 'bookmark' AND item_key = bookmarks.video_id
			     )
			 WHERE custom_title = ?`,
			label,
		); err != nil {
			return err
		}
		return nil
	})
}
