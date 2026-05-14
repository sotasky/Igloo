package db

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// --- Settings batch operations ---

var retiredGlobalSettingKeys = map[string]bool{
	"youtube_check_interval":   true,
	"shorts_check_interval":    true,
	"instagram_check_interval": true,
}

// GetAllSettings returns all global settings (user_id=”) as a key->value map.
func (db *DB) GetAllSettings() (map[string]string, error) {
	rows, err := db.conn.Query(
		"SELECT key, value FROM settings WHERE user_id = ''",
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	out := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		if retiredGlobalSettingKeys[k] {
			continue
		}
		out[k] = v
	}
	return out, rows.Err()
}

// UpdateSettings upserts all entries in the provided map as global settings (user_id=”).
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
		defer func() {
			_ = stmt.Close()
		}()
		for k, v := range settings {
			if retiredGlobalSettingKeys[k] {
				if _, err := tx.Exec(`DELETE FROM settings WHERE key = ?`, k); err != nil {
					return err
				}
				continue
			}
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
	VideoID        string `json:"video_id"`
	CategoryName   string `json:"category_name"`
	CustomTitle    string `json:"custom_title,omitempty"`
	AccountHandles string `json:"account_handles,omitempty"`
	MediaIndices   string `json:"media_indices,omitempty"`
	BookmarkedAt   int64  `json:"bookmarked_at,omitempty"`
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
	defer func() {
		_ = rows.Close()
	}()
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
	MaxVideos          int    `json:"max_videos,omitempty"`
	DownloadSubtitles  bool   `json:"download_subtitles,omitempty"`
	MediaOnly          *bool  `json:"media_only,omitempty"`
	MediaDownloadLimit *int   `json:"media_download_limit,omitempty"`
	IncludeReposts     *bool  `json:"include_reposts,omitempty"`
}

// LikedPostExport is a compact liked post for full data export.
type LikedPostExport struct {
	TweetID           string `json:"tweet_id"`
	SourceHandle      string `json:"source_handle,omitempty"`
	AuthorHandle      string `json:"author_handle"`
	AuthorDisplayName string `json:"author_display_name,omitempty"`
	BodyText          string `json:"body_text,omitempty"`
	Platform          string `json:"platform,omitempty"`
	PublishedAt       string `json:"published_at,omitempty"`
	PublishedAtMs     int64  `json:"published_at_ms,omitempty"`
	Link              string `json:"link,omitempty"`
	CanonicalXLink    string `json:"canonical_x_link,omitempty"`
	MediaURL          string `json:"media_url,omitempty"`
	AvatarURL         string `json:"avatar_url,omitempty"`
	MediaJSON         string `json:"media_json,omitempty"`
	QuotePayloadJSON  string `json:"quote_payload_json,omitempty"`
	LikedAt           int64  `json:"liked_at,omitempty"`
	UpdatedAt         int64  `json:"updated_at,omitempty"`
}

// BookmarkedVideoExport is a compact bookmarked video for full data export.
type BookmarkedVideoExport struct {
	VideoID       string `json:"video_id"`
	ChannelID     string `json:"channel_id"`
	Title         string `json:"title"`
	Platform      string `json:"platform,omitempty"`
	Duration      int    `json:"duration,omitempty"`
	PublishedAt   string `json:"published_at,omitempty"`
	PublishedAtMs int64  `json:"published_at_ms,omitempty"`
	CategoryName  string `json:"category_name,omitempty"`
	BookmarkedAt  int64  `json:"bookmarked_at,omitempty"`
}

// FeedSeenExport is a compact feed seen row for full data export.
type FeedSeenExport struct {
	TweetID string `json:"tweet_id"`
	SeenAt  int64  `json:"seen_at,omitempty"`
}

