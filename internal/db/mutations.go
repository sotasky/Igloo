package db

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
)

// errInvalidAction fires when a toggle endpoint receives action not in
// {"set", "clear"}. Handlers translate to envelope error_code=invalid_body.
var errInvalidAction = errors.New("action must be 'set' or 'clear'")

type invalidMutationError struct {
	message string
}

func (e *invalidMutationError) Error() string { return e.message }

func invalidMutation(message string) error {
	return &invalidMutationError{message: message}
}

func IsInvalidMutation(err error) bool {
	var invalid *invalidMutationError
	return errors.Is(err, errInvalidAction) || errors.As(err, &invalid)
}

type StaleMutationError struct {
	Kind               string
	ItemKey            string
	UpdatedAtMs        int64
	CurrentUpdatedAtMs int64
}

func (e *StaleMutationError) Error() string {
	return "a newer mutation already owns this state"
}

func IsStaleMutation(err error) bool {
	var stale *StaleMutationError
	return errors.As(err, &stale)
}

type MutationResult struct {
	CanonicalID     string
	Applied         bool
	Affected        int
	DeletedFileKeys []string
}

func mutationTimestamp(updatedAtMs int64) int64 {
	if updatedAtMs > 0 {
		return updatedAtMs
	}
	return time.Now().UnixMilli()
}

func claimMutationClockTx(tx *sql.Tx, kind, itemKey, action string, updatedAtMs int64) (bool, error) {
	if action != "set" && action != "clear" {
		return false, errInvalidAction
	}
	itemKey = strings.TrimSpace(itemKey)
	if itemKey == "" {
		return false, invalidMutation("mutation key required")
	}
	var currentAction string
	var currentUpdatedAtMs int64
	err := tx.QueryRow(`
		SELECT action, updated_at_ms
		FROM mutation_clocks
		WHERE kind = ? AND item_key = ?
	`, kind, itemKey).Scan(&currentAction, &currentUpdatedAtMs)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	if err == nil {
		switch {
		case updatedAtMs < currentUpdatedAtMs:
			return false, &StaleMutationError{
				Kind: kind, ItemKey: itemKey, UpdatedAtMs: updatedAtMs,
				CurrentUpdatedAtMs: currentUpdatedAtMs,
			}
		case updatedAtMs == currentUpdatedAtMs && action == currentAction:
			return false, nil
		case updatedAtMs == currentUpdatedAtMs && action == "set":
			return false, &StaleMutationError{
				Kind: kind, ItemKey: itemKey, UpdatedAtMs: updatedAtMs,
				CurrentUpdatedAtMs: currentUpdatedAtMs,
			}
		}
	}
	_, err = tx.Exec(`
		INSERT INTO mutation_clocks (kind, item_key, action, updated_at_ms)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(kind, item_key) DO UPDATE SET
		  action = excluded.action,
		  updated_at_ms = excluded.updated_at_ms
	`, kind, itemKey, action, updatedAtMs)
	return err == nil, err
}

func advanceMutationClockTx(tx *sql.Tx, kind, itemKey, action string, updatedAtMs int64) (int64, error) {
	if action != "set" && action != "clear" {
		return 0, errInvalidAction
	}
	itemKey = strings.TrimSpace(itemKey)
	if itemKey == "" {
		return 0, invalidMutation("mutation key required")
	}
	updatedAtMs = mutationTimestamp(updatedAtMs)
	var resolved int64
	err := tx.QueryRow(`
		INSERT INTO mutation_clocks (kind, item_key, action, updated_at_ms)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(kind, item_key) DO UPDATE SET
		  action = excluded.action,
		  updated_at_ms = CASE
		    WHEN mutation_clocks.updated_at_ms >= excluded.updated_at_ms
		    THEN mutation_clocks.updated_at_ms + 1
		    ELSE excluded.updated_at_ms
		  END
		RETURNING updated_at_ms
	`, kind, itemKey, action, updatedAtMs).Scan(&resolved)
	return resolved, err
}

