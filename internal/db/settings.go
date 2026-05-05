package db

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/screwys/igloo/internal/model"
	"github.com/screwys/igloo/internal/settings"
)

// IntSetting reads an integer setting from the DB, falling back to
// settings.Defaults[key] when the row is absent or unparseable. This is the
// canonical way for backend code to read a defaulted int setting.
func (db *DB) IntSetting(key string) int {
	v, err := db.GetSetting(key, "")
	if err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return settings.IntDefault(key)
}

// FloatSetting reads a float setting from the DB, falling back to fallback when
// the row is absent or unparseable.
func (db *DB) FloatSetting(key string, fallback float64) float64 {
	v, err := db.GetSetting(key, "")
	if err == nil && v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			return n
		}
	}
	return fallback
}

// BoolSetting reads a boolean setting from the DB, falling back to
// settings.Defaults[key] when the row is absent or unparseable.
func (db *DB) BoolSetting(key string) bool {
	v, err := db.GetSetting(key, "")
	if err == nil && strings.TrimSpace(v) != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	if def, ok := settings.Defaults[key].(bool); ok {
		return def
	}
	return false
}

func NormalizeMomentsTab(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "stories":
		return "stories"
	case "following":
		return "following"
	default:
		return "all"
	}
}

func NormalizeMomentsCursorScope(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "all":
		return "all", true
	case "following":
		return "following", true
	default:
		return "", false
	}
}

func NormalizeStoriesWindowHours(hours int) int {
	if hours < 1 {
		return 1
	}
	if hours > 168 {
		return 168
	}
	return hours
}

func IsValidRetentionDays(days int) bool {
	switch days {
	case 0, 1, 2, 3, 7, 14, 30, 60, 90:
		return true
	default:
		return false
	}
}

func (db *DB) MomentsDefaultTab() string {
	v, _ := db.GetSetting("moments_default_tab", "")
	return NormalizeMomentsTab(v)
}

func (db *DB) StoriesWindowHours() int {
	return NormalizeStoriesWindowHours(db.IntSetting("stories_window_hours"))
}

func (db *DB) MomentsIncludeRepostsEnabled() bool {
	return db.BoolSetting("moments_include_reposts_default")
}

func (db *DB) MomentsIncludeRepostsForChannel(channelID string) bool {
	if !db.MomentsIncludeRepostsEnabled() {
		return false
	}
	return db.channelIncludeRepostsEnabled(channelID)
}

func (db *DB) InstagramIncludeTaggedEnabled() bool {
	return db.BoolSetting("instagram_include_tagged_default")
}

func (db *DB) InstagramIncludeTaggedForChannel(channelID string) bool {
	if !db.InstagramIncludeTaggedEnabled() {
		return false
	}
	return db.channelIncludeRepostsEnabled(channelID)
}

func (db *DB) channelIncludeRepostsEnabled(channelID string) bool {
	var raw sql.NullInt64
	err := db.conn.QueryRow(`SELECT include_reposts FROM channel_settings WHERE channel_id = ?`, channelID).Scan(&raw)
	if err != nil || !raw.Valid {
		return true
	}
	return raw.Int64 != 0
}

// GetSetting returns a setting value, or fallback if not found or empty.
// Falls back to the legacy user_id='feed' row for settings migrated from
// the Python era that have not yet been saved under the default user.
func (db *DB) GetSetting(key, fallback string) (string, error) {
	var val string
	err := db.conn.QueryRow(`
		SELECT COALESCE(
			NULLIF((SELECT value FROM settings WHERE key = ? AND user_id = ''), ''),
			NULLIF((SELECT value FROM settings WHERE key = ? AND user_id = 'feed'), ''),
			?
		)`, key, key, fallback,
	).Scan(&val)
	if err != nil {
		return fallback, err
	}
	return val, nil
}

