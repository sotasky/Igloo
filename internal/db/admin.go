package db

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
	appsettings "github.com/screwys/igloo/internal/settings"
)

// --- Settings batch operations ---

var retiredGlobalSettingKeys = map[string]bool{
	"youtube_check_interval":   true,
	"shorts_check_interval":    true,
	"instagram_check_interval": true,
}

// GetAllSettings returns all settings as a key->value map.
func (db *DB) GetAllSettings() (map[string]string, error) {
	rows, err := db.reader().Query("SELECT key, value FROM settings")
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

// UpdateSettings upserts all entries in the provided map.
// All upserts occur in a single transaction.
func (db *DB) UpdateSettings(values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		if _, updatesDearrowMode := values["dearrow_mode"]; updatesDearrowMode {
			values["dearrow_mode"] = appsettings.NormalizeDearrowMode(values["dearrow_mode"])
		}
		stmt, err := tx.Prepare(`
			INSERT INTO settings (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value
		`)
		if err != nil {
			return err
		}
		defer func() {
			_ = stmt.Close()
		}()
		for k, v := range values {
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
	rows, err := db.reader().Query(`
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
	OwnerKind     string `json:"owner_kind"`
	Title         string `json:"title"`
	Duration      int    `json:"duration,omitempty"`
	PublishedAt   string `json:"published_at,omitempty"`
	PublishedAtMs int64  `json:"published_at_ms,omitempty"`
	CategoryName  string `json:"category_name,omitempty"`
	BookmarkedAt  int64  `json:"bookmarked_at,omitempty"`
}

const ConfigExportVersion = 2

// FeedSeenExport is a compact feed seen row for full data export.
type FeedSeenExport struct {
	TweetID string `json:"tweet_id"`
	SeenAt  int64  `json:"seen_at,omitempty"`
}

// ConfigExport is the config snapshot for export/import.
type ConfigExport struct {
	Version            int                     `json:"version"`
	Scope              string                  `json:"scope,omitempty"`
	ExportedAt         time.Time               `json:"exported_at"`
	Subscriptions      []ChannelExport         `json:"subscriptions"`
	BookmarkCategories []BookmarkCatExport     `json:"bookmark_categories"`
	Bookmarks          []BookmarkExport        `json:"bookmarks"`
	Settings           map[string]string       `json:"settings"`
	LikedPosts         []LikedPostExport       `json:"liked_posts,omitempty"`
	BookmarkedVideos   []BookmarkedVideoExport `json:"bookmarked_videos,omitempty"`
	FeedSeen           []FeedSeenExport        `json:"feed_seen,omitempty"`
}

func newConfigExport() ConfigExport {
	return ConfigExport{
		Version:       ConfigExportVersion,
		ExportedAt:    time.Now().UTC(),
		Subscriptions: make([]ChannelExport, 0),
	}
}

func (db *DB) exportSubscriptions() ([]ChannelExport, error) {
	out := make([]ChannelExport, 0)

	channels, err := db.GetSubscribedChannels()
	if err != nil {
		return nil, err
	}
	settingsByChannel, err := db.exportChannelSettingsMap()
	if err != nil {
		return nil, fmt.Errorf("export channel_settings: %w", err)
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
		out = append(out, ce)
	}
	return out, nil
}

// ExportSubscriptions gathers only the DB-backed subscription list.
func (db *DB) ExportSubscriptions() (ConfigExport, error) {
	cfg := newConfigExport()
	cfg.Scope = "subscriptions"
	subs, err := db.exportSubscriptions()
	if err != nil {
		return cfg, fmt.Errorf("export subscriptions: %w", err)
	}
	cfg.Subscriptions = subs
	return cfg, nil
}

// ExportConfig gathers preferences, subscriptions, bookmark categories, and bookmarks.
func (db *DB) ExportConfig() (ConfigExport, error) {
	cfg := newConfigExport()
	cfg.BookmarkCategories = make([]BookmarkCatExport, 0)
	cfg.Bookmarks = make([]BookmarkExport, 0)

	// Subscriptions: compact export
	subs, err := db.exportSubscriptions()
	if err != nil {
		return cfg, fmt.Errorf("export subscriptions: %w", err)
	}
	cfg.Subscriptions = subs

	// Settings
	cfg.Settings, err = db.GetAllSettings()
	if err != nil {
		return cfg, fmt.Errorf("export settings: %w", err)
	}

	// Bookmark categories
	catRows, err := db.reader().Query(
		"SELECT name, COALESCE(archive_path,'') FROM bookmark_categories ORDER BY id",
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
	bRows, err := db.reader().Query(`
		SELECT b.video_id, COALESCE(bc.name,''), COALESCE(b.custom_title,''),
		       COALESCE(b.account_handles,''), COALESCE(b.media_indices,''),
		       COALESCE(b.bookmarked_at, 0)
		FROM bookmarks b
		LEFT JOIN bookmark_categories bc ON bc.id = b.category_id
		ORDER BY b.bookmarked_at
	`)
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
func (db *DB) ExportFullData() (ConfigExport, error) {
	cfg, err := db.ExportConfig()
	if err != nil {
		return cfg, err
	}
	cfg.LikedPosts = make([]LikedPostExport, 0)
	cfg.BookmarkedVideos = make([]BookmarkedVideoExport, 0)
	cfg.FeedSeen = make([]FeedSeenExport, 0)

	// Liked posts
	likeRows, err := db.reader().Query(`
		SELECT fl.tweet_id, COALESCE(fi.source_handle,''), COALESCE(fi.author_handle,''),
		       COALESCE(fi.author_display_name,''), COALESCE(fi.body_text,''),
		       'twitter', COALESCE(fi.published_at,0),
		       COALESCE(fi.canonical_url,''), COALESCE(fi.canonical_url,''), '',
		       COALESCE(fi.author_avatar_url,''), COALESCE(fi.media_json,''), '',
		       COALESCE(fl.liked_at,0), COALESCE(fl.liked_at,0)
		FROM feed_likes fl
		JOIN feed_items_resolved fi ON fi.tweet_id = fl.tweet_id
		ORDER BY fl.liked_at DESC
	`)
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

	seenRows, err := db.reader().Query(`
		SELECT tweet_id, COALESCE(seen_at,0)
		FROM feed_seen
		ORDER BY seen_at DESC
	`)
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
	vidRows, err := db.reader().Query(`
		SELECT v.video_id, v.channel_id, v.owner_kind, COALESCE(v.title,''),
		       COALESCE(v.duration,0),
		       COALESCE(v.published_at,0), COALESCE(bc.name,''),
		       COALESCE(b.bookmarked_at,0)
		FROM bookmarks b
		JOIN videos v ON b.video_id = v.video_id
		LEFT JOIN bookmark_categories bc ON bc.id = b.category_id
		ORDER BY b.bookmarked_at DESC
	`)
	if err != nil {
		return cfg, fmt.Errorf("export bookmarked videos: %w", err)
	}
	defer func() {
		_ = vidRows.Close()
	}()
	for vidRows.Next() {
		var bv BookmarkedVideoExport
		if err := vidRows.Scan(&bv.VideoID, &bv.ChannelID, &bv.OwnerKind, &bv.Title,
			&bv.Duration, &bv.PublishedAtMs, &bv.CategoryName,
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

func ValidateConfigExport(cfg ConfigExport) error {
	if cfg.Version != ConfigExportVersion {
		return fmt.Errorf("unsupported config export version %d", cfg.Version)
	}
	for _, video := range cfg.BookmarkedVideos {
		if _, ok := videoPlatformForOwnerKind(video.OwnerKind); !ok {
			return fmt.Errorf("bookmarked video %s has invalid owner kind %q", video.VideoID, video.OwnerKind)
		}
	}
	return nil
}

// ImportConfig performs a config import. When replace is true, full config
// imports clear existing settings, bookmark categories, and bookmarks first.
// Subscription-scoped imports only replace follow/star/settings rows. Channels are always merged
// (INSERT OR IGNORE + UPDATE settings for existing), while follow/star/settings
// rows are replaced when the import carries a subscriptions section.
func (db *DB) ImportConfig(cfg ConfigExport, replace bool) (ImportResult, error) {
	var res ImportResult
	if err := ValidateConfigExport(cfg); err != nil {
		return res, err
	}
	subscriptionsOnly := strings.EqualFold(strings.TrimSpace(cfg.Scope), "subscriptions")
	fullReplace := replace && !subscriptionsOnly
	ownerAtMs := time.Now().UnixMilli()
	return res, db.WithWrite(func(tx *sql.Tx) error {
		// Replace mode: clear existing data
		if replace {
			if !subscriptionsOnly {
				if _, err := tx.Exec("DELETE FROM settings"); err != nil {
					return err
				}
				if err := advanceMutationClocksTx(tx, "bookmark", "clear", `
					SELECT video_id AS item_key, ? AS updated_at_ms FROM bookmarks
				`, ownerAtMs); err != nil {
					return err
				}
				if _, err := tx.Exec("DELETE FROM bookmarks"); err != nil {
					return err
				}
				if _, err := tx.Exec("DELETE FROM bookmark_categories"); err != nil {
					return err
				}
			}
			if cfg.Subscriptions != nil {
				if err := advanceMutationClocksTx(tx, "follow", "clear", `
					SELECT channel_id AS item_key, ? AS updated_at_ms FROM channel_follows
				`, ownerAtMs); err != nil {
					return err
				}
				if err := advanceMutationClocksTx(tx, "star", "clear", `
					SELECT channel_id AS item_key, ? AS updated_at_ms FROM channel_stars
				`, ownerAtMs); err != nil {
					return err
				}
				if _, err := tx.Exec(`
					INSERT INTO channel_settings (
						channel_id, max_videos, download_subtitles,
						media_only, media_download_limit, include_reposts, updated_at
					)
					SELECT cf.channel_id, NULL, NULL, NULL, NULL, NULL,
					       CASE
					         WHEN COALESCE(cs.updated_at, 0) >= ? THEN COALESCE(cs.updated_at, 0) + 1
					         ELSE ?
					       END
					FROM channel_follows cf
					LEFT JOIN channel_settings cs ON cs.channel_id = cf.channel_id
					WHERE 1
					ON CONFLICT(channel_id) DO UPDATE SET
						max_videos = NULL,
						download_subtitles = NULL,
						media_only = NULL,
						media_download_limit = NULL,
						include_reposts = NULL,
						updated_at = excluded.updated_at
				`, ownerAtMs, ownerAtMs); err != nil {
					return err
				}
				if _, err := tx.Exec("DELETE FROM channel_follows"); err != nil {
					return err
				}
				if _, err := tx.Exec("DELETE FROM channel_stars"); err != nil {
					return err
				}
			}
		}

		// Upsert settings
		if len(cfg.Settings) > 0 {
			stmt, err := tx.Prepare(`
				INSERT INTO settings (key, value) VALUES (?, ?)
				ON CONFLICT(key) DO UPDATE SET value = excluded.value
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
				INSERT OR IGNORE INTO bookmark_categories (name, archive_path)
				VALUES (?, ?)
			`, cat.Name, archivePath)
			if err != nil {
				return err
			}
			n, _ := result.RowsAffected()
			res.AddedCategories += int(n)
		}

		// Re-fetch category name→id map
		catMap := make(map[string]int64)
		catRows, err := tx.Query(
			"SELECT id, name FROM bookmark_categories",
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
					(video_id, category_id, custom_title, account_handles, media_indices, bookmarked_at)
				VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT(video_id) DO UPDATE SET
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
			`, bm.VideoID, catID, customTitle, nilIfEmpty(bm.AccountHandles),
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
			channelURL := buildChannelURL(ch)
			_, err := tx.Exec(`
				INSERT OR IGNORE INTO channels
					(channel_id, name, url, platform, quality)
				VALUES (?, ?, ?, ?, ?)
			`,
				ch.ChannelID, ch.Name, channelURL, ch.Platform,
				nilIfEmpty(ch.Quality),
			)
			if err != nil {
				return err
			}
			// Every listed subscription is authoritative for follow, star, and
			// its complete per-channel settings row.
			followedAt, err := advanceMutationClockTx(tx, "follow", ch.ChannelID, "set", ownerAtMs)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`
				INSERT INTO channel_follows (channel_id, followed_at)
				VALUES (?, ?)
				ON CONFLICT(channel_id) DO UPDATE SET followed_at = excluded.followed_at
			`, ch.ChannelID, followedAt); err != nil {
				return err
			}
			if ch.IsStarred {
				starredAt, err := advanceMutationClockTx(tx, "star", ch.ChannelID, "set", ownerAtMs)
				if err != nil {
					return err
				}
				if _, err := tx.Exec(`
					INSERT INTO channel_stars (channel_id, starred_at)
					VALUES (?, ?)
					ON CONFLICT(channel_id) DO UPDATE SET starred_at = excluded.starred_at
				`, ch.ChannelID, starredAt); err != nil {
					return err
				}
			} else {
				if _, err := advanceMutationClockTx(tx, "star", ch.ChannelID, "clear", ownerAtMs); err != nil {
					return err
				}
				if _, err := tx.Exec(`
					DELETE FROM channel_stars WHERE channel_id = ?
				`, ch.ChannelID); err != nil {
					return err
				}
			}

			hasSettings := ch.MaxVideos > 0 || ch.DownloadSubtitles ||
				ch.MediaOnly != nil || ch.MediaDownloadLimit != nil || ch.IncludeReposts != nil
			if hasSettings || replace {
				if _, err := mutateChannelSettingsTx(tx, ch.ChannelID, map[string]any{
					"max_videos":           intToNullInt(ch.MaxVideos),
					"download_subtitles":   nilIfFalse(ch.DownloadSubtitles),
					"media_only":           nilBoolPtr(ch.MediaOnly),
					"media_download_limit": nilIntPtr(ch.MediaDownloadLimit),
					"include_reposts":      nilBoolPtr(ch.IncludeReposts),
				}, followedAt, false); err != nil {
					return err
				}
			}
			if err := observeChannelProfileTx(tx, model.Channel{
				ChannelID: ch.ChannelID,
				Name:      ch.Name,
				URL:       channelURL,
				Platform:  ch.Platform,
			}, followedAt); err != nil {
				return err
			}

			res.AddedChannels++
		}

		// Import liked posts if present
		for _, lp := range cfg.LikedPosts {
			publishedAt := exportTimestampMillis(lp.PublishedAtMs, lp.PublishedAt)
			if publishedAt == 0 && isTwitterExportPlatform(lp.Platform, "") {
				publishedAt = twitterSnowflakeMillis(lp.TweetID)
			}
			fields := map[string]string{
				"source_handle": lp.SourceHandle, "author_handle": lp.AuthorHandle,
				"author_display_name": lp.AuthorDisplayName, "body_text": lp.BodyText,
				"link": lp.Link, "canonical_x_link": lp.CanonicalXLink,
				"avatar_url": lp.AvatarURL, "media_json": lp.MediaJSON,
				"published_at": strconv.FormatInt(publishedAt, 10),
			}
			if err := db.ensureFeedItemStubFromLikeTx(tx, lp.TweetID, fields); err != nil {
				return err
			}
			likedAt := lp.LikedAt
			if likedAt <= 0 {
				likedAt = time.Now().UnixMilli()
			}
			if _, err := tx.Exec(`
				INSERT INTO feed_likes (tweet_id, liked_at) VALUES (?, ?)
				ON CONFLICT(tweet_id) DO UPDATE SET liked_at = MAX(feed_likes.liked_at, excluded.liked_at)
			`, lp.TweetID, likedAt); err != nil {
				return err
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
				INSERT INTO feed_seen (tweet_id, seen_at)
				VALUES (?, ?)
				ON CONFLICT(tweet_id) DO UPDATE SET
					seen_at = MAX(feed_seen.seen_at, excluded.seen_at)
			`, seen.TweetID, seenAt); err != nil {
				return err
			}
		}

		// Import bookmarked videos if present
		for _, bv := range cfg.BookmarkedVideos {
			if err := requireVideoOwnerKindTx(tx, bv.VideoID, bv.OwnerKind); err != nil {
				return err
			}
			publishedAt := exportVideoPublishedAt(bv)
			if _, err := tx.Exec(`
				INSERT INTO videos
					(video_id, channel_id, owner_kind, title, duration, published_at)
				VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT(video_id) DO UPDATE SET
					owner_kind = excluded.owner_kind,
					published_at = CASE
						WHEN COALESCE(videos.published_at, 0) <= 0
						 AND excluded.published_at > 0
						THEN excluded.published_at
						ELSE videos.published_at
					END
			`, bv.VideoID, bv.ChannelID, bv.OwnerKind, bv.Title, bv.Duration, publishedAt); err != nil {
				return err
			}

			catID := int64(0)
			if bv.CategoryName != "" {
				if id, ok := catMap[bv.CategoryName]; ok {
					catID = id
				}
			}
			if _, err := tx.Exec(`
				INSERT INTO bookmarks (video_id, category_id, bookmarked_at)
				VALUES (?, ?, ?)
				ON CONFLICT(video_id) DO UPDATE SET
					category_id = CASE
						WHEN excluded.category_id != 0 AND bookmarks.category_id = 0 THEN excluded.category_id
						ELSE bookmarks.category_id
					END,
					bookmarked_at = CASE
						WHEN bookmarks.bookmarked_at <= 0 AND excluded.bookmarked_at > 0 THEN excluded.bookmarked_at
						ELSE bookmarks.bookmarked_at
					END
			`, bv.VideoID, catID, bv.BookmarkedAt); err != nil {
				return err
			}
		}

		if fullReplace || len(cfg.Bookmarks) > 0 || len(cfg.BookmarkedVideos) > 0 {
			if err := advanceMutationClocksTx(tx, "bookmark", "set", `
				SELECT video_id AS item_key,
				       CASE WHEN bookmarked_at > 0 THEN bookmarked_at ELSE ? END AS updated_at_ms
				FROM bookmarks
			`, ownerAtMs); err != nil {
				return err
			}
			if _, err := tx.Exec(`
				UPDATE bookmarks
				SET bookmarked_at = (
				  SELECT updated_at_ms FROM mutation_clocks
				  WHERE kind = 'bookmark' AND item_key = bookmarks.video_id
				)
				WHERE EXISTS (
				  SELECT 1 FROM mutation_clocks
				  WHERE kind = 'bookmark' AND item_key = bookmarks.video_id
				    AND action = 'set' AND updated_at_ms > bookmarks.bookmarked_at
				)
			`); err != nil {
				return err
			}
		}
		if len(cfg.LikedPosts) > 0 {
			if err := advanceMutationClocksTx(tx, "like", "set", `
				SELECT tweet_id AS item_key,
				       CASE WHEN liked_at > 0 THEN liked_at ELSE ? END AS updated_at_ms
				FROM feed_likes
			`, ownerAtMs); err != nil {
				return err
			}
			if _, err := tx.Exec(`
				UPDATE feed_likes
				SET liked_at = (
				  SELECT updated_at_ms FROM mutation_clocks
				  WHERE kind = 'like' AND item_key = feed_likes.tweet_id
				)
				WHERE EXISTS (
				  SELECT 1 FROM mutation_clocks
				  WHERE kind = 'like' AND item_key = feed_likes.tweet_id
				    AND action = 'set' AND updated_at_ms > feed_likes.liked_at
				)
			`); err != nil {
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
	if bv.OwnerKind == "tweet" {
		return twitterSnowflakeMillis(bv.VideoID)
	}
	if bv.OwnerKind == "tiktok_video" {
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
