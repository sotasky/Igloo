package db

import (
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
)

// ChannelSettings holds merged channel + global settings for a channel.
type ChannelSettings struct {
	Quality            string `json:"quality"`
	MaxVideos          int    `json:"max_videos"`
	DownloadSubtitles  bool   `json:"download_subtitles"`
	MediaOnly          bool   `json:"media_only"`
	MediaDownloadLimit int    `json:"media_download_limit"`
	IncludeReposts     bool   `json:"include_reposts"`
}

// GetSubscribedChannels returns every channel that has a `channel_follows`
// row (any user). In single-user mode that reduces to the former
// `is_subscribed=1` filter. The IsStarred flag is hydrated from
// `channel_stars`; IncludeReposts from `channel_settings`.
func (db *DB) GetSubscribedChannels() ([]model.Channel, error) {
	// Sort uses the rendered display name (display_name if set, else
	// channels.name) via the same COALESCE so rows sort how they render —
	// no more "sample_handle_ja" landing in the k-block while being shown as
	// "Example Display Name". COLLATE NOCASE keeps Latin case-insensitive; unicode
	// names fall back to codepoint order which is good enough for the
	// sidebar (users scan visually, not alphabetically).
	// channel_follows / channel_stars are keyed by (user_id, channel_id);
	// in single-user mode every row is written with user_id=''. Scoping the
	// joins to that canonical user keeps the result set-based and matches
	// the rest of the codebase (videos.go, GetChannelByID, FollowChannel).
	rows, err := db.conn.Query(`
		SELECT COALESCE(c.id, 0), cf.channel_id, COALESCE(c.source_id,''), COALESCE(c.name,''),
		       COALESCE(c.url,''), COALESCE(c.platform,''),
		       CASE WHEN cs2.channel_id IS NOT NULL THEN 1 ELSE 0 END AS is_starred,
		       COALESCE(c.quality,''),
		       c.last_checked, c.created_at,
		       cs.include_reposts,
		       COALESCE(cp.handle,'')       AS handle,
		       COALESCE(cp.display_name,'') AS display_name
		FROM channel_follows cf
		LEFT JOIN channels c          ON c.channel_id = cf.channel_id
		LEFT JOIN channel_stars cs2   ON cs2.channel_id = cf.channel_id AND cs2.user_id = ''
		LEFT JOIN channel_settings cs ON cs.channel_id = cf.channel_id
		LEFT JOIN channel_profiles cp ON cp.channel_id = cf.channel_id
		WHERE cf.user_id = ''
		ORDER BY COALESCE(NULLIF(cp.display_name,''), NULLIF(c.name,''), NULLIF(cp.handle,''), cf.channel_id) COLLATE NOCASE
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []model.Channel
	for rows.Next() {
		var ch model.Channel
		var lastChecked, createdAt sql.NullInt64
		var includeReposts sql.NullBool
		err := rows.Scan(
			&ch.ID, &ch.ChannelID, &ch.SourceID, &ch.Name,
			&ch.URL, &ch.Platform,
			&ch.IsStarred,
			&ch.Quality,
			&lastChecked, &createdAt,
			&includeReposts,
			&ch.Handle,
			&ch.DisplayName,
		)
		if err != nil {
			return nil, err
		}
		// Any channel surfaced by GetSubscribedChannels is, by
		// construction, followed.
		ch.IsSubscribed = true
		if includeReposts.Valid {
			v := includeReposts.Bool
			ch.IncludeReposts = &v
		}
		ch.LastChecked = millisToTimePtr(lastChecked)
		if t := millisToTimePtr(createdAt); t != nil {
			ch.CreatedAt = *t
		}
		applyChannelIDDefaults(&ch)
		ch.Platform = detectPlatform(ch.Platform, ch.URL)
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

func applyChannelIDDefaults(ch *model.Channel) {
	sourceID, name, urlValue, platform := channelDefaultsFromID(ch.ChannelID)
	if ch.SourceID == "" {
		ch.SourceID = sourceID
	}
	if ch.Name == "" {
		switch {
		case ch.DisplayName != "":
			ch.Name = ch.DisplayName
		case ch.Handle != "":
			ch.Name = ch.Handle
		default:
			ch.Name = name
		}
	}
	if ch.URL == "" {
		ch.URL = urlValue
	}
	if ch.Platform == "" {
		ch.Platform = platform
	}
}

func isSafeChannelDerivedID(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	if strings.Contains(id, "/") || strings.Contains(id, "\\") {
		return false
	}
	if strings.Contains(id, "..") {
		return false
	}
	if filepath.Clean(id) != id {
		return false
	}
	return true
}

func channelDefaultsFromID(channelID string) (sourceID, name, urlValue, platform string) {
	channelID = strings.TrimSpace(channelID)
	name = channelID
	lower := strings.ToLower(channelID)
	switch {
	case strings.HasPrefix(lower, "twitter_"):
		handle := strings.TrimSpace(channelID[len("twitter_"):])
		sourceID, name, platform = handle, handle, "twitter"
		if handle != "" {
			urlValue = "https://x.com/" + strings.TrimPrefix(handle, "@")
		}
	case strings.HasPrefix(lower, "x_"):
		handle := strings.TrimSpace(channelID[len("x_"):])
		sourceID, name, platform = handle, handle, "twitter"
		if handle != "" {
			urlValue = "https://x.com/" + strings.TrimPrefix(handle, "@")
		}
	case strings.HasPrefix(lower, "tiktok_"):
		handle := strings.TrimSpace(channelID[len("tiktok_"):])
		sourceID, name, platform = handle, handle, "tiktok"
		if handle != "" {
			urlValue = "https://www.tiktok.com/@" + strings.TrimPrefix(handle, "@")
		}
	case strings.HasPrefix(lower, "youtube_"):
		id := strings.TrimSpace(channelID[len("youtube_"):])
		sourceID, name, platform = id, id, "youtube"
		if id != "" {
			urlValue = "https://www.youtube.com/channel/" + id
		}
	}
	return sourceID, name, urlValue, platform
}

// GetAllVideoCountsByChannel returns a map of channel_id -> video count.
func (db *DB) GetAllVideoCountsByChannel() (map[string]int, error) {
	rows, err := db.conn.Query(`
		SELECT channel_id, COUNT(*) FROM videos GROUP BY channel_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var chID string
		var count int
		if err := rows.Scan(&chID, &count); err != nil {
			return nil, err
		}
		counts[chID] = count
	}
	return counts, rows.Err()
}