// GetStats returns aggregate database statistics for the sidebar.
func (db *DB) GetStats() (model.DBStats, error) {
	var s model.DBStats
	db.conn.QueryRow("SELECT COUNT(*) FROM channel_follows WHERE user_id = ''").Scan(&s.TotalChannels)
	db.conn.QueryRow("SELECT COUNT(*) FROM videos").Scan(&s.TotalVideos)
	db.conn.QueryRow("SELECT COUNT(*) FROM feed_items").Scan(&s.TotalFeedItems)

	var pageCount, pageSize int64
	db.conn.QueryRow("PRAGMA page_count").Scan(&pageCount)
	db.conn.QueryRow("PRAGMA page_size").Scan(&pageSize)
	s.DatabaseSizeMB = float64(pageCount*pageSize) / (1024 * 1024)

	return s, nil
}

// AuthUser represents a configured user from the auth_users setting.
type AuthUser struct {
	Username  string   `json:"username"`
	Password  string   `json:"password"`
	Role      string   `json:"role"`
	Platforms []string `json:"platforms"`
}

// GetAuthUsers returns the configured auth users from settings.
func (db *DB) GetAuthUsers() ([]AuthUser, error) {
	val, err := db.GetSetting("auth_users", "[]")
	if err != nil {
		return nil, err
	}
	var users []AuthUser
	if err := json.Unmarshal([]byte(val), &users); err != nil {
		return nil, nil
	}
	return users, nil
}

// DeleteSetting removes a setting row for the given user_id/key.
func (db *DB) DeleteSetting(userID, key string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec("DELETE FROM settings WHERE user_id = ? AND key = ?", userID, key)
		return err
	})
}

