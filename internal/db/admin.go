package db

import (
	"database/sql"
	"fmt"
	"time"
)

// --- Settings batch operations ---

// GetAllSettings returns all global settings (user_id='') as a key->value map.
func (db *DB) GetAllSettings() (map[string]string, error) {
	rows, err := db.conn.Query(
		"SELECT key, value FROM settings WHERE user_id = ''",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// UpdateSettings upserts all entries in the provided map as global settings (user_id='').
// All upserts occur in a single transaction.
func (db *DB) UpdateSettings(settings map[string]string) error {
	if len(settings) == 0 {
		return nil
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(`
			INSERT INTO settings (user_id, key, value) VALUES ('', ?, ?)
			ON CONFLICT(user_id, key) DO UPDATE SET value = excluded.value
		`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for k, v := range settings {
			if _, err := stmt.Exec(k, v); err != nil {
				return err
			}
		}
		return nil
	})
}

// --- Media file cleanup ---

// DeleteMediaFilesByOwner deletes all media_files rows for the given owner.
func (db *DB) DeleteMediaFilesByOwner(ownerType, ownerID string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			"DELETE FROM media_files WHERE owner_type = ? AND owner_id = ?",
			ownerType, ownerID,
		)
		return err
	})
}

// --- Config export/import ---

// BookmarkCatExport represents a single bookmark category for export.
type BookmarkCatExport struct {
	Name        string `json:"name"`
	ArchivePath string `json:"archive_path,omitempty"`
}

// BookmarkExport represents a single bookmark item for export.
type BookmarkExport struct {
	VideoID      string `json:"video_id"`
	CategoryName string `json:"category_name"`
	CustomTitle  string `json:"custom_title,omitempty"`
}

// exportedChannelSettings is the subset of channel_settings fields the export
// code consumes — only touched by ExportConfig.
type exportedChannelSettings struct {
	maxVideos          int
	downloadSubtitles  bool
	mediaOnly          *bool
	mediaDownloadLimit *int
	includeReposts     *bool
}