// detectPlatform corrects the platform based on URL patterns.
func detectPlatform(platform, url string) string {
	u, err := urlpkgParseHTTP(url)
	if err != nil {
		return platform
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	switch {
	case host == "tiktok.com" || strings.HasSuffix(host, ".tiktok.com"):
		return "tiktok"
	case host == "instagram.com" || strings.HasSuffix(host, ".instagram.com"):
		return "instagram"
	case host == "x.com" || strings.HasSuffix(host, ".x.com") || host == "twitter.com" || strings.HasSuffix(host, ".twitter.com"):
		return "twitter"
	default:
		return platform
	}
}

func urlpkgParseHTTP(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid URL")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("unsupported URL scheme")
	}
	return u, nil
}

// millisToTimePtr converts an INTEGER unix-millis DB column to a
// *time.Time. A zero or NULL value returns nil — the Go convention for
// "unset" — so existing consumers that compare against nil keep working.
func millisToTimePtr(ns sql.NullInt64) *time.Time {
	if !ns.Valid || ns.Int64 == 0 {
		return nil
	}
	t := time.UnixMilli(ns.Int64)
	return &t
}

// timePtrToMillis converts a *time.Time to a DB-suitable int64 millis
// value (0 for nil/zero), matching the `INTEGER NOT NULL DEFAULT 0`
// schema convention.
func timePtrToMillis(t *time.Time) int64 {
	if t == nil || t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// parseTimestampString converts a legacy string timestamp (as produced by
// Python-era exports) to unix-millis. Returns 0 for empty/unparsable
// input. Supports the common SQLite + RFC3339 + Twitter formats.
func parseTimestampString(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n > 1_000_000_000_000 {
			return n
		}
		if n > 0 {
			return n * 1000
		}
		return 0
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		time.RFC3339,
		"Mon Jan 02 15:04:05 +0000 2006",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UnixMilli()
		}
	}
	return 0
}