// ConfigExport is the config snapshot for export/import.
type ConfigExport struct {
	Version            int                     `json:"version"`
	UserID             string                  `json:"user_id,omitempty"`
	ExportedAt         time.Time               `json:"exported_at"`
	Subscriptions      []ChannelExport         `json:"subscriptions"`
	BookmarkCategories []BookmarkCatExport     `json:"bookmark_categories"`
	Bookmarks          []BookmarkExport        `json:"bookmarks"`
	Settings           map[string]string       `json:"settings"`
	LikedPosts         []LikedPostExport       `json:"liked_posts,omitempty"`
	BookmarkedVideos   []BookmarkedVideoExport `json:"bookmarked_videos,omitempty"`
	FeedSeen           []FeedSeenExport        `json:"feed_seen,omitempty"`
}

// resolveUserID returns the first user_id that has data, checking the given
// userID first, then ” (legacy). This handles the mismatch between old
// exports that used user_id=” and new data that uses the login username.
func (db *DB) resolveBookmarkUserID(preferredUserID string) string {
	for _, uid := range []string{preferredUserID, ""} {
		var count int
		_ = db.conn.QueryRow("SELECT COUNT(*) FROM bookmarks WHERE user_id = ?", uid).Scan(&count)
		if count > 0 {
			return uid
		}
	}
	return preferredUserID
}

func (db *DB) resolveCategoryUserID(preferredUserID string) string {
	for _, uid := range []string{preferredUserID, ""} {
		var count int
		_ = db.conn.QueryRow("SELECT COUNT(*) FROM bookmark_categories WHERE user_id = ?", uid).Scan(&count)
		if count > 0 {
			return uid
		}
	}
	return preferredUserID
}

