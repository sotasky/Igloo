package db

import (
	"database/sql"
	"sort"
	"strings"
	"time"

	"github.com/screwys/igloo/internal/model"
)

func normalizeTikTokHandle(handle string) string {
	handle = model.NormalizeTikTokHandle(handle)
	if model.IsTikTokInternalID(handle) {
		return ""
	}
	return handle
}

func canonicalTikTokChannelIDFromHandle(handle string) string {
	return model.TikTokChannelIDFromHandle(handle)
}

func normalizeInstagramHandle(handle string) string {
	return model.NormalizeInstagramHandle(handle)
}

func canonicalInstagramChannelIDFromHandle(handle string) string {
	return model.InstagramChannelIDFromHandle(handle)
}

// EnsureTikTokChannelForRepost creates an unfollowed original-author channel
// for a reposted TikTok video. It does not subscribe/follow the author.
func (db *DB) EnsureTikTokChannelForRepost(channelID, handle, displayName string) error {
	handle = normalizeTikTokHandle(handle)
	channelID = strings.TrimSpace(channelID)
	if handle != "" {
		channelID = canonicalTikTokChannelIDFromHandle(handle)
	} else {
		handle = model.TikTokHandleFromChannelID(channelID)
		channelID = canonicalTikTokChannelIDFromHandle(handle)
	}
	if channelID == "" {
		return nil
	}
	name := strings.TrimSpace(displayName)
	if name == "" {
		name = handle
	}
	if name == "" {
		name = channelID
	}
	url := ""
	if handle != "" {
		url = "https://www.tiktok.com/@" + handle
	}
	now := time.Now().UnixMilli()
	return db.WithWrite(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`
			INSERT INTO channels (channel_id, source_id, name, url, platform, created_at)
			VALUES (?, ?, ?, ?, 'tiktok', ?)
			ON CONFLICT(channel_id) DO UPDATE SET
				source_id = COALESCE(NULLIF(channels.source_id, ''), excluded.source_id),
				name = CASE WHEN TRIM(COALESCE(channels.name, '')) = '' THEN excluded.name ELSE channels.name END,
				url = COALESCE(NULLIF(channels.url, ''), excluded.url),
				platform = 'tiktok'
		`, channelID, nilIfEmpty(handle), name, nilIfEmpty(url), now); err != nil {
			return err
		}
		return observeProfileTx(tx, profileObservation{
			channelID: channelID, platform: "tiktok", handle: handle,
			displayName: displayName, observedAt: now,
		})
	})
}

// EnsureInstagramChannelForTagged creates an unfollowed original-owner channel
// for an Instagram post discovered through another followed account's tagged
// route. It does not subscribe/follow the owner.
func (db *DB) EnsureInstagramChannelForTagged(channelID, handle, displayName, _ string) error {
	handle = normalizeInstagramHandle(handle)
	channelID = strings.TrimSpace(channelID)
	if handle != "" {
		channelID = canonicalInstagramChannelIDFromHandle(handle)
	} else {
		handle = model.InstagramHandleFromChannelID(channelID)
		channelID = canonicalInstagramChannelIDFromHandle(handle)
	}
	if channelID == "" {
		return nil
	}
	name := strings.TrimSpace(displayName)
	if name == "" {
		name = handle
	}
	if name == "" {
		name = channelID
	}
	url := ""
	if handle != "" {
		url = "https://www.instagram.com/" + handle + "/"
	}
	now := time.Now().UnixMilli()
	return db.WithWrite(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`
			INSERT INTO channels (channel_id, source_id, name, url, platform, created_at)
			VALUES (?, ?, ?, ?, 'instagram', ?)
			ON CONFLICT(channel_id) DO UPDATE SET
				source_id = COALESCE(NULLIF(channels.source_id, ''), excluded.source_id),
				name = CASE WHEN TRIM(COALESCE(channels.name, '')) = '' THEN excluded.name ELSE channels.name END,
				url = COALESCE(NULLIF(channels.url, ''), excluded.url),
				platform = 'instagram'
		`, channelID, nilIfEmpty(handle), name, nilIfEmpty(url), now); err != nil {
			return err
		}
		return observeProfileTx(tx, profileObservation{
			channelID: channelID, platform: "instagram", handle: handle,
			displayName: displayName, observedAt: now,
		})
	})
}

// UpsertVideoRepostSources stores TikTok repost introducers.
func (db *DB) UpsertVideoRepostSources(rows []model.VideoRepostSource) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	changed := 0
	err := db.WithWrite(func(tx *sql.Tx) error {
		for _, row := range rows {
			ok, err := db.upsertVideoRepostSourceTx(tx, row)
			if err != nil {
				return err
			}
			if ok {
				changed++
			}
		}
		return nil
	})
	return changed, err
}

