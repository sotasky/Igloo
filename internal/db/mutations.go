package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// #11 — mutation apply-fns. Each writes to its side table + emits a
// sync_changes row in one transaction and returns the new
// sync_changes.version. Handlers surface that version as
// envelope.sync_version (plan #10 + #11).

// MutationResult is what every apply-fn returns to the handler.
type MutationResult struct {
	SyncVersion int64
}

// errInvalidAction fires when a toggle endpoint receives action not in
// {"set", "clear"}. Handlers translate to envelope error_code=invalid_body.
var errInvalidAction = errors.New("action must be 'set' or 'clear'")

// applyMutation wraps the common pattern: write in a tx, emit
// sync_changes with (type, item_id, valueJSON), read back max(version)
// from the same tx, return it.
func (db *DB) applyMutation(kind, itemID string, value any, writes func(tx *sql.Tx) error) (MutationResult, error) {
	var version int64
	err := db.WithWrite(func(tx *sql.Tx) error {
		if err := writes(tx); err != nil {
			return err
		}
		valueJSON := "{}"
		if value != nil {
			if b, err := json.Marshal(value); err == nil {
				valueJSON = string(b)
			}
		}
		if err := db.recordSyncChangeTx(tx, kind, itemID, valueJSON); err != nil {
			return err
		}
		return tx.QueryRow("SELECT COALESCE(MAX(version), 0) FROM sync_changes").Scan(&version)
	})
	if err != nil {
		return MutationResult{}, err
	}
	return MutationResult{SyncVersion: version}, nil
}

// ── like (toggle) ────────────────────────────────────────────────────

func (db *DB) ApplyLikeMutation(username, tweetID, action string, updatedAtMs int64) (MutationResult, error) {
	if updatedAtMs == 0 {
		updatedAtMs = time.Now().UnixMilli()
	}
	if action != "set" && action != "clear" {
		return MutationResult{}, errInvalidAction
	}

	value := map[string]any{"action": action, "liked": action == "set", "updated_at_ms": updatedAtMs}
	var version int64
	err := db.WithWrite(func(tx *sql.Tx) error {
		switch action {
		case "set":
			if _, err := tx.Exec(
				`INSERT OR IGNORE INTO feed_likes (username, tweet_id, liked_at) VALUES (?, ?, ?)`,
				username, tweetID, updatedAtMs,
			); err != nil {
				return err
			}
			if _, err := tx.Exec(
				`INSERT OR IGNORE INTO feed_seen (username, tweet_id, seen_at) VALUES (?, ?, ?)`,
				username, tweetID, updatedAtMs,
			); err != nil {
				return err
			}
			if err := db.bumpFeedItemAndSiblingsSyncSeqTx(tx, tweetID); err != nil {
				return err
			}
			valueJSON, _ := json.Marshal(value)
			if err := db.recordSyncChangeTx(tx, "like", tweetID, string(valueJSON)); err != nil {
				return err
			}
			seenValueJSON, _ := json.Marshal(map[string]any{
				"tweet_ids":     []string{tweetID},
				"updated_at_ms": updatedAtMs,
			})
			if err := db.recordSyncChangeTx(tx, "seen", tweetID, string(seenValueJSON)); err != nil {
				return err
			}
		case "clear":
			if _, err := tx.Exec(
				`DELETE FROM feed_likes WHERE username = ? AND tweet_id = ?`,
				username, tweetID,
			); err != nil {
				return err
			}
			if err := db.bumpFeedItemAndSiblingsSyncSeqTx(tx, tweetID); err != nil {
				return err
			}
			valueJSON, _ := json.Marshal(value)
			if err := db.recordSyncChangeTx(tx, "like", tweetID, string(valueJSON)); err != nil {
				return err
			}
		}
		return tx.QueryRow("SELECT COALESCE(MAX(version), 0) FROM sync_changes").Scan(&version)
	})
	if err != nil {
		return MutationResult{}, err
	}
	return MutationResult{SyncVersion: version}, nil
}

// ── bookmark (toggle + metadata) ─────────────────────────────────────

