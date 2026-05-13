package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/screwys/igloo/internal/model"
)

// compileFTSQuery converts a user query into FTS5 syntax.
// Wraps each term in quotes for prefix matching.
func compileFTSQuery(q string) string {
	terms := strings.Fields(strings.TrimSpace(q))
	if len(terms) == 0 {
		return ""
	}
	var parts []string
	for _, t := range terms {
		t = strings.ReplaceAll(t, `"`, `""`)
		parts = append(parts, `"`+t+`"*`)
	}
	return strings.Join(parts, " AND ")
}

// SearchChannelsFast searches channels using FTS5 index.
func (db *DB) SearchChannelsFast(q string, limit int) ([]model.Channel, error) {
	fts := compileFTSQuery(q)
	if fts == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	if !db.searchIndexReady() {
		return db.searchChannelsFallback(q, limit)
	}

	rows, err := db.conn.Query(`
		SELECT f.channel_id_pk,
		       COALESCE(NULLIF(cp.display_name, ''), NULLIF(c.name, ''), f.name) AS display_name,
		       COALESCE(NULLIF(c.channel_id, ''), f.channel_id_pk) AS channel_id,
		       COALESCE(c.source_id, '') AS source_id,
		       COALESCE(c.platform, 'youtube') AS platform,
		       CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END AS is_starred,
		       COALESCE(cp.handle, '') AS handle,
		       COALESCE(cp.display_name, '') AS profile_display_name
		FROM search_channels_fts f
		LEFT JOIN channels c ON c.channel_id = f.channel_id_pk
		LEFT JOIN channel_stars cs ON cs.channel_id = f.channel_id_pk
		LEFT JOIN channel_profiles cp ON cp.channel_id = f.channel_id_pk AND cp.tombstone = 0
		WHERE search_channels_fts MATCH ?
		ORDER BY rank
		LIMIT ?
	`, fts, limit)
	if err != nil {
		// FTS5 not available — fallback to LIKE
		return db.searchChannelsFallback(q, limit)
	}
	defer rows.Close()

	var channels []model.Channel
	for rows.Next() {
		var ch model.Channel
		var channelIDPK string
		if err := rows.Scan(&channelIDPK, &ch.Name, &ch.ChannelID, &ch.SourceID, &ch.Platform, &ch.IsStarred, &ch.Handle, &ch.DisplayName); err != nil {
			return nil, err
		}
		if ch.ChannelID == "" {
			ch.ChannelID = channelIDPK
		}
		ch.AvatarURL = "/api/media/avatar/" + ch.ChannelID
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

func (db *DB) searchChannelsFallback(q string, limit int) ([]model.Channel, error) {
	prefix := q + "%"
	wordStart := "% " + q + "%"
	// is_starred is not a column on channels — it's computed from channel_stars
	// via LEFT JOIN, matching the pattern used across the rest of the codebase.
	//
	// Display name comes from channel_profiles.display_name when available, else
	// channels.name. For Twitter in particular, channels.name is just the handle
	// (e.g. "samplehandle99") while channel_profiles.display_name has the real name
	// (e.g. "Example Display Name") — we want to show the real name.
	//
	// Matching rules:
	//   * channels.name: prefix OR word-start.
	//   * channels.source_id: prefix, only for tiktok/twitter where source_id
	//     is the handle. YouTube source_ids are opaque like
	//     "UCEXAMPLE000000000000003" so we never match them as substrings.
	//   * channel_profiles.display_name: prefix OR word-start. This is what
	//     makes CJK search work — typing "example" finds @samplehandle99.
	//   * channel_id is never searched — always opaque.
	//
	// Ordering: any prefix match (name or display) ranks above any word-start
	// match, with starred channels first within each rank.
	rows, err := db.conn.Query(`
		SELECT c.channel_id,
		       COALESCE(NULLIF(cp.display_name, ''), c.name) AS display_name,
		       COALESCE(c.source_id, '') AS source_id,
		       COALESCE(c.platform,'youtube'),
		       CASE WHEN cs.channel_id IS NOT NULL THEN 1 ELSE 0 END AS is_starred,
		       COALESCE(cp.handle, '') AS handle,
		       COALESCE(cp.display_name, '') AS profile_display_name
		FROM channels c
		LEFT JOIN channel_stars cs    ON cs.channel_id = c.channel_id
		LEFT JOIN channel_profiles cp ON cp.channel_id = c.channel_id AND cp.tombstone = 0
		WHERE c.name LIKE ?
		   OR c.name LIKE ?
		   OR (c.platform IN ('tiktok','twitter') AND c.source_id LIKE ?)
		   OR cp.display_name LIKE ?
		   OR cp.display_name LIKE ?
		ORDER BY
			CASE
				WHEN c.name          LIKE ? THEN 0
				WHEN cp.display_name LIKE ? THEN 0
				WHEN c.name          LIKE ? THEN 1
				WHEN cp.display_name LIKE ? THEN 1
				ELSE 2
			END,
			is_starred DESC,
			COALESCE(NULLIF(cp.display_name, ''), c.name) COLLATE NOCASE
		LIMIT ?
	`, prefix, wordStart, prefix, prefix, wordStart,
		prefix, prefix, wordStart, wordStart, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []model.Channel
	for rows.Next() {
		var ch model.Channel
		if err := rows.Scan(&ch.ChannelID, &ch.Name, &ch.SourceID, &ch.Platform, &ch.IsStarred, &ch.Handle, &ch.DisplayName); err != nil {
			return nil, err
		}
		ch.AvatarURL = "/api/media/avatar/" + ch.ChannelID
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

// SearchVideosFast searches videos using FTS5 index.
func (db *DB) SearchVideosFast(q string, limit int) ([]model.Video, error) {
	fts := compileFTSQuery(q)
	if fts == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 200
	}
	if !db.searchIndexReady() {
		return db.searchVideosFallback(q, limit)
	}

	rows, err := db.conn.Query(`
		SELECT f.video_id_pk,
		       COALESCE(v.title, f.title),
		       COALESCE(c.name, f.channel_name, ''),
		       COALESCE(v.channel_id, ''),
		       COALESCE(c.platform, 'youtube'),
		       v.published_at,
		       COALESCE(v.is_temp, 0),
		       v.dearrow_title, v.dearrow_title_casual, v.dearrow_thumb_path
		FROM search_videos_fts f
		LEFT JOIN videos v ON v.video_id = f.video_id_pk
		LEFT JOIN channels c ON c.channel_id = v.channel_id
		WHERE search_videos_fts MATCH ?
		ORDER BY rank
		LIMIT ?
	`, fts, limit)
	if err != nil {
		// FTS5 not available — fallback to LIKE
		return db.searchVideosFallback(q, limit)
	}
	defer rows.Close()

	var videos []model.Video
	for rows.Next() {
		var v model.Video
		var pubAt sql.NullInt64
		var isTemp bool
		if err := rows.Scan(&v.VideoID, &v.Title, &v.ChannelName, &v.ChannelID, &v.Platform, &pubAt, &isTemp,
			&v.DearrowTitle, &v.DearrowTitleCasual, &v.DearrowThumbPath); err != nil {
			return nil, err
		}
		v.PublishedAt = millisToTimePtr(pubAt)
		v.IsTemp = isTemp
		v.ThumbnailURL = "/api/media/thumbnail/" + v.VideoID
		if v.ChannelID != "" {
			v.AvatarURL = "/api/media/avatar/" + v.ChannelID
		}
		videos = append(videos, v)
	}
	return videos, rows.Err()
}

func (db *DB) searchVideosFallback(q string, limit int) ([]model.Video, error) {
	prefix := q + "%"
	wordStart := "% " + q + "%"
	// Match title, dearrow_title, and dearrow_title_casual with word-boundary
	// matching (prefix OR space-then-prefix). Prefix matches rank as tier 0
	// regardless of which title column matched, mirroring how channel search
	// ranks c.name and cp.display_name prefix matches as equal tier 0.
	rows, err := db.conn.Query(`
		SELECT v.video_id, v.title, COALESCE(c.name,''), v.channel_id,
		       COALESCE(c.platform,'youtube'), v.published_at, COALESCE(v.is_temp,0),
		       v.dearrow_title, v.dearrow_title_casual, v.dearrow_thumb_path
		FROM videos v
		LEFT JOIN channels c ON v.channel_id = c.channel_id
		WHERE v.title                LIKE ? OR v.title                LIKE ?
		   OR v.dearrow_title        LIKE ? OR v.dearrow_title        LIKE ?
		   OR v.dearrow_title_casual LIKE ? OR v.dearrow_title_casual LIKE ?
		ORDER BY
			CASE WHEN v.title                LIKE ? THEN 0
			     WHEN v.dearrow_title        LIKE ? THEN 0
			     WHEN v.dearrow_title_casual LIKE ? THEN 0
			     ELSE 1 END,
			v.published_at DESC
		LIMIT ?
	`, prefix, wordStart, prefix, wordStart, prefix, wordStart,
		prefix, prefix, prefix,
		limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var videos []model.Video
	for rows.Next() {
		var v model.Video
		var pubAt sql.NullInt64
		if err := rows.Scan(&v.VideoID, &v.Title, &v.ChannelName, &v.ChannelID, &v.Platform, &pubAt, &v.IsTemp,
			&v.DearrowTitle, &v.DearrowTitleCasual, &v.DearrowThumbPath); err != nil {
			return nil, err
		}
		v.PublishedAt = millisToTimePtr(pubAt)
		v.ThumbnailURL = "/api/media/thumbnail/" + v.VideoID
		if v.ChannelID != "" {
			v.AvatarURL = "/api/media/avatar/" + v.ChannelID
		}
		videos = append(videos, v)
	}
	return videos, rows.Err()
}

// RebuildSearchIndex repopulates all FTS5 tables. Returns count of indexed rows.
func (db *DB) RebuildSearchIndex(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin search index rebuild: %w", err)
	}
	defer tx.Rollback()

	var channelCount, videoCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM channels`).Scan(&channelCount); err != nil {
		return 0, fmt.Errorf("count channel search rows: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM videos`).Scan(&videoCount); err != nil {
		return 0, fmt.Errorf("count video search rows: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM search_channels_fts`); err != nil {
		return 0, fmt.Errorf("clear channel search index: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO search_channels_fts(rowid, channel_id_pk, name, source_id, display_name, handle)
		SELECT c.id, c.channel_id, COALESCE(c.name, ''), COALESCE(c.source_id, ''),
		       COALESCE(cp.display_name, ''), COALESCE(cp.handle, '')
		FROM channels c
		LEFT JOIN channel_profiles cp ON cp.channel_id = c.channel_id AND COALESCE(cp.tombstone, 0) = 0
	`); err != nil {
		return 0, fmt.Errorf("populate channel search index: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM search_videos_fts`); err != nil {
		return 0, fmt.Errorf("clear video search index: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO search_videos_fts(rowid, video_id_pk, title, dearrow_title, dearrow_title_casual, channel_name)
		SELECT v.id, v.video_id, COALESCE(v.title, ''), COALESCE(v.dearrow_title, ''),
		       COALESCE(v.dearrow_title_casual, ''), COALESCE(c.name, '')
		FROM videos v
		LEFT JOIN channels c ON c.channel_id = v.channel_id
	`); err != nil {
		return 0, fmt.Errorf("populate video search index: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO settings (user_id, key, value)
		VALUES ('', 'search_index_ready', '1')
		ON CONFLICT(user_id, key) DO UPDATE SET value = excluded.value
	`); err != nil {
		return 0, fmt.Errorf("mark search index ready: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit search index rebuild: %w", err)
	}
	return channelCount + videoCount, nil
}

func (db *DB) searchIndexReady() bool {
	var ready string
	err := db.conn.QueryRow(`SELECT value FROM settings WHERE user_id = '' AND key = 'search_index_ready'`).Scan(&ready)
	return err == nil && ready == "1"
}

// SearchFeedItems searches X posts by body text, author handle, or display name.
func (db *DB) SearchFeedItems(q string, limit int) ([]model.FeedItem, error) {
	if limit <= 0 {
		limit = 50
	}
	like := "%" + q + "%"

	rows, err := db.conn.Query(`
		SELECT tweet_id, COALESCE(source_handle,''), author_handle,
		       COALESCE(author_display_name,''), COALESCE(author_avatar_url,''),
		       COALESCE(body_text,''), COALESCE(lang,''),
		       COALESCE(is_retweet,0), COALESCE(retweeted_by_handle,''),
		       COALESCE(retweeted_by_display_name,''),
		       COALESCE(quote_tweet_id,''), COALESCE(quote_author_handle,''),
		       COALESCE(quote_author_display_name,''), COALESCE(quote_author_avatar_url,''),
		       COALESCE(quote_body_text,''), COALESCE(quote_lang,''),
		       COALESCE(quote_media_json,''), COALESCE(media_json,''),
		       COALESCE(canonical_url,''), COALESCE(reply_to_handle,''),
		       COALESCE(reply_to_status,''),
		       COALESCE(is_reply,0), COALESCE(is_ghost,0),
		       quote_published_at,
		       COALESCE(views,0), COALESCE(likes,0), COALESCE(retweets,0),
		       published_at, fetched_at,
		       COALESCE(content_hash,''), COALESCE(canonical_tweet_id,'')
		FROM feed_items
		WHERE body_text LIKE ? OR author_handle LIKE ? OR author_display_name LIKE ?
		ORDER BY published_at DESC
		LIMIT ?
	`, like, like, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFeedItems(rows)
}