func (db *DB) resolveLikeUsername(preferredUserID string) string {
	for _, uid := range []string{preferredUserID, ""} {
		var count int
		_ = db.conn.QueryRow("SELECT COUNT(*) FROM feed_likes WHERE username = ?", uid).Scan(&count)
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
		UserID:     userID,
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
	defer func() {
		_ = catRows.Close()
	}()
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
		SELECT b.video_id, COALESCE(bc.name,''), COALESCE(b.custom_title,''),
		       COALESCE(b.account_handles,''), COALESCE(b.media_indices,''),
		       COALESCE(b.bookmarked_at, 0)
		FROM bookmarks b
		LEFT JOIN bookmark_categories bc ON bc.id = b.category_id AND bc.user_id = ?
		WHERE b.user_id = ?
		ORDER BY b.bookmarked_at
	`, catUID, bmUID)
	if err != nil {
		return cfg, fmt.Errorf("export bookmarks: %w", err)
	}
	defer func() {
		_ = bRows.Close()
	}()
	for bRows.Next() {
		var bm BookmarkExport
		if err := bRows.Scan(&bm.VideoID, &bm.CategoryName, &bm.CustomTitle,
			&bm.AccountHandles, &bm.MediaIndices, &bm.BookmarkedAt); err != nil {
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
		SELECT tweet_id, COALESCE(source_handle,''), COALESCE(author_handle,''),
		       COALESCE(author_display_name,''), COALESCE(body_text,''),
		       COALESCE(platform,''), COALESCE(published_at,0),
		       COALESCE(link,''), COALESCE(canonical_x_link,''), COALESCE(media_url,''),
		       COALESCE(avatar_url,''), COALESCE(media_json,''), COALESCE(quote_payload_json,''),
		       COALESCE(liked_at,0), COALESCE(updated_at,0)
		FROM feed_likes WHERE username = ?
		ORDER BY liked_at DESC
	`, likeUID)
	if err != nil {
		return cfg, fmt.Errorf("export liked posts: %w", err)
	}
	defer func() {
		_ = likeRows.Close()
	}()
	for likeRows.Next() {
		var lp LikedPostExport
		if err := likeRows.Scan(&lp.TweetID, &lp.SourceHandle, &lp.AuthorHandle,
			&lp.AuthorDisplayName, &lp.BodyText, &lp.Platform, &lp.PublishedAtMs,
			&lp.Link, &lp.CanonicalXLink, &lp.MediaURL, &lp.AvatarURL, &lp.MediaJSON,
			&lp.QuotePayloadJSON, &lp.LikedAt, &lp.UpdatedAt); err != nil {
			return cfg, err
		}
		lp.PublishedAt = exportTimestampString(lp.PublishedAtMs)
		cfg.LikedPosts = append(cfg.LikedPosts, lp)
	}
	if err := likeRows.Err(); err != nil {
		return cfg, err
	}

	seenRows, err := db.conn.Query(`
		SELECT tweet_id, COALESCE(seen_at,0)
		FROM feed_seen
		WHERE username = ?
		ORDER BY seen_at DESC
	`, userID)
	if err != nil {
		return cfg, fmt.Errorf("export feed seen: %w", err)
	}
	defer func() {
		_ = seenRows.Close()
	}()
	for seenRows.Next() {
		var seen FeedSeenExport
		if err := seenRows.Scan(&seen.TweetID, &seen.SeenAt); err != nil {
			return cfg, err
		}
		cfg.FeedSeen = append(cfg.FeedSeen, seen)
	}
	if err := seenRows.Err(); err != nil {
		return cfg, err
	}

	// Bookmarked videos
	bmUID := db.resolveBookmarkUserID(userID)
	catUID := db.resolveCategoryUserID(userID)
	vidRows, err := db.conn.Query(`
		SELECT v.video_id, v.channel_id, COALESCE(v.title,''),
		       COALESCE(c.platform,''), COALESCE(v.duration,0),
		       COALESCE(v.published_at,0), COALESCE(bc.name,''),
		       COALESCE(b.bookmarked_at,0)
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
	defer func() {
		_ = vidRows.Close()
	}()
	for vidRows.Next() {
		var bv BookmarkedVideoExport
		if err := vidRows.Scan(&bv.VideoID, &bv.ChannelID, &bv.Title,
			&bv.Platform, &bv.Duration, &bv.PublishedAtMs, &bv.CategoryName,
			&bv.BookmarkedAt); err != nil {
			return cfg, err
		}
		bv.PublishedAt = exportTimestampString(bv.PublishedAtMs)
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

// ClaimBootstrapUserData moves user-owned rows imported before the first login
// from the legacy blank owner to the first real admin username.
func (db *DB) ClaimBootstrapUserData(userID string) error {
	if userID == "" {
		return nil
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`
			UPDATE bookmark_categories
			SET user_id = ?
			WHERE user_id = ''
		`, userID); err != nil {
			return err
		}
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO bookmarks
				(user_id, video_id, category_id, custom_title, account_handles, media_indices, bookmarked_at)
			SELECT ?, video_id, category_id, custom_title, account_handles, media_indices, bookmarked_at
			FROM bookmarks
			WHERE user_id = ''
		`, userID); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM bookmarks WHERE user_id = ''`); err != nil {
			return err
		}
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO feed_likes
				(username, tweet_id, source_handle, author_handle, author_display_name,
				 body_text, link, canonical_x_link, published_at, media_url, avatar_url,
				 media_json, platform, quote_payload_json, liked_at, updated_at)
			SELECT ?, tweet_id, source_handle, author_handle, author_display_name,
			       body_text, link, canonical_x_link, published_at, media_url, avatar_url,
			       media_json, platform, quote_payload_json, liked_at, updated_at
			FROM feed_likes
			WHERE username = ''
		`, userID); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM feed_likes WHERE username = ''`)
		return err
	})
}

// ImportConfig performs a config import. When replace is true, existing settings,
// bookmark categories, and bookmarks are cleared first. Channels are always merged
// (INSERT OR IGNORE + UPDATE settings for existing).
func (db *DB) ImportConfig(cfg ConfigExport, userID string, replace bool) (ImportResult, error) {
	var res ImportResult

	return res, db.WithWrite(func(tx *sql.Tx) error {
		// Replace mode: clear existing data
		if replace {
			_, _ = tx.Exec("DELETE FROM settings WHERE user_id = ''")
			_, _ = tx.Exec("DELETE FROM bookmarks WHERE user_id = ?", userID)
			_, _ = tx.Exec("DELETE FROM bookmark_categories WHERE user_id = ?", userID)
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
			defer func() {
				_ = stmt.Close()
			}()
			for k, v := range cfg.Settings {
				if retiredGlobalSettingKeys[k] {
					continue
				}
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
		defer func() {
			_ = catRows.Close()
		}()
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
				INSERT INTO bookmarks
					(user_id, video_id, category_id, custom_title, account_handles, media_indices, bookmarked_at)
				VALUES (?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(user_id, video_id) DO UPDATE SET
					category_id = CASE
						WHEN excluded.category_id != 0 AND bookmarks.category_id = 0 THEN excluded.category_id
						ELSE bookmarks.category_id
					END,
					custom_title = COALESCE(NULLIF(bookmarks.custom_title, ''), excluded.custom_title),
					account_handles = COALESCE(NULLIF(bookmarks.account_handles, ''), excluded.account_handles),
					media_indices = COALESCE(NULLIF(bookmarks.media_indices, ''), excluded.media_indices),
					bookmarked_at = CASE
						WHEN bookmarks.bookmarked_at <= 0 AND excluded.bookmarked_at > 0 THEN excluded.bookmarked_at
						ELSE bookmarks.bookmarked_at
					END
			`, userID, bm.VideoID, catID, customTitle, nilIfEmpty(bm.AccountHandles),
				nilIfEmpty(bm.MediaIndices), bm.BookmarkedAt)
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
			seq := db.NextSyncSeq()
			result, err := tx.Exec(`
				INSERT OR IGNORE INTO channels
					(channel_id, name, url, platform, quality, sync_seq)
				VALUES (?, ?, ?, ?, ?, ?)
			`,
				ch.ChannelID, ch.Name, buildChannelURL(ch), ch.Platform,
				nilIfEmpty(ch.Quality), seq,
			)
			if err != nil {
				return err
			}
			n, _ := result.RowsAffected()
			if n == 0 {
				if _, err := tx.Exec(`
					UPDATE channels
					SET sync_seq = ?
					WHERE channel_id = ?
					  AND COALESCE(sync_seq, 0) = 0
				`, seq, ch.ChannelID); err != nil {
					return err
				}
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
					(username, tweet_id, source_handle, author_handle, author_display_name,
					 body_text, link, canonical_x_link, published_at, media_url, avatar_url,
					 media_json, platform, quote_payload_json, liked_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`)
			if err != nil {
				return err
			}
			defer func() {
				_ = stmt.Close()
			}()
			for _, lp := range cfg.LikedPosts {
				publishedAt := exportTimestampMillis(lp.PublishedAtMs, lp.PublishedAt)
				if publishedAt == 0 && isTwitterExportPlatform(lp.Platform, "") {
					publishedAt = twitterSnowflakeMillis(lp.TweetID)
				}
				updatedAt := lp.UpdatedAt
				if updatedAt == 0 && lp.LikedAt > 0 {
					updatedAt = lp.LikedAt
				}
				if _, err := stmt.Exec(userID, lp.TweetID, nilIfEmpty(lp.SourceHandle),
					lp.AuthorHandle, lp.AuthorDisplayName, lp.BodyText, lp.Link,
					nilIfEmpty(lp.CanonicalXLink), publishedAt, nilIfEmpty(lp.MediaURL),
					nilIfEmpty(lp.AvatarURL), nilIfEmpty(lp.MediaJSON), lp.Platform,
					nilIfEmpty(lp.QuotePayloadJSON), lp.LikedAt, updatedAt); err != nil {
					return err
				}
			}
		}

		for _, seen := range cfg.FeedSeen {
			if strings.TrimSpace(seen.TweetID) == "" {
				continue
			}
			seenAt := seen.SeenAt
			if seenAt <= 0 {
				seenAt = time.Now().UnixMilli()
			}
			if _, err := tx.Exec(`
				INSERT INTO feed_seen (username, tweet_id, seen_at)
				VALUES (?, ?, ?)
				ON CONFLICT(username, tweet_id) DO UPDATE SET
					seen_at = MAX(feed_seen.seen_at, excluded.seen_at)
			`, userID, seen.TweetID, seenAt); err != nil {
				return err
			}
		}

		// Import bookmarked videos if present
		for _, bv := range cfg.BookmarkedVideos {
			seq := db.NextSyncSeq()
			publishedAt := exportVideoPublishedAt(bv)
			if _, err := tx.Exec(`
				INSERT INTO videos
					(video_id, channel_id, title, duration, published_at, file_path, sync_seq)
				VALUES (?, ?, ?, ?, ?, '', ?)
				ON CONFLICT(video_id) DO UPDATE SET
					published_at = CASE
						WHEN COALESCE(videos.published_at, 0) <= 0
						 AND excluded.published_at > 0
						THEN excluded.published_at
						ELSE videos.published_at
					END,
					sync_seq = CASE
						WHEN COALESCE(videos.sync_seq, 0) = 0
						  OR (COALESCE(videos.published_at, 0) <= 0
						      AND excluded.published_at > 0)
						THEN excluded.sync_seq
						ELSE videos.sync_seq
					END
			`, bv.VideoID, bv.ChannelID, bv.Title, bv.Duration, publishedAt, seq); err != nil {
				return err
			}

			catID := int64(0)
			if bv.CategoryName != "" {
				if id, ok := catMap[bv.CategoryName]; ok {
					catID = id
				}
			}
			if _, err := tx.Exec(`
				INSERT INTO bookmarks (user_id, video_id, category_id, bookmarked_at)
				VALUES (?, ?, ?, ?)
				ON CONFLICT(user_id, video_id) DO UPDATE SET
					category_id = CASE
						WHEN excluded.category_id != 0 AND bookmarks.category_id = 0 THEN excluded.category_id
						ELSE bookmarks.category_id
					END,
					bookmarked_at = CASE
						WHEN bookmarks.bookmarked_at <= 0 AND excluded.bookmarked_at > 0 THEN excluded.bookmarked_at
						ELSE bookmarks.bookmarked_at
					END
			`, userID, bv.VideoID, catID, bv.BookmarkedAt); err != nil {
				return err
			}
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

func exportTimestampMillis(ms int64, legacy string) int64 {
	if ms > 0 {
		return ms
	}
	return parseTimestampString(legacy)
}

func exportTimestampString(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

func exportVideoPublishedAt(bv BookmarkedVideoExport) int64 {
	publishedAt := exportTimestampMillis(bv.PublishedAtMs, bv.PublishedAt)
	if publishedAt != 0 {
		return publishedAt
	}
	if isTwitterExportPlatform(bv.Platform, bv.ChannelID) {
		return twitterSnowflakeMillis(bv.VideoID)
	}
	if isTikTokExportPlatform(bv.Platform, bv.ChannelID) {
		return tiktokSnowflakeMillis(bv.VideoID)
	}
	return 0
}

func isTwitterExportPlatform(platform, channelID string) bool {
	platform = strings.TrimSpace(strings.ToLower(platform))
	channelID = strings.TrimSpace(strings.ToLower(channelID))
	return platform == "twitter" || platform == "x" ||
		strings.HasPrefix(channelID, "twitter_") ||
		strings.HasPrefix(channelID, "x_")
}

func isTikTokExportPlatform(platform, channelID string) bool {
	platform = strings.TrimSpace(strings.ToLower(platform))
	channelID = strings.TrimSpace(strings.ToLower(channelID))
	return platform == "tiktok" || strings.HasPrefix(channelID, "tiktok_")
}

func tiktokSnowflakeMillis(id string) int64 {
	id = strings.TrimSpace(id)
	if id == "" {
		return 0
	}
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil || n == 0 {
		return 0
	}
	seconds := int64(n >> 32)
	if seconds <= 0 {
		return 0
	}
	return seconds * 1000
}