type BookmarkMutation struct {
	VideoID        string
	Action         string // "set" | "clear"
	CategoryID     *int64
	CustomTitle    string
	AccountHandles string
	MediaIndices   string
	UpdatedAtMs    int64
}

func (db *DB) ApplyBookmarkMutation(userID string, m BookmarkMutation) (MutationResult, error) {
	return db.applyMutation("bookmark", m.VideoID, map[string]any{
		"video_id":        m.VideoID,
		"action":          m.Action,
		"bookmarked":      m.Action == "set",
		"category_id":     m.CategoryID,
		"custom_title":    m.CustomTitle,
		"account_handles": m.AccountHandles,
		"media_indices":   m.MediaIndices,
		"updated_at_ms":   m.UpdatedAtMs,
	}, func(tx *sql.Tx) error {
		switch m.Action {
		case "set":
			var catID int64
			if m.CategoryID != nil {
				catID = *m.CategoryID
			}
			_, err := tx.Exec(`
				INSERT INTO bookmarks (user_id, video_id, category_id,
				  custom_title, account_handles, media_indices, bookmarked_at)
				VALUES (?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(user_id, video_id) DO UPDATE SET
				  category_id = excluded.category_id,
				  custom_title = excluded.custom_title,
				  account_handles = excluded.account_handles,
				  media_indices = excluded.media_indices`,
				userID, m.VideoID, catID, m.CustomTitle, m.AccountHandles, m.MediaIndices, m.UpdatedAtMs,
			)
			if err != nil {
				return err
			}
			return db.bumpBookmarkTargetSyncSeqTx(tx, m.VideoID)
		case "clear":
			_, err := tx.Exec(
				`DELETE FROM bookmarks WHERE user_id = ? AND video_id = ?`,
				userID, m.VideoID,
			)
			if err != nil {
				return err
			}
			return db.bumpBookmarkTargetSyncSeqTx(tx, m.VideoID)
		}
		return errInvalidAction
	})
}

// ── follow / star / mute (toggles) ──────────────────────────────────

// Follow / star are single-user channel state — like FollowChannel and
// ToggleChannelStar, they always write user_id=''. The mutation API
// signatures drop the username arg to match ApplyMuteMutation.

func (db *DB) ApplyFollowMutation(channelID, action string, updatedAtMs int64) (MutationResult, error) {
	if updatedAtMs == 0 {
		updatedAtMs = time.Now().UnixMilli()
	}
	if action == "set" {
		sourceID, _, _, platform := channelDefaultsFromID(channelID)
		if platform != "" && !isSafeChannelDerivedID(sourceID) {
			return MutationResult{}, fmt.Errorf("invalid channel_id")
		}
	}
	return db.applyMutation("follow", channelID,
		map[string]any{"action": action, "followed": action == "set", "updated_at_ms": updatedAtMs},
		func(tx *sql.Tx) error {
			switch action {
			case "set":
				if err := db.ensureChannelStubForFollowTx(tx, channelID, updatedAtMs); err != nil {
					return err
				}
				_, err := tx.Exec(
					`INSERT OR IGNORE INTO channel_follows (user_id, channel_id, followed_at) VALUES ('', ?, ?)`,
					channelID, updatedAtMs,
				)
				return err
			case "clear":
				_, err := tx.Exec(
					`DELETE FROM channel_follows WHERE user_id = '' AND channel_id = ?`,
					channelID,
				)
				return err
			}
			return errInvalidAction
		})
}