func advanceMutationClocksTx(tx *sql.Tx, kind, action, itemsQuery string, args ...any) error {
	if action != "set" && action != "clear" {
		return errInvalidAction
	}
	query := `
		INSERT INTO mutation_clocks (kind, item_key, action, updated_at_ms)
		SELECT ?, TRIM(item_key), ?,
		       CASE WHEN updated_at_ms > 0 THEN updated_at_ms ELSE 1 END
		FROM (` + itemsQuery + `) AS mutation_items
		WHERE TRIM(item_key) != ''
		ON CONFLICT(kind, item_key) DO UPDATE SET
		  action = excluded.action,
		  updated_at_ms = CASE
		    WHEN mutation_clocks.updated_at_ms >= excluded.updated_at_ms
		    THEN mutation_clocks.updated_at_ms + 1
		    ELSE excluded.updated_at_ms
		  END
	`
	queryArgs := make([]any, 0, len(args)+2)
	queryArgs = append(queryArgs, kind, action)
	queryArgs = append(queryArgs, args...)
	_, err := tx.Exec(query, queryArgs...)
	return err
}

// ── like (toggle) ────────────────────────────────────────────────────

type LikeMutation struct {
	TweetID     string
	Action      string
	Fields      map[string]string
	UpdatedAtMs int64
}

func (db *DB) MutateLike(m LikeMutation) (MutationResult, error) {
	var result MutationResult
	rawTweetID := strings.TrimSpace(m.TweetID)
	m.UpdatedAtMs = mutationTimestamp(m.UpdatedAtMs)
	err := db.WithWrite(func(tx *sql.Tx) error {
		if m.Action == "set" && len(m.Fields) > 0 {
			if err := db.ensureFeedItemStubFromLikeTx(tx, rawTweetID, m.Fields); err != nil {
				return err
			}
		}
		var err error
		if m.Action == "set" {
			result.CanonicalID, err = db.resolveFeedStateIDForWriteTx(tx, rawTweetID)
		} else {
			result.CanonicalID, err = resolveFeedStateIDTx(tx, rawTweetID)
		}
		if err != nil {
			return err
		}
		result.Applied, err = claimMutationClockTx(tx, "like", result.CanonicalID, m.Action, m.UpdatedAtMs)
		if err != nil || !result.Applied {
			return err
		}
		switch m.Action {
		case "set":
			if _, err := tx.Exec(
				`INSERT INTO feed_likes (tweet_id, liked_at) VALUES (?, ?)
				 ON CONFLICT(tweet_id) DO UPDATE SET liked_at = excluded.liked_at`,
				result.CanonicalID, m.UpdatedAtMs,
			); err != nil {
				return err
			}
			if _, err := tx.Exec(
				`INSERT INTO feed_seen (tweet_id, seen_at) VALUES (?, ?)
				 ON CONFLICT(tweet_id) DO UPDATE SET seen_at = MAX(feed_seen.seen_at, excluded.seen_at)`,
				result.CanonicalID, m.UpdatedAtMs,
			); err != nil {
				return err
			}
			if err := requireXContentAssetsForUserStateTx(tx, []string{rawTweetID, result.CanonicalID}, "like", m.UpdatedAtMs); err != nil {
				return err
			}
		case "clear":
			if _, err := tx.Exec(
				`DELETE FROM feed_likes WHERE tweet_id = ?`,
				result.CanonicalID,
			); err != nil {
				return err
			}
			if err := refreshXContentUserStateRequirementTx(tx, []string{rawTweetID, result.CanonicalID}, m.UpdatedAtMs); err != nil {
				return err
			}
		}
		return nil
	})
	return result, err
}

func (db *DB) ApplyLikeMutation(tweetID, action string, updatedAtMs int64) error {
	_, err := db.MutateLike(LikeMutation{TweetID: tweetID, Action: action, UpdatedAtMs: updatedAtMs})
	return err
}

// ── bookmark (toggle + metadata) ─────────────────────────────────────

type BookmarkMutation struct {
	VideoID        string
	Action         string // "set" | "clear"
	CategoryID     *int64
	CustomTitle    *string
	AccountHandles *string
	MediaIndices   *string
	UpdatedAtMs    int64
}

