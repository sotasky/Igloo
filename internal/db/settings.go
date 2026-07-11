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
	case "stories":
		return "stories", true
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
	err := db.reader().QueryRow(`SELECT include_reposts FROM channel_settings WHERE channel_id = ?`, channelID).Scan(&raw)
	if err != nil || !raw.Valid {
		return true
	}
	return raw.Int64 != 0
}

// GetSetting returns a setting value, or fallback if not found or empty.
func (db *DB) GetSetting(key, fallback string) (string, error) {
	var val string
	err := db.reader().QueryRow(
		`SELECT COALESCE((SELECT NULLIF(value, '') FROM settings WHERE key = ?), ?)`,
		key, fallback,
	).Scan(&val)
	if err != nil {
		return fallback, err
	}
	return val, nil
}

// GetStats returns aggregate database statistics for the sidebar.
func (db *DB) GetStats() (model.DBStats, error) {
	var s model.DBStats
	_ = db.reader().QueryRow("SELECT COUNT(*) FROM channel_follows").Scan(&s.TotalChannels)
	_ = db.reader().QueryRow("SELECT COUNT(*) FROM videos").Scan(&s.TotalVideos)
	_ = db.reader().QueryRow("SELECT COUNT(*) FROM feed_items").Scan(&s.TotalFeedItems)

	var pageCount, pageSize int64
	_ = db.reader().QueryRow("PRAGMA page_count").Scan(&pageCount)
	_ = db.reader().QueryRow("PRAGMA page_size").Scan(&pageSize)
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

// DeleteSetting removes a setting row.
func (db *DB) DeleteSetting(key string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec("DELETE FROM settings WHERE key = ?", key)
		return err
	})
}

// SetSetting sets a global setting value (upsert).
func (db *DB) SetSetting(key, value string) error {
	return db.WithWrite(func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			INSERT INTO settings (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value
		`, key, value)
		return err
	})
}