// ToggleChannelStar flips the star state in `channel_stars` for the single
// (server-side) user and records a sync change. Returns the new state.
func (db *DB) ToggleChannelStar(channelID string) (bool, error) {
	var newStarred bool
	err := db.WithWrite(func(tx *sql.Tx) error {
		var exists int
		err := tx.QueryRow(
			"SELECT COUNT(*) FROM channel_stars WHERE user_id = '' AND channel_id = ?",
			channelID,
		).Scan(&exists)
		if err != nil {
			return fmt.Errorf("read channel_stars: %w", err)
		}
		if exists > 0 {
			_, err = tx.Exec(
				"DELETE FROM channel_stars WHERE user_id = '' AND channel_id = ?",
				channelID,
			)
			if err != nil {
				return fmt.Errorf("delete channel_stars: %w", err)
			}
			newStarred = false
		} else {
			_, err = tx.Exec(
				"INSERT INTO channel_stars (user_id, channel_id, starred_at) VALUES ('', ?, ?)",
				channelID, time.Now().UnixMilli(),
			)
			if err != nil {
				return fmt.Errorf("insert channel_stars: %w", err)
			}
			newStarred = true
		}
		return db.recordSyncChangeTx(tx, "star", channelID, fmt.Sprintf(`{"starred":%t}`, newStarred))
	})
	return newStarred, err
}

func (db *DB) IsChannelStarred(channelID string) bool {
	var exists int
	err := db.conn.QueryRow(`
		SELECT 1
		FROM channel_stars
		WHERE user_id = '' AND channel_id = ?
		LIMIT 1
	`, channelID).Scan(&exists)
	return err == nil && exists == 1
}

// FollowChannel inserts a `channel_follows` row for the single user
// (user_id = ”). Idempotent.
func (db *DB) FollowChannel(channelID string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			INSERT OR IGNORE INTO channel_follows (user_id, channel_id, followed_at)
			VALUES ('', ?, ?)
		`, channelID, time.Now().UnixMilli())
		return err
	})
}

// UnfollowChannel removes the `channel_follows` row for the single user.
func (db *DB) UnfollowChannel(channelID string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM channel_follows WHERE user_id = '' AND channel_id = ?`, channelID)
		return err
	})
}

// IsChannelFollowed reports whether the single user follows the channel.
func (db *DB) IsChannelFollowed(channelID string) bool {
	var n int
	_ = db.conn.QueryRow(
		"SELECT COUNT(*) FROM channel_follows WHERE user_id = '' AND channel_id = ?",
		channelID,
	).Scan(&n)
	return n > 0
}