// ReplaceVideoRepostSources replaces the complete attachment set for a video.
// An empty rows slice intentionally clears all sources for that video.
func (db *DB) ReplaceVideoRepostSources(videoID string, rows []model.VideoRepostSource) error {
	videoID = strings.TrimSpace(videoID)
	if videoID == "" {
		return nil
	}
	return db.WithWrite(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM video_repost_sources WHERE video_id = ?`, videoID); err != nil {
			return err
		}
		for _, row := range rows {
			row.VideoID = videoID
			ok, err := db.upsertVideoRepostSourceTx(tx, row)
			if err != nil {
				return err
			}
			_ = ok
		}
		return nil
	})
}

// ReplaceVideoRepostSourcesForReposter replaces the complete introduced-video
// set for one followed source account while preserving rows from other sources.
// It returns video IDs that lost this source row.
func (db *DB) ReplaceVideoRepostSourcesForReposter(reposterChannelID string, rows []model.VideoRepostSource) ([]string, error) {
	reposterChannelID, reposterHandle := normalizeReposterIdentity(reposterChannelID, "")
	if reposterChannelID == "" {
		return nil, nil
	}
	nextRows := make(map[string]model.VideoRepostSource, len(rows))
	for _, row := range rows {
		row.VideoID = strings.TrimSpace(row.VideoID)
		if row.VideoID == "" {
			continue
		}
		row.ReposterChannelID = reposterChannelID
		if strings.TrimSpace(row.ReposterHandle) == "" {
			row.ReposterHandle = reposterHandle
		}
		nextRows[row.VideoID] = row
	}

	var removed []string
	err := db.WithWrite(func(tx *sql.Tx) error {
		existingRows, err := tx.Query(`
			SELECT video_id
			FROM video_repost_sources
			WHERE reposter_channel_id = ?
			ORDER BY video_id
		`, reposterChannelID)
		if err != nil {
			return err
		}
		var existing []string
		for existingRows.Next() {
			var videoID string
			if err := existingRows.Scan(&videoID); err != nil {
				_ = existingRows.Close()
				return err
			}
			existing = append(existing, videoID)
		}
		if err := existingRows.Close(); err != nil {
			return err
		}
		for _, videoID := range existing {
			if _, ok := nextRows[videoID]; ok {
				continue
			}
			if _, err := tx.Exec(`
				DELETE FROM video_repost_sources
				WHERE video_id = ? AND reposter_channel_id = ?
			`, videoID, reposterChannelID); err != nil {
				return err
			}
			removed = append(removed, videoID)
		}
		keys := make([]string, 0, len(nextRows))
		for videoID := range nextRows {
			keys = append(keys, videoID)
		}
		sort.Strings(keys)
		for _, videoID := range keys {
			if _, err := db.upsertVideoRepostSourceTx(tx, nextRows[videoID]); err != nil {
				return err
			}
		}
		return nil
	})
	return removed, err
}

func (db *DB) upsertVideoRepostSourceTx(tx *sql.Tx, row model.VideoRepostSource) (bool, error) {
	row.VideoID = strings.TrimSpace(row.VideoID)
	row.ReposterChannelID, row.ReposterHandle = normalizeReposterIdentity(row.ReposterChannelID, row.ReposterHandle)
	if row.VideoID == "" || row.ReposterChannelID == "" {
		return false, nil
	}
	now := time.Now().UnixMilli()
	_, _, _, platform := channelDefaultsFromID(row.ReposterChannelID)
	if err := observeProfileTx(tx, profileObservation{
		channelID: row.ReposterChannelID, platform: platform, handle: row.ReposterHandle,
		displayName: row.ReposterDisplayName, observedAt: now,
	}); err != nil {
		return false, err
	}
	inputFirstSeenAtMs := row.FirstSeenAtMs
	if inputFirstSeenAtMs <= 0 {
		row.FirstSeenAtMs = now
	}
	if row.UpdatedAtMs <= 0 {
		row.UpdatedAtMs = now
	}

	var oldReposted, oldFirstSeen sql.NullInt64
	err := tx.QueryRow(`
		SELECT reposted_at_ms, first_seen_at_ms
		FROM video_repost_sources
		WHERE video_id = ? AND reposter_channel_id = ?
	`, row.VideoID, row.ReposterChannelID).Scan(&oldReposted, &oldFirstSeen)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	if err == nil {
		if inputFirstSeenAtMs <= 0 && oldFirstSeen.Valid {
			row.FirstSeenAtMs = oldFirstSeen.Int64
		}
		if row.RepostedAtMs <= 0 && oldReposted.Valid && oldReposted.Int64 > 0 {
			row.RepostedAtMs = oldReposted.Int64
		}
		if oldReposted.Int64 == row.RepostedAtMs &&
			oldFirstSeen.Int64 == row.FirstSeenAtMs {
			return false, nil
		}
	}

	_, execErr := tx.Exec(`
		INSERT INTO video_repost_sources (
			video_id, reposter_channel_id, reposted_at_ms, first_seen_at_ms, updated_at_ms
		) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(video_id, reposter_channel_id) DO UPDATE SET
			reposted_at_ms = CASE
				WHEN excluded.reposted_at_ms > 0 THEN excluded.reposted_at_ms
				ELSE video_repost_sources.reposted_at_ms
			END,
			first_seen_at_ms = CASE
				WHEN video_repost_sources.first_seen_at_ms > 0 THEN video_repost_sources.first_seen_at_ms
				ELSE excluded.first_seen_at_ms
			END,
			updated_at_ms = excluded.updated_at_ms
	`, row.VideoID, row.ReposterChannelID, row.RepostedAtMs, row.FirstSeenAtMs, row.UpdatedAtMs)
	if execErr != nil {
		return false, execErr
	}
	return true, nil
}

func normalizeReposterIdentity(channelID, handle string) (string, string) {
	channelID = strings.TrimSpace(channelID)
	lowerChannelID := strings.ToLower(channelID)
	switch {
	case strings.HasPrefix(lowerChannelID, "instagram_"):
		handle = normalizeInstagramHandle(handle)
		if handle == "" {
			handle = model.InstagramHandleFromChannelID(channelID)
		}
		if handle != "" {
			channelID = canonicalInstagramChannelIDFromHandle(handle)
		}
	case strings.HasPrefix(lowerChannelID, "tiktok_") || channelID == "":
		handle = normalizeTikTokHandle(handle)
		if channelID == "" {
			channelID = canonicalTikTokChannelIDFromHandle(handle)
		}
		if handle == "" && strings.HasPrefix(strings.ToLower(channelID), "tiktok_") {
			handle = model.TikTokHandleFromChannelID(channelID)
		}
	default:
		handle = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(handle), "@"))
	}
	return channelID, handle
}

func (db *DB) GetVideoRepostSources(videoID string) ([]model.VideoRepostSource, error) {
	rows, err := db.reader().Query(`
		SELECT video_id, reposter_channel_id, COALESCE(reposter_handle, ''),
		       COALESCE(reposter_display_name, ''), COALESCE(reposted_at_ms, 0),
		       COALESCE(first_seen_at_ms, 0), COALESCE(updated_at_ms, 0)
		FROM video_repost_sources_resolved
		WHERE video_id = ?
		ORDER BY COALESCE(NULLIF(reposted_at_ms, 0), first_seen_at_ms) DESC, reposter_channel_id ASC
	`, videoID)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	return scanVideoRepostSources(rows)
}

func (db *DB) GetVideoRepostSourcesForVideoIDs(videoIDs []string) (map[string][]model.VideoRepostSource, error) {
	out := make(map[string][]model.VideoRepostSource, len(videoIDs))
	for _, chunk := range stringChunks(videoIDs, 400) {
		if len(chunk) == 0 {
			continue
		}
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := db.reader().Query(`
			SELECT video_id, reposter_channel_id, COALESCE(reposter_handle, ''),
			       COALESCE(reposter_display_name, ''), COALESCE(reposted_at_ms, 0),
			       COALESCE(first_seen_at_ms, 0), COALESCE(updated_at_ms, 0)
			FROM video_repost_sources_resolved
			WHERE video_id IN (`+placeholders(len(chunk))+`)
			ORDER BY video_id, COALESCE(NULLIF(reposted_at_ms, 0), first_seen_at_ms) DESC, reposter_channel_id ASC
		`, args...)
		if err != nil {
			return nil, err
		}
		reposts, scanErr := scanVideoRepostSources(rows)
		_ = rows.Close()
		if scanErr != nil {
			return nil, scanErr
		}
		for _, repost := range reposts {
			out[repost.VideoID] = append(out[repost.VideoID], repost)
		}
	}
	return out, nil
}

func scanVideoRepostSources(rows *sql.Rows) ([]model.VideoRepostSource, error) {
	var out []model.VideoRepostSource
	for rows.Next() {
		var row model.VideoRepostSource
		if err := rows.Scan(
			&row.VideoID,
			&row.ReposterChannelID,
			&row.ReposterHandle,
			&row.ReposterDisplayName,
			&row.RepostedAtMs,
			&row.FirstSeenAtMs,
			&row.UpdatedAtMs,
		); err != nil {
			return nil, err
		}
		row.RepostAuthorLabel = model.RepostAuthorLabel(row.ReposterDisplayName, row.ReposterHandle)
		out = append(out, row)
	}
	return out, rows.Err()
}