func (db *DB) MutateBookmark(m BookmarkMutation) (MutationResult, error) {
	var result MutationResult
	rawVideoID := strings.TrimSpace(m.VideoID)
	m.UpdatedAtMs = mutationTimestamp(m.UpdatedAtMs)
	err := db.WithWrite(func(tx *sql.Tx) error {
		var err error
		if m.Action == "set" {
			result.CanonicalID, err = db.resolveFeedStateIDForWriteTx(tx, rawVideoID)
		} else {
			result.CanonicalID, err = resolveFeedStateIDTx(tx, rawVideoID)
		}
		if err != nil {
			return err
		}
		result.Applied, err = claimMutationClockTx(tx, "bookmark", result.CanonicalID, m.Action, m.UpdatedAtMs)
		if err != nil || !result.Applied {
			return err
		}
		switch m.Action {
		case "set":
			if err := db.ensureBookmarkTargetStubsTx(tx, result.CanonicalID); err != nil {
				return err
			}
			var categoryID int64
			var customTitle, accountHandles, mediaIndices sql.NullString
			err := tx.QueryRow(`
				SELECT category_id, custom_title, account_handles, media_indices
				FROM bookmarks WHERE video_id = ?
			`, result.CanonicalID).Scan(&categoryID, &customTitle, &accountHandles, &mediaIndices)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			hadBookmark := err == nil
			oldCategoryID := categoryID
			oldCustomTitle := customTitle
			oldAccountHandles := accountHandles
			oldMediaIndices := mediaIndices
			if m.CategoryID != nil {
				categoryID = *m.CategoryID
			}
			if m.CustomTitle != nil {
				customTitle = sql.NullString{String: *m.CustomTitle, Valid: true}
			}
			if m.AccountHandles != nil {
				accountHandles = sql.NullString{String: *m.AccountHandles, Valid: true}
			}
			if m.MediaIndices != nil {
				mediaIndices = sql.NullString{String: *m.MediaIndices, Valid: true}
			}
			if !hadBookmark || categoryID != oldCategoryID || customTitle != oldCustomTitle ||
				accountHandles != oldAccountHandles || mediaIndices != oldMediaIndices {
				result.Affected = 1
			}
			_, err = tx.Exec(`
				INSERT INTO bookmarks (video_id, category_id,
				  custom_title, account_handles, media_indices, bookmarked_at)
				VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT(video_id) DO UPDATE SET
				  category_id = excluded.category_id,
				  custom_title = excluded.custom_title,
				  account_handles = excluded.account_handles,
				  media_indices = excluded.media_indices,
				  bookmarked_at = excluded.bookmarked_at`,
				result.CanonicalID, categoryID, customTitle, accountHandles, mediaIndices, m.UpdatedAtMs,
			)
			if err != nil {
				return err
			}
			if err := requireXContentAssetsForUserStateTx(tx, []string{rawVideoID, result.CanonicalID}, "bookmark", m.UpdatedAtMs); err != nil {
				return err
			}
		case "clear":
			res, err := tx.Exec(
				`DELETE FROM bookmarks WHERE video_id = ?`,
				result.CanonicalID,
			)
			if err != nil {
				return err
			}
			if n, rowsErr := res.RowsAffected(); rowsErr == nil {
				result.Affected = int(n)
			}
			if err := refreshXContentUserStateRequirementTx(tx, []string{rawVideoID, result.CanonicalID}, m.UpdatedAtMs); err != nil {
				return err
			}
		}
		return nil
	})
	return result, err
}

func (db *DB) ApplyBookmarkMutation(m BookmarkMutation) error {
	_, err := db.MutateBookmark(m)
	return err
}

// ── follow / star / mute (toggles) ──────────────────────────────────

