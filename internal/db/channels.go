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

// GetSubscribedChannels returns every followed channel.
func (db *DB) GetSubscribedChannels() ([]model.Channel, error) {
	// Sort uses the rendered display name (display_name if set, else
	// channels.name) via the same COALESCE so rows sort how they render —
	// no more "sample_handle_ja" landing in the k-block while being shown as
	// "Example Display Name". COLLATE NOCASE keeps Latin case-insensitive; unicode
	// names fall back to codepoint order which is good enough for the
	// sidebar (users scan visually, not alphabetically).
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
		LEFT JOIN channel_stars cs2   ON cs2.channel_id = cf.channel_id
		LEFT JOIN channel_settings cs ON cs.channel_id = cf.channel_id
		LEFT JOIN channel_profiles cp ON cp.channel_id = cf.channel_id
		ORDER BY COALESCE(NULLIF(cp.display_name,''), NULLIF(c.name,''), NULLIF(cp.handle,''), cf.channel_id) COLLATE NOCASE
	`)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

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
	case strings.HasPrefix(lower, "instagram_"):
		handle := strings.TrimSpace(channelID[len("instagram_"):])
		sourceID, name, platform = handle, handle, "instagram"
		if handle != "" {
			urlValue = "https://www.instagram.com/" + strings.TrimPrefix(handle, "@") + "/"
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
	defer func() {
		_ = rows.Close()
	}()

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
// (server-side) user. Returns the new state.
func (db *DB) ToggleChannelStar(channelID string) (bool, error) {
	var newStarred bool
	err := db.WithWrite(func(tx *sql.Tx) error {
		nowMs := time.Now().UnixMilli()
		var exists int
		err := tx.QueryRow(
			"SELECT COUNT(*) FROM channel_stars WHERE channel_id = ?",
			channelID,
		).Scan(&exists)
		if err != nil {
			return fmt.Errorf("read channel_stars: %w", err)
		}
		action := "set"
		if exists > 0 {
			action = "clear"
		}
		result, err := mutateToggleTx(tx, "star", "channel_stars", "channel_id", channelID, action, nowMs)
		if err != nil {
			return err
		}
		newStarred = action == "set"
		if !result.Applied {
			return nil
		}
		return nil
	})
	return newStarred, err
}

func (db *DB) IsChannelStarred(channelID string) bool {
	var exists int
	err := db.conn.QueryRow(`
		SELECT 1
		FROM channel_stars
		WHERE channel_id = ?
		LIMIT 1
	`, channelID).Scan(&exists)
	return err == nil && exists == 1
}

// FollowChannel inserts a channel follow. Idempotent.
func (db *DB) FollowChannel(channelID string) error {
	_, err := db.MutateFollow(channelID, "set", 0)
	return err
}

// UnfollowChannel removes a channel follow.
func (db *DB) UnfollowChannel(channelID string) error {
	keys, err := db.PurgeUnfollowedChannelContent(channelID)
	if err != nil {
		return err
	}
	for _, key := range keys {
		if _, err := db.RemoveAssetFileIfUnreferenced(key); err != nil {
			return err
		}
	}
	return nil
}

// IsChannelFollowed reports whether the channel is followed.
func (db *DB) IsChannelFollowed(channelID string) bool {
	var n int
	_ = db.conn.QueryRow(
		"SELECT COUNT(*) FROM channel_follows WHERE channel_id = ?",
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
	settingFields := make(map[string]any)
	for col, val := range fields {
		switch {
		case channelCols[col]:
			chClauses = append(chClauses, fmt.Sprintf("%s=?", col))
			chArgs = append(chArgs, val)
		case settingCols[col]:
			settingFields[col] = val
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
		if len(settingFields) > 0 {
			updatedAt := time.Now().UnixMilli()
			_, err := mutateChannelSettingsTx(tx, channelID, settingFields, updatedAt, false)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// AddChannel inserts a new channel. Returns an error if channel_id already
// exists. Per-channel settings (max_videos, download_subtitles, media_*,
// include_reposts) are written to the channel_settings side table via
// UpdateChannelSettings once the channel exists.
func (db *DB) AddChannel(ch model.Channel) error {
	nowMs := time.Now().UnixMilli()
	return db.WithWrite(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`
			INSERT INTO channels
				(channel_id, source_id, name, url, platform,
				 quality)
			VALUES (?, ?, ?, ?, ?, ?)
		`,
			ch.ChannelID,
			nilIfEmpty(ch.SourceID),
			ch.Name,
			nilIfEmpty(ch.URL),
			nilIfEmpty(ch.Platform),
			nilIfEmpty(ch.Quality),
		); err != nil {
			return err
		}
		if ch.IsSubscribed {
			followedAt, err := advanceMutationClockTx(tx, "follow", ch.ChannelID, "set", nowMs)
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
		}
		if ch.IsStarred {
			starredAt, err := advanceMutationClockTx(tx, "star", ch.ChannelID, "set", nowMs)
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
		}
		identity := ch
		if strings.TrimSpace(identity.DisplayName) == "" {
			identity.DisplayName = ch.Name
		}
		if err := observeChannelProfileTx(tx, identity, nowMs); err != nil {
			return err
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
		LEFT JOIN channel_follows cf ON cf.channel_id = c.channel_id
		LEFT JOIN channel_stars   cs ON cs.channel_id = c.channel_id
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

// PurgeUnfollowedChannelContent removes content that is no longer owned by the
// active follow set. Bookmarks protect video channels; followed Twitter repost
// references protect tweets after an author/source unfollow.
func (db *DB) PurgeUnfollowedChannelContent(channelID string) ([]string, error) {
	result, err := db.MutateFollow(channelID, "clear", 0)
	return result.DeletedFileKeys, err
}

func (db *DB) purgeVideoChannelAfterUnfollowTx(tx *sql.Tx, channelID string) ([]string, error) {
	keys, err := queryAssetFileKeysTx(tx, `
		SELECT DISTINCT mo.file_path
		FROM assets a
		JOIN media_objects mo ON mo.object_id = a.object_id
		JOIN videos v ON v.video_id = a.owner_id
		WHERE a.owner_kind = v.owner_kind
		  AND mo.published_revision > 0 AND mo.file_path != ''
		  AND v.channel_id = ?
		  AND NOT EXISTS (
		        SELECT 1
		        FROM bookmarks b
		        WHERE b.video_id = v.video_id
		  )
	`, channelID)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`
		DELETE FROM assets
		WHERE id IN (
		  SELECT a.id
		  FROM assets a
		  JOIN videos v ON v.video_id = a.owner_id
		  WHERE a.owner_kind = v.owner_kind
		    AND v.channel_id = ?
		    AND NOT EXISTS (
		        SELECT 1
		        FROM bookmarks b
		        WHERE b.video_id = v.video_id
		    )
		  )
	`, channelID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`
		DELETE FROM videos
		WHERE channel_id = ?
		  AND NOT EXISTS (
		    SELECT 1
		    FROM bookmarks b
		    WHERE b.video_id = videos.video_id
		  )
	`, channelID); err != nil {
		return nil, err
	}

	return keys, nil
}

func (db *DB) purgeTwitterAfterUnfollowTx(tx *sql.Tx, channelID string) ([]string, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil, nil
	}
	candidate := `
		(COALESCE(fi.channel_id, '') = ?
		 OR COALESCE(fi.source_channel_id, '') = ?
		 OR COALESCE(fi.reposter_channel_id, '') = ?)
	`
	protected := `
		EXISTS (SELECT 1 FROM bookmarks b WHERE b.video_id = fi.tweet_id)
		OR EXISTS (SELECT 1 FROM feed_likes fl WHERE fl.tweet_id = fi.tweet_id)
		OR EXISTS (
			SELECT 1
			FROM channel_follows cf
			WHERE cf.channel_id = COALESCE(NULLIF(fi.reposter_channel_id, ''), NULLIF(fi.source_channel_id, ''), fi.channel_id)
			  AND cf.channel_id != ?
		)
		OR EXISTS (
			SELECT 1
			FROM retweet_sources rs
			JOIN channel_follows cf
				  ON cf.channel_id = rs.retweeter_channel_id
			WHERE rs.content_hash = COALESCE(fi.content_hash, '')
			  AND COALESCE(fi.content_hash, '') != ''
			  AND rs.retweeter_channel_id != ?
		)
		OR EXISTS (
			SELECT 1
			FROM feed_items parent
			JOIN channel_follows cf
				  ON cf.channel_id = COALESCE(NULLIF(parent.reposter_channel_id, ''), NULLIF(parent.source_channel_id, ''), parent.channel_id)
			WHERE parent.quote_tweet_id = fi.tweet_id
		)
		OR EXISTS (
			SELECT 1
			FROM feed_items sibling
			JOIN channel_follows cf
				  ON cf.channel_id = COALESCE(NULLIF(sibling.reposter_channel_id, ''), NULLIF(sibling.source_channel_id, ''), sibling.channel_id)
			WHERE sibling.tweet_id != fi.tweet_id
			  AND sibling.content_hash = fi.content_hash
			  AND COALESCE(fi.content_hash, '') != ''
		)
	`
	args := []any{channelID, channelID, channelID, channelID, channelID}
	keys, err := queryAssetFileKeysTx(tx, `
		SELECT DISTINCT mo.file_path
		FROM assets a
		JOIN media_objects mo ON mo.object_id = a.object_id
		WHERE a.owner_kind = 'tweet'
		  AND mo.published_revision > 0 AND mo.file_path != ''
		  AND a.owner_id IN (
		    SELECT fi.tweet_id
		    FROM feed_items fi
		    WHERE `+candidate+`
		      AND NOT (`+protected+`)
		  )
	`, args...)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`
		DELETE FROM assets
		WHERE owner_kind = 'tweet'
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
		WHERE `+candidate, channelID, channelID, channelID).Scan(&retained); err != nil {
		return nil, err
	}
	if retained == 0 {
		if _, err := tx.Exec(`DELETE FROM channels WHERE channel_id = ?`, channelID); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

func queryAssetFileKeysTx(tx *sql.Tx, query string, args ...any) ([]string, error) {
	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		if key = strings.TrimSpace(key); key != "" {
			keys = append(keys, key)
		}
	}
	return keys, rows.Err()
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
	defer func() {
		_ = rows.Close()
	}()

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
		INNER JOIN channel_follows cf ON cf.channel_id = cs.channel_id
	`)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

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

// ClearChannelSettings removes all per-channel overrides while retaining the
// row timestamp that rejects older offline settings writes.
func (db *DB) ClearChannelSettings(channelID string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		changed, err := mutateChannelSettingsTx(tx, channelID, map[string]any{
			"media_only":           nil,
			"include_reposts":      nil,
			"media_download_limit": nil,
			"max_videos":           nil,
			"download_subtitles":   nil,
		}, time.Now().UnixMilli(), false)
		if err != nil || !changed {
			return err
		}
		return nil
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