func (db *DB) ensureChannelStubForFollowTx(tx *sql.Tx, channelID string, updatedAtMs int64) error {
	sourceID, name, urlValue, platform := channelDefaultsFromID(channelID)

	var profileHandle, profileName, profilePlatform string
	_ = tx.QueryRow(`
		SELECT COALESCE(handle, ''), COALESCE(display_name, ''), COALESCE(platform, '')
		FROM channel_profiles
		WHERE channel_id = ?
	`, channelID).Scan(&profileHandle, &profileName, &profilePlatform)

	if sourceID == "" && profileHandle != "" {
		sourceID = profileHandle
	}
	if name == "" || name == channelID {
		switch {
		case profileName != "":
			name = profileName
		case profileHandle != "":
			name = profileHandle
		default:
			name = channelID
		}
	}
	if platform == "" && profilePlatform != "" {
		platform = profilePlatform
	}
	if updatedAtMs == 0 {
		updatedAtMs = time.Now().UnixMilli()
	}

	_, err := tx.Exec(`
		INSERT OR IGNORE INTO channels
			(channel_id, source_id, name, url, platform, sync_seq, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, channelID, nilIfEmpty(sourceID), name, nilIfEmpty(urlValue), nilIfEmpty(platform), db.NextSyncSeq(), updatedAtMs)
	return err
}

func (db *DB) ApplyStarMutation(channelID, action string, updatedAtMs int64) (MutationResult, error) {
	return db.applyMutation("star", channelID,
		map[string]any{"action": action, "starred": action == "set", "updated_at_ms": updatedAtMs},
		func(tx *sql.Tx) error {
			switch action {
			case "set":
				_, err := tx.Exec(
					`INSERT OR IGNORE INTO channel_stars (user_id, channel_id, starred_at) VALUES ('', ?, ?)`,
					channelID, updatedAtMs,
				)
				return err
			case "clear":
				_, err := tx.Exec(
					`DELETE FROM channel_stars WHERE user_id = '' AND channel_id = ?`,
					channelID,
				)
				return err
			}
			return errInvalidAction
		})
}

func (db *DB) ApplyMuteMutation(handle, action string, updatedAtMs int64) (MutationResult, error) {
	return db.applyMutation("mute", handle,
		map[string]any{"action": action, "muted": action == "set", "updated_at_ms": updatedAtMs},
		func(tx *sql.Tx) error {
			switch action {
			case "set":
				_, err := tx.Exec(
					`INSERT OR IGNORE INTO muted_accounts (handle, muted_at) VALUES (?, ?)`,
					handle, updatedAtMs,
				)
				return err
			case "clear":
				_, err := tx.Exec(
					`DELETE FROM muted_accounts WHERE handle = ?`, handle,
				)
				return err
			}
			return errInvalidAction
		})
}

// ── channel_setting (PUT) ────────────────────────────────────────────

func (db *DB) ApplyChannelSettingMutation(channelID, field string, value any, updatedAtMs int64) (MutationResult, error) {
	// Whitelist per-channel setting columns to avoid SQL-injection via field.
	allowed := map[string]bool{
		"media_only": true, "include_reposts": true,
		"media_download_limit": true, "max_videos": true,
		"download_subtitles": true,
	}
	if !allowed[field] {
		return MutationResult{}, fmt.Errorf("unknown channel_setting field: %s", field)
	}
	return db.applyMutation("channel_setting", channelID,
		map[string]any{"field": field, "value": value, "updated_at_ms": updatedAtMs},
		func(tx *sql.Tx) error {
			// NULL-means-inherit sentinel: nil value clears the override.
			query := fmt.Sprintf(`
				INSERT INTO channel_settings (channel_id, %s, updated_at) VALUES (?, ?, ?)
				ON CONFLICT(channel_id) DO UPDATE SET
				  %s = excluded.%s,
				  updated_at = excluded.updated_at`, field, field, field)
			_, err := tx.Exec(query, channelID, value, updatedAtMs)
			return err
		})
}

// ── seen (batched) ───────────────────────────────────────────────────

func (db *DB) ApplySeenMutation(username string, tweetIDs []string, updatedAtMs int64) (MutationResult, error) {
	if len(tweetIDs) == 0 {
		v, err := db.GetCurrentSyncVersion()
		return MutationResult{SyncVersion: v}, err
	}
	// One apply-call, one sync_changes row keyed on the first tweet id
	// (value carries the full list so Android can mirror in one pass).
	return db.applyMutation("seen", tweetIDs[0],
		map[string]any{"tweet_ids": tweetIDs, "updated_at_ms": updatedAtMs},
		func(tx *sql.Tx) error {
			stmt, err := tx.Prepare(
				`INSERT OR IGNORE INTO feed_seen (username, tweet_id, seen_at) VALUES (?, ?, ?)`,
			)
			if err != nil {
				return err
			}
			defer stmt.Close()
			for _, id := range tweetIDs {
				if _, err := stmt.Exec(username, id, updatedAtMs); err != nil {
					return err
				}
			}
			return nil
		})
}

// ── moment_view ──────────────────────────────────────────────────────

func (db *DB) ApplyMomentViewMutation(username, videoID string, updatedAtMs int64) (MutationResult, error) {
	return db.applyMutation("moment_view", videoID,
		map[string]any{"updated_at_ms": updatedAtMs},
		func(tx *sql.Tx) error {
			_, err := tx.Exec(
				`INSERT INTO moment_views (username, video_id, viewed_at) VALUES (?, ?, ?)
				 ON CONFLICT(username, video_id) DO UPDATE SET viewed_at = excluded.viewed_at`,
				username, videoID, updatedAtMs,
			)
			return err
		})
}

// ── progress (PUT — LWW) ─────────────────────────────────────────────

func (db *DB) ApplyProgressMutation(username, videoID string, position, duration float64, source string, updatedAtMs int64) (MutationResult, error) {
	if updatedAtMs == 0 {
		updatedAtMs = time.Now().UnixMilli()
	}
	if position < 0 {
		position = 0
	}
	if duration < 0 {
		duration = 0
	}

	value := map[string]any{
		"position": position, "duration": duration,
		"source": source, "updated_at_ms": updatedAtMs,
	}

	var version int64
	err := db.WithWrite(func(tx *sql.Tx) error {
		var existingTs int64
		err := tx.QueryRow(
			`SELECT COALESCE(progress_updated_at_ms, 0) FROM watch_history WHERE user_id = ? AND video_id = ?`,
			username, videoID,
		).Scan(&existingTs)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil && existingTs >= updatedAtMs {
			return tx.QueryRow("SELECT COALESCE(MAX(version), 0) FROM sync_changes").Scan(&version)
		}

		_, err = tx.Exec(`
				INSERT INTO watch_history (user_id, video_id, playback_position, duration, progress_source, progress_updated_at_ms, last_watched)
				VALUES (?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(user_id, video_id) DO UPDATE SET
				  playback_position      = excluded.playback_position,
				  duration               = excluded.duration,
				  progress_source        = excluded.progress_source,
				  progress_updated_at_ms = excluded.progress_updated_at_ms,
				  last_watched           = excluded.last_watched`,
			username, videoID, position, duration, source, updatedAtMs, updatedAtMs,
		)
		if err != nil {
			return err
		}
		valueJSON, _ := json.Marshal(value)
		if err := db.recordSyncChangeTx(tx, "progress", videoID, string(valueJSON)); err != nil {
			return err
		}
		return tx.QueryRow("SELECT COALESCE(MAX(version), 0) FROM sync_changes").Scan(&version)
	})
	if err != nil {
		return MutationResult{}, err
	}
	return MutationResult{SyncVersion: version}, nil
}

// ── moments_cursor (PUT — LWW) ──────────────────────────────────────

func (db *DB) ApplyMomentsCursorMutation(username, videoID string, positionMs, updatedAtMs int64, scope string) (MutationResult, error) {
	if updatedAtMs == 0 {
		updatedAtMs = time.Now().UnixMilli()
	}
	normalizedScope, ok := NormalizeMomentsCursorScope(scope)
	if !ok {
		version, err := db.GetCurrentSyncVersion()
		if err != nil {
			return MutationResult{}, err
		}
		return MutationResult{SyncVersion: version}, nil
	}
	scope = normalizedScope

	value := map[string]any{"video_id": videoID, "position_ms": positionMs, "updated_at_ms": updatedAtMs, "scope": scope}

	var version int64
	err := db.WithWrite(func(tx *sql.Tx) error {
		var existingRaw string
		updatedKey := "shorts_cursor_updated_at_ms_" + username + "_" + scope
		err := tx.QueryRow(`SELECT value FROM settings WHERE key = ?`, updatedKey).Scan(&existingRaw)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == sql.ErrNoRows && scope == "all" {
			err = tx.QueryRow(`SELECT value FROM settings WHERE key = ?`, "shorts_cursor_updated_at_ms").Scan(&existingRaw)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
		}
		if err == nil {
			if existingMs, parseErr := strconv.ParseInt(existingRaw, 10, 64); parseErr == nil && existingMs >= updatedAtMs {
				return tx.QueryRow("SELECT COALESCE(MAX(version), 0) FROM sync_changes").Scan(&version)
			}
		}

		// The shorts cursor still has a legacy global setting read by web
		// page-load plus user-scoped settings consumed by newer clients.
		if scope == "all" {
			if _, err := tx.Exec(
				`INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)`,
				"shorts_cursor_video_id", videoID,
			); err != nil {
				return err
			}
			if _, err := tx.Exec(
				`INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)`,
				"shorts_cursor_updated_at_ms", fmt.Sprintf("%d", updatedAtMs),
			); err != nil {
				return err
			}
		}
		key := "shorts_cursor_video_id_" + username + "_" + scope
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)`,
			key, videoID,
		); err != nil {
			return err
		}
		posKey := "shorts_cursor_position_ms_" + username + "_" + scope
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)`,
			posKey, fmt.Sprintf("%d", positionMs),
		); err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)`,
			updatedKey, fmt.Sprintf("%d", updatedAtMs),
		); err != nil {
			return err
		}
		if scope == "all" {
			legacyUserKey := "shorts_cursor_video_id_" + username
			if _, err := tx.Exec(`INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)`, legacyUserKey, videoID); err != nil {
				return err
			}
			legacyPosKey := "shorts_cursor_position_ms_" + username
			if _, err := tx.Exec(`INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)`, legacyPosKey, fmt.Sprintf("%d", positionMs)); err != nil {
				return err
			}
		}
		valueJSON, _ := json.Marshal(value)
		if err := db.recordSyncChangeTx(tx, "moments_cursor", videoID, string(valueJSON)); err != nil {
			return err
		}
		return tx.QueryRow("SELECT COALESCE(MAX(version), 0) FROM sync_changes").Scan(&version)
	})
	if err != nil {
		return MutationResult{}, err
	}
	return MutationResult{SyncVersion: version}, nil
}