// GetChannelSettings reads a channel's settings from the channel_settings
// side table, merging global defaults for every NULL field.
func (db *DB) GetChannelSettings(channelID string) (*ChannelSettings, error) {
	row := db.conn.QueryRow(`
		SELECT COALESCE(c.quality,''), COALESCE(c.platform,''),
		       cs.max_videos, cs.download_subtitles,
		       cs.media_only, cs.media_download_limit, cs.include_reposts
		FROM channels c
		LEFT JOIN channel_settings cs ON cs.channel_id = c.channel_id
		WHERE c.channel_id = ?
	`, channelID)

	var s ChannelSettings
	var platform string
	var maxVideos, downloadSubtitles sql.NullInt64
	var mediaOnly, includeReposts sql.NullInt64
	var mediaDownloadLimit sql.NullInt64

	err := row.Scan(
		&s.Quality, &platform,
		&maxVideos, &downloadSubtitles,
		&mediaOnly, &mediaDownloadLimit, &includeReposts,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// DownloadSubtitles: channel override if non-nil, else global default.
	if downloadSubtitles.Valid {
		s.DownloadSubtitles = downloadSubtitles.Int64 != 0
	} else {
		v, _ := db.GetSetting("download_subtitles", "false")
		s.DownloadSubtitles = v == "1" || v == "true"
	}

	// MaxVideos: NULL = use global. Pick the right global by platform.
	if maxVideos.Valid && maxVideos.Int64 > 0 {
		s.MaxVideos = int(maxVideos.Int64)
	} else {
		s.MaxVideos = db.IntSetting(channelMaxVideosSettingKey(platform))
	}

	// Media/repost settings: channel override if non-nil, else global default.
	parseBoolSetting := func(key, fallback string) bool {
		v, _ := db.GetSetting(key, fallback)
		return v == "1" || v == "true"
	}

	if mediaOnly.Valid {
		s.MediaOnly = mediaOnly.Int64 != 0
	} else {
		s.MediaOnly = parseBoolSetting("media_only_default", "0")
	}

	if mediaDownloadLimit.Valid {
		s.MediaDownloadLimit = int(mediaDownloadLimit.Int64)
	} else {
		s.MediaDownloadLimit = db.IntSetting("media_download_limit_default")
	}

	if includeReposts.Valid {
		s.IncludeReposts = includeReposts.Int64 != 0
	} else {
		s.IncludeReposts = parseBoolSetting("include_reposts_default", "1")
	}

	return &s, nil
}

func channelMaxVideosSettingKey(platform string) string {
	switch platform {
	case "tiktok":
		return "shorts_max_videos"
	case "instagram":
		return "instagram_max_videos"
	default:
		return "youtube_max_videos"
	}
}

// UpdateChannelSettings updates provided fields for a channel. Channel-level
// columns (quality) are written to `channels`; per-channel
// settings (max_videos, download_subtitles, media_only, media_download_limit,
// include_reposts) are UPSERTed into `channel_settings`.
func (db *DB) UpdateChannelSettings(channelID string, fields map[string]any) error {
	channelCols := map[string]bool{
		"quality": true,
	}
	settingCols := map[string]bool{
		"max_videos":           true,
		"download_subtitles":   true,
		"media_only":           true,
		"media_download_limit": true,
		"include_reposts":      true,
	}

	var chClauses []string
	var chArgs []any
	var setCols []string
	var setArgs []any
	for col, val := range fields {
		switch {
		case channelCols[col]:
			chClauses = append(chClauses, fmt.Sprintf("%s=?", col))
			chArgs = append(chArgs, val)
		case settingCols[col]:
			setCols = append(setCols, col)
			setArgs = append(setArgs, val)
		default:
			return fmt.Errorf("UpdateChannelSettings: disallowed field %q", col)
		}
	}

	return db.WithWrite(func(tx *sql.Tx) error {
		if len(chClauses) > 0 {
			query := fmt.Sprintf(
				"UPDATE channels SET %s WHERE channel_id=?",
				strings.Join(chClauses, ", "),
			)
			args := append(chArgs, channelID)
			if _, err := tx.Exec(query, args...); err != nil {
				return err
			}
		}
		if len(setCols) > 0 {
			placeholders := make([]string, len(setCols))
			updates := make([]string, len(setCols))
			for i, col := range setCols {
				placeholders[i] = "?"
				updates[i] = fmt.Sprintf("%s=excluded.%s", col, col)
			}
			query := fmt.Sprintf(`
				INSERT INTO channel_settings (channel_id, %s, updated_at)
				VALUES (?, %s, ?)
				ON CONFLICT(channel_id) DO UPDATE SET %s, updated_at=excluded.updated_at
			`,
				strings.Join(setCols, ", "),
				strings.Join(placeholders, ", "),
				strings.Join(updates, ", "),
			)
			args := append([]any{channelID}, setArgs...)
			args = append(args, time.Now().UnixMilli())
			if _, err := tx.Exec(query, args...); err != nil {
				return err
			}
		}
		return nil
	})
}

// UpdateChannelName sets channels.name for channelID.
func (db *DB) UpdateChannelName(channelID, name string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE channels SET name=? WHERE channel_id=?`, name, channelID)
		return err
	})
}

// AddChannel inserts a new channel. Returns an error if channel_id already
// exists. Per-channel settings (max_videos, download_subtitles, media_*,
// include_reposts) are written to the channel_settings side table via
// UpdateChannelSettings once the channel exists.
func (db *DB) AddChannel(ch model.Channel) error {
	seq := db.NextSyncSeq()
	return db.WithWrite(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`
			INSERT INTO channels
				(channel_id, source_id, name, url, platform,
				 quality, sync_seq)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`,
			ch.ChannelID,
			nilIfEmpty(ch.SourceID),
			ch.Name,
			nilIfEmpty(ch.URL),
			nilIfEmpty(ch.Platform),
			nilIfEmpty(ch.Quality),
			seq,
		); err != nil {
			return err
		}
		if ch.IsSubscribed {
			if _, err := tx.Exec(`
				INSERT OR IGNORE INTO channel_follows (user_id, channel_id, followed_at)
				VALUES ('', ?, ?)
			`, ch.ChannelID, time.Now().UnixMilli()); err != nil {
				return err
			}
		}
		if ch.IsStarred {
			if _, err := tx.Exec(`
				INSERT OR IGNORE INTO channel_stars (user_id, channel_id, starred_at)
				VALUES ('', ?, ?)
			`, ch.ChannelID, time.Now().UnixMilli()); err != nil {
				return err
			}
		}
		return nil
	})
}

// GetChannelByID returns a single channel by its channel_id.
// Returns sql.ErrNoRows (wrapped) if not found.
func (db *DB) GetChannelByID(channelID string) (model.Channel, error) {
	var ch model.Channel
	var lastChecked, createdAt sql.NullInt64
	var isSubscribed, isStarred int
	err := db.conn.QueryRow(`
		SELECT c.id, c.channel_id, COALESCE(c.source_id,''), c.name,
		       COALESCE(c.url,''), COALESCE(c.platform,'youtube'),
		       CASE WHEN cf.channel_id IS NOT NULL THEN 1 ELSE 0 END,
		       CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END,
		       COALESCE(c.quality,''),
		       c.last_checked, c.created_at
		FROM channels c
		LEFT JOIN channel_follows cf ON cf.channel_id = c.channel_id AND cf.user_id = ''
		LEFT JOIN channel_stars   cs ON cs.channel_id = c.channel_id AND cs.user_id = ''
		WHERE c.channel_id = ?
	`, channelID).Scan(
		&ch.ID, &ch.ChannelID, &ch.SourceID, &ch.Name,
		&ch.URL, &ch.Platform,
		&isSubscribed, &isStarred,
		&ch.Quality,
		&lastChecked, &createdAt,
	)
	ch.IsSubscribed = isSubscribed != 0
	ch.IsStarred = isStarred != 0
	if err == sql.ErrNoRows {
		return ch, fmt.Errorf("channel not found: %s", channelID)
	}
	if err != nil {
		return ch, err
	}
	ch.LastChecked = millisToTimePtr(lastChecked)
	if t := millisToTimePtr(createdAt); t != nil {
		ch.CreatedAt = *t
	}
	ch.Platform = detectPlatform(ch.Platform, ch.URL)
	return ch, nil
}

// DeleteChannel removes a channel and all its associated data (avatars, media files, videos).
// Returns an error if the channel does not exist.
func (db *DB) DeleteChannel(channelID string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		// Verify existence first
		var exists int
		if err := tx.QueryRow(
			"SELECT COUNT(*) FROM channels WHERE channel_id = ?", channelID,
		).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return fmt.Errorf("channel not found: %s", channelID)
		}

		// Delete media_files for this channel's videos (thumbnails and feed media)
		if _, err := tx.Exec(`
			DELETE FROM media_files WHERE owner_type IN ('thumbnail', 'feed_media')
			AND owner_id IN (SELECT video_id FROM videos WHERE channel_id = ?)
		`, channelID); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM videos WHERE channel_id = ?", channelID); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM channel_settings WHERE channel_id = ?", channelID); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM channel_follows WHERE channel_id = ?", channelID); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM channel_stars WHERE channel_id = ?", channelID); err != nil {
			return err
		}
		// twitter_profiles + banner media_files are kept as cache — the
		// channel page should render the hero even for non-subscribed handles,
		// and re-following skips a refetch within the TTL window.
		if _, err := tx.Exec("DELETE FROM channels WHERE channel_id = ?", channelID); err != nil {
			return err
		}
		return nil
	})
}

// PurgeUnfollowedChannelContent removes content that is no longer owned by the
// active follow set. Bookmarks protect video channels; followed Twitter repost
// references protect tweets after an author/source unfollow.
func (db *DB) PurgeUnfollowedChannelContent(channelID string, username string) ([]model.Video, error) {
	var deletedVideos []model.Video
	platform, err := db.clearUnfollowState(channelID)
	if err != nil {
		return nil, err
	}
	if platform == "" {
		_, _, _, platform = channelDefaultsFromID(channelID)
	}

	err = db.WithWrite(func(tx *sql.Tx) error {
		if strings.EqualFold(platform, "twitter") || strings.HasPrefix(strings.ToLower(channelID), "twitter_") {
			var errTwitter error
			deletedVideos, errTwitter = db.purgeTwitterAfterUnfollowTx(tx, channelID, username)
			return errTwitter
		}

		var errVideos error
		deletedVideos, errVideos = db.purgeVideoChannelAfterUnfollowTx(tx, channelID, username)
		return errVideos
	})
	return deletedVideos, err
}

func (db *DB) clearUnfollowState(channelID string) (string, error) {
	var platform string
	err := db.WithWrite(func(tx *sql.Tx) error {
		err := tx.QueryRow(`SELECT COALESCE(platform, '') FROM channels WHERE channel_id = ?`, channelID).Scan(&platform)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM channel_follows WHERE user_id = '' AND channel_id = ?`, channelID); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM channel_stars WHERE user_id = '' AND channel_id = ?`, channelID); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM channel_settings WHERE channel_id = ?`, channelID); err != nil {
			return err
		}
		return nil
	})
	return platform, err
}