// SetSetting sets a global setting value (upsert).
func (db *DB) SetSetting(userID, key, value string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			INSERT INTO settings (user_id, key, value) VALUES (?, ?, ?)
			ON CONFLICT(user_id, key) DO UPDATE SET value = excluded.value
		`, userID, key, value)
		return err
	})
}

// SyncChange represents a sync changelog record.
type SyncChange struct {
	Version     int64
	Type        string
	ItemID      string
	Value       json.RawMessage
	CreatedAtMs int64
}

// GetSyncChanges returns sync changes since a version, up to limit.
func (db *DB) GetSyncChanges(sinceVersion int64, limit int) ([]SyncChange, bool, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	rows, err := db.conn.Query(`
		SELECT version, type, item_id, value, created_at
		FROM sync_changes WHERE version > ?
		ORDER BY version ASC
		LIMIT ?
	`, sinceVersion, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var changes []SyncChange
	for rows.Next() {
		var c SyncChange
		var valueStr string
		if err := rows.Scan(&c.Version, &c.Type, &c.ItemID, &valueStr, &c.CreatedAtMs); err != nil {
			return nil, false, err
		}
		// Store as raw JSON so it serializes as an object, not a double-encoded string.
		if json.Valid([]byte(valueStr)) {
			c.Value = json.RawMessage(valueStr)
		} else {
			c.Value = json.RawMessage(`{}`)
		}
		changes = append(changes, c)
	}
	truncated := len(changes) > limit
	if truncated {
		changes = changes[:limit]
	}
	return changes, truncated, rows.Err()
}

// GetMutationSyncChanges returns client-applicable interaction changes since a
// sync_changes version for userID. Android consumes this inbound user-state
// stream through mutation deltas; it intentionally excludes server-internal
// signals such as media_ready while preserving global version numbers so
// clients can store one opaque cursor.
func (db *DB) GetMutationSyncChanges(userID string, sinceVersion int64, limit int) ([]SyncChange, bool, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	cursor := sinceVersion
	changes := make([]SyncChange, 0, limit)
	for len(changes) <= limit {
		page, pageTruncated, err := db.getMutationSyncChangeCandidates(cursor, 500)
		if err != nil {
			return nil, false, err
		}
		if len(page) == 0 {
			break
		}
		lastVersion := cursor
		for _, c := range page {
			if c.Version > lastVersion {
				lastVersion = c.Version
			}
			include, err := db.includeMutationSyncChangeForUser(userID, c)
			if err != nil {
				return nil, false, err
			}
			if !include {
				continue
			}
			changes = append(changes, c)
			if len(changes) > limit {
				break
			}
		}
		if len(changes) > limit || !pageTruncated || lastVersion <= cursor {
			break
		}
		cursor = lastVersion
	}
	truncated := len(changes) > limit
	if truncated {
		changes = changes[:limit]
	}
	return changes, truncated, nil
}

func (db *DB) getMutationSyncChangeCandidates(sinceVersion int64, limit int) ([]SyncChange, bool, error) {
	rows, err := db.conn.Query(`
		SELECT version, type, item_id, value, created_at
		FROM sync_changes
		WHERE version > ?
		  AND type IN (
		    'like', 'bookmark', 'seen', 'mute',
		    'follow', 'subscribe', 'unsubscribe', 'star', 'channel_setting',
		    'moment_view', 'moments_cursor',
		    'progress', 'watch_progress', 'video_watched',
		    'create_category', 'bookmark_category', 'bookmark_alias'
		  )
		ORDER BY version ASC
		LIMIT ?
	`, sinceVersion, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var changes []SyncChange
	for rows.Next() {
		var c SyncChange
		var valueStr string
		if err := rows.Scan(&c.Version, &c.Type, &c.ItemID, &valueStr, &c.CreatedAtMs); err != nil {
			return nil, false, err
		}
		if json.Valid([]byte(valueStr)) {
			c.Value = json.RawMessage(valueStr)
		} else {
			c.Value = json.RawMessage(`{}`)
		}
		changes = append(changes, c)
	}
	truncated := len(changes) > limit
	if truncated {
		changes = changes[:limit]
	}
	return changes, truncated, rows.Err()
}

func (db *DB) includeMutationSyncChangeForUser(userID string, c SyncChange) (bool, error) {
	switch c.Type {
	case "bookmark_category", "create_category":
		return db.bookmarkCategorySyncChangeForUser(userID, c)
	default:
		return true, nil
	}
}

func (db *DB) bookmarkCategorySyncChangeForUser(userID string, c SyncChange) (bool, error) {
	var payload struct {
		UserID     *string `json:"user_id"`
		CategoryID int64   `json:"category_id"`
	}
	if len(c.Value) > 0 {
		_ = json.Unmarshal(c.Value, &payload)
	}
	if payload.UserID != nil {
		return *payload.UserID == userID, nil
	}

	categoryID := payload.CategoryID
	if categoryID == 0 {
		categoryID, _ = strconv.ParseInt(strings.TrimSpace(c.ItemID), 10, 64)
	}
	if categoryID <= 0 {
		return false, nil
	}
	var owner string
	err := db.conn.QueryRow("SELECT user_id FROM bookmark_categories WHERE id = ?", categoryID).Scan(&owner)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return owner == userID, nil
}

// RecordSyncChange inserts a sync change record.
func (db *DB) RecordSyncChange(changeType, itemID, valueJSON string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		return db.recordSyncChangeTx(tx, changeType, itemID, valueJSON)
	})
}

// recordSyncChangeTx inserts a sync change within an existing transaction.
func (db *DB) recordSyncChangeTx(tx *sql.Tx, changeType, itemID, valueJSON string) error {
	_, err := tx.Exec(
		"INSERT INTO sync_changes (type, item_id, value, created_at) VALUES (?, ?, ?, CAST(strftime('%s','now') AS INTEGER) * 1000)",
		changeType, itemID, valueJSON,
	)
	return err
}

// GetCurrentSyncVersion returns the latest sync_changes version.
func (db *DB) GetCurrentSyncVersion() (int64, error) {
	var version int64
	err := db.conn.QueryRow("SELECT COALESCE(MAX(version), 0) FROM sync_changes").Scan(&version)
	return version, err
}