// exportChannelSettingsMap loads every row of channel_settings keyed by channel_id.
func (db *DB) exportChannelSettingsMap() (map[string]exportedChannelSettings, error) {
	rows, err := db.conn.Query(`
		SELECT channel_id, max_videos, download_subtitles,
		       media_only, media_download_limit, include_reposts
		FROM channel_settings
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]exportedChannelSettings)
	for rows.Next() {
		var channelID string
		var maxVideos, downloadSubtitles, mediaOnly, mediaDownloadLimit, includeReposts sql.NullInt64
		if err := rows.Scan(&channelID, &maxVideos, &downloadSubtitles,
			&mediaOnly, &mediaDownloadLimit, &includeReposts); err != nil {
			return nil, err
		}
		s := exportedChannelSettings{}
		if maxVideos.Valid {
			s.maxVideos = int(maxVideos.Int64)
		}
		if downloadSubtitles.Valid {
			s.downloadSubtitles = downloadSubtitles.Int64 != 0
		}
		if mediaOnly.Valid {
			v := mediaOnly.Int64 != 0
			s.mediaOnly = &v
		}
		if mediaDownloadLimit.Valid {
			v := int(mediaDownloadLimit.Int64)
			s.mediaDownloadLimit = &v
		}
		if includeReposts.Valid {
			v := includeReposts.Int64 != 0
			s.includeReposts = &v
		}
		out[channelID] = s
	}
	return out, rows.Err()
}

// ChannelExport is a compact channel representation with only non-default fields.
type ChannelExport struct {
	ChannelID          string `json:"channel_id"`
	Name               string `json:"name"`
	Platform           string `json:"platform"`
	IsStarred          bool   `json:"starred,omitempty"`
	Quality            string `json:"quality,omitempty"`
	CheckInterval      *int   `json:"check_interval,omitempty"`
	MaxVideos          int    `json:"max_videos,omitempty"`
	DownloadSubtitles  bool   `json:"download_subtitles,omitempty"`
	MediaOnly          *bool  `json:"media_only,omitempty"`
	MediaDownloadLimit *int   `json:"media_download_limit,omitempty"`
	IncludeReposts     *bool  `json:"include_reposts,omitempty"`
}

// LikedPostExport is a compact liked post for full data export.
type LikedPostExport struct {
	TweetID           string `json:"tweet_id"`
	AuthorHandle      string `json:"author_handle"`
	AuthorDisplayName string `json:"author_display_name,omitempty"`
	BodyText          string `json:"body_text,omitempty"`
	Platform          string `json:"platform,omitempty"`
	PublishedAt       string `json:"published_at,omitempty"`
	Link              string `json:"link,omitempty"`
	MediaURL          string `json:"media_url,omitempty"`
}

// BookmarkedVideoExport is a compact bookmarked video for full data export.
type BookmarkedVideoExport struct {
	VideoID      string `json:"video_id"`
	ChannelID    string `json:"channel_id"`
	Title        string `json:"title"`
	Platform     string `json:"platform,omitempty"`
	Duration     int    `json:"duration,omitempty"`
	PublishedAt  string `json:"published_at,omitempty"`
	CategoryName string `json:"category_name,omitempty"`
}

// ConfigExport is the config snapshot for export/import.
type ConfigExport struct {
	Version            int                     `json:"version"`
	ExportedAt         time.Time               `json:"exported_at"`
	Subscriptions      []ChannelExport         `json:"subscriptions"`
	BookmarkCategories []BookmarkCatExport     `json:"bookmark_categories"`
	Bookmarks          []BookmarkExport        `json:"bookmarks"`
	Settings           map[string]string       `json:"settings"`
	LikedPosts         []LikedPostExport       `json:"liked_posts,omitempty"`
	BookmarkedVideos   []BookmarkedVideoExport `json:"bookmarked_videos,omitempty"`
}

// resolveUserID returns the first user_id that has data, checking the given
// userID first, then '' (legacy). This handles the mismatch between old
// exports that used user_id='' and new data that uses the login username.
func (db *DB) resolveBookmarkUserID(preferredUserID string) string {
	for _, uid := range []string{preferredUserID, ""} {
		var count int
		db.conn.QueryRow("SELECT COUNT(*) FROM bookmarks WHERE user_id = ?", uid).Scan(&count)
		if count > 0 {
			return uid
		}
	}
	return preferredUserID
}

func (db *DB) resolveCategoryUserID(preferredUserID string) string {
	for _, uid := range []string{preferredUserID, ""} {
		var count int
		db.conn.QueryRow("SELECT COUNT(*) FROM bookmark_categories WHERE user_id = ?", uid).Scan(&count)
		if count > 0 {
			return uid
		}
	}
	return preferredUserID
}

func (db *DB) resolveLikeUsername(preferredUserID string) string {
	for _, uid := range []string{preferredUserID, ""} {
		var count int
		db.conn.QueryRow("SELECT COUNT(*) FROM feed_likes WHERE username = ?", uid).Scan(&count)
		if count > 0 {
			return uid
		}
	}
	return preferredUserID
}

// ExportConfig gathers preferences, subscriptions, bookmark categories, and bookmarks.
func (db *DB) ExportConfig(userID string) (ConfigExport, error) {
	cfg := ConfigExport{
		Version:    1,
		ExportedAt: time.Now().UTC(),
	}

	// Subscriptions: compact export
	channels, err := db.GetSubscribedChannels()
	if err != nil {
		return cfg, fmt.Errorf("export subscriptions: %w", err)
	}
	settingsByChannel, err := db.exportChannelSettingsMap()
	if err != nil {
		return cfg, fmt.Errorf("export channel_settings: %w", err)
	}
	for _, ch := range channels {
		ce := ChannelExport{
			ChannelID: ch.ChannelID,
			Name:      ch.Name,
			Platform:  ch.Platform,
			IsStarred: ch.IsStarred,
			Quality:   ch.Quality,
		}
		if ch.CheckInterval != nil {
			ce.CheckInterval = ch.CheckInterval
		}
		if s, ok := settingsByChannel[ch.ChannelID]; ok {
			ce.MaxVideos = s.maxVideos
			ce.DownloadSubtitles = s.downloadSubtitles
			ce.MediaOnly = s.mediaOnly
			ce.MediaDownloadLimit = s.mediaDownloadLimit
			ce.IncludeReposts = s.includeReposts
		}
		cfg.Subscriptions = append(cfg.Subscriptions, ce)
	}

	// Settings
	cfg.Settings, err = db.GetAllSettings()
	if err != nil {
		return cfg, fmt.Errorf("export settings: %w", err)
	}

	// Bookmark categories
	catUID := db.resolveCategoryUserID(userID)
	catRows, err := db.conn.Query(
		"SELECT name, COALESCE(archive_path,'') FROM bookmark_categories WHERE user_id=? ORDER BY id",
		catUID,
	)
	if err != nil {
		return cfg, fmt.Errorf("export bookmark categories: %w", err)
	}
	defer catRows.Close()
	for catRows.Next() {
		var cat BookmarkCatExport
		if err := catRows.Scan(&cat.Name, &cat.ArchivePath); err != nil {
			return cfg, err
		}
		cfg.BookmarkCategories = append(cfg.BookmarkCategories, cat)
	}
	if err := catRows.Err(); err != nil {
		return cfg, err
	}

	// Bookmarks: join category name
	bmUID := db.resolveBookmarkUserID(userID)
	bRows, err := db.conn.Query(`
		SELECT b.video_id, COALESCE(bc.name,''), COALESCE(b.custom_title,'')
		FROM bookmarks b
		LEFT JOIN bookmark_categories bc ON bc.id = b.category_id AND bc.user_id = ?
		WHERE b.user_id = ?
		ORDER BY b.bookmarked_at
	`, catUID, bmUID)
	if err != nil {
		return cfg, fmt.Errorf("export bookmarks: %w", err)
	}
	defer bRows.Close()
	for bRows.Next() {
		var bm BookmarkExport
		if err := bRows.Scan(&bm.VideoID, &bm.CategoryName, &bm.CustomTitle); err != nil {
			return cfg, err
		}
		cfg.Bookmarks = append(cfg.Bookmarks, bm)
	}
	if err := bRows.Err(); err != nil {
		return cfg, err
	}

	return cfg, nil
}

// ExportFullData exports config plus liked posts and bookmarked videos for disaster recovery.
func (db *DB) ExportFullData(userID string) (ConfigExport, error) {
	cfg, err := db.ExportConfig(userID)
	if err != nil {
		return cfg, err
	}

	// Liked posts
	likeUID := db.resolveLikeUsername(userID)
	likeRows, err := db.conn.Query(`
		SELECT tweet_id, COALESCE(author_handle,''), COALESCE(author_display_name,''),
		       COALESCE(body_text,''), COALESCE(platform,''), COALESCE(published_at,''),
		       COALESCE(link,''), COALESCE(media_url,'')
		FROM feed_likes WHERE username = ?
		ORDER BY liked_at DESC
	`, likeUID)
	if err != nil {
		return cfg, fmt.Errorf("export liked posts: %w", err)
	}
	defer likeRows.Close()
	for likeRows.Next() {
		var lp LikedPostExport
		if err := likeRows.Scan(&lp.TweetID, &lp.AuthorHandle, &lp.AuthorDisplayName,
			&lp.BodyText, &lp.Platform, &lp.PublishedAt, &lp.Link, &lp.MediaURL); err != nil {
			return cfg, err
		}
		cfg.LikedPosts = append(cfg.LikedPosts, lp)
	}
	if err := likeRows.Err(); err != nil {
		return cfg, err
	}

	// Bookmarked videos
	bmUID := db.resolveBookmarkUserID(userID)
	catUID := db.resolveCategoryUserID(userID)
	vidRows, err := db.conn.Query(`
		SELECT v.video_id, v.channel_id, COALESCE(v.title,''),
		       COALESCE(c.platform,''), COALESCE(v.duration,0),
		       COALESCE(v.published_at,''), COALESCE(bc.name,'')
		FROM bookmarks b
		JOIN videos v ON b.video_id = v.video_id
		LEFT JOIN channels c ON v.channel_id = c.channel_id
		LEFT JOIN bookmark_categories bc ON bc.id = b.category_id AND bc.user_id = ?
		WHERE b.user_id = ?
		ORDER BY b.bookmarked_at DESC
	`, catUID, bmUID)
	if err != nil {
		return cfg, fmt.Errorf("export bookmarked videos: %w", err)
	}
	defer vidRows.Close()
	for vidRows.Next() {
		var bv BookmarkedVideoExport
		if err := vidRows.Scan(&bv.VideoID, &bv.ChannelID, &bv.Title,
			&bv.Platform, &bv.Duration, &bv.PublishedAt, &bv.CategoryName); err != nil {
			return cfg, err
		}
		cfg.BookmarkedVideos = append(cfg.BookmarkedVideos, bv)
	}
	if err := vidRows.Err(); err != nil {
		return cfg, err
	}

	return cfg, nil
}

// ImportResult reports what was added/skipped during an import.
type ImportResult struct {
	AddedChannels   int
	AddedBookmarks  int
	AddedCategories int
	UpdatedSettings int
	Skipped         int
}

// ImportConfig performs a config import. When replace is true, existing settings,
// bookmark categories, and bookmarks are cleared first. Channels are always merged
// (INSERT OR IGNORE + UPDATE settings for existing).
func (db *DB) ImportConfig(cfg ConfigExport, userID string, replace bool) (ImportResult, error) {
	var res ImportResult

	return res, db.WithWrite(func(tx *sql.Tx) error {
		// Replace mode: clear existing data
		if replace {
			tx.Exec("DELETE FROM settings WHERE user_id = ''")
			tx.Exec("DELETE FROM bookmarks WHERE user_id = ?", userID)
			tx.Exec("DELETE FROM bookmark_categories WHERE user_id = ?", userID)
		}

		// Upsert settings
		if len(cfg.Settings) > 0 {
			stmt, err := tx.Prepare(`
				INSERT INTO settings (user_id, key, value) VALUES ('', ?, ?)
				ON CONFLICT(user_id, key) DO UPDATE SET value = excluded.value
			`)
			if err != nil {
				return err
			}
			defer stmt.Close()
			for k, v := range cfg.Settings {
				if _, err := stmt.Exec(k, v); err != nil {
					return err
				}
				res.UpdatedSettings++
			}
		}

		// Insert bookmark categories
		for _, cat := range cfg.BookmarkCategories {
			var archivePath any
			if cat.ArchivePath != "" {
				archivePath = cat.ArchivePath
			}
			result, err := tx.Exec(`
				INSERT OR IGNORE INTO bookmark_categories (user_id, name, archive_path)
				VALUES (?, ?, ?)
			`, userID, cat.Name, archivePath)
			if err != nil {
				return err
			}
			n, _ := result.RowsAffected()
			res.AddedCategories += int(n)
		}

		// Re-fetch category name→id map
		catMap := make(map[string]int64)
		catRows, err := tx.Query(
			"SELECT id, name FROM bookmark_categories WHERE user_id=?", userID,
		)
		if err != nil {
			return err
		}
		defer catRows.Close()
		for catRows.Next() {
			var id int64
			var name string
			if err := catRows.Scan(&id, &name); err != nil {
				return err
			}
			catMap[name] = id
		}
		if err := catRows.Err(); err != nil {
			return err
		}

		// Insert bookmarks
		for _, bm := range cfg.Bookmarks {
			catID := int64(0)
			if bm.CategoryName != "" {
				var ok bool
				catID, ok = catMap[bm.CategoryName]
				if !ok {
					res.Skipped++
					continue
				}
			}
			var customTitle any
			if bm.CustomTitle != "" {
				customTitle = bm.CustomTitle
			}
			result, err := tx.Exec(`
				INSERT OR IGNORE INTO bookmarks (user_id, video_id, category_id, custom_title)
				VALUES (?, ?, ?, ?)
			`, userID, bm.VideoID, catID, customTitle)
			if err != nil {
				return err
			}
			n, _ := result.RowsAffected()
			res.AddedBookmarks += int(n)
			if n == 0 {
				res.Skipped++
			}
		}

		// Channels: INSERT OR IGNORE new, UPDATE settings for existing
		for _, ch := range cfg.Subscriptions {
			result, err := tx.Exec(`
				INSERT OR IGNORE INTO channels
					(channel_id, name, url, platform, quality, check_interval)
				VALUES (?, ?, ?, ?, ?, ?)
			`,
				ch.ChannelID, ch.Name, buildChannelURL(ch), ch.Platform,
				nilIfEmpty(ch.Quality), ch.CheckInterval,
			)
			if err != nil {
				return err
			}

			// Follow + star side tables — always ensure the follow row
			// exists (import assumes every listed channel is subscribed).
			nowMs := time.Now().UnixMilli()
			if _, err := tx.Exec(`
				INSERT OR IGNORE INTO channel_follows (user_id, channel_id, followed_at)
				VALUES ('', ?, ?)
			`, ch.ChannelID, nowMs); err != nil {
				return err
			}
			if ch.IsStarred {
				if _, err := tx.Exec(`
					INSERT OR IGNORE INTO channel_stars (user_id, channel_id, starred_at)
					VALUES ('', ?, ?)
				`, ch.ChannelID, nowMs); err != nil {
					return err
				}
			} else {
				if _, err := tx.Exec(`
					DELETE FROM channel_stars WHERE user_id = '' AND channel_id = ?
				`, ch.ChannelID); err != nil {
					return err
				}
			}

			// Per-channel settings side table. Only write rows that carry
			// at least one non-default value.
			if ch.MaxVideos > 0 || ch.DownloadSubtitles ||
				ch.MediaOnly != nil || ch.MediaDownloadLimit != nil || ch.IncludeReposts != nil {
				if _, err := tx.Exec(`
					INSERT INTO channel_settings
						(channel_id, max_videos, download_subtitles,
						 media_only, media_download_limit, include_reposts, updated_at)
					VALUES (?, ?, ?, ?, ?, ?, ?)
					ON CONFLICT(channel_id) DO UPDATE SET
						max_videos=excluded.max_videos,
						download_subtitles=excluded.download_subtitles,
						media_only=excluded.media_only,
						media_download_limit=excluded.media_download_limit,
						include_reposts=excluded.include_reposts,
						updated_at=excluded.updated_at
				`,
					ch.ChannelID,
					intToNullInt(ch.MaxVideos),
					nilIfFalse(ch.DownloadSubtitles),
					nilBoolPtr(ch.MediaOnly),
					nilIntPtr(ch.MediaDownloadLimit),
					nilBoolPtr(ch.IncludeReposts),
					nowMs,
				); err != nil {
					return err
				}
			}

			n, _ := result.RowsAffected()
			if n > 0 {
				res.AddedChannels++
			} else {
				res.AddedChannels++
			}
		}

		// Import liked posts if present
		if len(cfg.LikedPosts) > 0 {
			stmt, err := tx.Prepare(`
				INSERT OR IGNORE INTO feed_likes
					(username, tweet_id, author_handle, author_display_name,
					 body_text, platform, published_at, link, media_url,
					 liked_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`)
			if err != nil {
				return err
			}
			defer stmt.Close()
			nowMs := time.Now().UnixMilli()
			for _, lp := range cfg.LikedPosts {
				stmt.Exec(userID, lp.TweetID, lp.AuthorHandle, lp.AuthorDisplayName,
					lp.BodyText, lp.Platform, parseTimestampString(lp.PublishedAt),
					lp.Link, lp.MediaURL, nowMs, nowMs)
			}
		}

		// Import bookmarked videos if present
		for _, bv := range cfg.BookmarkedVideos {
			tx.Exec(`
				INSERT OR IGNORE INTO videos
					(video_id, channel_id, title, duration, published_at, file_path)
				VALUES (?, ?, ?, ?, ?, '')
			`, bv.VideoID, bv.ChannelID, bv.Title, bv.Duration, parseTimestampString(bv.PublishedAt))

			catID := int64(0)
			if bv.CategoryName != "" {
				if id, ok := catMap[bv.CategoryName]; ok {
					catID = id
				}
			}
			tx.Exec(`
				INSERT OR IGNORE INTO bookmarks (user_id, video_id, category_id)
				VALUES (?, ?, ?)
			`, userID, bv.VideoID, catID)
		}

		return nil
	})
}

// buildChannelURL constructs a URL from a ChannelExport.
func buildChannelURL(ch ChannelExport) string {
	switch ch.Platform {
	case "twitter":
		id := ch.ChannelID
		if len(id) > 8 && id[:8] == "twitter_" {
			id = id[8:]
		}
		return "https://x.com/" + id
	case "tiktok":
		id := ch.ChannelID
		if len(id) > 7 && id[:7] == "tiktok_" {
			id = id[7:]
		}
		return "https://www.tiktok.com/@" + id
	case "youtube":
		id := ch.ChannelID
		if len(id) > 8 && id[:8] == "youtube_" {
			id = id[8:]
		}
		return "https://www.youtube.com/channel/" + id
	}
	return ""
}

func nilBoolPtr(b *bool) any {
	if b == nil {
		return nil
	}
	return boolToInt(*b)
}

func nilIntPtr(n *int) any {
	if n == nil {
		return nil
	}
	return *n
}

// intToNullInt returns nil when n <= 0 so the DB stores NULL (meaning
// "inherit global") rather than a sentinel zero.
func intToNullInt(n int) any {
	if n <= 0 {
		return nil
	}
	return n
}