func (db *DB) purgeVideoChannelAfterUnfollowTx(tx *sql.Tx, channelID string, username string) ([]model.Video, error) {
	rows, err := tx.Query(`
		SELECT v.video_id, COALESCE(v.file_path, ''), COALESCE(v.thumbnail_path, '')
		FROM videos v
		WHERE v.channel_id = ?
		  AND NOT EXISTS (
		    SELECT 1
		    FROM bookmarks b
		    WHERE b.video_id = v.video_id
		      AND (b.user_id = '' OR b.user_id = ?)
		  )
	`, channelID, username)
	if err != nil {
		return nil, err
	}
	var deleted []model.Video
	for rows.Next() {
		var v model.Video
		if err := rows.Scan(&v.VideoID, &v.FilePath, &v.ThumbnailPath); err != nil {
			rows.Close()
			return nil, err
		}
		v.ChannelID = channelID
		deleted = append(deleted, v)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	mediaRows, err := tx.Query(`
		SELECT COALESCE(mf.file_path, '')
		FROM media_files mf
		WHERE COALESCE(mf.file_path, '') != ''
		  AND mf.owner_id IN (
		    SELECT v.video_id
		    FROM videos v
		    WHERE v.channel_id = ?
		      AND NOT EXISTS (
		        SELECT 1
		        FROM bookmarks b
		        WHERE b.video_id = v.video_id
		          AND (b.user_id = '' OR b.user_id = ?)
		      )
		  )
	`, channelID, username)
	if err != nil {
		return nil, err
	}
	for mediaRows.Next() {
		var path string
		if err := mediaRows.Scan(&path); err != nil {
			mediaRows.Close()
			return nil, err
		}
		if strings.TrimSpace(path) != "" {
			deleted = append(deleted, model.Video{ChannelID: channelID, FilePath: path})
		}
	}
	if err := mediaRows.Err(); err != nil {
		mediaRows.Close()
		return nil, err
	}
	mediaRows.Close()

	if _, err := tx.Exec(`
		DELETE FROM media_files
		WHERE owner_id IN (
		    SELECT v.video_id
		    FROM videos v
		    WHERE v.channel_id = ?
		      AND NOT EXISTS (
		        SELECT 1
		        FROM bookmarks b
		        WHERE b.video_id = v.video_id
		          AND (b.user_id = '' OR b.user_id = ?)
		      )
		  )
	`, channelID, username); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`
		DELETE FROM videos
		WHERE channel_id = ?
		  AND NOT EXISTS (
		    SELECT 1
		    FROM bookmarks b
		    WHERE b.video_id = videos.video_id
		      AND (b.user_id = '' OR b.user_id = ?)
		  )
	`, channelID, username); err != nil {
		return nil, err
	}

	var protected int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM videos WHERE channel_id = ?`, channelID).Scan(&protected); err != nil {
		return nil, err
	}
	if protected == 0 {
		profileMediaRows, err := tx.Query(`
			SELECT COALESCE(file_path, '')
			FROM media_files
			WHERE owner_id = ?
			  AND owner_type IN ('avatar', 'banner')
			  AND COALESCE(file_path, '') != ''
		`, channelID)
		if err != nil {
			return nil, err
		}
		for profileMediaRows.Next() {
			var path string
			if err := profileMediaRows.Scan(&path); err != nil {
				profileMediaRows.Close()
				return nil, err
			}
			if strings.TrimSpace(path) != "" {
				deleted = append(deleted, model.Video{ChannelID: channelID, FilePath: path})
			}
		}
		if err := profileMediaRows.Err(); err != nil {
			profileMediaRows.Close()
			return nil, err
		}
		profileMediaRows.Close()

		if _, err := tx.Exec(`DELETE FROM channel_profiles WHERE channel_id = ?`, channelID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`DELETE FROM media_files WHERE owner_id = ? AND owner_type IN ('avatar', 'banner')`, channelID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`DELETE FROM channels WHERE channel_id = ?`, channelID); err != nil {
			return nil, err
		}
	}
	return deleted, nil
}

func (db *DB) purgeTwitterAfterUnfollowTx(tx *sql.Tx, channelID string, username string) ([]model.Video, error) {
	handle := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(channelID)), "twitter_")
	if handle == "" {
		return nil, nil
	}
	candidate := `
		(lower(COALESCE(fi.author_handle, '')) = ?
		 OR lower(COALESCE(fi.source_handle, '')) = ?
		 OR lower(COALESCE(fi.retweeted_by_handle, '')) = ?)
	`
	protected := `
		EXISTS (SELECT 1 FROM bookmarks b WHERE b.video_id = fi.tweet_id AND (b.user_id = '' OR b.user_id = ?))
		OR EXISTS (SELECT 1 FROM feed_likes fl WHERE fl.tweet_id = fi.tweet_id AND fl.username = ?)
		OR EXISTS (
			SELECT 1
			FROM channel_follows cf
			WHERE cf.user_id = ''
			  AND cf.channel_id = 'twitter_' || lower(COALESCE(NULLIF(fi.source_handle, ''), NULLIF(fi.retweeted_by_handle, ''), ''))
			  AND lower(COALESCE(NULLIF(fi.source_handle, ''), NULLIF(fi.retweeted_by_handle, ''), '')) != ?
		)
		OR EXISTS (
			SELECT 1
			FROM retweet_sources rs
			JOIN channel_follows cf
			  ON cf.user_id = ''
			 AND cf.channel_id = 'twitter_' || lower(rs.retweeter_handle)
			WHERE rs.content_hash = COALESCE(fi.content_hash, '')
			  AND COALESCE(fi.content_hash, '') != ''
			  AND lower(rs.retweeter_handle) != ?
		)
		OR EXISTS (
			SELECT 1
			FROM feed_items parent
			JOIN channel_follows cf
			  ON cf.user_id = ''
			 AND cf.channel_id = 'twitter_' || lower(COALESCE(NULLIF(parent.source_handle, ''), NULLIF(parent.retweeted_by_handle, ''), parent.author_handle))
			WHERE parent.quote_tweet_id = fi.tweet_id
		)
		OR EXISTS (
			SELECT 1
			FROM feed_items sibling
			JOIN channel_follows cf
			  ON cf.user_id = ''
			 AND cf.channel_id = 'twitter_' || lower(COALESCE(NULLIF(sibling.source_handle, ''), NULLIF(sibling.retweeted_by_handle, ''), sibling.author_handle))
			WHERE sibling.tweet_id != fi.tweet_id
			  AND sibling.content_hash = fi.content_hash
			  AND COALESCE(fi.content_hash, '') != ''
		)
	`
	args := []any{handle, handle, handle, username, username, handle, handle}
	mediaRows, err := tx.Query(`
		SELECT COALESCE(file_path, '')
		FROM media_files
		WHERE owner_type IN ('feed_media', 'quote_media')
		  AND COALESCE(file_path, '') != ''
		  AND owner_id IN (
		    SELECT fi.tweet_id
		    FROM feed_items fi
		    WHERE `+candidate+`
		      AND NOT (`+protected+`)
		  )
	`, args...)
	if err != nil {
		return nil, err
	}
	var deleted []model.Video
	for mediaRows.Next() {
		var path string
		if err := mediaRows.Scan(&path); err != nil {
			mediaRows.Close()
			return nil, err
		}
		if strings.TrimSpace(path) != "" {
			deleted = append(deleted, model.Video{ChannelID: channelID, FilePath: path})
		}
	}
	if err := mediaRows.Err(); err != nil {
		mediaRows.Close()
		return nil, err
	}
	mediaRows.Close()

	if _, err := tx.Exec(`
		DELETE FROM media_files
		WHERE owner_type IN ('feed_media', 'quote_media')
		  AND owner_id IN (
		    SELECT fi.tweet_id
		    FROM feed_items fi
		    WHERE `+candidate+`
		      AND NOT (`+protected+`)
		  )
	`, args...); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`
		DELETE FROM feed_seen
		WHERE tweet_id IN (
		    SELECT fi.tweet_id
		    FROM feed_items fi
		    WHERE `+candidate+`
		      AND NOT (`+protected+`)
		  )
	`, args...); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`
		DELETE FROM feed_items AS fi
		WHERE `+candidate+`
		  AND NOT (`+protected+`)
	`, args...); err != nil {
		return nil, err
	}

	var retained int
	if err := tx.QueryRow(`
		SELECT COUNT(*)
		FROM feed_items fi
		WHERE `+candidate, handle, handle, handle).Scan(&retained); err != nil {
		return nil, err
	}
	if retained == 0 {
		if _, err := tx.Exec(`DELETE FROM channels WHERE channel_id = ?`, channelID); err != nil {
			return nil, err
		}
	}
	return deleted, nil
}

// GetVideosByChannel returns the video_id, file_path, and thumbnail_path for all videos
// belonging to the given channel.
func (db *DB) GetVideosByChannel(channelID string) ([]model.Video, error) {
	rows, err := db.conn.Query(`
		SELECT video_id, COALESCE(file_path,''), COALESCE(thumbnail_path,'')
		FROM videos
		WHERE channel_id = ?
		ORDER BY downloaded_at DESC
	`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var videos []model.Video
	for rows.Next() {
		var v model.Video
		if err := rows.Scan(&v.VideoID, &v.FilePath, &v.ThumbnailPath); err != nil {
			return nil, err
		}
		v.ChannelID = channelID
		videos = append(videos, v)
	}
	return videos, rows.Err()
}

// GetUnsubscribedChannelIDs returns channel_ids with no channel_follows row.
func (db *DB) GetUnsubscribedChannelIDs() ([]string, error) {
	rows, err := db.conn.Query(`
		SELECT c.channel_id
		FROM channels c
		LEFT JOIN channel_follows cf ON cf.channel_id = c.channel_id
		WHERE cf.channel_id IS NULL
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetStarredChannelIDs returns the set of followed + starred channel_ids.
func (db *DB) GetStarredChannelIDs() (map[string]bool, error) {
	rows, err := db.conn.Query(`
		SELECT cs.channel_id
		FROM channel_stars cs
		INNER JOIN channel_follows cf ON cf.channel_id = cs.channel_id AND cf.user_id = ''
		WHERE cs.user_id = ''
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	starred := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		starred[id] = true
	}
	return starred, rows.Err()
}

// ClearChannelSettings removes all per-channel overrides for a channel so
// every setting falls back to the global default. Implemented as a single
// row delete from channel_settings.
func (db *DB) ClearChannelSettings(channelID string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM channel_settings WHERE channel_id=?`, channelID)
		return err
	})
}

// ResolveSubscribeURL returns the canonical URL to re-subscribe to a channel.
// Prefers channels.url if present; otherwise reconstructs from the platform prefix
// so Android clients never have to. Returns "" for unknown prefixes or empty input.
func (db *DB) ResolveSubscribeURL(channelID string) string {
	if channelID == "" {
		return ""
	}
	var url string
	if err := db.conn.QueryRow(`SELECT COALESCE(url, '') FROM channels WHERE channel_id = ?`, channelID).Scan(&url); err == nil && url != "" {
		return url
	}
	idx := strings.IndexByte(channelID, '_')
	if idx < 0 || idx == len(channelID)-1 {
		return ""
	}
	prefix, handle := channelID[:idx], channelID[idx+1:]
	switch prefix {
	case "twitter", "x":
		return "https://x.com/" + handle
	case "tiktok":
		return "https://tiktok.com/@" + handle
	case "instagram":
		return "https://instagram.com/" + handle
	case "youtube":
		return "https://youtube.com/channel/" + handle
	}
	return ""
}