func (db *DB) MutateFollow(channelID, action string, updatedAtMs int64) (MutationResult, error) {
	channelID = strings.TrimSpace(channelID)
	updatedAtMs = mutationTimestamp(updatedAtMs)
	result := MutationResult{CanonicalID: channelID}
	if action == "set" {
		if looksLikeBareYouTubeChannelID(channelID) {
			return result, invalidMutation("invalid channel_id")
		}
		sourceID, _, _, platform := channelDefaultsFromID(channelID)
		if platform != "" && !isSafeChannelDerivedID(sourceID) {
			return result, invalidMutation("invalid channel_id")
		}
	}
	err := db.WithWrite(func(tx *sql.Tx) error {
		var err error
		result.Applied, err = claimMutationClockTx(tx, "follow", channelID, action, updatedAtMs)
		if err != nil || !result.Applied {
			return err
		}
		switch action {
		case "set":
			if err := db.ensureChannelStubForFollowTx(tx, channelID, updatedAtMs); err != nil {
				return err
			}
			res, err := tx.Exec(`
				INSERT INTO channel_follows (channel_id, followed_at) VALUES (?, ?)
				ON CONFLICT(channel_id) DO UPDATE SET followed_at = excluded.followed_at
			`, channelID, updatedAtMs)
			if err != nil {
				return err
			}
			if n, rowsErr := res.RowsAffected(); rowsErr == nil {
				result.Affected = int(n)
			}
		case "clear":
			var platform string
			err := tx.QueryRow(`SELECT COALESCE(platform, '') FROM channels WHERE channel_id = ?`, channelID).Scan(&platform)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			res, err := tx.Exec(`DELETE FROM channel_follows WHERE channel_id = ?`, channelID)
			if err != nil {
				return err
			}
			if n, rowsErr := res.RowsAffected(); rowsErr == nil {
				result.Affected = int(n)
			}

			starApplied, starErr := claimMutationClockTx(tx, "star", channelID, "clear", updatedAtMs)
			if starErr != nil && !IsStaleMutation(starErr) {
				return starErr
			}
			if starApplied {
				if _, err := tx.Exec(`DELETE FROM channel_stars WHERE channel_id = ?`, channelID); err != nil {
					return err
				}
			}

			if platform == "" {
				_, _, _, platform = channelDefaultsFromID(channelID)
			}
			if strings.EqualFold(platform, "twitter") || strings.HasPrefix(strings.ToLower(channelID), "twitter_") {
				result.DeletedFileKeys, err = db.purgeTwitterAfterUnfollowTx(tx, channelID)
			} else {
				result.DeletedFileKeys, err = db.purgeVideoChannelAfterUnfollowTx(tx, channelID)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
	return result, err
}

func (db *DB) ApplyFollowMutation(channelID, action string, updatedAtMs int64) error {
	_, err := db.MutateFollow(channelID, action, updatedAtMs)
	return err
}

func looksLikeBareYouTubeChannelID(channelID string) bool {
	channelID = strings.TrimSpace(channelID)
	return strings.HasPrefix(channelID, "UC") && len(channelID) >= 10
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

	if _, err := tx.Exec(`
		INSERT OR IGNORE INTO channels
			(channel_id, source_id, name, url, platform, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, channelID, nilIfEmpty(sourceID), name, nilIfEmpty(urlValue), nilIfEmpty(platform), updatedAtMs); err != nil {
		return err
	}
	return observeChannelProfileTx(tx, model.Channel{
		ChannelID:   channelID,
		SourceID:    sourceID,
		Name:        name,
		URL:         urlValue,
		Platform:    platform,
		Handle:      profileHandle,
		DisplayName: profileName,
	}, updatedAtMs)
}

func mutateToggleTx(tx *sql.Tx, kind, table, keyColumn, itemKey, action string, updatedAtMs int64) (MutationResult, error) {
	result := MutationResult{CanonicalID: itemKey}
	var err error
	result.Applied, err = claimMutationClockTx(tx, kind, itemKey, action, updatedAtMs)
	if err != nil || !result.Applied {
		return result, err
	}
	var res sql.Result
	switch action {
	case "set":
		timestampColumn := "muted_at"
		if kind == "star" {
			timestampColumn = "starred_at"
		}
		res, err = tx.Exec(fmt.Sprintf(`
			INSERT INTO %s (%s, %s) VALUES (?, ?)
			ON CONFLICT(%s) DO UPDATE SET %s = excluded.%s
		`, table, keyColumn, timestampColumn, keyColumn, timestampColumn, timestampColumn), itemKey, updatedAtMs)
	case "clear":
		res, err = tx.Exec(fmt.Sprintf(`DELETE FROM %s WHERE %s = ?`, table, keyColumn), itemKey)
	}
	if err != nil {
		return result, err
	}
	if n, rowsErr := res.RowsAffected(); rowsErr == nil {
		result.Affected = int(n)
	}
	return result, nil
}

func (db *DB) mutateToggle(kind, table, keyColumn, itemKey, action string, updatedAtMs int64) (MutationResult, error) {
	itemKey = strings.TrimSpace(itemKey)
	updatedAtMs = mutationTimestamp(updatedAtMs)
	var result MutationResult
	err := db.WithWrite(func(tx *sql.Tx) error {
		var err error
		result, err = mutateToggleTx(tx, kind, table, keyColumn, itemKey, action, updatedAtMs)
		if err != nil || !result.Applied {
			return err
		}
		return nil
	})
	return result, err
}

func (db *DB) MutateStar(channelID, action string, updatedAtMs int64) (MutationResult, error) {
	return db.mutateToggle("star", "channel_stars", "channel_id", channelID, action, updatedAtMs)
}

func (db *DB) ApplyStarMutation(channelID, action string, updatedAtMs int64) error {
	_, err := db.MutateStar(channelID, action, updatedAtMs)
	return err
}

func (db *DB) MutateMute(channelID, action string, updatedAtMs int64) (MutationResult, error) {
	return db.mutateToggle("mute", "muted_channels", "channel_id", channelID, action, updatedAtMs)
}

func (db *DB) ApplyMuteMutation(channelID, action string, updatedAtMs int64) error {
	_, err := db.MutateMute(channelID, action, updatedAtMs)
	return err
}

// ── channel_setting (PUT) ────────────────────────────────────────────

func (db *DB) MutateChannelSetting(channelID, field string, value any, updatedAtMs int64) (MutationResult, error) {
	result := MutationResult{CanonicalID: strings.TrimSpace(channelID)}
	if _, ok := channelSettingMutationColumn(field); !ok {
		return result, invalidMutation(fmt.Sprintf("unknown channel_setting field: %s", field))
	}
	updatedAtMs = mutationTimestamp(updatedAtMs)
	err := db.WithWrite(func(tx *sql.Tx) error {
		changed, err := mutateChannelSettingsTx(tx, result.CanonicalID, map[string]any{field: value}, updatedAtMs, true)
		if err != nil {
			return err
		}
		if !changed {
			return nil
		}
		result.Applied = true
		result.Affected = 1
		return nil
	})
	return result, err
}

func (db *DB) ApplyChannelSettingMutation(channelID, field string, value any, updatedAtMs int64) error {
	_, err := db.MutateChannelSetting(channelID, field, value, updatedAtMs)
	return err
}

func mutateChannelSettingsTx(tx *sql.Tx, channelID string, fields map[string]any, updatedAtMs int64, strictTimestamp bool) (bool, error) {
	columns := make([]string, 0, len(fields))
	for field := range fields {
		column, ok := channelSettingMutationColumn(field)
		if !ok {
			return false, invalidMutation(fmt.Sprintf("unknown channel_setting field: %s", field))
		}
		columns = append(columns, column)
	}
	if len(columns) == 0 {
		return false, nil
	}
	sort.Strings(columns)

	var currentUpdatedAt int64
	err := tx.QueryRow(`SELECT updated_at FROM channel_settings WHERE channel_id = ?`, channelID).Scan(&currentUpdatedAt)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	if err == nil && updatedAtMs <= currentUpdatedAt {
		same := updatedAtMs == currentUpdatedAt
		for _, column := range columns {
			var matches int
			query := fmt.Sprintf(`SELECT %s IS ? FROM channel_settings WHERE channel_id = ?`, column)
			if queryErr := tx.QueryRow(query, fields[column], channelID).Scan(&matches); queryErr != nil {
				return false, queryErr
			}
			same = same && matches != 0
		}
		if same {
			return false, nil
		}
		if !strictTimestamp {
			updatedAtMs = currentUpdatedAt + 1
		} else {
			return false, &StaleMutationError{
				Kind: "channel_setting", ItemKey: channelID,
				UpdatedAtMs: updatedAtMs, CurrentUpdatedAtMs: currentUpdatedAt,
			}
		}
	}

	placeholders := make([]string, len(columns))
	updates := make([]string, len(columns))
	args := make([]any, 0, len(columns)+2)
	args = append(args, channelID)
	for i, column := range columns {
		placeholders[i] = "?"
		updates[i] = column + " = excluded." + column
		args = append(args, fields[column])
	}
	args = append(args, updatedAtMs)
	query := fmt.Sprintf(`
		INSERT INTO channel_settings (channel_id, %s, updated_at)
		VALUES (?, %s, ?)
		ON CONFLICT(channel_id) DO UPDATE SET %s, updated_at = excluded.updated_at
	`, strings.Join(columns, ", "), strings.Join(placeholders, ", "), strings.Join(updates, ", "))
	_, err = tx.Exec(query, args...)
	return err == nil, err
}

func channelSettingMutationColumn(field string) (string, bool) {
	switch field {
	case "media_only":
		return "media_only", true
	case "include_reposts":
		return "include_reposts", true
	case "media_download_limit":
		return "media_download_limit", true
	case "max_videos":
		return "max_videos", true
	case "download_subtitles":
		return "download_subtitles", true
	default:
		return "", false
	}
}

// ── seen (batched) ───────────────────────────────────────────────────

func (db *DB) MutateSeen(tweetIDs []string, updatedAtMs int64) (MutationResult, error) {
	var result MutationResult
	if len(tweetIDs) == 0 {
		return result, nil
	}
	updatedAtMs = mutationTimestamp(updatedAtMs)
	err := db.WithWrite(func(tx *sql.Tx) error {
		cleanIDs := make([]string, 0, len(tweetIDs))
		seen := make(map[string]struct{}, len(tweetIDs))
		for _, id := range tweetIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			cleanIDs = append(cleanIDs, id)
		}
		expandedIDs, err := expandSeenConversationIDsTx(tx, cleanIDs)
		if err != nil {
			return err
		}
		stmt, err := tx.Prepare(
			`INSERT INTO feed_seen (tweet_id, seen_at) VALUES (?, ?)
				 ON CONFLICT(tweet_id) DO UPDATE SET seen_at = excluded.seen_at
				 WHERE excluded.seen_at > feed_seen.seen_at`,
		)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()
		for _, id := range expandedIDs {
			res, err := stmt.Exec(id, updatedAtMs)
			if err != nil {
				return err
			}
			if n, rowsErr := res.RowsAffected(); rowsErr == nil {
				result.Affected += int(n)
			}
		}
		if result.Affected == 0 {
			return nil
		}
		result.Applied = true
		return nil
	})
	return result, err
}

func (db *DB) ApplySeenMutation(tweetIDs []string, updatedAtMs int64) error {
	_, err := db.MutateSeen(tweetIDs, updatedAtMs)
	return err
}

// ── moment_view ──────────────────────────────────────────────────────

func (db *DB) MutateMomentView(videoID string, updatedAtMs int64) (MutationResult, error) {
	result := MutationResult{CanonicalID: strings.TrimSpace(videoID)}
	updatedAtMs = mutationTimestamp(updatedAtMs)
	err := db.WithWrite(func(tx *sql.Tx) error {
		res, err := tx.Exec(
			`INSERT INTO moment_views (video_id, viewed_at) VALUES (?, ?)
				 ON CONFLICT(video_id) DO UPDATE SET viewed_at = excluded.viewed_at
				 WHERE excluded.viewed_at > moment_views.viewed_at`,
			result.CanonicalID, updatedAtMs,
		)
		if err != nil {
			return err
		}
		if n, rowsErr := res.RowsAffected(); rowsErr == nil {
			result.Affected = int(n)
		}
		if result.Affected == 0 {
			return nil
		}
		result.Applied = true
		return nil
	})
	return result, err
}

func (db *DB) ApplyMomentViewMutation(videoID string, updatedAtMs int64) error {
	_, err := db.MutateMomentView(videoID, updatedAtMs)
	return err
}

// ── progress (PUT — LWW) ─────────────────────────────────────────────

func (db *DB) MutateProgress(videoID string, position, duration float64, updatedAtMs int64) (ProgressResult, error) {
	return db.mutateProgress(videoID, position, duration, "set", updatedAtMs, false)
}

func (db *DB) mutateProgress(videoID string, position, duration float64, action string, updatedAtMs int64, force bool) (ProgressResult, error) {
	videoID = strings.TrimSpace(videoID)
	updatedAtMs = mutationTimestamp(updatedAtMs)
	if position < 0 {
		position = 0
	}
	if duration < 0 {
		duration = 0
	}

	var result ProgressResult
	err := db.WithWrite(func(tx *sql.Tx) error {
		var existingPosition float64
		var existingTs int64
		var existingDuration sql.NullFloat64
		rowErr := tx.QueryRow(
			`SELECT playback_position, duration, updated_at_ms FROM watch_history WHERE video_id = ?`,
			videoID,
		).Scan(&existingPosition, &existingDuration, &existingTs)
		if rowErr != nil && rowErr != sql.ErrNoRows {
			return rowErr
		}
		if action == "set" && duration <= 0 && existingDuration.Valid {
			duration = existingDuration.Float64
		}

		applied := true
		var err error
		if force {
			updatedAtMs, err = advanceMutationClockTx(tx, "progress", videoID, action, updatedAtMs)
		} else {
			applied, err = claimMutationClockTx(tx, "progress", videoID, action, updatedAtMs)
		}
		if err != nil {
			return err
		}
		if !applied {
			if action == "set" && rowErr == nil && updatedAtMs == existingTs &&
				position == existingPosition && duration == existingDuration.Float64 {
				result.Accepted = true
				result.ResolvedPosition = existingPosition
				result.ResolvedUpdatedAtMs = existingTs
				return nil
			}
			if action == "clear" && rowErr == sql.ErrNoRows {
				result.Accepted = true
				result.ResolvedUpdatedAtMs = updatedAtMs
				return nil
			}
			return &StaleMutationError{
				Kind: "progress", ItemKey: videoID, UpdatedAtMs: updatedAtMs,
				CurrentUpdatedAtMs: updatedAtMs,
			}
		}

		switch action {
		case "set":
			durationValue := sql.NullFloat64{Float64: duration, Valid: duration > 0}
			_, err = tx.Exec(`
					INSERT INTO watch_history (video_id, playback_position, duration, updated_at_ms)
					VALUES (?, ?, ?, ?)
				ON CONFLICT(video_id) DO UPDATE SET
				  playback_position = excluded.playback_position,
				  duration          = excluded.duration,
				  updated_at_ms     = excluded.updated_at_ms`,
				videoID, position, durationValue, updatedAtMs,
			)
		case "clear":
			_, err = tx.Exec(`DELETE FROM watch_history WHERE video_id = ?`, videoID)
		default:
			return errInvalidAction
		}
		if err != nil {
			return err
		}
		result.Accepted = true
		if action == "set" {
			result.ResolvedPosition = position
		}
		result.ResolvedUpdatedAtMs = updatedAtMs
		return nil
	})
	return result, err
}

func (db *DB) ApplyProgressMutation(videoID string, position, duration float64, updatedAtMs int64) error {
	_, err := db.MutateProgress(videoID, position, duration, updatedAtMs)
	return err
}

func (db *DB) writeWebProgress(videoID string, position, duration float64, action string, updatedAtMs int64) error {
	_, err := db.mutateProgress(videoID, position, duration, action, updatedAtMs, true)
	return err
}

// ── moments_cursor (PUT — LWW) ──────────────────────────────────────

func (db *DB) ApplyMomentsCursorMutation(videoID string, positionMs, updatedAtMs int64, scope string) error {
	return db.ApplyMomentsCursorMutationWithSortAt(videoID, positionMs, updatedAtMs, scope, 0)
}

func (db *DB) ApplyMomentsCursorMutationWithSortAt(videoID string, positionMs, updatedAtMs int64, scope string, sortAtMs int64) error {
	_, err := db.MutateMomentsCursor(videoID, positionMs, updatedAtMs, scope, sortAtMs)
	return err
}

func (db *DB) MutateMomentsCursor(videoID string, positionMs, updatedAtMs int64, scope string, sortAtMs int64) (MutationResult, error) {
	updatedAtMs = mutationTimestamp(updatedAtMs)
	result := MutationResult{CanonicalID: strings.TrimSpace(videoID)}
	normalizedScope, ok := NormalizeMomentsCursorScope(scope)
	if !ok {
		return result, invalidMutation("invalid moments cursor scope")
	}
	scope = normalizedScope

	if sortAtMs <= 0 {
		var err error
		sortAtMs, _, err = db.GetShortsCursorSortAt(result.CanonicalID, scope)
		if err != nil {
			return result, err
		}
	}
	err := db.WithWrite(func(tx *sql.Tx) error {
		var existingVideoID string
		var existingPositionMs, existingSortAtMs, existingUpdatedAtMs int64
		err := tx.QueryRow(`
			SELECT video_id, position_ms, sort_at_ms, updated_at_ms
			FROM moments_cursors WHERE scope = ?
		`, scope).Scan(&existingVideoID, &existingPositionMs, &existingSortAtMs, &existingUpdatedAtMs)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil && updatedAtMs <= existingUpdatedAtMs {
			same := updatedAtMs == existingUpdatedAtMs && result.CanonicalID == existingVideoID &&
				positionMs == existingPositionMs && sortAtMs == existingSortAtMs
			if same {
				return nil
			}
			return &StaleMutationError{
				Kind: "moments_cursor", ItemKey: scope, UpdatedAtMs: updatedAtMs,
				CurrentUpdatedAtMs: existingUpdatedAtMs,
			}
		}
		if _, err := tx.Exec(`
			INSERT INTO moments_cursors (scope, video_id, position_ms, sort_at_ms, updated_at_ms)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(scope) DO UPDATE SET
			  video_id = excluded.video_id,
			  position_ms = excluded.position_ms,
			  sort_at_ms = excluded.sort_at_ms,
			  updated_at_ms = excluded.updated_at_ms
		`, scope, result.CanonicalID, positionMs, sortAtMs, updatedAtMs); err != nil {
			return err
		}
		result.Applied = true
		result.Affected = 1
		return nil
	})
	return result, err
}

type MutationMomentsCursor struct {
	Scope       string `json:"scope"`
	VideoID     string `json:"video_id"`
	PositionMs  int64  `json:"position_ms"`
	SortAtMs    int64  `json:"sort_at_ms"`
	UpdatedAtMs int64  `json:"updated_at_ms"`
}

func (db *DB) GetMomentsCursor(scope string) (MutationMomentsCursor, bool, error) {
	var cursor MutationMomentsCursor
	normalizedScope, ok := NormalizeMomentsCursorScope(scope)
	if !ok {
		return cursor, false, invalidMutation("invalid moments cursor scope")
	}
	err := db.conn.QueryRow(`
		SELECT scope, video_id, position_ms, sort_at_ms, updated_at_ms
		FROM moments_cursors
		WHERE scope = ?
	`, normalizedScope).Scan(
		&cursor.Scope, &cursor.VideoID, &cursor.PositionMs,
		&cursor.SortAtMs, &cursor.UpdatedAtMs,
	)
	if err == sql.ErrNoRows {
		return cursor, false, nil
	}
	return cursor, err == nil, err
}

// ── create_category (provisional → real ID) ─────────────────────────

type CategoryCreated struct {
	CategoryID    int64
	ProvisionalID string
}

func (db *DB) ApplyCreateCategoryMutation(name, provisionalID, requestID string, updatedAtMs int64) (CategoryCreated, error) {
	updatedAtMs = mutationTimestamp(updatedAtMs)
	var out CategoryCreated
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return out, invalidMutation("request_id required")
	}
	err := db.WithWrite(func(tx *sql.Tx) error {
		err := tx.QueryRow(`
			SELECT category_id, provisional_id
			FROM category_create_receipts
			WHERE request_id = ?
		`, requestID).Scan(&out.CategoryID, &out.ProvisionalID)
		if err == nil {
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		res, err := tx.Exec(
			`INSERT INTO bookmark_categories (name, created_at) VALUES (?, ?)`,
			name, updatedAtMs,
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
		if _, err := tx.Exec(`
			INSERT INTO category_create_receipts
				(request_id, category_id, provisional_id, created_at_ms)
			VALUES (?, ?, ?, ?)
		`, requestID, categoryID, provisionalID, updatedAtMs); err != nil {
			return err
		}

		return nil
	})
	return out, err
}