// ── create_category (provisional → real ID) ─────────────────────────

type CategoryCreated struct {
	CategoryID    int64
	ProvisionalID string
	SyncVersion   int64
}

func (db *DB) ApplyCreateCategoryMutation(userID, name, provisionalID string, updatedAtMs int64) (CategoryCreated, error) {
	if updatedAtMs == 0 {
		updatedAtMs = time.Now().UnixMilli()
	}
	var out CategoryCreated
	err := db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(
			`INSERT INTO bookmark_categories (user_id, name, created_at) VALUES (?, ?, ?)`,
			userID, name, updatedAtMs,
		)
		if err != nil {
			return err
		}
		categoryID, err := res.LastInsertId()
		if err != nil {
			return err
		}
		out.CategoryID = categoryID
		out.ProvisionalID = provisionalID

		valueJSON, _ := json.Marshal(map[string]any{
			"name":           name,
			"provisional_id": provisionalID,
			"category_id":    categoryID,
			"user_id":        userID,
			"updated_at_ms":  updatedAtMs,
		})
		if err := db.recordSyncChangeTx(tx, "create_category", fmt.Sprintf("%d", categoryID), string(valueJSON)); err != nil {
			return err
		}
		if err := db.recordBookmarkCategorySyncChangeTx(tx, userID, "set", categoryID, name, "", updatedAtMs); err != nil {
			return err
		}
		return tx.QueryRow("SELECT COALESCE(MAX(version), 0) FROM sync_changes").Scan(&out.SyncVersion)
	})
	return out, err
}

// ── bookmark_alias (PUT) ────────────────────────────────────────────

func (db *DB) ApplyBookmarkAliasMutation(originalHandle, displayAlias string, updatedAtMs int64) (MutationResult, error) {
	// bookmark_aliases is out-of-scope for single-user deployment, but
	// the sync_changes row still propagates the rename to Android's
	// alias store.
	return db.applyMutation("bookmark_alias", originalHandle,
		map[string]any{"display_alias": displayAlias, "updated_at_ms": updatedAtMs},
		func(tx *sql.Tx) error { return nil })
}
